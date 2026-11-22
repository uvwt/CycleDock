package channels

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/attachments"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

// AdapterFactory 根据通道配置与解密后凭据创建平台适配器，便于生产默认装配与单测注入。
type AdapterFactory func(ch store.RemoteChannel, secrets store.RemoteChannelSecrets) (Adapter, error)

// Manager 负责管理所有已启用远程通道的生命周期：
// 随服务启停、热加载配置变更、指数退避重连、连接状态上报、并发安全出站投递。
type Manager struct {
	store       *store.Store
	attachments *attachments.Service
	logger      *slog.Logger
	publicURL   string
	factory     AdapterFactory
	weixinAuth  *WeixinLoginStore
	qrAuth      *QRConnectStore

	mu      sync.Mutex
	baseCtx context.Context
	cancel  context.CancelFunc
	runners map[uuid.UUID]*channelRunner
	wg      sync.WaitGroup
}

type channelRunner struct {
	channel    store.RemoteChannel
	configHash string
	adapter    Adapter
	cancel     context.CancelFunc
	done       chan struct{}
}

// channelConfigFingerprint 计算影响通道长连接运行的配置指纹（不含运行态游标与心跳时间戳）。
// 用于避免心跳/出站/游标持久化触发 SyncAll 误判配置变更并重启正在长轮询的连接。
func channelConfigFingerprint(ch store.RemoteChannel) string {
	type snapshot struct {
		Platform         domain.ChannelPlatform    `json:"platform"`
		Role             domain.ChannelRole        `json:"role"`
		Transport        domain.ChannelTransport   `json:"transport"`
		Enabled          bool                      `json:"enabled"`
		ProjectID        uuid.UUID                 `json:"project_id"`
		TargetCardID     uuid.NullUUID             `json:"target_card_id"`
		GroupPolicy      domain.ChannelGroupPolicy `json:"group_policy"`
		AllowFrom        []string                  `json:"allow_from"`
		Config           store.RemoteChannelConfig `json:"config"`
		SecretCiphertext []byte                    `json:"secret_ciphertext"`
	}
	raw, _ := json.Marshal(snapshot{
		Platform:         ch.Platform,
		Role:             ch.Role,
		Transport:        ch.Transport,
		Enabled:          ch.Enabled,
		ProjectID:        ch.ProjectID,
		TargetCardID:     ch.TargetCardID,
		GroupPolicy:      ch.GroupPolicy,
		AllowFrom:        ch.AllowFrom,
		Config:           ch.Config,
		SecretCiphertext: ch.SecretCiphertext,
	})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func NewManager(st *store.Store, logger *slog.Logger, publicURL string) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	weixinAuth := NewWeixinLoginStore(nil, defaultWeixinBaseURL)
	return &Manager{
		store:      st,
		logger:     logger,
		publicURL:  strings.TrimRight(strings.TrimSpace(publicURL), "/"),
		factory:    DefaultAdapterFactory(nil),
		weixinAuth: weixinAuth,
		qrAuth:     NewQRConnectStore(nil, weixinAuth),
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

// SetAttachmentsService 可选注入附件存储服务，使远程通道入站的图片/纯文本附件自动落盘并绑定到对话 Turn。
func (m *Manager) SetAttachmentsService(svc *attachments.Service) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attachments = svc
}

func (m *Manager) SetWeixinLoginStore(ls *WeixinLoginStore) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ls != nil {
		m.weixinAuth = ls
		m.qrAuth = NewQRConnectStore(nil, ls)
	}
}

func (m *Manager) WeixinLoginStore() *WeixinLoginStore {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.weixinAuth
}

func (m *Manager) SetQRConnectStore(qs *QRConnectStore) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if qs != nil {
		m.qrAuth = qs
		if qs.WeixinStore() != nil {
			m.weixinAuth = qs.WeixinStore()
		}
	}
}

