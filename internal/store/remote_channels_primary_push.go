package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/uvwt/CycleDock/internal/domain"
)

// ProjectPrimaryPushKey 用于在跨工作区批量分发时索引项目的唯一主推路由。
type ProjectPrimaryPushKey struct {
	WorkspaceID uuid.UUID
	ProjectID   uuid.UUID
}

// ProjectPrimaryPushRoute 表示项目的持久化远程主推路由配置。
type ProjectPrimaryPushRoute struct {
	WorkspaceID uuid.UUID
	ProjectID   uuid.UUID
	Mode        domain.ProjectPrimaryPushMode
	ChannelID   uuid.NullUUID
}

// ResolvedProjectPrimaryPushRoute 在持久化配置之外附带当前实际生效的主推通道信息，
// 供 HTTP API、前端设置页与分发器共享同一套判定语义。
type ResolvedProjectPrimaryPushRoute struct {
	ProjectID                uuid.UUID
	Mode                     domain.ProjectPrimaryPushMode
	ChannelID                uuid.NullUUID
	EffectiveChannelID       uuid.NullUUID
	EffectiveChannelName     string
	EffectiveChannelPlatform domain.ChannelPlatform
}

type primaryPushQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// NormalizeProjectPrimaryPushMode 校验并规整项目主推路由模式。
func NormalizeProjectPrimaryPushMode(raw domain.ProjectPrimaryPushMode) (domain.ProjectPrimaryPushMode, error) {
	mode := domain.ProjectPrimaryPushMode(strings.ToLower(strings.TrimSpace(string(raw))))
	switch mode {
	case domain.PrimaryPushModeLastActive, domain.PrimaryPushModeChannel, domain.PrimaryPushModeNone:
		return mode, nil
	default:
		return "", fmt.Errorf("%w: unsupported primary_push_mode %q", domain.ErrConflict, raw)
	}
}

// ResolveProjectPrimaryPushChannel 按项目主推策略从候选通道中解析出唯一生效的主推通道。
// 该函数是 HTTP API 与后台 Dispatcher 的唯一判定来源，避免复制平行路由状态机：
//   - none: 仅网页端/免打扰，绝不向任何远程通道推项目级主动消息与提醒；
//   - channel: 仅匹配 primary_push_channel_id 指向的同项目已启用 main_chat 通道；
//     若目标被删除、禁用、改为 scheduled_push 或迁移到其他项目，则安全静默，绝不退化为全量广播；
//   - last_active: 自动选择同项目已启用、具备出站目标且有入站记录（last_inbound_at 非空）的
//     main_chat 通道中最近活跃的那 1 个；若尚无活跃通道，则暂不推远程。
func ResolveProjectPrimaryPushChannel(
	route ProjectPrimaryPushRoute,
	channels []RemoteChannel,
) (RemoteChannel, bool) {
	mode := route.Mode
	if mode == "" {
		mode = domain.PrimaryPushModeLastActive
	}
	switch mode {
	case domain.PrimaryPushModeNone:
		return RemoteChannel{}, false
	case domain.PrimaryPushModeChannel:
		if !route.ChannelID.Valid {
			return RemoteChannel{}, false
		}
		for _, ch := range channels {
			if ch.ID != route.ChannelID.UUID {
				continue
			}
			if ch.ProjectID != route.ProjectID || !ch.Enabled || ch.Role != domain.ChannelRoleMainChat {
				return RemoteChannel{}, false
			}
			return ch, true
		}
		return RemoteChannel{}, false
	case domain.PrimaryPushModeLastActive:
		var best RemoteChannel
		found := false
		for _, ch := range channels {
			if ch.ProjectID != route.ProjectID || !ch.Enabled || ch.Role != domain.ChannelRoleMainChat {
				continue
			}
			if ch.LastInboundAt == nil || !ch.HasOutboundTarget() {
				continue
			}
			if !found || isMoreRecentlyActiveChannel(ch, best) {
				best = ch
				found = true
			}
		}
		return best, found
	default:
		return RemoteChannel{}, false
	}
}

