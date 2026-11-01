package store

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/uvwt/CycleDock/internal/domain"
)

const (
	// BuiltInProactiveTurnPrefix 标识主聊天内置定时任务产生的系统 Turn。
	// 该前缀用于在前端视图查询中隐藏内部触发输入与后台运行态，最终只呈现生成的单条回复气泡。
	BuiltInProactiveTurnPrefix = "builtin-proactive:"

	builtInProactiveTimezone      = "Asia/Shanghai"
	builtInProactiveJitter        = time.Hour
	builtInProactiveGrace         = 30 * time.Minute
	builtInProactiveMinProjectAge = time.Minute
)

// BuiltInProactiveSlot 定义主聊天每日内置的三个主动触发时段。
// 每个时段以本地整点为中心，在前后各 1 小时范围内按项目与日期做确定性浮动。
type BuiltInProactiveSlot struct {
	Key   string
	Label string
	Hour  int
}

var builtInProactiveSlots = []BuiltInProactiveSlot{
	{Key: "morning_10", Label: "早上10点", Hour: 10},
	{Key: "afternoon_14", Label: "下午2点", Hour: 14},
	{Key: "evening_20", Label: "晚上8点", Hour: 20},
}

func BuiltInProactiveSlots() []BuiltInProactiveSlot {
	out := make([]BuiltInProactiveSlot, len(builtInProactiveSlots))
	copy(out, builtInProactiveSlots)
	return out
}

func IsBuiltInProactiveTurnKey(idempotencyKey string) bool {
	return strings.HasPrefix(strings.TrimSpace(idempotencyKey), BuiltInProactiveTurnPrefix)
}

// BuiltInProactiveTriggerWindow 计算指定项目在当天某内置时段的实际触发时间与有效窗口上限。
// 使用 SHA-256(projectID + date + slotKey) 生成 [-1h, +1h] 的确定性浮动偏移：
// 同一项目同一天在多次轮询或进程重启后保持同一个触发点，不同日期和不同项目则自然分散。
func BuiltInProactiveTriggerWindow(
	projectID uuid.UUID,
	now time.Time,
	slot BuiltInProactiveSlot,
) (triggerAt, windowEnd time.Time, dateKey string, err error) {
	loc, err := time.LoadLocation(builtInProactiveTimezone)
	if err != nil {
		return time.Time{}, time.Time{}, "", fmt.Errorf("load built-in proactive timezone: %w", err)
	}
	local := now.In(loc)
	dateKey = local.Format("2006-01-02")
	base := time.Date(local.Year(), local.Month(), local.Day(), slot.Hour, 0, 0, 0, loc)

	sum := sha256.Sum256([]byte(fmt.Sprintf("builtin-proactive-jitter:%s:%s:%s", projectID, dateKey, slot.Key)))
	jitterSpanSeconds := uint64(2*builtInProactiveJitter/time.Second) + 1
	offsetSeconds := int64(binary.BigEndian.Uint64(sum[:8])%jitterSpanSeconds) - int64(builtInProactiveJitter/time.Second)
	offset := time.Duration(offsetSeconds) * time.Second

	triggerAt = base.Add(offset).UTC()
	windowEnd = base.Add(builtInProactiveJitter + builtInProactiveGrace).UTC()
	return triggerAt, windowEnd, dateKey, nil
}

type builtInProactiveProject struct {
	ID        uuid.UUID
	CreatedAt time.Time
}

