package channels

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

// AdapterFactory 根据通道配置与解密后凭据创建平台适配器，便于生产默认装配与单测注入。
type AdapterFactory func(ch store.RemoteChannel, secrets store.RemoteChannelSecrets) (Adapter, error)

// Manager 负责管理所有已启用远程通道的生命周期：
// 随服务启停、热加载配置变更、指数退避重连、连接状态上报、并发安全出站投递。
type Manager struct {
	store      *store.Store
	logger     *slog.Logger
	publicURL  string
	factory    AdapterFactory
	weixinAuth *WeixinLoginStore

	mu      sync.Mutex
	baseCtx context.Context
	cancel  context.CancelFunc
	runners map[uuid.UUID]*channelRunner
	wg      sync.WaitGroup
}

type channelRunner struct {
	channel store.RemoteChannel
	adapter Adapter
	cancel  context.CancelFunc
	done    chan struct{}
}

func NewManager(st *store.Store, logger *slog.Logger, publicURL string) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		store:      st,
		logger:     logger,
		publicURL:  strings.TrimRight(strings.TrimSpace(publicURL), "/"),
		factory:    DefaultAdapterFactory(nil),
		weixinAuth: NewWeixinLoginStore(nil, defaultWeixinBaseURL),
		runners:    make(map[uuid.UUID]*channelRunner),
	}
}

func (m *Manager) SetAdapterFactory(factory AdapterFactory) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if factory != nil {
		m.factory = factory
	}
}

func (m *Manager) SetWeixinLoginStore(ls *WeixinLoginStore) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ls != nil {
		m.weixinAuth = ls
	}
}

func (m *Manager) WeixinLoginStore() *WeixinLoginStore {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.weixinAuth
}

// DefaultAdapterFactory 构造生产 6 平台适配器工厂。
func DefaultAdapterFactory(httpClient *http.Client) AdapterFactory {
	return func(ch store.RemoteChannel, secrets store.RemoteChannelSecrets) (Adapter, error) {
		switch ch.Platform {
		case domain.ChannelPlatformWeixin:
			return NewWeixinAdapter(ch, secrets, httpClient), nil
		case domain.ChannelPlatformWeCom:
			return NewWeComAdapter(ch, secrets, httpClient), nil
		case domain.ChannelPlatformFeishu:
			return NewFeishuAdapter(ch, secrets, httpClient), nil
		case domain.ChannelPlatformDingTalk:
			return NewDingTalkAdapter(ch, secrets, httpClient), nil
		case domain.ChannelPlatformQQ:
			return NewQQAdapter(ch, secrets, httpClient), nil
		case domain.ChannelPlatformTelegram:
			return NewTelegramAdapter(ch, secrets, httpClient)
		default:
			return nil, fmt.Errorf("unsupported channel platform %q", ch.Platform)
		}
	}
}

// Start 启动所有已启用通道，并在后台持续运行。
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.baseCtx != nil {
		m.mu.Unlock()
		return nil
	}
	m.baseCtx, m.cancel = context.WithCancel(ctx)
	m.mu.Unlock()

	if err := m.SyncAll(ctx); err != nil {
		return err
	}
	return nil
}

// Stop 优雅停止所有运行中的通道连接。
func (m *Manager) Stop() {
	m.mu.Lock()
	cancel := m.cancel
	runners := make([]*channelRunner, 0, len(m.runners))
	for _, r := range m.runners {
		runners = append(runners, r)
	}
	m.runners = make(map[uuid.UUID]*channelRunner)
	m.baseCtx = nil
	m.cancel = nil
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	for _, r := range runners {
		r.cancel()
	}
	m.wg.Wait()
}

// SyncAll 从数据库同步全部已启用通道：启动新增通道、停止已禁用或已删除通道。
func (m *Manager) SyncAll(ctx context.Context) error {
	if m.store == nil {
		return nil
	}
	enabled, err := m.store.ListAllEnabledRemoteChannels(ctx)
	if err != nil {
		return err
	}
	wanted := make(map[uuid.UUID]store.RemoteChannel, len(enabled))
	for _, ch := range enabled {
		wanted[ch.ID] = ch
	}

	m.mu.Lock()
	var toStop []uuid.UUID
	for id, runner := range m.runners {
		next, ok := wanted[id]
		if !ok || !next.UpdatedAt.Equal(runner.channel.UpdatedAt) {
			toStop = append(toStop, id)
		}
	}
	m.mu.Unlock()

	for _, id := range toStop {
		m.StopChannel(id)
	}
	for _, ch := range enabled {
		m.mu.Lock()
		_, running := m.runners[ch.ID]
		m.mu.Unlock()
		if !running {
			if err := m.StartChannel(ch); err != nil {
				m.logger.Warn("start remote channel failed", "channel_id", ch.ID, "platform", ch.Platform, "err", err)
			}
		}
	}
	return nil
}

