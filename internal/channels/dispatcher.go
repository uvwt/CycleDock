package channels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

const (
	defaultDispatcherBackoffBase     = 10 * time.Second
	defaultDispatcherBackoffMax      = 5 * time.Minute
	defaultDispatcherMaxEventRetries = 5

	// 微信 iLink ret=-2 (prepare failed) 存在账号级限流惩罚升级：在惩罚期内频繁调用
	// sendmessage 会把原本约 1 小时的冷却窗口不断拉长至十余小时。
	// 因此命中 ErrWeixinSendThrottled 后直接进入长冷却（15m -> 30m -> 60m），停止每 5 分钟探测。
	defaultWeixinThrottleBackoffBase = 15 * time.Minute
	defaultWeixinThrottleBackoffMax  = 60 * time.Minute
)

type channelBackoffState struct {
	failures      int
	nextRetryAt   time.Time
	lastInboundAt time.Time
}

// Dispatcher 挂在与 Web Push 一致的终态消息扫描分发链路：
// 1. main_chat（双向对话通道）：
//
//   - 通道专属普通讨论卡（ch.ConversationCardID）：始终回推最终回复气泡（含通过该通道发问的回复、
//     该讨论派生的子任务回调、以及用户在 Web 端打开该专属讨论卡发起对话的回复）；严格过滤 end_turn=false 中间进度气泡。
//   - 项目主聊天（card.IsMainChat）：单向推送主聊天终态回复（绝不写回本通道专属讨论卡上下文），推送时带「【主聊天】」标识；
//     其中系统/定时/触发器/子任务回调终态回复默认推送，Web 主聊天的人工问答最终回复在开启 mirror_web_messages 时单向推送。
//   - 项目内高价值提醒：仅当 Todo/Ongoing 的监督回调目标既不是本通道专属讨论卡也不是项目主聊天时推送【待验收】/【执行异常】模板提醒，
//     【安全审批挂起】始终保留并附带卡片直达链接；日常对话回复不附带冗余卡片链接。
//   - 每成功发送或跳过一条事件即刻落盘出站游标，避免中途报错导致已发送消息重复刷屏。
//
// 2. scheduled_push（定时推送）：
//   - 仅同步所选项目或指定 Ongoing 卡片的定时巡检/最终执行汇报（支持 all / warnings_only），
//     正常健康巡检与普通汇报精简不附冗余链接，WARNING/CRITICAL 与执行异常告警附带直达链接。
type Dispatcher struct {
	store     *store.Store
	manager   *Manager
	logger    *slog.Logger
	publicURL string

	mu              sync.Mutex
	backoffBase     time.Duration
	backoffMax      time.Duration
	maxEventRetries int
	nowFn           func() time.Time
	channelBackoff  map[uuid.UUID]*channelBackoffState
	eventFailures   map[string]int
	unboundLogged   map[uuid.UUID]bool
}

func NewDispatcher(st *store.Store, mgr *Manager, logger *slog.Logger, publicURL string) *Dispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Dispatcher{
		store:           st,
		manager:         mgr,
		logger:          logger,
		publicURL:       strings.TrimRight(strings.TrimSpace(publicURL), "/"),
		backoffBase:     defaultDispatcherBackoffBase,
		backoffMax:      defaultDispatcherBackoffMax,
		maxEventRetries: defaultDispatcherMaxEventRetries,
		nowFn:           time.Now,
		channelBackoff:  make(map[uuid.UUID]*channelBackoffState),
		eventFailures:   make(map[string]int),
		unboundLogged:   make(map[uuid.UUID]bool),
	}
}

// SetBackoffBase 允许测试配置通道失败退避基线（0 表示禁用等待延迟）。
func (d *Dispatcher) SetBackoffBase(base time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.backoffBase = base
}

// SetMaxEventRetries 允许测试配置单条事件连续失败后的死信跳过阈值。
func (d *Dispatcher) SetMaxEventRetries(maxRetries int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if maxRetries > 0 {
		d.maxEventRetries = maxRetries
	}
}

// SetNowFunc 允许测试注入可控时钟以验证退避窗口行为。
func (d *Dispatcher) SetNowFunc(fn func() time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if fn != nil {
		d.nowFn = fn
	}
}

func (d *Dispatcher) now() time.Time {
	d.mu.Lock()
	fn := d.nowFn
	d.mu.Unlock()
	if fn != nil {
		return fn().UTC()
	}
	return time.Now().UTC()
}

// channelHasOutboundTarget 判断通道是否已经具备可发送的目标会话（或 Webhook URL）。
// 双向通道刚启用但用户尚未发来首条消息且未手填 target_chat_id 时返回 false，
// 以便 Dispatcher 优雅跳过并推进初始化游标，避免每 2 秒狂打 WARN 日志。
func channelHasOutboundTarget(ch store.RemoteChannel) bool {
	if ch.Transport == domain.ChannelTransportWebhook {
		return true
	}
	if strings.TrimSpace(ch.Config.TargetChatID) != "" {
		return true
	}
	if strings.TrimSpace(ch.Config.WeixinUserID) != "" {
		return true
	}
	if strings.TrimSpace(ch.RuntimeState.LastChatID) != "" {
		return true
	}
	if strings.TrimSpace(ch.RuntimeState.LastSessionWebhook) != "" {
		return true
	}
	return false
}

func channelInboundTimestamp(ch store.RemoteChannel) time.Time {
	if ch.LastInboundAt != nil {
		return ch.LastInboundAt.UTC()
	}
	return time.Time{}
}

