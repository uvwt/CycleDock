package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

// Dispatcher 挂在与 Web Push 一致的终态消息扫描分发链路：
// 1. main_chat：
//
//   - 同步项目主聊天的最终回复气泡（严格过滤 end_turn=false 中间进度气泡）；
//     其中本通道入站回复与所有后台/系统/定时/触发器产生的终态回复默认始终推送，
//     仅 Web 端（或非本通道）人类发起对话的 AI 最终回复受 mirror_web_messages 开关控制；
//     不再回传用户在 Web 端输入的评论原文。
//
//   - 项目内关键高价值提醒：仅当 Todo 回调目标不是主聊天时推送 Review 模板提醒（避免与主聊天终态回调总结重复）；
//     任务失败/超时熔断与 Tool Guard 审批挂起保持提醒（主聊天已承接失败回调的子任务不重复刷屏）。
//
//     2. scheduled_push：仅同步所选项目或指定 Ongoing 卡片的定时巡检/最终执行汇报（支持 all / warnings_only），
//     绝不写入主聊天 Session 上下文。
type Dispatcher struct {
	store     *store.Store
	manager   *Manager
	logger    *slog.Logger
	publicURL string
}

func NewDispatcher(st *store.Store, mgr *Manager, logger *slog.Logger, publicURL string) *Dispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Dispatcher{
		store:     st,
		manager:   mgr,
		logger:    logger,
		publicURL: strings.TrimRight(strings.TrimSpace(publicURL), "/"),
	}
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
		switch ch.Role {
		case domain.ChannelRoleMainChat:
			if err := d.deliverMainChatChannel(ctx, ch); err != nil {
				d.logger.Warn("deliver main_chat remote channel failed", "channel_id", ch.ID, "err", err)
			}
		case domain.ChannelRoleScheduledPush:
			if err := d.deliverScheduledPushChannel(ctx, ch); err != nil {
				d.logger.Warn("deliver scheduled_push remote channel failed", "channel_id", ch.ID, "err", err)
			}
		}
	}
	return nil
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

// shouldDeliverMainChatMessage 判断主聊天的一条终态回复气泡是否应推送到指定远程通道。
// 规则：
//  1. 源自当前远程通道入站的 Turn 始终推送；
//  2. 无 attempt_id 或非 human 来源（system/schedule/trigger 等系统回调、定时与主动消息）始终推送；
//  3. 非本通道发起的 human Turn（如 Web 主聊天或其它通道）仅在开启 mirror_web_messages 时推送。
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

