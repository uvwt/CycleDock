package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
)

func TestReplyMessageValidate(t *testing.T) {
	cases := []struct {
		name string
		msg  ReplyMessage
		ok   bool
	}{
		{"text", ReplyMessage{Type: "text", Content: "hello"}, true},
		{"empty text", ReplyMessage{Type: "text"}, false},
		{"attachment", ReplyMessage{Type: "attachment", URL: "https://example.com/a.png"}, true},
		{"javascript URL", ReplyMessage{Type: "attachment", URL: "javascript:alert(1)"}, false},
		{"credentials in URL", ReplyMessage{Type: "attachment", URL: "https://user:pass@example.com"}, false},
		{"unsafe image", ReplyMessage{Type: "text", Content: "hello", Images: []string{"data:image/svg+xml,foo"}}, false},
		{"choice", ReplyMessage{Type: "choice", EndTurn: true, Widget: &ChoiceWidget{
			Question: "Proceed?", Options: []ChoiceOption{{Value: "yes", Label: "Yes"}, {Value: "no", Label: "No"}},
		}}, true},
		{"choice not ending", ReplyMessage{Type: "choice", Widget: &ChoiceWidget{
			Question: "Proceed?", Options: []ChoiceOption{{Value: "yes", Label: "Yes"}, {Value: "no", Label: "No"}},
		}}, false},
		{"duplicate choices", ReplyMessage{Type: "choice", EndTurn: true, Widget: &ChoiceWidget{
			Question: "Proceed?", Options: []ChoiceOption{{Value: "yes", Label: "Yes"}, {Value: "yes", Label: "Same"}},
		}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.msg.Validate(); (err == nil) != tc.ok {
				t.Fatalf("valid=%v want %v: %v", err == nil, tc.ok, err)
			}
		})
	}
}

func TestPublishReplyMultipleBubblesAndWaitingHuman(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()
	if _, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		Content: "Inspect", IdempotencyKey: "start", DispatchMode: domain.DispatchModeEnqueue,
	}); err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	p := PublishReplyParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AttemptID: claim.Attempt.ID,
		RunnerSourceID: f.AgentID, IdempotencyKey: "bubble-1",
		Message: ReplyMessage{Type: "text", Content: "Looking at the server", EndTurn: false},
	}
	first, reused, err := s.PublishReply(ctx, p)
	if err != nil || reused || first == uuid.Nil {
		t.Fatalf("first reply id=%s reused=%t err=%v", first, reused, err)
	}
	if again, reused, err := s.PublishReply(ctx, p); err != nil || !reused || again != first {
		t.Fatalf("idempotent retry id=%s reused=%t err=%v", again, reused, err)
	}
	p.Message.Content = "different"
	if _, _, err := s.PublishReply(ctx, p); !errors.Is(err, domain.ErrIdempotencyMismatch) {
		t.Fatalf("mismatched replay: %v", err)
	}
	p.IdempotencyKey = "bubble-2"
	p.Message = ReplyMessage{Type: "choice", EndTurn: true, Widget: &ChoiceWidget{
		Question: "Continue?", Options: []ChoiceOption{{Value: "yes", Label: "Yes"}, {Value: "no", Label: "No"}},
	}}
	second, reused, err := s.PublishReply(ctx, p)
	if err != nil || reused || second == first {
		t.Fatalf("choice id=%s reused=%t err=%v", second, reused, err)
	}
	attempt, err := s.GetAttempt(ctx, f.WorkspaceID, claim.Attempt.ID)
	if err != nil || attempt.Status != domain.AttemptStatusWaitingHuman {
		t.Fatalf("waiting status=%s err=%v", attempt.Status, err)
	}
	rows, err := s.ListCardTimeline(ctx, f.WorkspaceID, f.CardID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, row := range rows {
		if row.EventType == "message" {
			count++
			if len(row.Message) == 0 {
				t.Fatal("structured message missing from timeline")
			}
		}
	}
	if count != 2 {
		t.Fatalf("message bubbles=%d want 2", count)
	}
	p.IdempotencyKey = "bubble-3"
	p.Message = ReplyMessage{Type: "text", Content: "stale"}
	if _, _, err := s.PublishReply(ctx, p); !errors.Is(err, domain.ErrAttemptNotCurrent) {
		t.Fatalf("stale reply: %v", err)
	}
	p.RunnerSourceID = f.Agent2ID
	if _, _, err := s.PublishReply(ctx, p); !errors.Is(err, domain.ErrAttemptNotCurrent) {
		t.Fatalf("foreign source reply: %v", err)
	}
}

func TestExternalReplyEndTurnCompletesWithoutDuplicateResult(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()
	if _, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		Content: "Finish", IdempotencyKey: "finish", DispatchMode: domain.DispatchModeEnqueue,
	}); err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	p := PublishReplyParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AttemptID: claim.Attempt.ID,
		RunnerSourceID: f.AgentID, IdempotencyKey: "last",
		Message:     ReplyMessage{Type: "text", Content: "All done", EndTurn: true},
		FinishOnEnd: true,
	}
	if _, _, err := s.PublishReply(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, reused, err := s.PublishReply(ctx, p); err != nil || !reused {
		t.Fatalf("terminal retry reused=%v err=%v", reused, err)
	}
	attempt, err := s.GetAttempt(ctx, f.WorkspaceID, claim.Attempt.ID)
	if err != nil || attempt.Status != domain.AttemptStatusCompleted {
		t.Fatalf("completed status=%s err=%v", attempt.Status, err)
	}
	rows, err := s.ListCardTimeline(ctx, f.WorkspaceID, f.CardID)
	if err != nil {
		t.Fatal(err)
	}
	var bubbles, legacy int
	for _, row := range rows {
		if row.EventType == "message" {
			bubbles++
		}
		if row.EventType == "result" {
			legacy++
		}
	}
	if bubbles != 1 || legacy != 0 {
		t.Fatalf("bubbles=%d legacy result=%d", bubbles, legacy)
	}
}