func (d *Dispatcher) shouldSkipByBackoff(ch store.RemoteChannel) bool {
	now := d.now()
	inboundAt := channelInboundTimestamp(ch)
	d.mu.Lock()
	defer d.mu.Unlock()
	st, ok := d.channelBackoff[ch.ID]
	if !ok {
		return false
	}
	// 一旦收到新的用户入站消息（例如微信用户重新发消息刷新了 context_token），立即解除退避恢复秒级回复。
	if !inboundAt.IsZero() && inboundAt.After(st.lastInboundAt) {
		delete(d.channelBackoff, ch.ID)
		return false
	}
	return now.Before(st.nextRetryAt)
}

func (d *Dispatcher) recordChannelSuccess(channelID uuid.UUID) {
	d.mu.Lock()
	delete(d.channelBackoff, channelID)
	delete(d.unboundLogged, channelID)
	d.mu.Unlock()
}

func (d *Dispatcher) recordChannelFailure(ch store.RemoteChannel, err error) time.Duration {
	now := d.now()
	inboundAt := channelInboundTimestamp(ch)
	d.mu.Lock()
	defer d.mu.Unlock()
	st, ok := d.channelBackoff[ch.ID]
	if !ok {
		st = &channelBackoffState{}
		d.channelBackoff[ch.ID] = st
	}
	st.failures++
	st.lastInboundAt = inboundAt
	if d.backoffBase <= 0 {
		st.nextRetryAt = now
		return 0
	}
	if errors.Is(err, ErrWeixinSendThrottled) {
		// 微信限流惩罚期专用长冷却：15m -> 30m -> 60m（封顶 60m），避免短间隔反复探测续期惩罚。
		wait := defaultWeixinThrottleBackoffBase
		for i := 1; i < st.failures; i++ {
			wait *= 2
			if wait >= defaultWeixinThrottleBackoffMax {
				wait = defaultWeixinThrottleBackoffMax
				break
			}
		}
		st.nextRetryAt = now.Add(wait)
		return wait
	}
	// 指数退避：10s -> 30s -> 60s -> 120s -> 300s（封顶 5 分钟），彻底避免异常通道每 2 秒死打外部 API。
	wait := d.backoffBase
	for i := 1; i < st.failures; i++ {
		if i == 1 {
			wait *= 3
		} else {
			wait *= 2
		}
		if wait >= d.backoffMax {
			wait = d.backoffMax
			break
		}
	}
	st.nextRetryAt = now.Add(wait)
	return wait
}

// sendOutboundEvent 封装带「单条最大重试与死信跳过」保护的出站发送：
// - 返回 (true, nil)：发送成功或该条消息连续失败已达死信阈值被强制跳过，调用方必须立即落盘推进游标；
// - 返回 (false, err)：本次失败尚未达死信阈值，保留该条游标待退避窗口后重试（已发的前置消息游标不受影响）。
func (d *Dispatcher) sendOutboundEvent(
	ctx context.Context,
	ch store.RemoteChannel,
	cardID uuid.UUID,
	eventKey string,
	msg OutboundMessage,
) (bool, error) {
	err := d.manager.SendToChannel(ctx, ch, msg)
	if err == nil {
		d.mu.Lock()
		delete(d.eventFailures, eventKey)
		delete(d.channelBackoff, ch.ID)
		d.mu.Unlock()
		return true, nil
	}
	if errors.Is(err, ErrTargetNotBound) {
		return false, err
	}
	if errors.Is(err, ErrWeixinProactiveQuotaExceeded) {
		// 微信主动推送触发本地 24h 安全配额时，直接跳过该条非关键主动推送并推进游标，
		// 不污染通道退避状态，也不影响用户专属对话卡的实时回复。
		d.logger.Warn("remote channel proactive push skipped by local safety quota",
			"channel_id", ch.ID,
			"platform", ch.Platform,
			"card_id", cardID,
			"event_key", eventKey,
			"err", err,
		)
		return true, nil
	}
	if errors.Is(err, ErrWeixinSendThrottled) {
		// 微信 ret=-2 属于网关账号级限流冷却：
		// - 专属对话卡的用户实时回复不计入死信丢弃，待冷却解除或用户新入站消息后继续投递；
		// - 非专属卡片（主聊天镜像/历史通知）照常受 maxEventRetries 限制，避免旧积压长期卡住通道。
		if ch.ConversationCardID.Valid && cardID == ch.ConversationCardID.UUID {
			return false, err
		}
	}
	d.mu.Lock()
	d.eventFailures[eventKey]++
	attempts := d.eventFailures[eventKey]
	maxRetries := d.maxEventRetries
	if maxRetries <= 0 {
		maxRetries = defaultDispatcherMaxEventRetries
	}
	if attempts >= maxRetries {
		delete(d.eventFailures, eventKey)
		d.mu.Unlock()
		d.logger.Error("remote channel outbound event exceeded max retries; skipping to unblock channel",
			"channel_id", ch.ID,
			"platform", ch.Platform,
			"card_id", cardID,
			"event_key", eventKey,
			"attempts", attempts,
			"err", err,
		)
		return true, nil
	}
	d.mu.Unlock()
	return false, err
}

// Run 周期扫描并分发远程通道出站消息（间隔与 Web Push 一致）。
func (d *Dispatcher) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := d.DeliverOnce(ctx); err != nil && ctx.Err() == nil {
				d.logger.Warn("remote channel dispatch tick failed", "err", err)
			}
		}
	}
}

