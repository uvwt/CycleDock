package store

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/attachments"
	"github.com/uvwt/CycleDock/internal/domain"
)

func makeStoreTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 30, G: 110, B: 210, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return buf.Bytes()
}

func TestLoadAgentCardContextCardTurnNumberWithHighGlobalSequence(t *testing.T) {
	s := requireIntegration(t)
	f := newFixture(t, s, domain.CardKindTodo)
	ctx := context.Background()

	attSvc, err := attachments.New(s.pool, attachments.Config{
		Dir:                t.TempDir(),
		TTL:                24 * time.Hour,
		MaxTotalBytes:      8 << 20,
		MaxTextUploadBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("attachments.New: %v", err)
	}

	// 先在其他卡片上生成多条 Turn，将全库全局序列 card_turns_sequence_seq 推高到 > 1，
	// 复现生产环境新建带附件卡片首轮 TurnSequence 远大于 1 的真实条件。
	for i := 1; i <= 3; i++ {
		if _, err := s.CreateHumanTurn(
			ctx, f.WorkspaceID, f.CardID, f.HumanID,
			fmt.Sprintf("preexisting turn %d", i),
			fmt.Sprintf("preexisting-turn-%d", i),
			domain.DispatchModeEnqueue,
		); err != nil {
			t.Fatalf("create preexisting turn %d: %v", i, err)
		}
	}

	imgAtt, err := attSvc.Upload(ctx, attachments.UploadParams{
		WorkspaceID:        f.WorkspaceID,
		UploadedBySourceID: f.HumanID,
		RawData:            makeStoreTestPNG(t),
	})
	if err != nil {
		t.Fatalf("upload image attachment: %v", err)
	}
	txtAtt, err := attSvc.Upload(ctx, attachments.UploadParams{
		WorkspaceID:        f.WorkspaceID,
		UploadedBySourceID: f.HumanID,
		FileName:           "context.txt",
		RawData:            []byte("important card context\n"),
	})
	if err != nil {
		t.Fatalf("upload text attachment: %v", err)
	}

	// 场景 1：autoStart=true 新建带图片+文本附件的待办卡。
	autoCard, _, err := s.CreateTodoCardWithAttachmentsAndOptions(
		ctx, f.WorkspaceID, f.ProjectID, f.HumanID,
		"带附件自动启动待办", "请检查附带图片和文本",
		uuid.NullUUID{}, 1, []uuid.UUID{imgAtt.ID, txtAtt.ID}, true,
	)
	if err != nil {
		t.Fatalf("CreateTodoCardWithAttachmentsAndOptions(autoStart=true): %v", err)
	}

	claim1, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, autoCard.ID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("claim auto-start first turn: %v", err)
	}
	ctx1, err := s.LoadAgentCardContext(ctx, f.WorkspaceID, claim1.Attempt.ID, f.AgentID)
	if err != nil {
		t.Fatalf("LoadAgentCardContext(first turn): %v", err)
	}
	if ctx1.TurnSequence <= 1 {
		t.Fatalf("expected global TurnSequence > 1, got %d", ctx1.TurnSequence)
	}
	if ctx1.CardTurnNumber != 1 {
		t.Fatalf("expected CardTurnNumber == 1 on card first turn, got %d", ctx1.CardTurnNumber)
	}
	if len(ctx1.CardAttachments) != 2 {
		t.Fatalf("expected 2 card attachments loaded, got %d", len(ctx1.CardAttachments))
	}

	// 首轮失败后重试（AttemptNumber == 2）：仍应保持 CardTurnNumber == 1。
	if err := s.FinishAttempt(ctx, f.WorkspaceID, autoCard.ID, claim1.Attempt.ID, domain.AttemptStatusFailed, "mock failure", time.Now().UTC()); err != nil {
		t.Fatalf("FinishAttempt(failed): %v", err)
	}
	retryRes, err := s.RetryAttempt(ctx, f.WorkspaceID, autoCard.ID, claim1.Attempt.ID, f.AgentID, domain.AttemptReasonRetry)
	if err != nil {
		t.Fatalf("RetryAttempt: %v", err)
	}
	claim1Retry, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, autoCard.ID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("claim retry attempt: %v", err)
	}
	if claim1Retry.Attempt.ID != retryRes.Attempt.ID {
		t.Fatalf("claimed retry attempt=%s want=%s", claim1Retry.Attempt.ID, retryRes.Attempt.ID)
	}
	ctx1Retry, err := s.LoadAgentCardContext(ctx, f.WorkspaceID, claim1Retry.Attempt.ID, f.AgentID)
	if err != nil {
		t.Fatalf("LoadAgentCardContext(retry): %v", err)
	}
	if ctx1Retry.AttemptNumber != 2 || ctx1Retry.CardTurnNumber != 1 {
		t.Fatalf("expected AttemptNumber=2 and CardTurnNumber=1 on retry, got attempt=%d cardTurn=%d", ctx1Retry.AttemptNumber, ctx1Retry.CardTurnNumber)
	}
	if err := s.FinishAttempt(ctx, f.WorkspaceID, autoCard.ID, claim1Retry.Attempt.ID, domain.AttemptStatusCompleted, "", time.Now().UTC()); err != nil {
		t.Fatalf("FinishAttempt(completed): %v", err)
	}

	// 同一卡片第 2 轮：CardTurnNumber 应递增为 2。
	if _, err := s.CreateHumanTurn(ctx, f.WorkspaceID, autoCard.ID, f.HumanID, "补充追问", "auto-card-turn-2", domain.DispatchModeEnqueue); err != nil {
		t.Fatalf("create second turn: %v", err)
	}
	claim2, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, autoCard.ID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("claim second turn: %v", err)
	}
	ctx2, err := s.LoadAgentCardContext(ctx, f.WorkspaceID, claim2.Attempt.ID, f.AgentID)
	if err != nil {
		t.Fatalf("LoadAgentCardContext(second turn): %v", err)
	}
	if ctx2.CardTurnNumber != 2 {
		t.Fatalf("expected CardTurnNumber == 2 on second turn, got %d", ctx2.CardTurnNumber)
	}

	// 场景 2：autoStart=false 建卡后，通过 StartCardExecution 一键启动，CardTurnNumber 仍为 1。
	manualStartCard, _, err := s.CreateTodoCardWithAttachmentsAndOptions(
		ctx, f.WorkspaceID, f.ProjectID, f.HumanID,
		"手动启动带附件待办", "稍后点击开始执行",
		uuid.NullUUID{}, 1, nil, false,
	)
	if err != nil {
		t.Fatalf("create manual start card: %v", err)
	}
	if _, err := s.StartCardExecution(ctx, StartCardExecutionParams{
		WorkspaceID:    f.WorkspaceID,
		CardID:         manualStartCard.ID,
		AuthorSourceID: f.HumanID,
		IdempotencyKey: "manual-start-btn",
	}); err != nil {
		t.Fatalf("StartCardExecution: %v", err)
	}
	manualClaim, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, manualStartCard.ID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("claim manual start card: %v", err)
	}
	manualCtx, err := s.LoadAgentCardContext(ctx, f.WorkspaceID, manualClaim.Attempt.ID, f.AgentID)
	if err != nil {
		t.Fatalf("LoadAgentCardContext(manual start): %v", err)
	}
	if manualCtx.TurnSequence <= 1 || manualCtx.CardTurnNumber != 1 {
		t.Fatalf("expected TurnSequence>1 and CardTurnNumber=1 after StartCardExecution, got seq=%d cardTurn=%d", manualCtx.TurnSequence, manualCtx.CardTurnNumber)
	}

	// 场景 3：未执行的排队 Turn 被取消后，卡片下一条真正执行的 Turn 依然是 CardTurnNumber == 1。
	cancelledFirstCard, _, err := s.CreateTodoCardWithAttachmentsAndOptions(
		ctx, f.WorkspaceID, f.ProjectID, f.HumanID,
		"首轮取消后再发消息", "验证 cancelled_at 不影响首轮计数",
		uuid.NullUUID{}, 1, nil, false,
	)
	if err != nil {
		t.Fatalf("create cancelled-first card: %v", err)
	}
	queuedTurn, err := s.CreateHumanTurn(ctx, f.WorkspaceID, cancelledFirstCard.ID, f.HumanID, "先撤回这条", "cancel-me-turn", domain.DispatchModeEnqueue)
	if err != nil {
		t.Fatalf("create queued turn to cancel: %v", err)
	}
	if err := s.CancelPendingTurn(ctx, f.WorkspaceID, cancelledFirstCard.ID, queuedTurn.Turn.ID, f.HumanID, "取消误发"); err != nil {
		t.Fatalf("CancelPendingTurn: %v", err)
	}
	if _, err := s.CreateHumanTurn(ctx, f.WorkspaceID, cancelledFirstCard.ID, f.HumanID, "真正的第一条消息", "real-first-turn", domain.DispatchModeEnqueue); err != nil {
		t.Fatalf("create real first turn: %v", err)
	}
	realFirstClaim, err := s.ClaimCardTurnAttempt(ctx, f.WorkspaceID, cancelledFirstCard.ID, f.AgentID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("claim real first turn: %v", err)
	}
	realFirstCtx, err := s.LoadAgentCardContext(ctx, f.WorkspaceID, realFirstClaim.Attempt.ID, f.AgentID)
	if err != nil {
		t.Fatalf("LoadAgentCardContext(real first turn): %v", err)
	}
	if realFirstCtx.TurnSequence <= 1 || realFirstCtx.CardTurnNumber != 1 {
		t.Fatalf("expected cancelled turn to be ignored so CardTurnNumber=1, got seq=%d cardTurn=%d", realFirstCtx.TurnSequence, realFirstCtx.CardTurnNumber)
	}
}