// ReloadChannel 热加载单个通道的配置变更（创建、更新、启停、扫码确认后调用）。
func (m *Manager) ReloadChannel(ctx context.Context, workspaceID, channelID uuid.UUID) error {
	m.StopChannel(channelID)
	ch, err := m.store.GetRemoteChannel(ctx, workspaceID, channelID)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil
		}
		return err
	}
	if !ch.Enabled {
		_ = m.store.UpdateRemoteChannelRuntime(ctx, workspaceID, channelID, domain.ChannelRuntimeDisconnected, "", false)
		return nil
	}
	return m.StartChannel(ch)
}

// StopChannel 停止单个通道的运行协程。
func (m *Manager) StopChannel(channelID uuid.UUID) {
	m.mu.Lock()
	r, ok := m.runners[channelID]
	if ok {
		delete(m.runners, channelID)
	}
	m.mu.Unlock()
	if ok {
		r.cancel()
		<-r.done
	}
}

// StartChannel 为单个已启用通道构建适配器并启动带指数退避重连的运行循环。
func (m *Manager) StartChannel(ch store.RemoteChannel) error {
	m.mu.Lock()
	baseCtx := m.baseCtx
	factory := m.factory
	if old, exists := m.runners[ch.ID]; exists {
		delete(m.runners, ch.ID)
		m.mu.Unlock()
		old.cancel()
		<-old.done
		m.mu.Lock()
		baseCtx = m.baseCtx
		factory = m.factory
	}
	if baseCtx == nil {
		m.mu.Unlock()
		return nil
	}
	secrets, err := m.store.DecryptChannelSecrets(ch)
	if err != nil {
		m.mu.Unlock()
		_ = m.store.UpdateRemoteChannelRuntime(baseCtx, ch.WorkspaceID, ch.ID, domain.ChannelRuntimeError, err.Error(), false)
		return err
	}
	adapter, err := factory(ch, secrets)
	if err != nil {
		m.mu.Unlock()
		_ = m.store.UpdateRemoteChannelRuntime(baseCtx, ch.WorkspaceID, ch.ID, domain.ChannelRuntimeError, err.Error(), false)
		return err
	}
	runCtx, cancel := context.WithCancel(baseCtx)
	r := &channelRunner{
		channel: ch,
		adapter: adapter,
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	m.runners[ch.ID] = r
	m.wg.Add(1)
	m.mu.Unlock()

	go m.superviseRunner(runCtx, r)
	return nil
}

func (m *Manager) superviseRunner(ctx context.Context, r *channelRunner) {
	defer m.wg.Done()
	defer close(r.done)

	ch := r.channel
	backoff := 2 * time.Second
	const maxBackoff = 60 * time.Second

	rt := AdapterRuntime{
		OnInbound: func(inCtx context.Context, msg InboundMessage) error {
			return m.handleInbound(inCtx, ch.WorkspaceID, ch.ID, msg)
		},
		OnStatus: func(stCtx context.Context, status domain.ChannelRuntimeStatus, lastErr string, heartbeat bool) {
			_ = m.store.UpdateRemoteChannelRuntime(stCtx, ch.WorkspaceID, ch.ID, status, lastErr, heartbeat)
		},
		SaveState: func(stCtx context.Context, mutate func(*store.RemoteChannelRuntimeState)) error {
			latest, err := m.store.GetRemoteChannel(stCtx, ch.WorkspaceID, ch.ID)
			if err != nil {
				return err
			}
			state := latest.RuntimeState
			mutate(&state)
			return m.store.UpdateRemoteChannelRuntimeState(stCtx, ch.WorkspaceID, ch.ID, state)
		},
	}

	for {
		if ctx.Err() != nil {
			_ = m.store.UpdateRemoteChannelRuntime(context.Background(), ch.WorkspaceID, ch.ID, domain.ChannelRuntimeDisconnected, "", false)
			return
		}
		_ = m.store.UpdateRemoteChannelRuntime(ctx, ch.WorkspaceID, ch.ID, domain.ChannelRuntimeConnecting, "", false)
		startedAt := time.Now()
		err := r.adapter.Run(ctx, rt)
		if ctx.Err() != nil {
			_ = m.store.UpdateRemoteChannelRuntime(context.Background(), ch.WorkspaceID, ch.ID, domain.ChannelRuntimeDisconnected, "", false)
			return
		}
		if err != nil {
			m.logger.Warn("remote channel disconnected, will retry",
				"channel_id", ch.ID, "platform", ch.Platform, "backoff", backoff.String(), "err", err)
			_ = m.store.UpdateRemoteChannelRuntime(ctx, ch.WorkspaceID, ch.ID, domain.ChannelRuntimeError, err.Error(), false)
		}
		if time.Since(startedAt) > 30*time.Second {
			backoff = 2 * time.Second
		}
		select {
		case <-ctx.Done():
			_ = m.store.UpdateRemoteChannelRuntime(context.Background(), ch.WorkspaceID, ch.ID, domain.ChannelRuntimeDisconnected, "", false)
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (m *Manager) handleInbound(ctx context.Context, workspaceID, channelID uuid.UUID, msg InboundMessage) error {
	latest, err := m.store.GetRemoteChannel(ctx, workspaceID, channelID)
	if err != nil {
		return err
	}
	if !latest.Enabled || !PolicyAllows(latest, msg) {
		return nil
	}
	_, err = m.store.IngestRemoteChannelMessage(ctx, latest, msg.ExternalMessageID, msg.SenderID, msg.ChatID, msg.Content)
	if err != nil {
		m.logger.Warn("ingest remote channel inbound message failed",
			"channel_id", channelID, "platform", latest.Platform, "err", err)
	}
	return err
}

// SendToChannel 通过已运行或按需构造的适配器向指定通道发送一条出站消息，并更新健康状态。
func (m *Manager) SendToChannel(ctx context.Context, ch store.RemoteChannel, msg OutboundMessage) error {
	m.mu.Lock()
	var adapter Adapter
	if r, ok := m.runners[ch.ID]; ok {
		adapter = r.adapter
	}
	factory := m.factory
	m.mu.Unlock()

	if adapter == nil {
		secrets, err := m.store.DecryptChannelSecrets(ch)
		if err != nil {
			_ = m.store.TouchRemoteChannelOutbound(ctx, ch.WorkspaceID, ch.ID, err.Error())
			return err
		}
		adapter, err = factory(ch, secrets)
		if err != nil {
			_ = m.store.TouchRemoteChannelOutbound(ctx, ch.WorkspaceID, ch.ID, err.Error())
			return err
		}
	}
	if msg.ChatID == "" {
		if strings.TrimSpace(ch.Config.TargetChatID) != "" {
			msg.ChatID = strings.TrimSpace(ch.Config.TargetChatID)
		} else {
			msg.ChatID = strings.TrimSpace(ch.RuntimeState.LastChatID)
		}
	}
	if msg.ChatType == "" {
		if strings.TrimSpace(ch.Config.TargetChatType) != "" {
			msg.ChatType = strings.TrimSpace(ch.Config.TargetChatType)
		} else {
			msg.ChatType = strings.TrimSpace(ch.RuntimeState.LastChatType)
		}
	}
	if msg.ThreadID == "" {
		if ch.Config.MessageThreadID > 0 {
			msg.ThreadID = formatNonZeroInt64(ch.Config.MessageThreadID)
		} else {
			msg.ThreadID = strings.TrimSpace(ch.RuntimeState.LastThreadID)
		}
	}
	if msg.SessionWebhook == "" {
		msg.SessionWebhook = strings.TrimSpace(ch.RuntimeState.LastSessionWebhook)
	}
	if msg.ContextToken == "" && msg.ChatID != "" && ch.RuntimeState.WeixinContextTokens != nil {
		msg.ContextToken = strings.TrimSpace(ch.RuntimeState.WeixinContextTokens[msg.ChatID])
	}

	err := adapter.Send(ctx, msg)
	if err != nil {
		_ = m.store.TouchRemoteChannelOutbound(ctx, ch.WorkspaceID, ch.ID, err.Error())
		return err
	}
	_ = m.store.TouchRemoteChannelOutbound(ctx, ch.WorkspaceID, ch.ID, "")
	return nil
}

// TestChannel 测试指定通道的连通性，或在已有目标会话/Webhook 时直接发送测试消息。
func (m *Manager) TestChannel(ctx context.Context, workspaceID, channelID uuid.UUID, customMessage string) error {
	ch, err := m.store.GetRemoteChannel(ctx, workspaceID, channelID)
	if err != nil {
		return err
	}
	testText := strings.TrimSpace(customMessage)
	if testText == "" {
		testText = fmt.Sprintf("[CycleDock] 远程通道「%s」连通性测试成功（%s）", ch.Name, time.Now().Format("2006-01-02 15:04:05"))
	}

	m.mu.Lock()
	var adapter Adapter
	if r, ok := m.runners[ch.ID]; ok {
		adapter = r.adapter
	}
	factory := m.factory
	m.mu.Unlock()

	if adapter == nil {
		secrets, err := m.store.DecryptChannelSecrets(ch)
		if err != nil {
			_ = m.store.TouchRemoteChannelOutbound(ctx, workspaceID, channelID, err.Error())
			return err
		}
		adapter, err = factory(ch, secrets)
		if err != nil {
			_ = m.store.TouchRemoteChannelOutbound(ctx, workspaceID, channelID, err.Error())
			return err
		}
	}
	if err := adapter.Test(ctx, testText); err != nil {
		_ = m.store.TouchRemoteChannelOutbound(ctx, workspaceID, channelID, err.Error())
		return err
	}
	_ = m.store.TouchRemoteChannelOutbound(ctx, workspaceID, channelID, "")
	return nil
}
