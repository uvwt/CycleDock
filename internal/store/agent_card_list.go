package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	defaultAgentListCardsLimit = 50
	maxAgentListCardsLimit     = 200
	agentCardDescSummaryRunes  = 160
)

// AgentListCardsParams 描述内置只读工具 cycledock_list_cards 的筛选参数。
// WorkspaceID 必须来自当前认证/执行上下文，严禁跨工作区查询。
type AgentListCardsParams struct {
	WorkspaceID uuid.UUID
	ProjectID   uuid.UUID
	Scope       string // "project"（默认） | "workspace"
	Kind        string // "all"（默认） | "todo" | "ongoing" | "discussion"
	State       string // 可选：todo / working / review / done / active / paused / archived
	Health      string // 可选（仅对 ongoing 生效）：healthy / warning / critical / acknowledged / unknown / abnormal
	Query       string // 可选：对标题做不区分大小写的模糊匹配
	IncludeDone bool   // 默认 false，排除 done 与 archived；显式 true 或 state 指定 done/archived 时包含
	Limit       int    // 1..200，默认 50
}

// AgentCardListItem 是 cycledock_list_cards 返回的精简卡片摘要结构。
type AgentCardListItem struct {
	CardID                   uuid.UUID
	ProjectID                uuid.UUID
	ProjectName              string
	Kind                     string
	Title                    string
	DescriptionSummary       string
	State                    string
	Health                   string
	CurrentAttemptStatus     string
	LatestAttemptStatus      string
	PendingToolApprovalCount int64
	Pinned                   bool
	NotificationsMuted       bool
	IsMainDiscussion         bool
	UpdatedAt                time.Time
}

// AgentListCardsResult 包含命中的卡片切片与截断元数据。
type AgentListCardsResult struct {
	Items        []AgentCardListItem
	TotalMatched int
	Truncated    bool
}

