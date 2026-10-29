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
	// 完成活动只表达状态，不回退显示用户输入或最终回复正文。
	activities, err := s.ListProjectActivity(ctx, f.WorkspaceID, f.ProjectID, 20, uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, activity := range activities {
		if activity.ID == claim.Attempt.ID && activity.ActivityType == "attempt" {
			found = true
			if activity.Status != "completed" || activity.Content != "" {
				t.Fatalf("completed activity leaked reply content: %+v", activity)
			}
		}
	}
	if !found {
		t.Fatal("completed attempt missing from project activity")
	}
}

func TestChoiceRepliesRejectOldQuestionsAndAcceptOnlyOneAnswer(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()
	if _, err := s.CreateHumanTurn(ctx, f.WorkspaceID, f.CardID, f.HumanID,
		"Ask a question", "choice-start", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	first, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	message := ReplyMessage{Type: "choice", EndTurn: true, Widget: &ChoiceWidget{
		Question: "Continue?", Options: []ChoiceOption{{Value: "yes", Label: "Yes"}, {Value: "no", Label: "No"}},
	}}
	firstMessage, _, err := s.PublishReply(ctx, PublishReplyParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AttemptID: first.Attempt.ID,
		RunnerSourceID: f.AgentID, Message: message, IdempotencyKey: "question-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	answer := SubmitSessionCommentParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		Content: "yes", IdempotencyKey: "answer-1", ReplyToAttemptID: first.Attempt.ID, ChoiceMessageID: firstMessage,
	}
	accepted, err := s.ReplySessionAndContinue(ctx, answer)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	secondMessage, _, err := s.PublishReply(ctx, PublishReplyParams{
		WorkspaceID: f.WorkspaceID, CardID: f.CardID, AttemptID: second.Attempt.ID,
		RunnerSourceID: f.AgentID, Message: message, IdempotencyKey: "question-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if retry, err := s.ReplySessionAndContinue(ctx, answer); err != nil || !retry.Reused || retry.TurnID != accepted.TurnID {
		t.Fatalf("original answer retry=%+v err=%v", retry, err)
	}
	for _, tc := range []struct {
		name                 string
		attemptID, messageID uuid.UUID
		value                string
		want                 error
	}{
		{"old page answers previous question", first.Attempt.ID, firstMessage, "yes", domain.ErrAttemptNotCurrent},
		{"old message with new attempt", second.Attempt.ID, firstMessage, "yes", domain.ErrConflict},
		{"unknown message", second.Attempt.ID, uuid.New(), "yes", domain.ErrConflict},
		{"unknown option", second.Attempt.ID, secondMessage, "other", domain.ErrConflict},
		{"missing attempt", uuid.Nil, secondMessage, "yes", domain.ErrConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := answer
			p.IdempotencyKey = uuid.NewString()
			p.ReplyToAttemptID, p.ChoiceMessageID, p.Content = tc.attemptID, tc.messageID, tc.value
			if _, err := s.ReplySessionAndContinue(ctx, p); !errors.Is(err, tc.want) {
				t.Fatalf("reply error=%v want=%v", err, tc.want)
			}
		})
	}
	changed := answer
	changed.ReplyToAttemptID, changed.ChoiceMessageID = second.Attempt.ID, secondMessage
	if _, err := s.ReplySessionAndContinue(ctx, changed); !errors.Is(err, domain.ErrIdempotencyMismatch) {
		t.Fatalf("reused key answered a different question: %v", err)
	}
	if pending, err := s.ListPendingTurns(ctx, f.WorkspaceID, f.CardID); err != nil || len(pending) != 0 {
		t.Fatalf("rejected replies created turns: %+v err=%v", pending, err)
	}

	// 两个页面同时回答同一道题，Card 行锁只允许一个新 Turn。
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, value := range []string{"yes", "no"} {
		go func(value string) {
			<-start
			p := changed
			p.IdempotencyKey, p.Content = "answer-2-"+value, value
			_, err := s.ReplySessionAndContinue(ctx, p)
			results <- err
		}(value)
	}
	close(start)
	acceptedCount := 0
	for range 2 {
		err := <-results
		if err == nil {
			acceptedCount++
		} else if !errors.Is(err, domain.ErrAttemptNotCurrent) {
			t.Fatalf("concurrent answer error=%v", err)
		}
	}
	if acceptedCount != 1 {
		t.Fatalf("accepted answers=%d want 1", acceptedCount)
	}
	if pending, err := s.ListPendingTurns(ctx, f.WorkspaceID, f.CardID); err != nil || len(pending) != 1 {
		t.Fatalf("accepted answer turns=%+v err=%v", pending, err)
	}
}
