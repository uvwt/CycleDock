package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store/db"
)

const (
	mainChatTitle       = "主聊天"
	mainChatDescription = "这个项目的新消息会汇总到这里。在这里回复只继续这张讨论，不会回到原来的卡片。"
)

// MainDiscussion 返回项目已经有的主聊天。还没创建时返回 ErrNotFound。
func (s *Store) MainDiscussion(ctx context.Context, workspaceID, projectID uuid.UUID) (db.Card, error) {
	var cardID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT main_discussion_id
		FROM projects
		WHERE workspace_id = $1 AND id = $2 AND main_discussion_id IS NOT NULL
	`, workspaceID, projectID).Scan(&cardID)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Card{}, domain.ErrNotFound
	}
	if err != nil {
		return db.Card{}, err
	}
	return s.GetCard(ctx, workspaceID, cardID)
}

// EnsureMainDiscussion 打开主聊天。项目还没有时创建一张置顶 Discussion。
func (s *Store) EnsureMainDiscussion(
	ctx context.Context,
	workspaceID, projectID, createdBy uuid.UUID,
) (db.Card, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return db.Card{}, false, err
	}
	defer tx.Rollback(ctx)

	current, err := lockProjectMainDiscussion(ctx, tx, workspaceID, projectID)
	if err != nil {
		return db.Card{}, false, err
	}
	cardID, created, err := ensureMainDiscussionTx(ctx, tx, workspaceID, projectID, createdBy, current)
	if err != nil {
		return db.Card{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.Card{}, false, err
	}
	card, err := s.GetCard(ctx, workspaceID, cardID)
	return card, created, err
}

// relayVisibleMessage 把别的卡片上刚落库的可见消息摘要写进主聊天。
// 调用方已经锁住来源卡。这里不创建 Turn：通知不是新的工作输入，也不能写回来源卡的 Session。
func relayVisibleMessage(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sourceCardID, sourceEventID uuid.UUID,
	preview string,
) error {
	var projectID uuid.UUID
	var kind, title string
	if err := tx.QueryRow(ctx, `
		SELECT project_id, kind, title
		FROM cards
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, sourceCardID).Scan(&projectID, &kind, &title); err != nil {
		return err
	}

	var current uuid.NullUUID
	err := tx.QueryRow(ctx, `
		SELECT main_discussion_id FROM projects WHERE workspace_id = $1 AND id = $2
	`, workspaceID, projectID).Scan(&current)
	if err != nil {
		return err
	}
	// 主聊天自己的消息不再写回自己。先无锁判断，避免和正在回复主聊天的事务对锁。
	if current.Valid && current.UUID == sourceCardID {
		return nil
	}

	current, err = lockProjectMainDiscussion(ctx, tx, workspaceID, projectID)
	if err != nil {
		return err
	}
	if current.Valid && current.UUID == sourceCardID {
		return nil
	}
	mainID, _, err := ensureMainDiscussionTx(ctx, tx, workspaceID, projectID, uuid.Nil, current)
	if err != nil {
		return err
	}
	if mainID == sourceCardID {
		return nil
	}
	if err := lockCard(ctx, tx, workspaceID, mainID); err != nil {
		return err
	}
	actorID, err := ensureCycleAgentSource(ctx, tx, workspaceID)
	if err != nil {
		return err
	}

	// 四段用单元分隔符拼起来，界面按来源类型翻译，不把一种语言写死在事件正文里。
	content := strings.Join([]string{
		kind,
		oneLine(title),
		oneLine(preview),
		cardAppPath(kind, projectID, sourceCardID),
	}, "\x1f")
	tag, err := tx.Exec(ctx, `
		INSERT INTO events (
			id, workspace_id, card_id, event_type, actor_source_id, content, idempotency_key
		)
		VALUES ($1, $2, $3, 'notice', $4, $5, $6)
		ON CONFLICT (workspace_id, idempotency_key)
		WHERE event_type = 'notice' AND idempotency_key IS NOT NULL
		DO NOTHING
	`, uuid.New(), workspaceID, mainID, actorID, content, sourceEventID.String())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return nil
	}
	_, err = tx.Exec(ctx, `
		UPDATE cards SET updated_at = now()
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, mainID)
	return err
}

func lockProjectMainDiscussion(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, projectID uuid.UUID,
) (uuid.NullUUID, error) {
	var current uuid.NullUUID
	err := tx.QueryRow(ctx, `
		SELECT main_discussion_id
		FROM projects
		WHERE workspace_id = $1 AND id = $2
		FOR UPDATE
	`, workspaceID, projectID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.NullUUID{}, domain.ErrNotFound
	}
	return current, err
}

func ensureMainDiscussionTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, projectID, createdBy uuid.UUID,
	current uuid.NullUUID,
) (uuid.UUID, bool, error) {
	if current.Valid {
		usable, err := mainDiscussionUsable(ctx, tx, workspaceID, projectID, current.UUID)
		if err != nil {
			return uuid.Nil, false, err
		}
		if usable {
			return current.UUID, false, nil
		}
	}
	if createdBy == uuid.Nil {
		var err error
		createdBy, err = ensureCycleAgentSource(ctx, tx, workspaceID)
		if err != nil {
			return uuid.Nil, false, err
		}
	}
	cardID := uuid.New()
	_, err := tx.Exec(ctx, `
		INSERT INTO cards (
			id, workspace_id, project_id, kind, title, description,
			discussion_state, required_confirmations, created_by_source_id, pinned_at
		)
		VALUES ($1, $2, $3, 'discussion', $4, $5, 'active', 1, $6, now())
	`, cardID, workspaceID, projectID, mainChatTitle, mainChatDescription, createdBy)
	if err != nil {
		return uuid.Nil, false, err
	}
	_, err = tx.Exec(ctx, `
		UPDATE projects SET main_discussion_id = $3, updated_at = now()
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, projectID, cardID)
	if err != nil {
		return uuid.Nil, false, err
	}
	return cardID, true, nil
}