// ProcessBuiltInProactiveTurns 扫描当前 Workspace 下到期的主聊天内置定时任务并入队。
// 该定时任务不写入 card_schedules，不在前端计划页或待执行队列展示；
// 仅在项目已有用户消息记录、主聊天当前空闲且到达当天浮动触发点时创建一条系统 Turn。
func (s *Store) ProcessBuiltInProactiveTurns(
	ctx context.Context,
	workspaceID uuid.UUID,
	now time.Time,
) ([]uuid.UUID, error) {
	now = now.UTC()

	var runnable bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM workspace_agent_settings cfg
			JOIN workspace_model_routes r
			  ON r.workspace_id = cfg.workspace_id
			 AND r.enabled
			WHERE cfg.workspace_id = $1
		)
	`, workspaceID).Scan(&runnable); err != nil {
		return nil, err
	}
	if !runnable {
		return nil, nil
	}

	rows, err := s.pool.Query(ctx, `
		SELECT p.id, p.created_at
		FROM projects p
		WHERE p.workspace_id = $1
		  AND p.created_at <= $2
		  AND EXISTS (
			SELECT 1
			FROM card_turns t
			JOIN cards c
			  ON c.workspace_id = t.workspace_id
			 AND c.id = t.card_id
			WHERE t.workspace_id = p.workspace_id
			  AND c.project_id = p.id
			  AND t.source = 'human'
		  )
		ORDER BY p.created_at, p.id
	`, workspaceID, now.Add(-builtInProactiveMinProjectAge))
	if err != nil {
		return nil, err
	}
	projects, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (builtInProactiveProject, error) {
		var item builtInProactiveProject
		err := row.Scan(&item.ID, &item.CreatedAt)
		return item, err
	})
	if err != nil {
		return nil, err
	}

	var created []uuid.UUID
	for _, project := range projects {
		for _, slot := range builtInProactiveSlots {
			triggerAt, windowEnd, dateKey, err := BuiltInProactiveTriggerWindow(project.ID, now, slot)
			if err != nil {
				return created, err
			}
			// 仅处理项目创建之后、已到达触发时刻且未超过当天宽限窗口的时段；历史错过不补跑。
			if triggerAt.Before(project.CreatedAt) || now.Before(triggerAt) || now.After(windowEnd) {
				continue
			}
			idempotencyKey := fmt.Sprintf("%s%s:%s:%s", BuiltInProactiveTurnPrefix, project.ID, dateKey, slot.Key)
			turnID, ok, err := s.tryCreateBuiltInProactiveTurn(ctx, workspaceID, project.ID, slot, now, idempotencyKey)
			if err != nil {
				return created, err
			}
			if ok {
				created = append(created, turnID)
			}
		}
	}
	if len(created) > 0 {
		s.notifyWorkReady()
	}
	return created, nil
}

func (s *Store) tryCreateBuiltInProactiveTurn(
	ctx context.Context,
	workspaceID, projectID uuid.UUID,
	slot BuiltInProactiveSlot,
	now time.Time,
	idempotencyKey string,
) (uuid.UUID, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, false, err
	}
	defer tx.Rollback(ctx)

	currentMain, err := lockProjectMainDiscussion(ctx, tx, workspaceID, projectID)
	if errors.Is(err, domain.ErrNotFound) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	cycleAgentID, err := ensureCycleAgentSource(ctx, tx, workspaceID)
	if err != nil {
		return uuid.Nil, false, err
	}
	if _, found, err := findTurnByIdempotency(ctx, tx, workspaceID, cycleAgentID, idempotencyKey); err != nil {
		return uuid.Nil, false, err
	} else if found {
		return uuid.Nil, false, nil
	}

	mainCardID, _, err := ensureMainDiscussionTx(ctx, tx, workspaceID, projectID, cycleAgentID, currentMain)
	if err != nil {
		return uuid.Nil, false, err
	}
	if err := lockCard(ctx, tx, workspaceID, mainCardID); err != nil {
		return uuid.Nil, false, err
	}

	// 主聊天已有进行中/等待回复的 Attempt 或未认领的排队输入时，本轮礼让用户对话，稍后窗口内再试。
	var busy bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM turn_attempts a
			WHERE a.workspace_id = $1
			  AND a.card_id = $2
			  AND a.status IN ('queued', 'running', 'waiting_human')
		) OR EXISTS (
			SELECT 1
			FROM card_turns t
			WHERE t.workspace_id = $1
			  AND t.card_id = $2
			  AND t.cancelled_at IS NULL
			  AND NOT EXISTS (
				SELECT 1
				FROM turn_attempts a
				WHERE a.workspace_id = t.workspace_id
				  AND a.turn_id = t.id
			  )
		)
	`, workspaceID, mainCardID).Scan(&busy); err != nil {
		return uuid.Nil, false, err
	}
	if busy {
		return uuid.Nil, false, nil
	}

	interests, err := loadProjectUserInterestsTx(ctx, tx, workspaceID, projectID)
	if err != nil {
		return uuid.Nil, false, err
	}
	content := buildBuiltInProactivePrompt(slot, now, interests)
	requestHash := stableRequestHash(
		"builtin_proactive_turn", workspaceID.String(), mainCardID.String(), cycleAgentID.String(), idempotencyKey,
	)
	turn, err := insertTurn(ctx, tx, insertTurnParams{
		WorkspaceID:       workspaceID,
		CardID:            mainCardID,
		Source:            domain.TurnSourceSystem,
		Content:           content,
		CreatedBySourceID: uuid.NullUUID{UUID: cycleAgentID, Valid: true},
		DispatchMode:      domain.DispatchModeEnqueue,
		IdempotencyKey:    idempotencyKey,
		RequestHash:       requestHash,
	})
	if err != nil {
		return uuid.Nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, false, err
	}
	return turn.ID, true, nil
}