// DeliverOnce 执行一轮完整的出站扫描与投递，供后台定时循环与集成测试直接调用。
func (d *Dispatcher) DeliverOnce(ctx context.Context) error {
	if d.store == nil || d.manager == nil {
		return nil
	}
	channels, err := d.store.ListAllEnabledRemoteChannels(ctx)
	if err != nil {
		return err
	}
	for _, ch := range channels {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !channelHasOutboundTarget(ch) {
			d.handleUnboundChannel(ctx, ch)
			continue
		}
		if d.shouldSkipByBackoff(ch) {
			continue
		}
		var deliverErr error
		switch ch.Role {
		case domain.ChannelRoleMainChat:
			deliverErr = d.deliverMainChatChannel(ctx, ch)
		case domain.ChannelRoleScheduledPush:
			deliverErr = d.deliverScheduledPushChannel(ctx, ch)
		default:
			continue
		}
		if deliverErr == nil {
			d.recordChannelSuccess(ch.ID)
			continue
		}
		if errors.Is(deliverErr, ErrTargetNotBound) {
			d.handleUnboundChannel(ctx, ch)
			continue
		}
		wait := d.recordChannelFailure(ch, deliverErr)
		d.logger.Warn("deliver remote channel failed, backing off",
			"channel_id", ch.ID,
			"platform", ch.Platform,
			"role", ch.Role,
			"next_retry_in", wait.String(),
			"err", deliverErr,
		)
	}
	return nil
}

// handleUnboundChannel 对尚未绑定目标会话的通道静默推进初始水位并跳过投递，
// 防止每 2 秒狂打 target empty WARN，也避免日后首次绑定时把此前积压的历史消息全部轰炸出去。
func (d *Dispatcher) handleUnboundChannel(ctx context.Context, ch store.RemoteChannel) {
	d.mu.Lock()
	alreadyLogged := d.unboundLogged[ch.ID]
	if !alreadyLogged {
		d.unboundLogged[ch.ID] = true
	}
	d.mu.Unlock()
	if !alreadyLogged {
		d.logger.Info("remote channel has no bound target chat yet; skipping outbound until first inbound or target_chat_id configured",
			"channel_id", ch.ID, "platform", ch.Platform, "role", ch.Role)
	}
	cards, err := d.listProjectCards(ctx, ch.WorkspaceID, ch.ProjectID)
	if err != nil {
		return
	}
	for _, card := range cards {
		if ch.Role == domain.ChannelRoleScheduledPush {
			if card.Kind != domain.CardKindOngoing {
				continue
			}
			if ch.TargetCardID.Valid && card.ID != ch.TargetCardID.UUID {
				continue
			}
		}
		cursor := d.latestCardCursorWatermark(ctx, ch, card.ID)
		_ = d.persistCursor(ctx, cursor)
	}
}

// latestCardCursorWatermark 将游标水位推进到该卡片当前最新已产生事件，供未绑定目标通道静默锚定。
func (d *Dispatcher) latestCardCursorWatermark(ctx context.Context, ch store.RemoteChannel, cardID uuid.UUID) store.RemoteChannelOutboundCursor {
	maxSeq := func(evType string) int64 {
		var seq int64
		_ = d.store.Pool().QueryRow(ctx, `
			SELECT COALESCE(MAX(sequence), 0)
			FROM events
			WHERE workspace_id = $1 AND card_id = $2 AND event_type = $3
		`, ch.WorkspaceID, cardID, evType).Scan(&seq)
		return seq
	}
	cursor := store.RemoteChannelOutboundCursor{
		WorkspaceID:    ch.WorkspaceID,
		ChannelID:      ch.ID,
		CardID:         cardID,
		LastMessageSeq: maxSeq("message"),
		LastCommentSeq: maxSeq(string(domain.EventTypeComment)),
		LastCheckinSeq: maxSeq(string(domain.EventTypeCheckin)),
		LastStatusSeq:  maxSeq(string(domain.EventTypeStatusChange)),
	}
	if attemptID, status, _, ok := d.latestCardAttempt(ctx, ch.WorkspaceID, cardID); ok {
		cursor.LastAttemptID = uuid.NullUUID{UUID: attemptID, Valid: true}
		cursor.LastAttemptStatus = status
	}
	if approvalID, _, _, ok := d.latestPendingApproval(ctx, ch.WorkspaceID, cardID); ok {
		cursor.LastApprovalID = uuid.NullUUID{UUID: approvalID, Valid: true}
	}
	return cursor
}

type cardSummary struct {
	ID                     uuid.UUID
	ProjectID              uuid.UUID
	Kind                   domain.CardKind
	Title                  string
	IsMainChat             bool
	MainDiscussionID       uuid.NullUUID
	MainDiscussionActive   bool
	SourceDiscussionID     uuid.NullUUID
	SourceDiscussionActive bool
}

