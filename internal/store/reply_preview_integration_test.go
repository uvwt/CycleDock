package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/uvwt/CycleDock/internal/domain"
)

func TestReplyPreviewSurvivesStoppedAttemptAndRejectsLateText(t *testing.T) {
	for _, mode := range []string{"cancel", "interrupt"} {
		t.Run(mode, func(t *testing.T) {
			s := requireIntegration(t)
			f := newFixture(t, s, domain.CardKindTodo)
			ctx := context.Background()
			if _, err := s.CreateHumanTurn(ctx, f.WorkspaceID, f.CardID, f.HumanID, "写长文", "input", domain.DispatchModeEnqueue); err != nil {
				t.Fatal(err)
			}
			claim, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			p := ReplyPreviewParams{WorkspaceID: f.WorkspaceID, CardID: f.CardID, AttemptID: claim.Attempt.ID, RunnerSourceID: f.AgentID, PreviewID: "generation:call", Content: "已经输出的半篇正文"}
			if err := s.SaveReplyPreview(ctx, p); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "cancel":
				err = s.CancelAttempt(ctx, f.WorkspaceID, f.CardID, claim.Attempt.ID, f.HumanID, "停止", time.Now().UTC())
			case "interrupt":
				_, err = s.CreateHumanTurn(ctx, f.WorkspaceID, f.CardID, f.HumanID, "新要求", "interrupt", domain.DispatchModeInterrupt)
			}
			if err != nil {
				t.Fatal(err)
			}
			p.Content += "迟到的片段"
			if err := s.SaveReplyPreview(ctx, p); !errors.Is(err, domain.ErrAttemptNotCurrent) {
				t.Fatalf("late preview=%v", err)
			}
			// 新建 Store 模拟刷新/进程重启后的读取，不依赖 SSE Hub 或页面内存。
			restored, err := New(s.pool).ListCardTimeline(ctx, f.WorkspaceID, f.CardID)
			if err != nil {
				t.Fatal(err)
			}
			var count int
			for _, event := range restored {
				if event.EventType != "message" {
					continue
				}
				count++
				if event.Content != "已经输出的半篇正文" || event.AttemptID.UUID != claim.Attempt.ID {
					t.Fatalf("restored=%+v", event)
				}
			}
			if count != 1 {
				t.Fatalf("restored bubbles=%d", count)
			}
			other, err := New(s.pool).ListCardTimeline(ctx, uuid.New(), f.CardID)
			if err == nil && len(other) > 0 {
				t.Fatal("preview leaked across workspace")
			}
		})
	}
}