// ListCardsForAgent 供 Discussion 会话只读盘点当前项目或整个工作区的卡片。
// 强制以 workspace_id 作为第一隔离条件，按异常/待审批优先、置顶、最近更新排序。
func (s *Store) ListCardsForAgent(
	ctx context.Context,
	params AgentListCardsParams,
) (AgentListCardsResult, error) {
	if params.WorkspaceID == uuid.Nil {
		return AgentListCardsResult{}, fmt.Errorf("workspace_id is required")
	}

	scope := strings.ToLower(strings.TrimSpace(params.Scope))
	if scope == "" {
		scope = "project"
	}
	switch scope {
	case "project":
		if params.ProjectID == uuid.Nil {
			return AgentListCardsResult{}, fmt.Errorf("project_id is required when scope is project")
		}
	case "workspace":
	default:
		return AgentListCardsResult{}, fmt.Errorf("unsupported scope %q (allowed: project, workspace)", scope)
	}

	kind := strings.ToLower(strings.TrimSpace(params.Kind))
	if kind == "" {
		kind = "all"
	}
	switch kind {
	case "all", "todo", "ongoing", "discussion":
	default:
		return AgentListCardsResult{}, fmt.Errorf("unsupported kind %q (allowed: all, todo, ongoing, discussion)", kind)
	}

	state := strings.ToLower(strings.TrimSpace(params.State))
	switch state {
	case "", "todo", "working", "review", "done", "active", "paused", "archived":
	default:
		return AgentListCardsResult{}, fmt.Errorf("unsupported state %q", state)
	}

	health := strings.ToLower(strings.TrimSpace(params.Health))
	switch health {
	case "", "healthy", "warning", "critical", "acknowledged", "unknown", "abnormal":
	default:
		return AgentListCardsResult{}, fmt.Errorf("unsupported health %q", health)
	}

	limit := params.Limit
	if limit == 0 {
		limit = defaultAgentListCardsLimit
	} else if limit < 0 {
		return AgentListCardsResult{}, fmt.Errorf("limit must be between 1 and %d", maxAgentListCardsLimit)
	}
	if limit > maxAgentListCardsLimit {
		limit = maxAgentListCardsLimit
	}

	includeDone := params.IncludeDone || state == "done" || state == "archived"
	query := strings.TrimSpace(params.Query)

	// 构造严格参数化的动态过滤条件，所有值通过绑定参数传入，避免字符串拼接风险。
	args := []any{params.WorkspaceID}
	nextArg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	where := []string{"c.workspace_id = $1"}
	if scope == "project" {
		where = append(where, "c.project_id = "+nextArg(params.ProjectID))
	}
	if kind != "all" {
		where = append(where, "c.kind = "+nextArg(kind))
	}
	if state != "" {
		where = append(where, "COALESCE(c.todo_state, c.ongoing_state, c.discussion_state, '') = "+nextArg(state))
	}
	if !includeDone {
		where = append(where, "COALESCE(c.todo_state, c.ongoing_state, c.discussion_state, '') NOT IN ('done', 'archived')")
	}
	if health != "" {
		// 健康度语义仅存在于 Ongoing 卡片；指定 health 时同步限定 kind = 'ongoing'。
		where = append(where, "c.kind = 'ongoing'")
		if health == "abnormal" {
			where = append(where, "COALESCE(latest_checkin.checkin_status, 'unknown') IN ('warning', 'critical')")
		} else {
			where = append(where, "COALESCE(latest_checkin.checkin_status, 'unknown') = "+nextArg(health))
		}
	}
	if query != "" {
		pattern := "%" + escapeLikePattern(query) + "%"
		where = append(where, `c.title ILIKE `+nextArg(pattern)+` ESCAPE '\'`)
	}

	limitArg := nextArg(limit)
	sqlQuery := fmt.Sprintf(`
		SELECT
			c.id,
			c.project_id,
			p.name,
			c.kind,
			c.title,
			c.description,
			COALESCE(c.todo_state, c.ongoing_state, c.discussion_state, '')::text AS state,
			CASE
				WHEN c.kind = 'ongoing' THEN COALESCE(latest_checkin.checkin_status, 'unknown')
				ELSE ''
			END::text AS health,
			COALESCE(current_attempt.status, '')::text AS current_attempt_status,
			COALESCE(latest_attempt.status, '')::text AS latest_attempt_status,
			COALESCE(pending_approval.pending_count, 0)::bigint AS pending_tool_approval_count,
			(c.pinned_at IS NOT NULL)::boolean AS pinned,
			c.notifications_muted,
			COALESCE(p.main_discussion_id = c.id, false)::boolean AS is_main_discussion,
			c.updated_at,
			COUNT(*) OVER()::int AS total_matched
		FROM cards c
		JOIN projects p
		  ON p.workspace_id = c.workspace_id
		 AND p.id = c.project_id
		LEFT JOIN LATERAL (
			SELECT
				CASE
					WHEN e.checkin_status IN ('warning', 'critical') AND EXISTS (
						SELECT 1 FROM events ack
						WHERE ack.workspace_id = e.workspace_id
						  AND ack.card_id = e.card_id
						  AND ack.event_type = 'checkin_ack'
						  AND ack.sequence > e.sequence
					) THEN 'acknowledged'
					ELSE e.checkin_status
				END::text AS checkin_status
			FROM events e
			WHERE e.workspace_id = c.workspace_id
			  AND e.card_id = c.id
			  AND e.event_type = 'checkin'
			ORDER BY e.sequence DESC
			LIMIT 1
		) latest_checkin ON true
		LEFT JOIN LATERAL (
			SELECT a.status
			FROM turn_attempts a
			WHERE a.workspace_id = c.workspace_id
			  AND a.card_id = c.id
			  AND a.status IN ('queued', 'running')
			ORDER BY a.created_at DESC, a.attempt_number DESC
			LIMIT 1
		) current_attempt ON true
		LEFT JOIN LATERAL (
			SELECT a.status
			FROM turn_attempts a
			WHERE a.workspace_id = c.workspace_id
			  AND a.card_id = c.id
			ORDER BY a.created_at DESC, a.attempt_number DESC
			LIMIT 1
		) latest_attempt ON true
		LEFT JOIN LATERAL (
			SELECT COUNT(*) AS pending_count
			FROM card_tool_approvals ta
			WHERE ta.workspace_id = c.workspace_id
			  AND ta.card_id = c.id
			  AND ta.status = 'pending'
		) pending_approval ON true
		WHERE %s
		ORDER BY
			CASE
				WHEN c.kind = 'ongoing' AND COALESCE(latest_checkin.checkin_status, 'unknown') = 'critical' THEN 0
				WHEN c.kind = 'ongoing' AND COALESCE(latest_checkin.checkin_status, 'unknown') = 'warning' THEN 1
				WHEN COALESCE(pending_approval.pending_count, 0) > 0 THEN 2
				ELSE 3
			END ASC,
			c.pinned_at DESC NULLS LAST,
			c.updated_at DESC,
			c.id ASC
		LIMIT %s
	`, strings.Join(where, "\n\t\t  AND "), limitArg)

	rows, err := s.pool.Query(ctx, sqlQuery, args...)
	if err != nil {
		return AgentListCardsResult{}, err
	}
	defer rows.Close()

	items := make([]AgentCardListItem, 0)
	totalMatched := 0
	for rows.Next() {
		var item AgentCardListItem
		var description string
		var updatedAt pgtype.Timestamptz
		var total int
		if err := rows.Scan(
			&item.CardID,
			&item.ProjectID,
			&item.ProjectName,
			&item.Kind,
			&item.Title,
			&description,
			&item.State,
			&item.Health,
			&item.CurrentAttemptStatus,
			&item.LatestAttemptStatus,
			&item.PendingToolApprovalCount,
			&item.Pinned,
			&item.NotificationsMuted,
			&item.IsMainDiscussion,
			&updatedAt,
			&total,
		); err != nil {
			return AgentListCardsResult{}, err
		}
		item.DescriptionSummary = compactSnippet(description, agentCardDescSummaryRunes)
		if updatedAt.Valid {
			item.UpdatedAt = updatedAt.Time.UTC()
		}
		totalMatched = total
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return AgentListCardsResult{}, err
	}

	return AgentListCardsResult{
		Items:        items,
		TotalMatched: totalMatched,
		Truncated:    totalMatched > len(items),
	}, nil
}

func escapeLikePattern(raw string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(raw)
}
