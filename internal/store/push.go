package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

const pushActiveWindow = 45 * time.Second

// PushSubscription 是一个浏览器的 Web Push 订阅。AuthSecret 是这条订阅的加密密钥，发送时必须用到。
type PushSubscription struct {
	Endpoint   string
	P256dh     string
	AuthSecret string
}

// PushCardSnapshot 是上次已经处理过的卡片通知版本。
type PushCardSnapshot struct {
	CardID         uuid.UUID
	Revision       string
	EventSequence  string
	MessagePreview string
	AttemptStatus  string
	Title          string
}

// SavePushSubscription 按 endpoint 覆盖订阅，并把页面标成仍在前台。
// 同一浏览器切换 Workspace 时，这条订阅跟着当前登录身份走。
func (s *Store) SavePushSubscription(ctx context.Context, workspaceID, userID uuid.UUID, sub PushSubscription) error {
	_, err := s.pool.Exec(ctx, `
INSERT INTO push_subscriptions (workspace_id, user_id, endpoint, p256dh, auth_secret, active_until)
VALUES ($1, $2, $3, $4, $5, now() + ($6::int * interval '1 second'))
ON CONFLICT (endpoint) DO UPDATE
SET workspace_id = EXCLUDED.workspace_id,
    user_id = EXCLUDED.user_id,
    p256dh = EXCLUDED.p256dh,
    auth_secret = EXCLUDED.auth_secret,
    active_until = EXCLUDED.active_until,
    updated_at = now()
`, workspaceID, userID, sub.Endpoint, sub.P256dh, sub.AuthSecret, int(pushActiveWindow.Seconds()))
	return err
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

// IdlePushSubscription 在页面关闭时立刻允许推送，不等心跳过期。
func (s *Store) IdlePushSubscription(ctx context.Context, workspaceID, userID uuid.UUID, endpoint string) error {
	_, err := s.pool.Exec(ctx, `
UPDATE push_subscriptions
SET active_until = now(), updated_at = now()
WHERE workspace_id = $1 AND user_id = $2 AND endpoint = $3
`, workspaceID, userID, endpoint)
	return err
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

// ListIdlePushSubscriptions 返回页面已经关闭、可以接收推送的订阅。
func (s *Store) ListIdlePushSubscriptions(ctx context.Context, workspaceID uuid.UUID) ([]PushSubscription, error) {
	rows, err := s.pool.Query(ctx, `
SELECT endpoint, p256dh, auth_secret
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
		if err := rows.Scan(&sub.Endpoint, &sub.P256dh, &sub.AuthSecret); err != nil {
			return nil, err
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

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
