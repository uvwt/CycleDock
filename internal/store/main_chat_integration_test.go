package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/uvwt/CycleDock/internal/domain"
)

func TestMainChatReceivesNoticeWithoutTurn(t *testing.T) {
	s := requireIntegration(t)
	ctx := context.Background()
	f := newFixture(t, s, domain.CardKindTodo)

	first, created, err := s.EnsureMainDiscussion(ctx, f.WorkspaceID, f.ProjectID, f.HumanID)
	if err != nil || !created || first.Title != "主聊天" || !first.PinnedAt.Valid {
		t.Fatalf("create main chat: card=%+v created=%v err=%v", first, created, err)
	}
	second, created, err := s.EnsureMainDiscussion(ctx, f.WorkspaceID, f.ProjectID, f.HumanID)
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("reopen main chat: card=%s created=%v err=%v", second.ID, created, err)
	}

	_, err = s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		Content: "服务器巡检完成", IdempotencyKey: "todo-note", DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: first.ID, AuthorSourceID: f.HumanID,
		Content: "主聊天里直接说", IdempotencyKey: "main-note", DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}

	timeline, err := s.ListCardTimeline(ctx, f.WorkspaceID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	notices := 0
	for _, event := range timeline {
		if event.EventType != "notice" {
			continue
		}
		notices++
		parts := strings.Split(event.Content, "\x1f")
		if len(parts) != 4 || parts[0] != "todo" || parts[1] != "Normal" || parts[2] != "服务器巡检完成" {
			t.Fatalf("notice content = %q", event.Content)
		}
		if !strings.Contains(parts[3], "/cards/") || !strings.Contains(parts[3], f.CardID.String()) {
			t.Fatalf("notice path = %q", parts[3])
		}
	}
	if notices != 1 {
		t.Fatalf("notices = %d, want 1", notices)
	}

	var turns int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM card_turns
		WHERE workspace_id = $1 AND card_id = $2 AND source <> 'human'
	`, f.WorkspaceID, first.ID).Scan(&turns); err != nil {
		t.Fatal(err)
	}
	if turns != 0 {
		t.Fatalf("main chat gained %d non-human turns", turns)
	}

	if err := s.ArchiveDiscussion(ctx, f.WorkspaceID, first.ID, f.HumanID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("archive main chat error=%v, want ErrConflict", err)
	}
	if err := s.DeleteDiscussion(ctx, f.WorkspaceID, first.ID, f.HumanID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("delete main chat error=%v, want ErrConflict", err)
	}
}
