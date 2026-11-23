package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
)

func insertTestWorkspaceAttachment(
	t *testing.T,
	s *Store,
	workspaceID, sourceID uuid.UUID,
	cardID *uuid.UUID,
	kind string,
) uuid.UUID {
	t.Helper()
	id := uuid.New()
	width, height := 120, 80
	mimeType := "image/png"
	fileName := "demo.png"
	encoding := "identity"
	if kind == "text" {
		width, height = 0, 0
		mimeType = "text/plain"
		fileName = "notes.txt"
		encoding = "gzip"
	}
	now := time.Now().UTC()
	_, err := s.pool.Exec(context.Background(), `
		INSERT INTO workspace_attachments (
			id, workspace_id, card_id, uploaded_by_source_id,
			kind, file_name, storage_encoding,
			mime_type, size_bytes, stored_size_bytes, line_count, width, height,
			sha256, relative_path, created_at, expires_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7,
			$8, 128, 128, 3, $9, $10,
			$11, $12, $13, $14
		)
	`, id, workspaceID, cardID, sourceID,
		kind, fileName, encoding,
		mimeType, width, height,
		"sha-"+id.String(), workspaceID.String()+"/2026-11/"+id.String()+".bin",
		now, now.Add(24*time.Hour),
	)
	if err != nil {
		t.Fatalf("insert workspace_attachment(%s): %v", kind, err)
	}
	return id
}

