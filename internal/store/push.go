package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// pushActiveWindow 控制前台页面心跳的宽限时间。
// 前端每 10s 发送一次心跳，20s 允许丢 1 次网络波动，同时在 iOS 锁屏/划掉未发出 inactive 时大幅缩短推送等待。
const pushActiveWindow = 20 * time.Second

// PushSubscription 是一个浏览器的 Web Push 订阅。AuthSecret 是这条订阅的加密密钥，发送时必须用到。
type PushSubscription struct {
	ID         uuid.UUID
	Endpoint   string
	P256dh     string
	AuthSecret string
}

// PushCardSnapshot 是上次已经处理过的卡片通知版本（按订阅维度隔离，避免多设备互吞推送）。
type PushCardSnapshot struct {
	SubscriptionID uuid.UUID
	CardID         uuid.UUID
	Revision       string
	EventSequence  string
	MessagePreview string
	AttemptStatus  string
	Title          string
}

// SavePushSubscription 按 endpoint 覆盖订阅，并把页面标成仍在前台。
// 同一浏览器切换 Workspace 时，这条订阅跟着当前登录身份走，同时清理旧工作区残留的订阅卡片快照。
func (s *Store) SavePushSubscription(ctx context.Context, workspaceID, userID uuid.UUID, sub PushSubscription) (uuid.UUID, error) {
	var subID uuid.UUID
	err := s.pool.QueryRow(ctx, `
INSERT INTO push_subscriptions (workspace_id, user_id, endpoint, p256dh, auth_secret, active_until)
VALUES ($1, $2, $3, $4, $5, now() + ($6::int * interval '1 second'))
ON CONFLICT (endpoint) DO UPDATE
SET workspace_id = EXCLUDED.workspace_id,
    user_id = EXCLUDED.user_id,
    p256dh = EXCLUDED.p256dh,
    auth_secret = EXCLUDED.auth_secret,
    active_until = EXCLUDED.active_until,
    updated_at = now()
RETURNING id
`, workspaceID, userID, sub.Endpoint, sub.P256dh, sub.AuthSecret, int(pushActiveWindow.Seconds())).Scan(&subID)
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := s.pool.Exec(ctx, `
DELETE FROM push_subscription_card_revisions
WHERE subscription_id = $1 AND workspace_id <> $2
`, subID, workspaceID); err != nil {
		return uuid.Nil, err
	}
	return subID, nil
}

// GetPushSubscriptionIDByEndpoint 查找当前工作区下某个用户对应 endpoint 的订阅 ID。
func (s *Store) GetPushSubscriptionIDByEndpoint(ctx context.Context, workspaceID, userID uuid.UUID, endpoint string) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
SELECT id FROM push_subscriptions
WHERE workspace_id = $1 AND user_id = $2 AND endpoint = $3
`, workspaceID, userID, endpoint).Scan(&id)
	if err == pgx.ErrNoRows {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return id, true, nil
}

// TouchPushSubscription 延长页面存活时间。订阅不存在时返回 false。
func (s *Store) TouchPushSubscription(ctx context.Context, workspaceID, userID uuid.UUID, endpoint string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
UPDATE push_subscriptions
SET active_until = now() + ($4::int * interval '1 second'), updated_at = now()
WHERE workspace_id = $1 AND user_id = $2 AND endpoint = $3
`, workspaceID, userID, endpoint, int(pushActiveWindow.Seconds()))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// IdlePushSubscription 在页面切后台或关闭时立刻允许推送，不等心跳过期，并主动唤醒本工作区推送检查。
func (s *Store) IdlePushSubscription(ctx context.Context, workspaceID, userID uuid.UUID, endpoint string) error {
	tag, err := s.pool.Exec(ctx, `
UPDATE push_subscriptions
SET active_until = now(), updated_at = now()
WHERE workspace_id = $1 AND user_id = $2 AND endpoint = $3
`, workspaceID, userID, endpoint)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		s.notifyWorkspaceChanged(workspaceID)
	}
	return nil
}