// shouldDeliverMainChatMessage 判断项目主聊天的一条终态回复气泡是否应单向推送到指定远程通道。
// 规则：
//  1. 源自当前远程通道入站的 Turn（兼容升级前历史遗留数据）始终推送；
//  2. 无 attempt_id 或非 human 来源（system/schedule/trigger 等系统回调、定时与主动消息）始终推送；
//  3. 非本通道发起的 human Turn（如 Web 主聊天人工问答）仅在开启 mirror_web_messages 时单向推送。
func shouldDeliverMainChatMessage(
	channelID uuid.UUID,
	mirrorWebMessages bool,
	hasAttemptID bool,
	turnOrigin store.AttemptTurnOrigin,
	foundOrigin bool,
) bool {
	if !hasAttemptID {
		return true
	}
	if !foundOrigin {
		return mirrorWebMessages
	}
	if turnOrigin.HasOrigin && turnOrigin.OriginChannelID == channelID {
		return true
	}
	if turnOrigin.Source != "" && turnOrigin.Source != domain.TurnSourceHuman {
		return true
	}
	return mirrorWebMessages
}

// supervisorCallbackTargetDiscussion 返回子任务卡片的 supervisor_callback 实际会回调到的讨论卡 ID。
// 语义与 internal/store/supervisor_callback.go 中的 supervisorDiscussionID 保持一致：
//   - 若卡片存在活跃的 source_discussion_id，则回调到该讨论卡；
//   - 否则回落到项目活跃的 main_discussion_id。
func supervisorCallbackTargetDiscussion(card cardSummary) (uuid.UUID, bool) {
	if card.Kind != domain.CardKindTodo && card.Kind != domain.CardKindOngoing {
		return uuid.Nil, false
	}
	if card.SourceDiscussionID.Valid && card.SourceDiscussionActive {
		return card.SourceDiscussionID.UUID, true
	}
	if card.MainDiscussionID.Valid && card.MainDiscussionActive {
		return card.MainDiscussionID.UUID, true
	}
	return uuid.Nil, false
}

// supervisorCallbackTargetsMainChat 判断子任务卡片的 supervisor_callback 回调目标是否落到项目主聊天。
func supervisorCallbackTargetsMainChat(card cardSummary) bool {
	targetID, ok := supervisorCallbackTargetDiscussion(card)
	return ok && card.MainDiscussionID.Valid && targetID == card.MainDiscussionID.UUID
}

// supervisorCallbackHandledByChannel 判断子任务卡片的监督回调是否会被当前通道自动收到 AI 终态汇报
// （包括回调到本通道专属讨论卡，或回调到项目主聊天——因为主聊天系统终态回调默认单向推送到本通道）。
func supervisorCallbackHandledByChannel(ch store.RemoteChannel, card cardSummary) (uuid.UUID, bool) {
	targetID, ok := supervisorCallbackTargetDiscussion(card)
	if !ok {
		return uuid.Nil, false
	}
	if ch.ConversationCardID.Valid && targetID == ch.ConversationCardID.UUID {
		return targetID, true
	}
	if card.MainDiscussionID.Valid && targetID == card.MainDiscussionID.UUID {
		return targetID, true
	}
	return uuid.Nil, false
}

// formatMainChatMirrorText 为来自项目主聊天的单向推送补充简短来源标识，避免与本通道专属讨论卡对话混淆。
func formatMainChatMirrorText(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	if strings.HasPrefix(body, "【主聊天】") {
		return body
	}
	return "【主聊天】\n" + body
}

// shouldAttachCardURLForCheckin 决定定时巡检 checkin 是否附带直达卡片链接：仅异常/告警附带，正常巡检保持精简。
func shouldAttachCardURLForCheckin(status string) bool {
	return status == string(domain.CheckinWarning) || status == string(domain.CheckinCritical)
}

type eventRow struct {
	ID            uuid.UUID
	Seq           int64
	EventType     string
	AttemptID     uuid.NullUUID
	Content       string
	CheckinStatus string
	Message       json.RawMessage
}