func isMoreRecentlyActiveChannel(a, b RemoteChannel) bool {
	at := a.LastInboundAt.UTC()
	bt := b.LastInboundAt.UTC()
	if !at.Equal(bt) {
		return at.After(bt)
	}
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	return a.ID.String() < b.ID.String()
}

func buildResolvedProjectPrimaryPushRoute(
	route ProjectPrimaryPushRoute,
	channels []RemoteChannel,
) ResolvedProjectPrimaryPushRoute {
	mode := route.Mode
	if mode == "" {
		mode = domain.PrimaryPushModeLastActive
	}
	out := ResolvedProjectPrimaryPushRoute{
		ProjectID: route.ProjectID,
		Mode:      mode,
		ChannelID: route.ChannelID,
	}
	if effective, ok := ResolveProjectPrimaryPushChannel(route, channels); ok {
		out.EffectiveChannelID = uuid.NullUUID{UUID: effective.ID, Valid: true}
		out.EffectiveChannelName = effective.Name
		out.EffectiveChannelPlatform = effective.Platform
	}
	return out
}

func listProjectRemoteChannels(
	ctx context.Context,
	q primaryPushQuerier,
	workspaceID, projectID uuid.UUID,
) ([]RemoteChannel, error) {
	rows, err := q.Query(ctx, `
		SELECT `+remoteChannelSelectCols+`
		FROM remote_channels
		WHERE workspace_id = $1 AND project_id = $2
		ORDER BY created_at DESC, id
	`, workspaceID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]RemoteChannel, 0)
	for rows.Next() {
		ch, err := scanRemoteChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ch)
	}
	return out, rows.Err()
}