// supervisorCallbackTargetsMainChat 判断子任务卡片的 supervisor_callback 回调目标是否落到项目主聊天。
// 语义与 internal/store/supervisor_callback.go 中的 supervisorDiscussionID 完全一致：
//   - 若卡片存在活跃的 source_discussion_id，则回调到该讨论卡（当且仅当它就是主聊天时返回 true）；
//   - 若 source_discussion_id 为空或对应普通讨论已归档/不可用，则回落到项目活跃的 main_discussion_id。
func supervisorCallbackTargetsMainChat(card cardSummary) bool {
	if card.Kind != domain.CardKindTodo && card.Kind != domain.CardKindOngoing {
		return false
	}
	if !card.MainDiscussionID.Valid || !card.MainDiscussionActive {
		return false
	}
	if card.SourceDiscussionID.Valid && card.SourceDiscussionActive {
		return card.SourceDiscussionID.UUID == card.MainDiscussionID.UUID
	}
	return true
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

// hasSupervisorFailureCallbackOnMainChat 检查某子任务失败 Attempt 是否已成功向项目主聊天入队监督回调 Turn，
// 若已入队则由主聊天系统回调统一汇报，避免再发一条重复的模板失败告警。
func (d *Dispatcher) hasSupervisorFailureCallbackOnMainChat(
	ctx context.Context,
	workspaceID uuid.UUID,
	card cardSummary,
	attemptID uuid.UUID,
) bool {
	if !card.MainDiscussionID.Valid || d.store == nil {
		return false
	}
	key := store.SupervisorFailureCallbackKey(card.ID, attemptID)
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
	`, workspaceID, card.MainDiscussionID.UUID, key).Scan(&exists)
	if err != nil {
		return false
	}
	return exists
}

func (d *Dispatcher) deliverMainChatChannel(ctx context.Context, ch store.RemoteChannel) error {
	cards, err := d.listProjectCards(ctx, ch.WorkspaceID, ch.ProjectID)
	if err != nil {
		return err
	}
	for _, card := range cards {
		cursor, exists, err := d.store.GetChannelOutboundCursor(ctx, ch.WorkspaceID, ch.ID, card.ID)
		if err != nil {
			return err
		}
		if !exists {
			// 首次遇到该卡片：将历史水位锚定在通道创建时刻之前，仅同步通道创建之后产生的新事件，避免刷屏重放旧历史。
			cursor = d.seedInitialCursor(ctx, ch, card.ID)
			if err := d.store.UpsertChannelOutboundCursor(ctx, cursor); err != nil {
				return err
			}
		}

		dirty := false
		cardURL := ChannelCardURL(d.publicURL, card.ProjectID, card.ID)

		// 1. 项目主聊天：同步最终回复气泡（严格过滤 end_turn=false 中间进度气泡）。
		// - 本通道入站回复、以及所有后台/系统/定时/触发器触发的终态回复（含无 attempt_id 的系统终态消息）默认始终推送；
		// - 仅非本通道人类发起的对话（如 Web 主聊天）受 mirror_web_messages 开关控制；
		// - 彻底不再回传用户在 Web 端输入的评论原文（仅推进 comment 游标保持兼容）。
		if card.IsMainChat {
			if msgs, err := d.listCardEventsAfter(ctx, ch.WorkspaceID, card.ID, "message", cursor.LastMessageSeq); err == nil {
				for _, ev := range msgs {
					cursor.LastMessageSeq = ev.Seq
					dirty = true
					var payload struct {
						Type    string `json:"type"`
						Content string `json:"content"`
						EndTurn bool   `json:"end_turn"`
						Widget  *struct {
							Question string `json:"question"`
						} `json:"widget"`
					}
					if err := json.Unmarshal(ev.Message, &payload); err != nil {
						continue
					}
					// 铁律：严禁把中间进度气泡（end_turn=false）推送到外部 IM。
					if !payload.EndTurn && payload.Type != "choice" {
						continue
					}
					var turnOrigin store.AttemptTurnOrigin
					var foundOrigin bool
					if ev.AttemptID.Valid {
						turnOrigin, foundOrigin, _ = d.store.AttemptTurnOrigin(ctx, ch.WorkspaceID, ev.AttemptID.UUID)
					}
					if !shouldDeliverMainChatMessage(ch.ID, ch.MirrorWebMessages, ev.AttemptID.Valid, turnOrigin, foundOrigin) {
						continue
					}
					body := strings.TrimSpace(payload.Content)
					if body == "" {
						body = strings.TrimSpace(ev.Content)
					}
					if body == "" && payload.Widget != nil {
						body = strings.TrimSpace(payload.Widget.Question)
					}
					if body == "" {
						continue
					}
					if err := d.manager.SendToChannel(ctx, ch, OutboundMessage{
						Text:    body,
						CardURL: cardURL,
					}); err != nil {
						return err
					}
				}
			}

			// 推进 comment 游标但不回传人类评论原文（即便开启 mirror_web_messages 也只推 AI 最终回复）。
			if comments, err := d.listCardEventsAfter(ctx, ch.WorkspaceID, card.ID, string(domain.EventTypeComment), cursor.LastCommentSeq); err == nil {
				for _, ev := range comments {
					cursor.LastCommentSeq = ev.Seq
					dirty = true
				}
			}
		}

		// 2. 项目内关键高价值提醒：Todo 进入 Review 待验收。
		// 当 Todo 的 supervisor_callback 回调目标就是项目主聊天时，主聊天会自动收到系统终态回调并回复总结，
		// 因此这里跳过重复的纯模板提醒；仅当回调落到非主聊天讨论（或项目无活跃主聊天）时保留模板提醒。
		if statuses, err := d.listCardEventsAfter(ctx, ch.WorkspaceID, card.ID, string(domain.EventTypeStatusChange), cursor.LastStatusSeq); err == nil {
			for _, ev := range statuses {
				cursor.LastStatusSeq = ev.Seq
				dirty = true
				lower := strings.ToLower(strings.TrimSpace(ev.Content))
				if strings.HasSuffix(lower, "-> review") || strings.Contains(lower, "-> review") {
					if card.Kind == domain.CardKindTodo && supervisorCallbackTargetsMainChat(card) {
						continue
					}
					text := fmt.Sprintf("【待验收】%s 已进入 Review，请及时核查确认。", card.Title)
					if err := d.manager.SendToChannel(ctx, ch, OutboundMessage{
						Text:    text,
						CardURL: cardURL,
					}); err != nil {
						return err
					}
				}
			}
		}

		// 3. 项目内关键高价值提醒：任务失败/超时熔断。
		// 若该失败 Attempt 已成功入队回调到项目主聊天（主聊天将自动处理/总结并同步到本通道），则跳过重复模板提醒。
		if attemptID, status, errSummary, ok := d.latestCardAttempt(ctx, ch.WorkspaceID, card.ID); ok {
			if !cursor.LastAttemptID.Valid || attemptID != cursor.LastAttemptID.UUID || status != cursor.LastAttemptStatus {
				cursor.LastAttemptID = uuid.NullUUID{UUID: attemptID, Valid: true}
				cursor.LastAttemptStatus = status
				dirty = true
				if status == string(domain.AttemptStatusFailed) || status == string(domain.AttemptStatusExpired) {
					if supervisorCallbackTargetsMainChat(card) && d.hasSupervisorFailureCallbackOnMainChat(ctx, ch.WorkspaceID, card, attemptID) {
						// 主聊天已收到本次失败回调，等待主聊天 AI 总结推送，避免模板提醒重复刷屏。
					} else {
						reason := strings.TrimSpace(errSummary)
						if reason == "" {
							reason = status
						}
						text := fmt.Sprintf("【执行异常】%s 执行失败/超时：%s", card.Title, reason)
						if err := d.manager.SendToChannel(ctx, ch, OutboundMessage{
							Text:    text,
							CardURL: cardURL,
						}); err != nil {
							return err
						}
					}
				}
			}
		}

		// 4. 项目内关键高价值提醒：Tool Guard 审批挂起。
		if approvalID, toolName, reason, ok := d.latestPendingApproval(ctx, ch.WorkspaceID, card.ID); ok {
			if !cursor.LastApprovalID.Valid || cursor.LastApprovalID.UUID != approvalID {
				cursor.LastApprovalID = uuid.NullUUID{UUID: approvalID, Valid: true}
				dirty = true
				text := fmt.Sprintf("【安全审批挂起】%s 请求执行受保护工具 `%s`：%s", card.Title, toolName, strings.TrimSpace(reason))
				if err := d.manager.SendToChannel(ctx, ch, OutboundMessage{
					Text:    text,
					CardURL: cardURL,
				}); err != nil {
					return err
				}
			}
		}

		if dirty {
			if err := d.store.UpsertChannelOutboundCursor(ctx, cursor); err != nil {
				return err
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
	for _, card := range cards {
		if card.Kind != domain.CardKindOngoing {
			continue
		}
		if ch.TargetCardID.Valid && card.ID != ch.TargetCardID.UUID {
			continue
		}
		cursor, exists, err := d.store.GetChannelOutboundCursor(ctx, ch.WorkspaceID, ch.ID, card.ID)
		if err != nil {
			return err
		}
		if !exists {
			cursor = d.seedInitialCursor(ctx, ch, card.ID)
			if err := d.store.UpsertChannelOutboundCursor(ctx, cursor); err != nil {
				return err
			}
		}

		dirty := false
		cardURL := ChannelCardURL(d.publicURL, card.ProjectID, card.ID)

		// 1. Ongoing 巡检汇报（checkin）
		if checkins, err := d.listCardEventsAfter(ctx, ch.WorkspaceID, card.ID, string(domain.EventTypeCheckin), cursor.LastCheckinSeq); err == nil {
			for _, ev := range checkins {
				cursor.LastCheckinSeq = ev.Seq
				dirty = true
				status := strings.TrimSpace(ev.CheckinStatus)
				if ch.PushFilter == domain.ChannelPushFilterWarningsOnly &&
					status != string(domain.CheckinWarning) &&
					status != string(domain.CheckinCritical) {
					continue
				}
				text := fmt.Sprintf("【定时巡检·%s】%s\n%s", strings.ToUpper(status), card.Title, strings.TrimSpace(ev.Content))
				if err := d.manager.SendToChannel(ctx, ch, OutboundMessage{
					Text:    text,
					CardURL: cardURL,
				}); err != nil {
					return err
				}
			}
		}

		// 2. Ongoing 定时执行的终态回复气泡（仅 push_filter=all 时同步普通终态汇总，严禁同步中间进度气泡）
		if msgs, err := d.listCardEventsAfter(ctx, ch.WorkspaceID, card.ID, "message", cursor.LastMessageSeq); err == nil {
			for _, ev := range msgs {
				cursor.LastMessageSeq = ev.Seq
				dirty = true
				if ch.PushFilter == domain.ChannelPushFilterWarningsOnly {
					continue
				}
				var payload struct {
					Content string `json:"content"`
					EndTurn bool   `json:"end_turn"`
				}
				if err := json.Unmarshal(ev.Message, &payload); err != nil {
					continue
				}
				if !payload.EndTurn {
					continue
				}
				body := strings.TrimSpace(payload.Content)
				if body == "" {
					body = strings.TrimSpace(ev.Content)
				}
				if body == "" {
					continue
				}
				text := fmt.Sprintf("【定时任务汇报】%s\n%s", card.Title, body)
				if err := d.manager.SendToChannel(ctx, ch, OutboundMessage{
					Text:    text,
					CardURL: cardURL,
				}); err != nil {
					return err
				}
			}
		}

		// 3. Ongoing 定时执行失败/超时熔断告警（无论 all 还是 warnings_only 都必须推送）
		if attemptID, status, errSummary, ok := d.latestCardAttempt(ctx, ch.WorkspaceID, card.ID); ok {
			if !cursor.LastAttemptID.Valid || attemptID != cursor.LastAttemptID.UUID || status != cursor.LastAttemptStatus {
				cursor.LastAttemptID = uuid.NullUUID{UUID: attemptID, Valid: true}
				cursor.LastAttemptStatus = status
				dirty = true
				if status == string(domain.AttemptStatusFailed) || status == string(domain.AttemptStatusExpired) {
					reason := strings.TrimSpace(errSummary)
					if reason == "" {
						reason = status
					}
					text := fmt.Sprintf("【定时任务异常】%s 执行失败/超时：%s", card.Title, reason)
					if err := d.manager.SendToChannel(ctx, ch, OutboundMessage{
						Text:    text,
						CardURL: cardURL,
					}); err != nil {
						return err
					}
				}
			}
		}

		if dirty {
			if err := d.store.UpsertChannelOutboundCursor(ctx, cursor); err != nil {
				return err
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
