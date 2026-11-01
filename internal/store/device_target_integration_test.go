package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/uvwt/CycleDock/internal/domain"
)

func TestDeviceTarget_排队编辑和重试保留每条输入的设备快照(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()
	input := SubmitSessionCommentParams{WorkspaceID: f.WorkspaceID, CardID: f.CardID, AuthorSourceID: f.HumanID,
		Content: "Inspect", IdempotencyKey: "device-a", DispatchMode: domain.DispatchModeEnqueue, DeviceTarget: &DeviceTarget{NodeID: "node-a", Name: "RN"}}
	first, err := s.SubmitSessionComment(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if replay, err := s.SubmitSessionComment(ctx, input); err != nil || !replay.Reused || replay.TurnID != first.TurnID {
		t.Fatalf("重发应复用原 Turn: %+v %v", replay, err)
	}
	input.DeviceTarget = &DeviceTarget{NodeID: "node-b", Name: "Mac"}
	if _, err := s.SubmitSessionComment(ctx, input); !errors.Is(err, domain.ErrIdempotencyMismatch) {
		t.Fatalf("同一幂等键不能改设备: %v", err)
	}
	input.IdempotencyKey = "device-b"
	second, err := s.SubmitSessionComment(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	input.IdempotencyKey = "no-device"
	input.DeviceTarget = nil
	third, err := s.SubmitSessionComment(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EditPendingTurn(ctx, f.WorkspaceID, f.CardID, second.TurnID, f.HumanID, "Edited inspection"); err != nil {
		t.Fatal(err)
	}
	pending, err := s.ListPendingTurns(ctx, f.WorkspaceID, f.CardID)
	if err != nil || len(pending) != 3 || pending[0].DeviceNodeID != "node-a" || pending[1].DeviceNodeID != "node-b" || pending[2].DeviceNodeID != "" {
		t.Fatalf("队列目标丢失: %+v %v", pending, err)
	}
	claim, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil || claim.Turn.ID != first.TurnID || claim.Turn.DeviceNodeID != "node-a" {
		t.Fatalf("Claim 必须使用原设备: %+v %v", claim, err)
	}
	loaded, err := s.LoadAgentCardContext(ctx, f.WorkspaceID, claim.Attempt.ID, f.AgentID)
	if err != nil || loaded.DeviceNodeID != "node-a" || loaded.DeviceName != "RN" || loaded.TurnContent != "Inspect" {
		t.Fatalf("运行时目标错误或污染用户正文: %+v %v", loaded, err)
	}
	if err := s.FinishAttempt(ctx, f.WorkspaceID, f.CardID, claim.Attempt.ID, domain.AttemptStatusFailed, "test retry", time.Now()); err != nil {
		t.Fatal(err)
	}
	retry, err := s.RetryAttempt(ctx, f.WorkspaceID, f.CardID, claim.Attempt.ID, f.AgentID, domain.AttemptReasonRetry)
	if err != nil {
		t.Fatal(err)
	}
	retried, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil || retried.Attempt.ID != retry.Attempt.ID || retried.Turn.ID != first.TurnID || retried.Turn.DeviceNodeID != "node-a" {
		t.Fatalf("技术重试应保留原 Turn 与目标: %+v %v", retried, err)
	}
	if err := s.FinishAttempt(ctx, f.WorkspaceID, f.CardID, retried.Attempt.ID, domain.AttemptStatusCompleted, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		id   string
		node string
	}{{second.TurnID.String(), "node-b"}, {third.TurnID.String(), ""}} {
		next, err := s.ClaimNextTurnAttempt(ctx, f.WorkspaceID, f.AgentID, time.Now().UTC(), time.Minute)
		if err != nil || next.Turn.ID.String() != want.id || next.Turn.DeviceNodeID != want.node {
			t.Fatalf("切换或取消指定必须按 Turn 生效: %+v %v", next, err)
		}
		if err := s.FinishAttempt(ctx, f.WorkspaceID, f.CardID, next.Attempt.ID, domain.AttemptStatusCompleted, "", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
}