func TestDeleteCardWithCardAndCommentAttachments(t *testing.T) {
	s := requireIntegration(t)
	ctx := context.Background()
	fx := newFixture(t, s, domain.CardKindTodo)

	// 场景 1：创建带卡片级附件（图片 + 文本）的待办卡片，删除时应正常解绑 workspace_attachments.card_id 并级联清理 card_attachments。
	imgID := insertTestWorkspaceAttachment(t, s, fx.WorkspaceID, fx.HumanID, nil, "image")
	txtID := insertTestWorkspaceAttachment(t, s, fx.WorkspaceID, fx.HumanID, nil, "text")

	cardWithInitialAtts, metas, err := s.CreateTodoCardWithAttachments(
		ctx,
		fx.WorkspaceID, fx.ProjectID, fx.HumanID,
		"带初始附件的待办", "验证可正常删除",
		uuid.NullUUID{}, 1,
		[]uuid.UUID{imgID, txtID},
	)
	if err != nil {
		t.Fatalf("CreateTodoCardWithAttachments: %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("card attachment count=%d want 2", len(metas))
	}

	if err := s.DeleteCard(ctx, fx.WorkspaceID, cardWithInitialAtts.ID, fx.HumanID); err != nil {
		t.Fatalf("DeleteCard(with initial attachments): %v", err)
	}
	if _, err := s.GetCard(ctx, fx.WorkspaceID, cardWithInitialAtts.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted card still exists: err=%v", err)
	}

	var joinCount int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM card_attachments WHERE workspace_id = $1 AND card_id = $2
	`, fx.WorkspaceID, cardWithInitialAtts.ID).Scan(&joinCount); err != nil {
		t.Fatal(err)
	}
	if joinCount != 0 {
		t.Fatalf("card_attachments not cascaded: count=%d", joinCount)
	}

	for _, attID := range []uuid.UUID{imgID, txtID} {
		var wsID uuid.UUID
		var cardID uuid.NullUUID
		if err := s.pool.QueryRow(ctx, `
			SELECT workspace_id, card_id FROM workspace_attachments WHERE id = $1
		`, attID).Scan(&wsID, &cardID); err != nil {
			t.Fatalf("query workspace_attachment %s: %v", attID, err)
		}
		if wsID != fx.WorkspaceID {
			t.Fatalf("workspace_id=%s want %s", wsID, fx.WorkspaceID)
		}
		if cardID.Valid {
			t.Fatalf("workspace_attachment %s card_id=%s want NULL", attID, cardID.UUID)
		}
	}

	// 场景 2：卡片评论/上传阶段直接绑定到 card_id 的附件，删除卡片时同样只置空 card_id。
	commentAttID := insertTestWorkspaceAttachment(t, s, fx.WorkspaceID, fx.HumanID, &fx.CardID, "image")
	if _, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID:    fx.WorkspaceID,
		CardID:         fx.CardID,
		AuthorSourceID: fx.HumanID,
		Content:        "附上截图",
		AttachmentIDs:  []uuid.UUID{commentAttID},
		DispatchMode:   domain.DispatchModeEnqueue,
		IdempotencyKey: "comment-with-att-1",
	}); err != nil {
		t.Fatalf("SubmitSessionComment: %v", err)
	}

	if err := s.DeleteCard(ctx, fx.WorkspaceID, fx.CardID, fx.HumanID); err != nil {
		t.Fatalf("DeleteCard(with comment attachment): %v", err)
	}

	var commentAttCardID uuid.NullUUID
	if err := s.pool.QueryRow(ctx, `
		SELECT card_id FROM workspace_attachments WHERE id = $1 AND workspace_id = $2
	`, commentAttID, fx.WorkspaceID).Scan(&commentAttCardID); err != nil {
		t.Fatalf("query comment attachment: %v", err)
	}
	if commentAttCardID.Valid {
		t.Fatalf("comment attachment card_id=%s want NULL", commentAttCardID.UUID)
	}
}

func TestDeleteCardReferencedByCrossCardEventAndRemoteChannel(t *testing.T) {
	s := requireIntegration(t)
	ctx := context.Background()
	fx := newFixture(t, s, domain.CardKindTodo)

	targetCard, err := s.CreateTodoCard(ctx, fx.WorkspaceID, fx.ProjectID, fx.HumanID, "目标卡片", "", uuid.NullUUID{}, 1)
	if err != nil {
		t.Fatalf("CreateTodoCard: %v", err)
	}

	// 跨卡消息在目标卡片写入 source_card_id = fx.CardID 的事件。
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO events (
			id, workspace_id, card_id, event_type,
			actor_source_id, source_card_id, content
		) VALUES (
			$1, $2, $3, 'comment',
			$4, $5, '跨卡委派内容'
		)
	`, uuid.New(), fx.WorkspaceID, targetCard.ID, fx.AgentID, fx.CardID); err != nil {
		t.Fatalf("insert cross-card event: %v", err)
	}

	// 定时推送远程通道指向 target_card_id = fx.CardID。
	channelID := uuid.New()
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO remote_channels (
			id, workspace_id, name, platform, role, transport,
			project_id, target_card_id, created_by_source_id
		) VALUES (
			$1, $2, '定时推送测试', 'wecom', 'scheduled_push', 'webhook',
			$3, $4, $5
		)
	`, channelID, fx.WorkspaceID, fx.ProjectID, fx.CardID, fx.HumanID); err != nil {
		t.Fatalf("insert remote_channel: %v", err)
	}

	if err := s.DeleteCard(ctx, fx.WorkspaceID, fx.CardID, fx.HumanID); err != nil {
		t.Fatalf("DeleteCard(referenced by event & remote_channel): %v", err)
	}

	var nonNullSourceCards int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM events
		WHERE workspace_id = $1 AND card_id = $2 AND source_card_id IS NOT NULL
	`, fx.WorkspaceID, targetCard.ID).Scan(&nonNullSourceCards); err != nil {
		t.Fatal(err)
	}
	if nonNullSourceCards != 0 {
		t.Fatalf("expected cross-card event source_card_id to be set NULL, got count=%d", nonNullSourceCards)
	}

	var channelTargetCardID uuid.NullUUID
	if err := s.pool.QueryRow(ctx, `
		SELECT target_card_id FROM remote_channels WHERE id = $1 AND workspace_id = $2
	`, channelID, fx.WorkspaceID).Scan(&channelTargetCardID); err != nil {
		t.Fatal(err)
	}
	if channelTargetCardID.Valid {
		t.Fatalf("expected remote_channel target_card_id to be NULL, got %s", channelTargetCardID.UUID)
	}
}