// GetProjectPrimaryPushRoute 查询单个项目的主推路由配置及当前实际生效的主推通道。
func (s *Store) GetProjectPrimaryPushRoute(
	ctx context.Context,
	workspaceID, projectID uuid.UUID,
) (ResolvedProjectPrimaryPushRoute, error) {
	var modeStr string
	var channelID uuid.NullUUID
	err := s.pool.QueryRow(ctx, `
		SELECT primary_push_mode, primary_push_channel_id
		FROM projects
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, projectID).Scan(&modeStr, &channelID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ResolvedProjectPrimaryPushRoute{}, domain.ErrNotFound
	}
	if err != nil {
		return ResolvedProjectPrimaryPushRoute{}, err
	}
	channels, err := listProjectRemoteChannels(ctx, s.pool, workspaceID, projectID)
	if err != nil {
		return ResolvedProjectPrimaryPushRoute{}, err
	}
	route := ProjectPrimaryPushRoute{
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
		Mode:        domain.ProjectPrimaryPushMode(modeStr),
		ChannelID:   channelID,
	}
	return buildResolvedProjectPrimaryPushRoute(route, channels), nil
}

// ListWorkspaceProjectPrimaryPushRoutes 批量返回工作区下全部项目的主推路由配置与生效结果。
// 调用方若已加载工作区通道列表，可直接复用 channels 避免重复查库。
func (s *Store) ListWorkspaceProjectPrimaryPushRoutes(
	ctx context.Context,
	workspaceID uuid.UUID,
	channels []RemoteChannel,
) ([]ResolvedProjectPrimaryPushRoute, error) {
	if channels == nil {
		var err error
		channels, err = s.ListRemoteChannels(ctx, workspaceID)
		if err != nil {
			return nil, err
		}
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, primary_push_mode, primary_push_channel_id
		FROM projects
		WHERE workspace_id = $1
		ORDER BY created_at, id
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byProject := make(map[uuid.UUID][]RemoteChannel)
	for _, ch := range channels {
		byProject[ch.ProjectID] = append(byProject[ch.ProjectID], ch)
	}

	out := make([]ResolvedProjectPrimaryPushRoute, 0)
	for rows.Next() {
		var (
			projectID uuid.UUID
			modeStr   string
			channelID uuid.NullUUID
		)
		if err := rows.Scan(&projectID, &modeStr, &channelID); err != nil {
			return nil, err
		}
		route := ProjectPrimaryPushRoute{
			WorkspaceID: workspaceID,
			ProjectID:   projectID,
			Mode:        domain.ProjectPrimaryPushMode(modeStr),
			ChannelID:   channelID,
		}
		out = append(out, buildResolvedProjectPrimaryPushRoute(route, byProject[projectID]))
	}
	return out, rows.Err()
}

// ListProjectPrimaryPushRoutesForEnabledMainChat 一次性加载所有包含已启用双向通道的项目主推路由，
// 供后台 Dispatcher 每轮 DeliverOnce 共享解析，避免按通道逐条查库。
func (s *Store) ListProjectPrimaryPushRoutesForEnabledMainChat(
	ctx context.Context,
) (map[ProjectPrimaryPushKey]ProjectPrimaryPushRoute, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT p.workspace_id, p.id, p.primary_push_mode, p.primary_push_channel_id
		FROM projects p
		JOIN remote_channels rc
		  ON rc.workspace_id = p.workspace_id AND rc.project_id = p.id
		WHERE rc.enabled = true AND rc.role = 'main_chat'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[ProjectPrimaryPushKey]ProjectPrimaryPushRoute)
	for rows.Next() {
		var (
			r       ProjectPrimaryPushRoute
			modeStr string
		)
		if err := rows.Scan(&r.WorkspaceID, &r.ProjectID, &modeStr, &r.ChannelID); err != nil {
			return nil, err
		}
		r.Mode = domain.ProjectPrimaryPushMode(modeStr)
		out[ProjectPrimaryPushKey{WorkspaceID: r.WorkspaceID, ProjectID: r.ProjectID}] = r
	}
	return out, rows.Err()
}

// UpdateProjectPrimaryPushRoute 原子更新项目级远程主推路由配置。
// 当本次切换导致实际生效的主推通道发生变化时，自动将该新主推通道在项目内非专属卡片上的出站游标
// 对齐到当前最新水位，彻底防止切换主推后把此前积压的主聊天/系统/审批历史一次性灌爆。
func (s *Store) UpdateProjectPrimaryPushRoute(
	ctx context.Context,
	workspaceID, projectID uuid.UUID,
	mode domain.ProjectPrimaryPushMode,
	channelID uuid.NullUUID,
) (ResolvedProjectPrimaryPushRoute, error) {
	normalizedMode, err := NormalizeProjectPrimaryPushMode(mode)
	if err != nil {
		return ResolvedProjectPrimaryPushRoute{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ResolvedProjectPrimaryPushRoute{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var oldModeStr string
	var oldChannelID uuid.NullUUID
	err = tx.QueryRow(ctx, `
		SELECT primary_push_mode, primary_push_channel_id
		FROM projects
		WHERE workspace_id = $1 AND id = $2
		FOR UPDATE
	`, workspaceID, projectID).Scan(&oldModeStr, &oldChannelID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ResolvedProjectPrimaryPushRoute{}, domain.ErrNotFound
	}
	if err != nil {
		return ResolvedProjectPrimaryPushRoute{}, err
	}

	projectChannels, err := listProjectRemoteChannels(ctx, tx, workspaceID, projectID)
	if err != nil {
		return ResolvedProjectPrimaryPushRoute{}, err
	}

	oldRoute := ProjectPrimaryPushRoute{
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
		Mode:        domain.ProjectPrimaryPushMode(oldModeStr),
		ChannelID:   oldChannelID,
	}
	oldEffective, oldHasPrimary := ResolveProjectPrimaryPushChannel(oldRoute, projectChannels)

	var nextChannelID uuid.NullUUID
	if normalizedMode == domain.PrimaryPushModeChannel {
		if !channelID.Valid {
			return ResolvedProjectPrimaryPushRoute{}, fmt.Errorf("%w: channel_id is required when mode is channel", domain.ErrConflict)
		}
		var target RemoteChannel
		foundInProject := false
		for _, ch := range projectChannels {
			if ch.ID == channelID.UUID {
				target = ch
				foundInProject = true
				break
			}
		}
		if !foundInProject {
			// 区分「通道不存在」与「通道属于其他项目」，返回明确错误。
			var exists bool
			if checkErr := tx.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM remote_channels
					WHERE workspace_id = $1 AND id = $2
				)
			`, workspaceID, channelID.UUID).Scan(&exists); checkErr != nil {
				return ResolvedProjectPrimaryPushRoute{}, checkErr
			}
			if !exists {
				return ResolvedProjectPrimaryPushRoute{}, domain.ErrNotFound
			}
			return ResolvedProjectPrimaryPushRoute{}, fmt.Errorf("%w: primary push channel must belong to the target project", domain.ErrConflict)
		}
		if target.Role != domain.ChannelRoleMainChat {
			return ResolvedProjectPrimaryPushRoute{}, fmt.Errorf("%w: primary push channel must have role main_chat", domain.ErrConflict)
		}
		nextChannelID = channelID
	}

	if _, err := tx.Exec(ctx, `
		UPDATE projects
		SET primary_push_mode = $3,
		    primary_push_channel_id = $4,
		    updated_at = now()
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, projectID, string(normalizedMode), nextChannelID); err != nil {
		return ResolvedProjectPrimaryPushRoute{}, err
	}

	newRoute := ProjectPrimaryPushRoute{
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
		Mode:        normalizedMode,
		ChannelID:   nextChannelID,
	}
	newEffective, newHasPrimary := ResolveProjectPrimaryPushChannel(newRoute, projectChannels)
	if newHasPrimary && (!oldHasPrimary || newEffective.ID != oldEffective.ID) {
		if err := alignChannelProjectPushCursorsTx(
			ctx,
			tx,
			workspaceID,
			projectID,
			newEffective.ID,
			newEffective.ConversationCardID,
		); err != nil {
			return ResolvedProjectPrimaryPushRoute{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return ResolvedProjectPrimaryPushRoute{}, err
	}
	return buildResolvedProjectPrimaryPushRoute(newRoute, projectChannels), nil
}

// alignChannelProjectPushCursorsTx 将指定通道在当前项目内所有非专属卡片上的出站游标单调推进至最新已落定水位。
// 说明：
//   - 排除 excludeConversationCardID，保证不干扰通道专属讨论卡的实时对话回复；
//   - 对 message 排除仍在流式生成的 preview=true 草稿，避免切换瞬间永久跳过刚转正的正式消息；
//   - 序列号采用 GREATEST 单调推进，绝不回退游标。
func alignChannelProjectPushCursorsTx(
	ctx context.Context,
	q primaryPushQuerier,
	workspaceID, projectID, channelID uuid.UUID,
	excludeConversationCardID uuid.NullUUID,
) error {
	_, err := q.Exec(ctx, `
		INSERT INTO remote_channel_outbound_cursors (
			workspace_id, channel_id, card_id,
			last_message_seq, last_comment_seq, last_checkin_seq, last_status_seq,
			last_attempt_id, last_attempt_status, last_approval_id, updated_at
		)
		SELECT
			c.workspace_id,
			$3::uuid AS channel_id,
			c.id AS card_id,
			COALESCE(ev.max_message_seq, 0),
			COALESCE(ev.max_comment_seq, 0),
			COALESCE(ev.max_checkin_seq, 0),
			COALESCE(ev.max_status_seq, 0),
			la.id,
			COALESCE(la.status, ''),
			ap.id,
			now()
		FROM cards c
		LEFT JOIN LATERAL (
			SELECT
				MAX(e.sequence) FILTER (
					WHERE e.event_type = 'message'
					  AND NOT COALESCE((e.message->>'preview')::boolean, false)
				) AS max_message_seq,
				MAX(e.sequence) FILTER (WHERE e.event_type = 'comment') AS max_comment_seq,
				MAX(e.sequence) FILTER (WHERE e.event_type = 'checkin') AS max_checkin_seq,
				MAX(e.sequence) FILTER (WHERE e.event_type = 'status_change') AS max_status_seq
			FROM events e
			WHERE e.workspace_id = c.workspace_id AND e.card_id = c.id
		) ev ON true
		LEFT JOIN LATERAL (
			SELECT a.id, a.status
			FROM turn_attempts a
			WHERE a.workspace_id = c.workspace_id AND a.card_id = c.id
			ORDER BY a.created_at DESC, a.attempt_number DESC
			LIMIT 1
		) la ON true
		LEFT JOIN LATERAL (
			SELECT ta.id
			FROM card_tool_approvals ta
			WHERE ta.workspace_id = c.workspace_id AND ta.card_id = c.id AND ta.status = 'pending'
			ORDER BY ta.created_at DESC
			LIMIT 1
		) ap ON true
		WHERE c.workspace_id = $1
		  AND c.project_id = $2
		  AND ($4::uuid IS NULL OR c.id <> $4::uuid)
		ON CONFLICT (workspace_id, channel_id, card_id)
		DO UPDATE SET
			last_message_seq = GREATEST(remote_channel_outbound_cursors.last_message_seq, EXCLUDED.last_message_seq),
			last_comment_seq = GREATEST(remote_channel_outbound_cursors.last_comment_seq, EXCLUDED.last_comment_seq),
			last_checkin_seq = GREATEST(remote_channel_outbound_cursors.last_checkin_seq, EXCLUDED.last_checkin_seq),
			last_status_seq = GREATEST(remote_channel_outbound_cursors.last_status_seq, EXCLUDED.last_status_seq),
			last_attempt_id = COALESCE(EXCLUDED.last_attempt_id, remote_channel_outbound_cursors.last_attempt_id),
			last_attempt_status = CASE
				WHEN EXCLUDED.last_attempt_id IS NOT NULL THEN EXCLUDED.last_attempt_status
				ELSE remote_channel_outbound_cursors.last_attempt_status
			END,
			last_approval_id = COALESCE(EXCLUDED.last_approval_id, remote_channel_outbound_cursors.last_approval_id),
			updated_at = now()
	`, workspaceID, projectID, channelID, excludeConversationCardID)
	return err
}

// alignCursorsOnLastActiveTakeover 在 last_active 模式下，当某个非当前主推的双向通道收到新入站消息
// 即将接管项目主推位置时，将其在项目非专属卡片上的游标水位对齐到当前时刻，避免接管瞬间补发历史积压。
// 若该通道本就已是当前生效主推，则不触碰游标，避免误吞尚未投递的新通知。
func (s *Store) alignCursorsOnLastActiveTakeover(
	ctx context.Context,
	workspaceID, projectID, channelID uuid.UUID,
	ownConversationCardID uuid.NullUUID,
) {
	resolved, err := s.GetProjectPrimaryPushRoute(ctx, workspaceID, projectID)
	if err != nil || resolved.Mode != domain.PrimaryPushModeLastActive {
		return
	}
	if resolved.EffectiveChannelID.Valid && resolved.EffectiveChannelID.UUID == channelID {
		return
	}
	_ = alignChannelProjectPushCursorsTx(ctx, s.pool, workspaceID, projectID, channelID, ownConversationCardID)
}

// alignEffectivePrimaryAfterChannelMutation 在通道启停或配置变更导致项目生效主推对象切换时，
// 对齐新主推通道的非专属卡片游标，防止停用恢复或备选通道接棒时灌爆历史积压。
func (s *Store) alignEffectivePrimaryAfterChannelMutation(
	ctx context.Context,
	workspaceID, projectID uuid.UUID,
	oldEffectiveID uuid.NullUUID,
) {
	resolved, err := s.GetProjectPrimaryPushRoute(ctx, workspaceID, projectID)
	if err != nil || !resolved.EffectiveChannelID.Valid {
		return
	}
	if oldEffectiveID.Valid && oldEffectiveID.UUID == resolved.EffectiveChannelID.UUID {
		return
	}
	ch, err := s.GetRemoteChannel(ctx, workspaceID, resolved.EffectiveChannelID.UUID)
	if err != nil {
		return
	}
	_ = alignChannelProjectPushCursorsTx(ctx, s.pool, workspaceID, projectID, ch.ID, ch.ConversationCardID)
}