func mainDiscussionUsable(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, projectID, cardID uuid.UUID,
) (bool, error) {
	var owner uuid.UUID
	var kind, state string
	err := tx.QueryRow(ctx, `
		SELECT project_id, kind, COALESCE(discussion_state, '')
		FROM cards
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, cardID).Scan(&owner, &kind, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return owner == projectID && kind == string(domain.CardKindDiscussion) && state == string(domain.DiscussionStateActive), nil
}

func ensureCycleAgentSource(ctx context.Context, tx pgx.Tx, workspaceID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id FROM sources
		WHERE workspace_id = $1 AND kind = 'cycle_agent'
		ORDER BY created_at, id
		LIMIT 1
	`, workspaceID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}
	id = uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO sources (id, workspace_id, kind, name)
		VALUES ($1, $2, 'cycle_agent', 'Cycle Agent')
	`, id, workspaceID)
	return id, err
}

func isMainDiscussion(ctx context.Context, q db.DBTX, workspaceID, cardID uuid.UUID) (bool, error) {
	var isMain bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM projects
			WHERE workspace_id = $1 AND main_discussion_id = $2
		)
	`, workspaceID, cardID).Scan(&isMain)
	return isMain, err
}

func rejectMainDiscussionMutation(ctx context.Context, q db.DBTX, workspaceID, cardID uuid.UUID) error {
	isMain, err := isMainDiscussion(ctx, q, workspaceID, cardID)
	if err != nil {
		return err
	}
	if isMain {
		return fmt.Errorf("%w: main discussion stays with the project", domain.ErrConflict)
	}
	return nil
}

func oneLine(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > 180 {
		return string(runes[:180]) + "…"
	}
	return value
}

func cardAppPath(kind string, projectID, cardID uuid.UUID) string {
	section := "cards"
	switch domain.CardKind(kind) {
	case domain.CardKindDiscussion:
		section = "chat"
	case domain.CardKindOngoing:
		section = "plans"
	}
	return "/" + section + "/" + url.PathEscape(projectID.String()) + "/" + url.PathEscape(cardID.String())
}