func TestReplyPreviewPromotionKeepsIdentityAndCannotBeReopened(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()
	if _, err := s.CreateHumanTurn(ctx, f.WorkspaceID, f.CardID, f.HumanID, "回复", "input", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	p := ReplyPreviewParams{WorkspaceID: f.WorkspaceID, CardID: f.CardID, AttemptID: claim.Attempt.ID, RunnerSourceID: f.AgentID, PreviewID: "generation:call", Content: "你好"}
	if err := s.SaveReplyPreview(ctx, p); err != nil {
		t.Fatal(err)
	}
	before, err := s.ListCardTimeline(ctx, f.WorkspaceID, f.CardID)
	if err != nil {
		t.Fatal(err)
	}
	draft := before[len(before)-1]
	p.Content = "你好，世界"
	if err := s.SaveReplyPreview(ctx, p); err != nil {
		t.Fatal(err)
	}
	// 同一快照重发幂等；较短快照不能倒退正文。
	if err := s.SaveReplyPreview(ctx, p); err != nil {
		t.Fatal(err)
	}
	shorter := p
	shorter.Content = "你好"
	if err := s.SaveReplyPreview(ctx, shorter); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("shorter preview=%v", err)
	}
	foreign := p
	foreign.RunnerSourceID = f.Agent2ID
	if err := s.SaveReplyPreview(ctx, foreign); !errors.Is(err, domain.ErrAttemptNotCurrent) {
		t.Fatalf("foreign source=%v", err)
	}
	publish := PublishReplyParams{WorkspaceID: f.WorkspaceID, CardID: f.CardID, AttemptID: claim.Attempt.ID, RunnerSourceID: f.AgentID,
		PreviewID: p.PreviewID, IdempotencyKey: "final", Message: ReplyMessage{Type: "text", Content: "你好，世界！", EndTurn: true}}
	id, reused, err := s.PublishReply(ctx, publish)
	if err != nil || reused || id != draft.ID {
		t.Fatalf("promoted=%s reused=%t err=%v", id, reused, err)
	}
	if _, reused, err := s.PublishReply(ctx, publish); err != nil || !reused {
		t.Fatalf("retry reused=%t err=%v", reused, err)
	}
	replayPreview := p
	replayPreview.PreviewID = "retry-generation:call"
	if err := s.SaveReplyPreview(ctx, replayPreview); err != nil {
		t.Fatal(err)
	}
	replay := publish
	replay.PreviewID = replayPreview.PreviewID
	if id, reused, err := s.PublishReply(ctx, replay); err != nil || !reused || id != draft.ID {
		t.Fatalf("new preview replay id=%s reused=%t err=%v", id, reused, err)
	}
	p.Content += "迟到"
	if err := s.SaveReplyPreview(ctx, p); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("reopened preview=%v", err)
	}
	after, err := New(s.pool).ListCardTimeline(ctx, f.WorkspaceID, f.CardID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("duplicate bubbles before=%d after=%d", len(before), len(after))
	}
	final := after[len(after)-1]
	var payload replyPayload
	if err := json.Unmarshal(final.Message, &payload); err != nil {
		t.Fatal(err)
	}
	if final.ID != draft.ID || final.Sequence != draft.Sequence || !final.CreatedAt.Time.Equal(draft.CreatedAt.Time) || payload.Preview || !payload.EndTurn || final.Content != "你好，世界！" {
		t.Fatalf("draft=%+v final=%+v payload=%+v", draft, final, payload)
	}
}

func TestReplyPreviewRacingCancellationPreservesAcceptedPrefix(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()
	if _, err := s.CreateHumanTurn(ctx, f.WorkspaceID, f.CardID, f.HumanID, "回复", "input", domain.DispatchModeEnqueue); err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	p := ReplyPreviewParams{WorkspaceID: f.WorkspaceID, CardID: f.CardID, AttemptID: claim.Attempt.ID, RunnerSourceID: f.AgentID, PreviewID: "generation:call", Content: "已输出"}
	if err := s.SaveReplyPreview(ctx, p); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	saved := make(chan error, 1)
	cancelled := make(chan error, 1)
	p.Content = "已输出新片段"
	go func() { <-start; saved <- s.SaveReplyPreview(ctx, p) }()
	go func() {
		<-start
		cancelled <- s.CancelAttempt(ctx, f.WorkspaceID, f.CardID, claim.Attempt.ID, f.HumanID, "停止", time.Now().UTC())
	}()
	close(start)
	saveErr := <-saved
	if saveErr != nil && !errors.Is(saveErr, domain.ErrAttemptNotCurrent) {
		t.Fatal(saveErr)
	}
	if err := <-cancelled; err != nil {
		t.Fatal(err)
	}
	rows, err := New(s.pool).ListCardTimeline(ctx, f.WorkspaceID, f.CardID)
	if err != nil {
		t.Fatal(err)
	}
	want := "已输出新片段"
	if saveErr != nil {
		want = "已输出"
	}
	if rows[len(rows)-1].Content != want {
		t.Fatalf("saved err=%v restored=%q want=%q", saveErr, rows[len(rows)-1].Content, want)
	}
	if err := s.SaveReplyPreview(ctx, p); !errors.Is(err, domain.ErrAttemptNotCurrent) {
		t.Fatalf("after cancel=%v", err)
	}
}