func (m *Manager) QRConnectStore() *QRConnectStore {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.qrAuth
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
		if !ok || channelConfigFingerprint(next) != runner.configHash {
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
		channel:    ch,
		configHash: channelConfigFingerprint(ch),
		adapter:    adapter,
		cancel:     cancel,
		done:       make(chan struct{}),
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

	chLogger := m.logger.With(
		"channel_id", ch.ID,
		"platform", ch.Platform,
		"channel_name", ch.Name,
		"role", ch.Role,
		"transport", ch.Transport,
	)
	chLogger.Info("remote channel runner started")

	var statusMu sync.Mutex
	var lastReportedStatus domain.ChannelRuntimeStatus
	var lastReportedErr string

	rt := AdapterRuntime{
		Logger: chLogger,
		OnInbound: func(inCtx context.Context, msg InboundMessage) error {
			return m.handleInbound(inCtx, ch.WorkspaceID, ch.ID, msg)
		},
		OnStatus: func(stCtx context.Context, status domain.ChannelRuntimeStatus, lastErr string, heartbeat bool) {
			statusMu.Lock()
			changed := status != lastReportedStatus || lastErr != lastReportedErr
			if changed {
				lastReportedStatus = status
				lastReportedErr = lastErr
			}
			statusMu.Unlock()
			if changed {
				if lastErr != "" {
					chLogger.Warn("remote channel status changed with error", "status", status, "err", lastErr)
				} else {
					chLogger.Info("remote channel status changed", "status", status)
				}
			}
			_ = m.store.UpdateRemoteChannelRuntime(stCtx, ch.WorkspaceID, ch.ID, status, lastErr, heartbeat)
		},
		SaveState: func(stCtx context.Context, mutate func(*store.RemoteChannelRuntimeState)) error {
			latest, err := m.store.GetRemoteChannel(stCtx, ch.WorkspaceID, ch.ID)
			if err != nil {
				chLogger.Warn("load remote channel before saving runtime state failed", "err", err)
				return err
			}
			state := latest.RuntimeState
			mutate(&state)
			if err := m.store.UpdateRemoteChannelRuntimeState(stCtx, ch.WorkspaceID, ch.ID, state); err != nil {
				chLogger.Warn("save remote channel runtime state failed", "err", err)
				return err
			}
			return nil
		},
	}

	for {
		if ctx.Err() != nil {
			chLogger.Info("remote channel runner stopped")
			_ = m.store.UpdateRemoteChannelRuntime(context.Background(), ch.WorkspaceID, ch.ID, domain.ChannelRuntimeDisconnected, "", false)
			return
		}
		_ = m.store.UpdateRemoteChannelRuntime(ctx, ch.WorkspaceID, ch.ID, domain.ChannelRuntimeConnecting, "", false)
		startedAt := time.Now()
		err := r.adapter.Run(ctx, rt)
		if ctx.Err() != nil {
			chLogger.Info("remote channel runner stopped")
			_ = m.store.UpdateRemoteChannelRuntime(context.Background(), ch.WorkspaceID, ch.ID, domain.ChannelRuntimeDisconnected, "", false)
			return
		}
		if err != nil {
			chLogger.Warn("remote channel disconnected, will retry", "backoff", backoff.String(), "err", err)
			_ = m.store.UpdateRemoteChannelRuntime(ctx, ch.WorkspaceID, ch.ID, domain.ChannelRuntimeError, err.Error(), false)
		} else {
			chLogger.Info("remote channel adapter run exited cleanly, will reconnect", "backoff", backoff.String())
		}
		if time.Since(startedAt) > 30*time.Second {
			backoff = 2 * time.Second
		}
		select {
		case <-ctx.Done():
			chLogger.Info("remote channel runner stopped during backoff")
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
		m.logger.Warn("load remote channel for inbound message failed",
			"channel_id", channelID, "err", err)
		return err
	}
	chLogger := m.logger.With(
		"channel_id", channelID,
		"platform", latest.Platform,
		"channel_name", latest.Name,
		"chat_type", msg.ChatType,
		"chat_id_hint", MaskRemoteIdentifier(msg.ChatID),
		"sender_hint", MaskRemoteIdentifier(msg.SenderID),
		"external_msg_id", msg.ExternalMessageID,
		"content_len", len([]rune(strings.TrimSpace(msg.Content))),
		"attachments_count", len(msg.Attachments),
	)
	if !latest.Enabled {
		chLogger.Debug("ignore inbound message because remote channel is disabled")
		return nil
	}
	if !PolicyAllows(latest, msg) {
		chLogger.Info("remote channel inbound message ignored by policy",
			"role", latest.Role, "group_policy", latest.GroupPolicy, "mentioned", msg.Mentioned, "allow_from_count", len(latest.AllowFrom))
		return nil
	}
	chLogger.Info("remote channel inbound message received")
	// 收到首条合法入站消息且未配置默认推送目标时，自动记忆 target_chat_id（及飞书/Telegram 等会话类型/Thread），
	// 这样无论扫码建卡还是 Telegram 单填 Bot Token，都不需要用户手填 Chat ID / OpenID。
	chatID := strings.TrimSpace(msg.ChatID)
	if chatID != "" && strings.TrimSpace(latest.Config.TargetChatID) == "" {
		chatType := strings.TrimSpace(msg.ChatType)
		threadID, _ := strconv.ParseInt(strings.TrimSpace(msg.ThreadID), 10, 64)
		if updated, saveErr := m.store.UpdateRemoteChannelSecretsAndConfig(
			ctx,
			workspaceID,
			channelID,
			store.RemoteChannelSecrets{},
			func(cfg *store.RemoteChannelConfig) {
				if strings.TrimSpace(cfg.TargetChatID) == "" {
					cfg.TargetChatID = chatID
				}
				if chatType != "" && strings.TrimSpace(cfg.TargetChatType) == "" {
					cfg.TargetChatType = chatType
				}
				if threadID > 0 && cfg.MessageThreadID == 0 {
					cfg.MessageThreadID = threadID
				}
			},
		); saveErr == nil {
			latest = updated
			m.mu.Lock()
			if r, ok := m.runners[channelID]; ok {
				r.channel = updated
				r.configHash = channelConfigFingerprint(updated)
			}
			m.mu.Unlock()
			chLogger.Info("auto-bound remote channel target_chat_id from first inbound message")
		} else {
			chLogger.Warn("auto-bind remote channel target_chat_id failed", "err", saveErr)
		}
	}
	// 附带远端媒体时先做一次只读去重检查，避免重连重放同一 external_message_id 时重复下载和产生孤立附件。
	if len(msg.Attachments) > 0 {
		if existing, found, checkErr := m.store.HasIngestedRemoteChannelMessage(ctx, latest.WorkspaceID, latest.ID, msg.ExternalMessageID); checkErr == nil && found {
			chLogger.Info("remote channel inbound message with attachments already ingested",
				"card_id", existing.CardID, "turn_id", existing.TurnID, "reused", true)
			return nil
		}
	}

	content, attachmentIDs := m.prepareInboundAttachments(ctx, latest, msg, chLogger)
	res, err := m.store.IngestRemoteChannelMessageWithAttachments(ctx, latest, msg.ExternalMessageID, msg.SenderID, msg.ChatID, content, attachmentIDs)
	if err != nil {
		chLogger.Warn("ingest remote channel inbound message failed", "err", err)
		_ = m.store.UpdateRemoteChannelRuntime(ctx, workspaceID, channelID, latest.RuntimeStatus, "入站消息处理失败："+err.Error(), false)
		return err
	}
	chLogger.Info("remote channel inbound message ingested",
		"card_id", res.CardID, "turn_id", res.TurnID, "reused", res.Reused, "uploaded_attachments", len(attachmentIDs))
	return nil
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
		if !errors.Is(err, ErrTargetNotBound) {
			_ = m.store.TouchRemoteChannelOutbound(ctx, ch.WorkspaceID, ch.ID, err.Error())
		}
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