func (d *Dispatcher) listProjectCards(ctx context.Context, workspaceID, projectID uuid.UUID) ([]cardSummary, error) {
	rows, err := d.store.Pool().Query(ctx, `
		SELECT c.id, c.project_id, c.kind, c.title,
		       (p.main_discussion_id IS NOT NULL AND p.main_discussion_id = c.id) AS is_main_chat,
		       p.main_discussion_id,
		       (mc.id IS NOT NULL AND mc.kind = 'discussion' AND COALESCE(mc.discussion_state, '') = 'active') AS main_discussion_active,
		       c.source_discussion_id,
		       (sc.id IS NOT NULL AND sc.kind = 'discussion' AND COALESCE(sc.discussion_state, '') = 'active') AS source_discussion_active
		FROM cards c
		JOIN projects p ON p.workspace_id = c.workspace_id AND p.id = c.project_id
		LEFT JOIN cards mc ON mc.workspace_id = p.workspace_id AND mc.id = p.main_discussion_id
		LEFT JOIN cards sc ON sc.workspace_id = c.workspace_id AND sc.id = c.source_discussion_id
		WHERE c.workspace_id = $1 AND c.project_id = $2
		ORDER BY c.created_at, c.id
	`, workspaceID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]cardSummary, 0)
	for rows.Next() {
		var c cardSummary
		var kind string
		if err := rows.Scan(
			&c.ID, &c.ProjectID, &kind, &c.Title, &c.IsMainChat,
			&c.MainDiscussionID, &c.MainDiscussionActive,
			&c.SourceDiscussionID, &c.SourceDiscussionActive,
		); err != nil {
			return nil, err
		}
		c.Kind = domain.CardKind(kind)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (d *Dispatcher) listCardEventsAfter(ctx context.Context, workspaceID, cardID uuid.UUID, eventType string, afterSeq int64) ([]eventRow, error) {
	rows, err := d.store.Pool().Query(ctx, `
		SELECT id, sequence, event_type, attempt_id, COALESCE(content, ''),
		       COALESCE(checkin_status, ''), COALESCE(message, '{}'::jsonb)
		FROM events
		WHERE workspace_id = $1 AND card_id = $2 AND event_type = $3 AND sequence > $4
		ORDER BY sequence ASC
		LIMIT 50
	`, workspaceID, cardID, eventType, afterSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]eventRow, 0)
	for rows.Next() {
		var ev eventRow
		if err := rows.Scan(&ev.ID, &ev.Seq, &ev.EventType, &ev.AttemptID, &ev.Content, &ev.CheckinStatus, &ev.Message); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (d *Dispatcher) latestCardAttempt(ctx context.Context, workspaceID, cardID uuid.UUID) (uuid.UUID, string, string, bool) {
	var attemptID uuid.UUID
	var status, errSummary string
	err := d.store.Pool().QueryRow(ctx, `
		SELECT a.id, a.status, COALESCE(a.error_summary, '')
		FROM turn_attempts a
		WHERE a.workspace_id = $1 AND a.card_id = $2
		ORDER BY a.created_at DESC, a.attempt_number DESC
		LIMIT 1
	`, workspaceID, cardID).Scan(&attemptID, &status, &errSummary)
	if err != nil {
		return uuid.Nil, "", "", false
	}
	return attemptID, status, errSummary, true
}

func (d *Dispatcher) latestPendingApproval(ctx context.Context, workspaceID, cardID uuid.UUID) (uuid.UUID, string, string, bool) {
	var approvalID uuid.UUID
	var toolName, reason string
	err := d.store.Pool().QueryRow(ctx, `
		SELECT id, tool_name, reason
		FROM card_tool_approvals
		WHERE workspace_id = $1 AND card_id = $2 AND status = 'pending'
		ORDER BY created_at DESC
		LIMIT 1
	`, workspaceID, cardID).Scan(&approvalID, &toolName, &reason)
	if err != nil {
		return uuid.Nil, "", "", false
	}
	return approvalID, toolName, reason, true
}

// hasSupervisorFailureCallbackOnDiscussion 检查某子任务失败 Attempt 是否已成功向目标讨论卡入队监督回调 Turn，
// 若已入队则由该讨论卡的系统回调统一汇报，避免再发一条重复的模板失败告警。
func (d *Dispatcher) hasSupervisorFailureCallbackOnDiscussion(
	ctx context.Context,
	workspaceID, discussionID, cardID, attemptID uuid.UUID,
) bool {
	if discussionID == uuid.Nil || d.store == nil {
		return false
	}
	key := store.SupervisorFailureCallbackKey(cardID, attemptID)
	var exists bool
	err := d.store.Pool().QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM card_turns
			WHERE workspace_id = $1
			  AND card_id = $2
			  AND source = 'system'
			  AND idempotency_key = $3
		)
	`, workspaceID, discussionID, key).Scan(&exists)
	if err != nil {
		return false
	}
	return exists
}

// persistCursor 立即将单条推进后的游标落盘。
// 这是根治远程通道「发送中途报错 -> 下一轮重复刷屏前面已发送消息」的关键保证。
func (d *Dispatcher) persistCursor(ctx context.Context, cursor store.RemoteChannelOutboundCursor) error {
	return d.store.UpsertChannelOutboundCursor(ctx, cursor)
}

// prioritizeMainChatCards 将当前通道的专属讨论卡排在项目主聊天及其他子任务卡片之前。
// 这样用户在外部 IM（尤其是存在主动推送配额的微信）发消息后，实时对话回复永远优先于旧主聊天/通知积压，
// 不会被历史推送抢占额度或提前触发限流熔断。
func prioritizeMainChatCards(ch store.RemoteChannel, cards []cardSummary) []cardSummary {
	if !ch.ConversationCardID.Valid || len(cards) <= 1 {
		return cards
	}
	ownID := ch.ConversationCardID.UUID
	ownIdx := -1
	for i, c := range cards {
		if c.ID == ownID {
			ownIdx = i
			break
		}
	}
	if ownIdx <= 0 {
		return cards
	}
	reordered := make([]cardSummary, 0, len(cards))
	reordered = append(reordered, cards[ownIdx])
	reordered = append(reordered, cards[:ownIdx]...)
	reordered = append(reordered, cards[ownIdx+1:]...)
	return reordered
}

func (d *Dispatcher) deliverMainChatChannel(ctx context.Context, ch store.RemoteChannel) error {
	cards, err := d.listProjectCards(ctx, ch.WorkspaceID, ch.ProjectID)
	if err != nil {
		return err
	}
	cards = prioritizeMainChatCards(ch, cards)
	var firstCardErr error
	for _, card := range cards {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := d.deliverMainChatCard(ctx, ch, card); err != nil {
			d.logger.Warn("deliver main_chat card failed",
				"channel_id", ch.ID, "card_id", card.ID, "err", err)
			if firstCardErr == nil {
				firstCardErr = err
			}
			// 微信 ret=-2 限流惩罚一旦触发，立即中止本轮该通道其余卡片发送，
			// 避免同一轮内连打多张卡片把网关惩罚窗口继续拉长。
			if errors.Is(err, ErrWeixinSendThrottled) {
				return err
			}
			// 单张卡片失败不中断其它卡片已就绪事件的投递；已发送的游标也已经逐条落盘。
			continue
		}
	}
	return firstCardErr
}

func (d *Dispatcher) deliverMainChatCard(ctx context.Context, ch store.RemoteChannel, card cardSummary) error {
	cursor, exists, err := d.store.GetChannelOutboundCursor(ctx, ch.WorkspaceID, ch.ID, card.ID)
	if err != nil {
		return err
	}
	if !exists {
		// 首次遇到该卡片：将历史水位锚定在通道创建时刻之前，仅同步通道创建之后产生的新事件，避免刷屏重放旧历史。
		cursor = d.seedInitialCursor(ctx, ch, card.ID)
		if err := d.persistCursor(ctx, cursor); err != nil {
			return err
		}
	}

	cardURL := ChannelCardURL(d.publicURL, card.ProjectID, card.ID)
	isOwnConversation := ch.ConversationCardID.Valid && card.ID == ch.ConversationCardID.UUID

	// 1. 本通道专属讨论卡或项目主聊天的终态回复气泡：
	// - 专属讨论卡（isOwnConversation）：始终将所有终态回复回推到本通道，不附带冗余卡片链接；
	// - 项目主聊天（card.IsMainChat）：仅单向推送，带「【主聊天】」标识，绝不写回专属讨论卡上下文；
	//   系统/定时/子任务回调终态回复默认推送，Web 主聊天的人工问答回复受 mirror_web_messages 控制；
	// - 严格过滤 end_turn=false 中间进度气泡；每发完或跳过一条即刻落盘游标，杜绝刷屏。
	if isOwnConversation || card.IsMainChat {
		if msgs, err := d.listCardEventsAfter(ctx, ch.WorkspaceID, card.ID, "message", cursor.LastMessageSeq); err == nil {
			for _, ev := range msgs {
				var payload struct {
					Type    string `json:"type"`
					Content string `json:"content"`
					EndTurn bool   `json:"end_turn"`
					Widget  *struct {
						Question string `json:"question"`
					} `json:"widget"`
				}
				if err := json.Unmarshal(ev.Message, &payload); err != nil {
					cursor.LastMessageSeq = ev.Seq
					if saveErr := d.persistCursor(ctx, cursor); saveErr != nil {
						return saveErr
					}
					continue
				}
				// 铁律：严禁把中间进度气泡（end_turn=false）推送到外部 IM。
				if !payload.EndTurn && payload.Type != "choice" {
					cursor.LastMessageSeq = ev.Seq
					if saveErr := d.persistCursor(ctx, cursor); saveErr != nil {
						return saveErr
					}
					continue
				}
				if !isOwnConversation {
					var turnOrigin store.AttemptTurnOrigin
					var foundOrigin bool
					if ev.AttemptID.Valid {
						turnOrigin, foundOrigin, _ = d.store.AttemptTurnOrigin(ctx, ch.WorkspaceID, ev.AttemptID.UUID)
					}
					if !shouldDeliverMainChatMessage(ch.ID, ch.MirrorWebMessages, ev.AttemptID.Valid, turnOrigin, foundOrigin) {
						cursor.LastMessageSeq = ev.Seq
						if saveErr := d.persistCursor(ctx, cursor); saveErr != nil {
							return saveErr
						}
						continue
					}
				}
				body := strings.TrimSpace(payload.Content)
				if body == "" {
					body = strings.TrimSpace(ev.Content)
				}
				if body == "" && payload.Widget != nil {
					body = strings.TrimSpace(payload.Widget.Question)
				}
				body = SanitizeExternalCardLinks(body)
				if body == "" {
					cursor.LastMessageSeq = ev.Seq
					if saveErr := d.persistCursor(ctx, cursor); saveErr != nil {
						return saveErr
					}
					continue
				}
				outText := body
				isProactive := !isOwnConversation
				if !isOwnConversation && card.IsMainChat {
					outText = formatMainChatMirrorText(body)
				}
				// 日常对话回复与主聊天单向镜像均保持干净纯文本，不再末尾拼接冗余「查看卡片：https://...」。
				eventKey := fmt.Sprintf("msg:%s:%s:%d", ch.ID, card.ID, ev.Seq)
				if _, err := d.sendOutboundEvent(ctx, ch, card.ID, eventKey, OutboundMessage{
					Text:      outText,
					Proactive: isProactive,
				}); err != nil {
					return err
				}
				cursor.LastMessageSeq = ev.Seq
				if saveErr := d.persistCursor(ctx, cursor); saveErr != nil {
					return saveErr
				}
			}
		}

		// 推进 comment 游标但不回传人类评论原文（即便开启 mirror_web_messages 也只推 AI 最终回复）。
		if comments, err := d.listCardEventsAfter(ctx, ch.WorkspaceID, card.ID, string(domain.EventTypeComment), cursor.LastCommentSeq); err == nil && len(comments) > 0 {
			cursor.LastCommentSeq = comments[len(comments)-1].Seq
			if saveErr := d.persistCursor(ctx, cursor); saveErr != nil {
				return saveErr
			}
		}
	}

	// 2. 项目内关键高价值提醒：Todo 进入 Review 待验收（保留卡片直达链接，方便去 Web 验收）。
	// 当 Todo 的监督回调目标落到本通道专属讨论卡或项目主聊天时，对应讨论卡会收到系统终态回调并回复总结，
	// 因此跳过重复的纯模板提醒；仅当回调落到其它普通讨论卡（或无活跃目标讨论）时发送模板提醒。
	if statuses, err := d.listCardEventsAfter(ctx, ch.WorkspaceID, card.ID, string(domain.EventTypeStatusChange), cursor.LastStatusSeq); err == nil {
		for _, ev := range statuses {
			lower := strings.ToLower(strings.TrimSpace(ev.Content))
			if strings.HasSuffix(lower, "-> review") || strings.Contains(lower, "-> review") {
				_, handledByChannel := supervisorCallbackHandledByChannel(ch, card)
				if !(card.Kind == domain.CardKindTodo && handledByChannel) {
					text := fmt.Sprintf("【待验收】%s 已进入 Review，请及时核查确认。", card.Title)
					eventKey := fmt.Sprintf("status:%s:%s:%d", ch.ID, card.ID, ev.Seq)
					if _, err := d.sendOutboundEvent(ctx, ch, card.ID, eventKey, OutboundMessage{
						Text:      text,
						CardURL:   cardURL,
						Proactive: true,
					}); err != nil {
						return err
					}
				}
			}
			cursor.LastStatusSeq = ev.Seq
			if saveErr := d.persistCursor(ctx, cursor); saveErr != nil {
				return saveErr
			}
		}
	}

	// 3. 项目内关键高价值提醒：任务失败/超时熔断（保留卡片直达链接）。
	// 若该失败 Attempt 已成功入队回调到本通道专属讨论卡或项目主聊天，则跳过重复模板提醒。
	if attemptID, status, errSummary, ok := d.latestCardAttempt(ctx, ch.WorkspaceID, card.ID); ok {
		if !cursor.LastAttemptID.Valid || attemptID != cursor.LastAttemptID.UUID || status != cursor.LastAttemptStatus {
			if status == string(domain.AttemptStatusFailed) || status == string(domain.AttemptStatusExpired) {
				targetDiscID, handledByChannel := supervisorCallbackHandledByChannel(ch, card)
				if !(handledByChannel && d.hasSupervisorFailureCallbackOnDiscussion(ctx, ch.WorkspaceID, targetDiscID, card.ID, attemptID)) {
					reason := strings.TrimSpace(errSummary)
					if reason == "" {
						reason = status
					}
					text := fmt.Sprintf("【执行异常】%s 执行失败/超时：%s", card.Title, reason)
					eventKey := fmt.Sprintf("attempt:%s:%s:%s:%s", ch.ID, card.ID, attemptID, status)
					if _, err := d.sendOutboundEvent(ctx, ch, card.ID, eventKey, OutboundMessage{
						Text:      text,
						CardURL:   cardURL,
						Proactive: true,
					}); err != nil {
						return err
					}
				}
			}
			cursor.LastAttemptID = uuid.NullUUID{UUID: attemptID, Valid: true}
			cursor.LastAttemptStatus = status
			if saveErr := d.persistCursor(ctx, cursor); saveErr != nil {
				return saveErr
			}
		}
	}

	// 4. 项目内关键高价值提醒：Tool Guard 审批挂起（始终保留卡片直达链接）。
	if approvalID, toolName, reason, ok := d.latestPendingApproval(ctx, ch.WorkspaceID, card.ID); ok {
		if !cursor.LastApprovalID.Valid || cursor.LastApprovalID.UUID != approvalID {
			text := fmt.Sprintf("【安全审批挂起】%s 请求执行受保护工具 `%s`：%s", card.Title, toolName, strings.TrimSpace(reason))
			eventKey := fmt.Sprintf("approval:%s:%s:%s", ch.ID, card.ID, approvalID)
			if _, err := d.sendOutboundEvent(ctx, ch, card.ID, eventKey, OutboundMessage{
				Text:      text,
				CardURL:   cardURL,
				Proactive: !isOwnConversation,
			}); err != nil {
				return err
			}
			cursor.LastApprovalID = uuid.NullUUID{UUID: approvalID, Valid: true}
			if saveErr := d.persistCursor(ctx, cursor); saveErr != nil {
				return saveErr
			}
		}
	}

	return nil
}

func (d *Dispatcher) deliverScheduledPushChannel(ctx context.Context, ch store.RemoteChannel) error {
	cards, err := d.listProjectCards(ctx, ch.WorkspaceID, ch.ProjectID)
	if err != nil {
		return err
	}
	var firstCardErr error
	for _, card := range cards {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if card.Kind != domain.CardKindOngoing {
			continue
		}
		if ch.TargetCardID.Valid && card.ID != ch.TargetCardID.UUID {
			continue
		}
		if err := d.deliverScheduledPushCard(ctx, ch, card); err != nil {
			d.logger.Warn("deliver scheduled_push card failed",
				"channel_id", ch.ID, "card_id", card.ID, "err", err)
			if firstCardErr == nil {
				firstCardErr = err
			}
			if errors.Is(err, ErrWeixinSendThrottled) {
				return err
			}
			continue
		}
	}
	return firstCardErr
}

func (d *Dispatcher) deliverScheduledPushCard(ctx context.Context, ch store.RemoteChannel, card cardSummary) error {
	cursor, exists, err := d.store.GetChannelOutboundCursor(ctx, ch.WorkspaceID, ch.ID, card.ID)
	if err != nil {
		return err
	}
	if !exists {
		cursor = d.seedInitialCursor(ctx, ch, card.ID)
		if err := d.persistCursor(ctx, cursor); err != nil {
			return err
		}
	}

	cardURL := ChannelCardURL(d.publicURL, card.ProjectID, card.ID)

	// 1. Ongoing 巡检汇报（checkin）：仅 WARNING/CRITICAL 附带卡片直达链接，正常健康巡检精简不附链接。
	if checkins, err := d.listCardEventsAfter(ctx, ch.WorkspaceID, card.ID, string(domain.EventTypeCheckin), cursor.LastCheckinSeq); err == nil {
		for _, ev := range checkins {
			status := strings.TrimSpace(ev.CheckinStatus)
			if !(ch.PushFilter == domain.ChannelPushFilterWarningsOnly &&
				status != string(domain.CheckinWarning) &&
				status != string(domain.CheckinCritical)) {
				text := fmt.Sprintf("【定时巡检·%s】%s\n%s", strings.ToUpper(status), card.Title, SanitizeExternalCardLinks(strings.TrimSpace(ev.Content)))
				outURL := ""
				if shouldAttachCardURLForCheckin(status) {
					outURL = cardURL
				}
				eventKey := fmt.Sprintf("checkin:%s:%s:%d", ch.ID, card.ID, ev.Seq)
				if _, err := d.sendOutboundEvent(ctx, ch, card.ID, eventKey, OutboundMessage{
					Text:      text,
					CardURL:   outURL,
					Proactive: true,
				}); err != nil {
					return err
				}
			}
			cursor.LastCheckinSeq = ev.Seq
			if saveErr := d.persistCursor(ctx, cursor); saveErr != nil {
				return saveErr
			}
		}
	}

	// 2. Ongoing 定时执行的终态回复气泡（仅 push_filter=all 时同步普通终态汇总，精简不附冗余链接）
	if msgs, err := d.listCardEventsAfter(ctx, ch.WorkspaceID, card.ID, "message", cursor.LastMessageSeq); err == nil {
		for _, ev := range msgs {
			if ch.PushFilter != domain.ChannelPushFilterWarningsOnly {
				var payload struct {
					Content string `json:"content"`
					EndTurn bool   `json:"end_turn"`
				}
				if err := json.Unmarshal(ev.Message, &payload); err == nil && payload.EndTurn {
					body := strings.TrimSpace(payload.Content)
					if body == "" {
						body = strings.TrimSpace(ev.Content)
					}
					body = SanitizeExternalCardLinks(body)
					if body != "" {
						text := fmt.Sprintf("【定时任务汇报】%s\n%s", card.Title, body)
						eventKey := fmt.Sprintf("sched_msg:%s:%s:%d", ch.ID, card.ID, ev.Seq)
						if _, err := d.sendOutboundEvent(ctx, ch, card.ID, eventKey, OutboundMessage{
							Text:      text,
							Proactive: true,
						}); err != nil {
							return err
						}
					}
				}
			}
			cursor.LastMessageSeq = ev.Seq
			if saveErr := d.persistCursor(ctx, cursor); saveErr != nil {
				return saveErr
			}
		}
	}

	// 3. Ongoing 定时执行失败/超时熔断告警（无论 all 还是 warnings_only 都必须推送并附带直达链接）
	if attemptID, status, errSummary, ok := d.latestCardAttempt(ctx, ch.WorkspaceID, card.ID); ok {
		if !cursor.LastAttemptID.Valid || attemptID != cursor.LastAttemptID.UUID || status != cursor.LastAttemptStatus {
			if status == string(domain.AttemptStatusFailed) || status == string(domain.AttemptStatusExpired) {
				reason := strings.TrimSpace(errSummary)
				if reason == "" {
					reason = status
				}
				text := fmt.Sprintf("【定时任务异常】%s 执行失败/超时：%s", card.Title, reason)
				eventKey := fmt.Sprintf("sched_attempt:%s:%s:%s:%s", ch.ID, card.ID, attemptID, status)
				if _, err := d.sendOutboundEvent(ctx, ch, card.ID, eventKey, OutboundMessage{
					Text:      text,
					CardURL:   cardURL,
					Proactive: true,
				}); err != nil {
					return err
				}
			}
			cursor.LastAttemptID = uuid.NullUUID{UUID: attemptID, Valid: true}
			cursor.LastAttemptStatus = status
			if saveErr := d.persistCursor(ctx, cursor); saveErr != nil {
				return saveErr
			}
		}
	}

	return nil
}

// seedInitialCursor 在首次初始化某卡片游标时，仅跳过早于通道创建时间之前的旧历史事件，
// 通道创建之后才产生的事件仍会被正常投递。
func (d *Dispatcher) seedInitialCursor(ctx context.Context, ch store.RemoteChannel, cardID uuid.UUID) store.RemoteChannelOutboundCursor {
	seqBefore := func(evType string) int64 {
		var seq int64
		_ = d.store.Pool().QueryRow(ctx, `
			SELECT COALESCE(MAX(sequence), 0)
			FROM events
			WHERE workspace_id = $1 AND card_id = $2 AND event_type = $3 AND created_at < $4
		`, ch.WorkspaceID, cardID, evType, ch.CreatedAt).Scan(&seq)
		return seq
	}
	return store.RemoteChannelOutboundCursor{
		WorkspaceID:    ch.WorkspaceID,
		ChannelID:      ch.ID,
		CardID:         cardID,
		LastMessageSeq: seqBefore("message"),
		LastCommentSeq: seqBefore(string(domain.EventTypeComment)),
		LastCheckinSeq: seqBefore(string(domain.EventTypeCheckin)),
		LastStatusSeq:  seqBefore(string(domain.EventTypeStatusChange)),
	}
}
