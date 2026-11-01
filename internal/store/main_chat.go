package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store/db"
)

const (
	mainChatTitle       = "主聊天"
	mainChatDescription = "项目的主聊天。"
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
