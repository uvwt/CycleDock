package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/uvwt/CycleDock/internal/agentdata"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store/db"
)

// CardAttachmentMeta 表示卡片本身（如新建待办时）附带的图片元数据。
// 仅暴露轻量元数据与鉴权访问 URL，绝不包含图片二进制或 base64。
type CardAttachmentMeta struct {
	ID          uuid.UUID `json:"id"`
	WorkspaceID uuid.UUID `json:"workspace_id"`
	CardID      uuid.UUID `json:"card_id"`
	URL         string    `json:"url"`
	MimeType    string    `json:"mime_type"`
	SizeBytes   int64     `json:"size_bytes"`
	Width       int       `json:"width"`
	Height      int       `json:"height"`
	Status      string    `json:"status"` // active | expired | purged
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// CreateTodoCardWithAttachments 在单个事务内创建待办卡片，并对传入的 attachmentIDs
// 加行锁校验（归属当前 workspace、未过期/未清理、未绑定到其他卡片），
// 随后绑定 workspace_attachments.card_id 并按顺序写入 card_attachments。
func (s *Store) CreateTodoCardWithAttachments(
	ctx context.Context,
	workspaceID, projectID, sourceID uuid.UUID,
	title, description string,
	sourceDiscussionID uuid.NullUUID,
	requiredConfirmations int32,
	attachmentIDs []uuid.UUID,
) (db.Card, []CardAttachmentMeta, error) {
	return s.CreateTodoCardWithAttachmentsAndOptions(
		ctx, workspaceID, projectID, sourceID,
		title, description, sourceDiscussionID, requiredConfirmations,
		attachmentIDs, false,
	)
}

// CreateTodoCardWithAttachmentsAndOptions 在单个事务内创建待办卡片，绑定附件，并在 autoStart=true 时立即写入首轮系统执行 Turn。
func (s *Store) CreateTodoCardWithAttachmentsAndOptions(
	ctx context.Context,
	workspaceID, projectID, sourceID uuid.UUID,
	title, description string,
	sourceDiscussionID uuid.NullUUID,
	requiredConfirmations int32,
	attachmentIDs []uuid.UUID,
	autoStart bool,
) (db.Card, []CardAttachmentMeta, error) {
	if requiredConfirmations <= 0 {
		requiredConfirmations = 1
	}
	title, _ = agentdata.NormalizeText(strings.TrimSpace(title))
	description, _ = agentdata.NormalizeText(strings.TrimSpace(description))
	normalizedIDs, err := normalizeCommentAttachmentIDs(attachmentIDs)
	if err != nil {
		return db.Card{}, nil, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return db.Card{}, nil, err
	}
	defer tx.Rollback(ctx)

	q := db.New(tx)
	card, err := q.CreateCard(ctx, db.CreateCardParams{
		ID:                    uuid.New(),
		WorkspaceID:           workspaceID,
		ProjectID:             projectID,
		Kind:                  string(domain.CardKindTodo),
		Title:                 title,
		Description:           description,
		TodoState:             pgtype.Text{String: string(domain.TodoStateTodo), Valid: true},
		SourceDiscussionID:    sourceDiscussionID,
		RequiredConfirmations: requiredConfirmations,
		CreatedBySourceID:     sourceID,
	})
	if err != nil {
		return db.Card{}, nil, mapConstraintError(err)
	}

	var metas []CardAttachmentMeta
	if len(normalizedIDs) > 0 {
		// 复用评论附件同一套行锁校验与 card_id 绑定逻辑，保证所有校验口径一致。
		if _, err := resolveAndBindCommentAttachmentsTx(ctx, tx, workspaceID, card.ID, normalizedIDs); err != nil {
			return db.Card{}, nil, err
		}
		for position, attachmentID := range normalizedIDs {
			if _, err := tx.Exec(ctx, `
				INSERT INTO card_attachments (workspace_id, card_id, attachment_id, position)
				VALUES ($1, $2, $3, $4)
			`, workspaceID, card.ID, attachmentID, position); err != nil {
				return db.Card{}, nil, fmt.Errorf("bind card attachment: %w", err)
			}
		}
		metas, err = listCardAttachmentsTx(ctx, tx, workspaceID, card.ID)
		if err != nil {
			return db.Card{}, nil, err
		}
	}

	if autoStart {
		initialTurnContent := buildInitialWorkTurnContent(title, description)
		turnRequestHash := stableRequestHash(
			"manual_todo_initial_turn",
			workspaceID.String(),
			card.ID.String(),
			sourceID.String(),
			initialTurnContent,
		)
		if _, err := insertTurn(ctx, tx, insertTurnParams{
			WorkspaceID:       workspaceID,
			CardID:            card.ID,
			Source:            domain.TurnSourceSystem,
			Content:           initialTurnContent,
			CreatedBySourceID: uuid.NullUUID{UUID: sourceID, Valid: true},
			DispatchMode:      domain.DispatchModeEnqueue,
			IdempotencyKey:    "manual-todo-start:" + card.ID.String(),
			RequestHash:       turnRequestHash,
		}); err != nil {
			return db.Card{}, nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return db.Card{}, nil, err
	}
	s.notifyWorkspaceChanged(workspaceID)
	if autoStart {
		s.notifyWorkReady()
	}
	return card, metas, nil
}

// ListCardAttachments 查询卡片本身附带的有序图片元数据，并结合当前时间计算实时状态（active/expired/purged）。
func (s *Store) ListCardAttachments(
	ctx context.Context,
	workspaceID, cardID uuid.UUID,
) ([]CardAttachmentMeta, error) {
	return listCardAttachmentsTx(ctx, s.pool, workspaceID, cardID)
}

type cardAttachmentQueryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func listCardAttachmentsTx(
	ctx context.Context,
	q cardAttachmentQueryer,
	workspaceID, cardID uuid.UUID,
) ([]CardAttachmentMeta, error) {
	rows, err := q.Query(ctx, `
		SELECT
			wa.id,
			wa.workspace_id,
			ca.card_id,
			wa.mime_type,
			wa.size_bytes,
			wa.width,
			wa.height,
			wa.created_at,
			wa.expires_at,
			wa.purged_at
		FROM card_attachments ca
		JOIN workspace_attachments wa
		  ON wa.workspace_id = ca.workspace_id
		 AND wa.id = ca.attachment_id
		WHERE ca.workspace_id = $1
		  AND ca.card_id = $2
		ORDER BY ca.position ASC, ca.created_at ASC, wa.id ASC
	`, workspaceID, cardID)
	if err != nil {
		return nil, fmt.Errorf("list card attachments: %w", err)
	}
	defer rows.Close()

	now := time.Now().UTC()
	out := make([]CardAttachmentMeta, 0)
	for rows.Next() {
		var item CardAttachmentMeta
		var purgedAt *time.Time
		if err := rows.Scan(
			&item.ID,
			&item.WorkspaceID,
			&item.CardID,
			&item.MimeType,
			&item.SizeBytes,
			&item.Width,
			&item.Height,
			&item.CreatedAt,
			&item.ExpiresAt,
			&purgedAt,
		); err != nil {
			return nil, fmt.Errorf("scan card attachment: %w", err)
		}
		item.URL = fmt.Sprintf("/api/v1/workspaces/%s/attachments/%s", item.WorkspaceID.String(), item.ID.String())
		switch {
		case purgedAt != nil:
			item.Status = "purged"
		case !item.ExpiresAt.After(now):
			item.Status = "expired"
		default:
			item.Status = "active"
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
