package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/uvwt/CycleDock/internal/domain"
)

func TestSubmitSessionCommentQuotePersistenceAndEditPreservesQuote(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindDiscussion)
	ctx := context.Background()

	// 先创建一条人类消息与一条 Agent 回复，作为被引用的目标消息。
	_, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID:    f.WorkspaceID,
		CardID:         f.CardID,
		AuthorSourceID: f.HumanID,
		Content:        "请帮我梳理发布步骤",
		IdempotencyKey: "quote-base-q1",
		DispatchMode:   domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatalf("submit base comment: %v", err)
	}
	claim1, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("claim turn 1: %v", err)
	}
	replyEventID, _, err := s.PublishReply(ctx, PublishReplyParams{
		WorkspaceID:    f.WorkspaceID,
		CardID:         f.CardID,
		AttemptID:      claim1.Attempt.ID,
		RunnerSourceID: f.AgentID,
		Message: ReplyMessage{
			Type:    "text",
			Content: "第一步先检查数据库迁移，第二步执行灰度发布。",
			EndTurn: true,
		},
		IdempotencyKey: "quote-base-a1",
		FinishOnEnd:    true,
	})
	if err != nil {
		t.Fatalf("publish reply 1: %v", err)
	}

	// 提交带结构化 quote 的新评论（含超长文本与 CRLF），验证清理、截断、正文纯净及 LoadAgentCardContext 读取。
	longQuoteText := "  第一行\r\n" + strings.Repeat("灰度", 600) + "  "
	quoted, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID:    f.WorkspaceID,
		CardID:         f.CardID,
		AuthorSourceID: f.HumanID,
		Content:        "第二步具体怎么回滚？",
		Quote: &CommentQuote{
			EventID: replyEventID,
			Sender:  "  Cycle  ",
			Text:    longQuoteText,
		},
		IdempotencyKey: "quote-follow-q2",
		DispatchMode:   domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatalf("submit quoted comment: %v", err)
	}

	// 验证重复提交相同 idempotency key 但不同 quote 会触发幂等冲突。
	if _, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID:    f.WorkspaceID,
		CardID:         f.CardID,
		AuthorSourceID: f.HumanID,
		Content:        "第二步具体怎么回滚？",
		Quote: &CommentQuote{
			EventID: replyEventID,
			Sender:  "Cycle",
			Text:    "不同的引用文本",
		},
		IdempotencyKey: "quote-follow-q2",
		DispatchMode:   domain.DispatchModeEnqueue,
	}); !errors.Is(err, domain.ErrIdempotencyMismatch) {
		t.Fatalf("expected ErrIdempotencyMismatch when quote differs, got %v", err)
	}

	// 验证时间线中的正文纯净且 events.message 保存了规范化后的结构化 quote。
	timeline, err := s.ListCardTimeline(ctx, f.WorkspaceID, f.CardID)
	if err != nil {
		t.Fatalf("list timeline: %v", err)
	}
	var found bool
	for _, ev := range timeline {
		if ev.ID != quoted.EventID {
			continue
		}
		found = true
		if ev.Content != "第二步具体怎么回滚？" {
			t.Fatalf("expected clean content, got %q", ev.Content)
		}
		var payload CommentMessagePayload
		if err := json.Unmarshal(ev.Message, &payload); err != nil {
			t.Fatalf("unmarshal event message: %v", err)
		}
		if payload.Quote == nil {
			t.Fatalf("expected quote in event message payload")
		}
		if payload.Quote.EventID != replyEventID || payload.Quote.Sender != "Cycle" {
			t.Fatalf("unexpected quote metadata: %+v", payload.Quote)
		}
		if strings.Contains(payload.Quote.Text, "\r") {
			t.Fatalf("expected CRLF normalized in quote text")
		}
		if gotRunes := len([]rune(payload.Quote.Text)); gotRunes > MaxCommentQuoteTextRunes {
			t.Fatalf("expected quote text capped at %d runes, got %d", MaxCommentQuoteTextRunes, gotRunes)
		}
	}
	if !found {
		t.Fatalf("quoted event %s not found in timeline", quoted.EventID)
	}

	// 验证 LoadAgentCardContext 能读到 TurnQuote。
	claim2, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("claim turn 2: %v", err)
	}
	cardCtx, err := s.LoadAgentCardContext(ctx, f.WorkspaceID, claim2.Attempt.ID, f.AgentID)
	if err != nil {
		t.Fatalf("load agent card context: %v", err)
	}
	if cardCtx.TurnContent != "第二步具体怎么回滚？" {
		t.Fatalf("unexpected TurnContent: %q", cardCtx.TurnContent)
	}
	if cardCtx.TurnQuote == nil || cardCtx.TurnQuote.EventID != replyEventID || cardCtx.TurnQuote.Sender != "Cycle" {
		t.Fatalf("expected TurnQuote populated, got %+v", cardCtx.TurnQuote)
	}

	// 完成第 2 轮后编辑该带引用的用户消息并重跑，确认 quote 元数据被完整保留。
	if _, _, err := s.PublishReply(ctx, PublishReplyParams{
		WorkspaceID:    f.WorkspaceID,
		CardID:         f.CardID,
		AttemptID:      claim2.Attempt.ID,
		RunnerSourceID: f.AgentID,
		Message:        ReplyMessage{Type: "text", Content: "旧回答", EndTurn: true},
		IdempotencyKey: "quote-follow-a2",
		FinishOnEnd:    true,
	}); err != nil {
		t.Fatalf("publish reply 2: %v", err)
	}

	if _, err := s.EditSessionCommentAndRerun(ctx, EditSessionCommentParams{
		WorkspaceID:    f.WorkspaceID,
		CardID:         f.CardID,
		EventID:        quoted.EventID,
		AuthorSourceID: f.HumanID,
		Content:        "第二步灰度失败怎么快速回滚？",
	}); err != nil {
		t.Fatalf("edit quoted comment: %v", err)
	}

	claim3, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, f.CardID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("claim rerun turn: %v", err)
	}
	rerunCtx, err := s.LoadAgentCardContext(ctx, f.WorkspaceID, claim3.Attempt.ID, f.AgentID)
	if err != nil {
		t.Fatalf("load rerun context: %v", err)
	}
	if rerunCtx.TurnContent != "第二步灰度失败怎么快速回滚？" {
		t.Fatalf("unexpected rerun TurnContent: %q", rerunCtx.TurnContent)
	}
	if rerunCtx.TurnQuote == nil || rerunCtx.TurnQuote.EventID != replyEventID {
		t.Fatalf("expected quote preserved after edit+rerun, got %+v", rerunCtx.TurnQuote)
	}
}