type projectUserInterests struct {
	RecentMessages []string
	ActiveCards    []string
}

func loadProjectUserInterestsTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, projectID uuid.UUID,
) (projectUserInterests, error) {
	var out projectUserInterests

	msgRows, err := tx.Query(ctx, `
		SELECT COALESCE(NULLIF(btrim(c.title), ''), '未命名对话'), e.content
		FROM events e
		JOIN cards c
		  ON c.workspace_id = e.workspace_id
		 AND c.id = e.card_id
		JOIN sources s
		  ON s.workspace_id = e.workspace_id
		 AND s.id = e.actor_source_id
		WHERE e.workspace_id = $1
		  AND c.project_id = $2
		  AND e.event_type = 'comment'
		  AND s.kind = 'human'
		ORDER BY e.sequence DESC
		LIMIT 10
	`, workspaceID, projectID)
	if err != nil {
		return projectUserInterests{}, err
	}
	for msgRows.Next() {
		var cardTitle, content string
		if err := msgRows.Scan(&cardTitle, &content); err != nil {
			msgRows.Close()
			return projectUserInterests{}, err
		}
		snippet := compactSnippet(content, 240)
		if snippet != "" {
			out.RecentMessages = append(out.RecentMessages, fmt.Sprintf("[%s] %s", compactSnippet(cardTitle, 40), snippet))
		}
	}
	if err := msgRows.Err(); err != nil {
		msgRows.Close()
		return projectUserInterests{}, err
	}
	msgRows.Close()

	// 若用户通过 API 直接创建了未带 comment 事件的 human turn，也一并纳入最近兴趣线索。
	if len(out.RecentMessages) == 0 {
		turnRows, err := tx.Query(ctx, `
			SELECT COALESCE(NULLIF(btrim(c.title), ''), '未命名对话'), t.content
			FROM card_turns t
			JOIN cards c
			  ON c.workspace_id = t.workspace_id
			 AND c.id = t.card_id
			WHERE t.workspace_id = $1
			  AND c.project_id = $2
			  AND t.source = 'human'
			  AND t.cancelled_at IS NULL
			ORDER BY t.sequence DESC
			LIMIT 10
		`, workspaceID, projectID)
		if err != nil {
			return projectUserInterests{}, err
		}
		for turnRows.Next() {
			var cardTitle, content string
			if err := turnRows.Scan(&cardTitle, &content); err != nil {
				turnRows.Close()
				return projectUserInterests{}, err
			}
			snippet := compactSnippet(content, 240)
			if snippet != "" {
				out.RecentMessages = append(out.RecentMessages, fmt.Sprintf("[%s] %s", compactSnippet(cardTitle, 40), snippet))
			}
		}
		if err := turnRows.Err(); err != nil {
			turnRows.Close()
			return projectUserInterests{}, err
		}
		turnRows.Close()
	}

	cardRows, err := tx.Query(ctx, `
		SELECT c.kind, COALESCE(c.todo_state, c.ongoing_state, c.discussion_state, ''), c.title, c.description
		FROM cards c
		JOIN projects p
		  ON p.workspace_id = c.workspace_id
		 AND p.id = c.project_id
		WHERE c.workspace_id = $1
		  AND c.project_id = $2
		  AND (p.main_discussion_id IS NULL OR c.id <> p.main_discussion_id)
		  AND (
			(c.kind = 'todo' AND c.todo_state <> 'done')
			OR (c.kind = 'ongoing' AND c.ongoing_state = 'active')
			OR (c.kind = 'discussion' AND c.discussion_state = 'active' AND btrim(c.title) <> '')
		  )
		ORDER BY c.updated_at DESC, c.id
		LIMIT 8
	`, workspaceID, projectID)
	if err != nil {
		return projectUserInterests{}, err
	}
	for cardRows.Next() {
		var kind, state, title, description string
		if err := cardRows.Scan(&kind, &state, &title, &description); err != nil {
			cardRows.Close()
			return projectUserInterests{}, err
		}
		line := fmt.Sprintf("[%s/%s] %s", kind, state, compactSnippet(title, 60))
		if desc := compactSnippet(description, 160); desc != "" {
			line += "：" + desc
		}
		out.ActiveCards = append(out.ActiveCards, line)
	}
	if err := cardRows.Err(); err != nil {
		cardRows.Close()
		return projectUserInterests{}, err
	}
	cardRows.Close()

	return out, nil
}

