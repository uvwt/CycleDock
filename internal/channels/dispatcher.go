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
// 1. main_chat：仅同步项目主聊天的最终回复气泡（严禁同步 end_turn=false 中间进度气泡）、
//    可选镜像 Web 人类消息（防回环）、以及项目内 Review 待验收、任务失败/超时熔断、Tool Guard 审批挂起。
// 2. scheduled_push：仅同步所选项目或指定 Ongoing 卡片的定时巡检/最终执行汇报（支持 all / warnings_only），
//    绝不写入主聊天 Session 上下文。
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
	ID         uuid.UUID
	ProjectID  uuid.UUID
	Kind       domain.CardKind
	Title      string
	IsMainChat bool
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
		       (p.main_discussion_id IS NOT NULL AND p.main_discussion_id = c.id) AS is_main_chat
		FROM cards c
		JOIN projects p ON p.workspace_id = c.workspace_id AND p.id = c.project_id
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
		if err := rows.Scan(&c.ID, &c.ProjectID, &kind, &c.Title, &c.IsMainChat); err != nil {
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

		// 1. 项目主聊天：同步最终回复气泡（严格过滤 end_turn=false 中间进度气泡）与可选 Web 人类消息镜像。
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
					// 未开启 Web 镜像时，仅回传来自本远程通道触发的终态回复。
					if !ch.MirrorWebMessages {
						if !ev.AttemptID.Valid {
							continue
						}
						originCh, ok, _ := d.store.AttemptOriginChannelID(ctx, ch.WorkspaceID, ev.AttemptID.UUID)
						if !ok || originCh != ch.ID {
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

			// 开启双向镜像开关时，将人类在 Web 主聊天发送的新消息单向镜像到已绑定 IM（严格防回环）。
			if comments, err := d.listCardEventsAfter(ctx, ch.WorkspaceID, card.ID, string(domain.EventTypeComment), cursor.LastCommentSeq); err == nil {
				for _, ev := range comments {
					cursor.LastCommentSeq = ev.Seq
					dirty = true
					if !ch.MirrorWebMessages {
						continue
					}
					originCh, fromRemote, _ := d.store.EventOriginChannelID(ctx, ch.WorkspaceID, ev.ID)
					if fromRemote && originCh == ch.ID {
						// 来自当前通道自身的入站消息绝不回传，杜绝回环。
						continue
					}
					text := strings.TrimSpace(ev.Content)
					if text == "" {
						continue
					}
					if err := d.manager.SendToChannel(ctx, ch, OutboundMessage{
						Text:    "[Web 主聊天] " + text,
						CardURL: cardURL,
					}); err != nil {
						return err
					}
				}
			}
		}

		// 2. 项目内关键高价值提醒：Todo 进入 Review 待验收。
		if statuses, err := d.listCardEventsAfter(ctx, ch.WorkspaceID, card.ID, string(domain.EventTypeStatusChange), cursor.LastStatusSeq); err == nil {
			for _, ev := range statuses {
				cursor.LastStatusSeq = ev.Seq
				dirty = true
				lower := strings.ToLower(strings.TrimSpace(ev.Content))
				if strings.HasSuffix(lower, "-> review") || strings.Contains(lower, "-> review") {
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