func TestSubmitSessionCommentQuoteValidation(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindDiscussion)
	ctx := context.Background()

	// 无效 UUID（Nil）应返回 Conflict。
	if _, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID:    f.WorkspaceID,
		CardID:         f.CardID,
		AuthorSourceID: f.HumanID,
		Content:        "测试",
		Quote:          &CommentQuote{EventID: uuid.Nil, Sender: "Cycle", Text: "片段"},
		IdempotencyKey: "quote-invalid-nil",
		DispatchMode:   domain.DispatchModeEnqueue,
	}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict for nil quote event_id, got %v", err)
	}

	// 空文本应返回 Conflict。
	if _, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID:    f.WorkspaceID,
		CardID:         f.CardID,
		AuthorSourceID: f.HumanID,
		Content:        "测试",
		Quote:          &CommentQuote{EventID: uuid.New(), Sender: "Cycle", Text: "   "},
		IdempotencyKey: "quote-invalid-empty",
		DispatchMode:   domain.DispatchModeEnqueue,
	}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected ErrConflict for empty quote text, got %v", err)
	}

	// 不属于当前卡片的 event_id 应返回 NotFound。
	if _, err := s.SubmitSessionComment(ctx, SubmitSessionCommentParams{
		WorkspaceID:    f.WorkspaceID,
		CardID:         f.CardID,
		AuthorSourceID: f.HumanID,
		Content:        "测试",
		Quote:          &CommentQuote{EventID: uuid.New(), Sender: "Cycle", Text: "跨卡引用"},
		IdempotencyKey: "quote-invalid-missing",
		DispatchMode:   domain.DispatchModeEnqueue,
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing quoted event, got %v", err)
	}
}