func buildBuiltInProactivePrompt(
	slot BuiltInProactiveSlot,
	now time.Time,
	interests projectUserInterests,
) string {
	loc, err := time.LoadLocation(builtInProactiveTimezone)
	if err != nil {
		loc = time.UTC
	}
	localTime := now.In(loc).Format("2006-01-02 15:04")

	var b strings.Builder
	fmt.Fprintf(&b, "这是系统在%s左右（当前本地时间 %s）触发的主聊天主动消息任务。\n\n", slot.Label, localTime)
	if len(interests.RecentMessages) > 0 {
		b.WriteString("用户最近在项目中的消息与可能感兴趣的话题：\n")
		for _, item := range interests.RecentMessages {
			b.WriteString("- ")
			b.WriteString(item)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(interests.ActiveCards) > 0 {
		b.WriteString("项目当前活跃事项：\n")
		for _, item := range interests.ActiveCards {
			b.WriteString("- ")
			b.WriteString(item)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("请按以下要求执行：\n")
	b.WriteString("1. 结合用户最近可能感兴趣的消息、主聊天上下文以及 Recall 长期记忆（如有 recall_search / recall_read 可主动检索），挑选一个用户当下可能关心或觉得有用的话题（避免与主聊天上一条主动消息完全重复）。\n")
	b.WriteString("2. 如果已连接设备（NexusDock）或 MCP 工具，主动调用不具破坏性（只读、查询类、无副作用）的设备 Skill 或 MCP 工具获取最新真实信息或状态；严禁执行修改文件、删除数据、重启服务、改写配置等任何破坏性操作。\n")
	b.WriteString("3. 禁止发送任何中间进度气泡（严禁调用 end_turn=false 的 reply_message）。完成所有只读查询后，将最终内容整理成自然、简短的一段话，只调用 1 次 reply_message（type=\"text\", end_turn=true）发送 1 条气泡到主聊天。")
	return b.String()
}

func compactSnippet(raw string, maxRunes int) string {
	cleaned := strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
	if maxRunes <= 0 || utf8.RuneCountInString(cleaned) <= maxRunes {
		return cleaned
	}
	runes := []rune(cleaned)
	return string(runes[:maxRunes]) + "…"
}
