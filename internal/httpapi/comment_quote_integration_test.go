package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestCommentQuoteHTTPFlowAndValidation(t *testing.T) {
	handler, _, dataStore, _ := newHTTPIntegrationWithAttachments(t)

	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"password": httpIntegrationPassword,
	}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	cookie := responseCookie(t, login, sessionCookieName)
	var identity identityResponse
	decodeRecorder(t, login, &identity)
	workspaceID := uuid.MustParse(identity.Workspace.ID)
	humanSourceID := uuid.MustParse(identity.Source.ID)

	ctx := context.Background()
	project, err := dataStore.CreateProject(ctx, workspaceID, "Quote HTTP Test", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateDiscussion(ctx, workspaceID, project.ID, humanSourceID)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := dataStore.CreateSource(ctx, workspaceID, domain.SourceKindCycleAgent, "Cycle Agent")
	if err != nil {
		t.Fatal(err)
	}

	// 先创建一条基础对话和 Agent 回复。
	if _, err := dataStore.SubmitSessionComment(ctx, store.SubmitSessionCommentParams{
		WorkspaceID:    workspaceID,
		CardID:         card.ID,
		AuthorSourceID: humanSourceID,
		Content:        "说明一下缓存失效策略",
		IdempotencyKey: "http-quote-q1",
		DispatchMode:   domain.DispatchModeEnqueue,
	}); err != nil {
		t.Fatal(err)
	}
	claim, err := dataStore.ClaimCardTurnAttempt(ctx, workspaceID, card.ID, agent.ID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	replyEventID, _, err := dataStore.PublishReply(ctx, store.PublishReplyParams{
		WorkspaceID:    workspaceID,
		CardID:         card.ID,
		AttemptID:      claim.Attempt.ID,
		RunnerSourceID: agent.ID,
		Message: store.ReplyMessage{
			Type:    "text",
			Content: "建议采用写后失效，并配合短 TTL 兜底。",
			EndTurn: true,
		},
		IdempotencyKey: "http-quote-a1",
		FinishOnEnd:    true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 上传一张图片，测试 quote 与 attachments 共同持久化且 enrichTimelineAttachments 不丢失 quote。
	upload := doMultipartUpload(t, handler, "/api/v1/workspaces/"+workspaceID.String()+"/attachments", "file", "shot.png", makeHTTPTestPNG(t, 16, 16), cookie)
	if upload.Code != http.StatusCreated {
		t.Fatalf("upload attachment: %d %s", upload.Code, upload.Body.String())
	}
	var uploaded struct {
		ID string `json:"id"`
	}
	decodeRecorder(t, upload, &uploaded)

	// 1. 校验非法 quote.event_id 返回 400。
	badEventID := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+card.ID.String()+"/comments", map[string]any{
		"content": "追问一下",
		"quote": map[string]any{
			"event_id": "not-a-uuid",
			"sender":   "Cycle",
			"text":     "写后失效",
		},
	}, cookie, map[string]string{"Idempotency-Key": "http-quote-bad-id"})
	if badEventID.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid quote event_id, got %d %s", badEventID.Code, badEventID.Body.String())
	}

	// 2. 校验空 quote.text 返回 400。
	badText := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+card.ID.String()+"/comments", map[string]any{
		"content": "追问一下",
		"quote": map[string]any{
			"event_id": replyEventID.String(),
			"sender":   "Cycle",
			"text":     "   ",
		},
	}, cookie, map[string]string{"Idempotency-Key": "http-quote-bad-text"})
	if badText.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty quote text, got %d %s", badText.Code, badText.Body.String())
	}

	// 3. 合法 quote + attachment 提交成功。
	created := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+card.ID.String()+"/comments", map[string]any{
		"content":        "TTL 设成多少秒合适？",
		"attachment_ids": []string{uploaded.ID},
		"quote": map[string]any{
			"event_id": replyEventID.String(),
			"sender":   "Cycle",
			"text":     "并配合短 TTL 兜底",
		},
	}, cookie, map[string]string{"Idempotency-Key": "http-quote-valid"})
	if created.Code != http.StatusCreated {
		t.Fatalf("submit quoted comment: %d %s", created.Code, created.Body.String())
	}
	var createdResp struct {
		EventID string `json:"event_id"`
	}
	decodeRecorder(t, created, &createdResp)

	// 4. GET /api/v1/cards/{cardID} 返回完整结构化 quote 与 attachments，且正文干净。
	checkDetail := func(wantContent string) {
		t.Helper()
		rec := doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+card.ID.String(), nil, cookie, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("get card detail: %d %s", rec.Code, rec.Body.String())
		}
		var detail cardDetailResponse
		decodeRecorder(t, rec, &detail)
		for _, ev := range detail.Timeline {
			if ev.ID != createdResp.EventID {
				continue
			}
			if ev.Content != wantContent {
				t.Fatalf("content=%q want %q", ev.Content, wantContent)
			}
			var payload store.CommentMessagePayload
			if err := json.Unmarshal(ev.Message, &payload); err != nil {
				t.Fatalf("unmarshal timeline event message: %v", err)
			}
			if payload.Quote == nil {
				t.Fatalf("expected quote preserved in card detail timeline, message=%s", string(ev.Message))
			}
			if payload.Quote.EventID != replyEventID || payload.Quote.Sender != "Cycle" || payload.Quote.Text != "并配合短 TTL 兜底" {
				t.Fatalf("unexpected quote payload: %+v", payload.Quote)
			}
			if len(payload.Attachments) != 1 || payload.Attachments[0].Status != "active" {
				t.Fatalf("expected active attachment alongside quote, got %+v", payload.Attachments)
			}
			return
		}
		t.Fatalf("event %s missing from timeline", createdResp.EventID)
	}

	checkDetail("TTL 设成多少秒合适？")

	// 5. PATCH editComment 保留既有 quote 元数据。
	edited := doJSON(t, handler, http.MethodPatch, "/api/v1/cards/"+card.ID.String()+"/comments/"+createdResp.EventID, map[string]any{
		"content": "短 TTL 一般设 30 秒还是 60 秒？",
	}, cookie, nil)
	if edited.Code != http.StatusOK {
		t.Fatalf("edit quoted comment: %d %s", edited.Code, edited.Body.String())
	}
	checkDetail("短 TTL 一般设 30 秒还是 60 秒？")
}
