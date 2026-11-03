package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/uvwt/CycleDock/internal/domain"
)

func TestTimelinePagesLoadTheirOwnAttempts(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()
	// 超过原先 50 次执行的上限，确保最早的历史页也能加载状态。
	for i := range 60 {
		_, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
			WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
			Content: fmt.Sprintf("Question %d", i), IdempotencyKey: fmt.Sprintf("input-%d", i), DispatchMode: domain.DispatchModeEnqueue,
		})
		if err != nil {
			t.Fatal(err)
		}
		claim, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = s.PublishReply(ctx, PublishReplyParams{
			WorkspaceID: f.WorkspaceID, CardID: f.CardID, AttemptID: claim.Attempt.ID, RunnerSourceID: f.AgentID,
			Message: ReplyMessage{Type: "text", Content: "Answer", EndTurn: true}, IdempotencyKey: fmt.Sprintf("reply-%d", i), FinishOnEnd: true,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	seen := make(map[uuid.UUID]bool)
	before := int64(1<<62 - 1)
	for {
		rows, err := s.ListCardTimelineBefore(ctx, f.WorkspaceID, f.CardID, before, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			break
		}
		oldest := rows[0].Sequence
		attempts, err := s.ListTimelineAttempts(ctx, f.WorkspaceID, f.CardID, oldest, before)
		if err != nil {
			t.Fatal(err)
		}
		if len(attempts) != len(rows)/2 {
			t.Fatalf("page attempts=%d want %d", len(attempts), len(rows)/2)
		}
		eventIDs := make(map[uuid.UUID]bool)
		for _, e := range rows {
			eventIDs[e.ID] = true
		}
		for _, a := range attempts {
			if !a.SourceEventID.Valid || !eventIDs[a.SourceEventID.UUID] {
				t.Fatalf("attempt from unloaded input: %+v", a)
			}
			if seen[a.ID] {
				t.Fatalf("duplicate page attempt %s", a.ID)
			}
			seen[a.ID] = true
		}
		before = oldest
	}
	if len(seen) != 60 {
		t.Fatalf("loaded attempts=%d want 60", len(seen))
	}
}