func (s *Store) DeletePushSubscription(ctx context.Context, workspaceID, userID uuid.UUID, endpoint string) error {
	_, err := s.pool.Exec(ctx, `
DELETE FROM push_subscriptions
WHERE workspace_id = $1 AND user_id = $2 AND endpoint = $3
`, workspaceID, userID, endpoint)
	return err
}

func (s *Store) DeletePushSubscriptionByEndpoint(ctx context.Context, endpoint string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM push_subscriptions WHERE endpoint = $1`, endpoint)
	return err
}

func (s *Store) ListPushWorkspaces(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT workspace_id FROM push_subscriptions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ListIdlePushSubscriptions 返回页面已经切后台或关闭、可以接收推送的订阅。
func (s *Store) ListIdlePushSubscriptions(ctx context.Context, workspaceID uuid.UUID) ([]PushSubscription, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, endpoint, p256dh, auth_secret
FROM push_subscriptions
WHERE workspace_id = $1 AND (active_until IS NULL OR active_until <= now())
`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var subs []PushSubscription
	for rows.Next() {
		var sub PushSubscription
		if err := rows.Scan(&sub.ID, &sub.Endpoint, &sub.P256dh, &sub.AuthSecret); err != nil {
			return nil, err
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

// ListPushSubscriptions 返回工作区下所有订阅（包含活跃与空闲）。
func (s *Store) ListPushSubscriptions(ctx context.Context, workspaceID uuid.UUID) ([]PushSubscription, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, endpoint, p256dh, auth_secret
FROM push_subscriptions
WHERE workspace_id = $1
`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var subs []PushSubscription
	for rows.Next() {
		var sub PushSubscription
		if err := rows.Scan(&sub.ID, &sub.Endpoint, &sub.P256dh, &sub.AuthSecret); err != nil {
			return nil, err
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

// ListPushSubscriptionCardSnapshots 读取指定订阅在该工作区下已记录的全部卡片版本快照。
func (s *Store) ListPushSubscriptionCardSnapshots(ctx context.Context, workspaceID, subscriptionID uuid.UUID) ([]PushCardSnapshot, error) {
	rows, err := s.pool.Query(ctx, `
SELECT subscription_id, card_id, revision, event_sequence, message_preview, attempt_status, title
FROM push_subscription_card_revisions
WHERE workspace_id = $1 AND subscription_id = $2
`, workspaceID, subscriptionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var snapshots []PushCardSnapshot
	for rows.Next() {
		var snapshot PushCardSnapshot
		if err := rows.Scan(&snapshot.SubscriptionID, &snapshot.CardID, &snapshot.Revision, &snapshot.EventSequence, &snapshot.MessagePreview, &snapshot.AttemptStatus, &snapshot.Title); err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, rows.Err()
}

// ListWorkspacePushSubscriptionCardSnapshots 一次查询读取工作区内所有订阅的卡片快照，避免按订阅 N+1 查询。
func (s *Store) ListWorkspacePushSubscriptionCardSnapshots(ctx context.Context, workspaceID uuid.UUID) ([]PushCardSnapshot, error) {
	rows, err := s.pool.Query(ctx, `
SELECT subscription_id, card_id, revision, event_sequence, message_preview, attempt_status, title
FROM push_subscription_card_revisions
WHERE workspace_id = $1
`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var snapshots []PushCardSnapshot
	for rows.Next() {
		var snapshot PushCardSnapshot
		if err := rows.Scan(&snapshot.SubscriptionID, &snapshot.CardID, &snapshot.Revision, &snapshot.EventSequence, &snapshot.MessagePreview, &snapshot.AttemptStatus, &snapshot.Title); err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, rows.Err()
}

// SavePushSubscriptionCardSnapshot 写入单条订阅的卡片已处理版本快照。
func (s *Store) SavePushSubscriptionCardSnapshot(ctx context.Context, workspaceID, subscriptionID uuid.UUID, snapshot PushCardSnapshot) error {
	_, err := s.pool.Exec(ctx, `
INSERT INTO push_subscription_card_revisions (subscription_id, workspace_id, card_id, revision, event_sequence, message_preview, attempt_status, title)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (subscription_id, card_id) DO UPDATE
SET workspace_id = EXCLUDED.workspace_id,
    revision = EXCLUDED.revision,
    event_sequence = EXCLUDED.event_sequence,
    message_preview = EXCLUDED.message_preview,
    attempt_status = EXCLUDED.attempt_status,
    title = EXCLUDED.title,
    updated_at = now()
`, subscriptionID, workspaceID, snapshot.CardID, snapshot.Revision, snapshot.EventSequence, snapshot.MessagePreview, snapshot.AttemptStatus, snapshot.Title)
	return err
}

// SavePushSubscriptionCardSnapshotsBatch 批量写入单条订阅的一组卡片快照（用于订阅基线初始化与 /seen 批量确认）。
func (s *Store) SavePushSubscriptionCardSnapshotsBatch(ctx context.Context, workspaceID, subscriptionID uuid.UUID, snapshots []PushCardSnapshot) error {
	if len(snapshots) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	const sql = `
INSERT INTO push_subscription_card_revisions (subscription_id, workspace_id, card_id, revision, event_sequence, message_preview, attempt_status, title)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (subscription_id, card_id) DO UPDATE
SET workspace_id = EXCLUDED.workspace_id,
    revision = EXCLUDED.revision,
    event_sequence = EXCLUDED.event_sequence,
    message_preview = EXCLUDED.message_preview,
    attempt_status = EXCLUDED.attempt_status,
    title = EXCLUDED.title,
    updated_at = now()
`
	for _, snapshot := range snapshots {
		batch.Queue(sql, subscriptionID, workspaceID, snapshot.CardID, snapshot.Revision, snapshot.EventSequence, snapshot.MessagePreview, snapshot.AttemptStatus, snapshot.Title)
	}
	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range snapshots {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

// ListPushCardSnapshots 返回旧工作区级快照（保留兼容读取）。
func (s *Store) ListPushCardSnapshots(ctx context.Context, workspaceID uuid.UUID) ([]PushCardSnapshot, error) {
	rows, err := s.pool.Query(ctx, `
SELECT card_id, revision, event_sequence, message_preview, attempt_status, title
FROM push_card_revisions
WHERE workspace_id = $1
`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var snapshots []PushCardSnapshot
	for rows.Next() {
		var snapshot PushCardSnapshot
		if err := rows.Scan(&snapshot.CardID, &snapshot.Revision, &snapshot.EventSequence, &snapshot.MessagePreview, &snapshot.AttemptStatus, &snapshot.Title); err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, rows.Err()
}

// SavePushCardSnapshot 写入旧工作区级快照（仅用于兼容未上报 endpoint 的旧版前端）。
func (s *Store) SavePushCardSnapshot(ctx context.Context, workspaceID uuid.UUID, snapshot PushCardSnapshot) error {
	_, err := s.pool.Exec(ctx, `
INSERT INTO push_card_revisions (workspace_id, card_id, revision, event_sequence, message_preview, attempt_status, title)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (workspace_id, card_id) DO UPDATE
SET revision = EXCLUDED.revision,
    event_sequence = EXCLUDED.event_sequence,
    message_preview = EXCLUDED.message_preview,
    attempt_status = EXCLUDED.attempt_status,
    title = EXCLUDED.title,
    updated_at = now()
`, workspaceID, snapshot.CardID, snapshot.Revision, snapshot.EventSequence, snapshot.MessagePreview, snapshot.AttemptStatus, snapshot.Title)
	return err
}
