package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestRegenerateMessageAndRetryAttemptModelRouteEndpoints(t *testing.T) {
	handler, _, dataStore := newHTTPIntegration(t)
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]string{"password": httpIntegrationPassword}, nil, nil)
	cookie := responseCookie(t, login, sessionCookieName)
	var me identityResponse
	decodeRecorder(t, login, &me)
	ctx := context.Background()
	workspaceID := uuid.MustParse(me.Workspace.ID)
	sourceID := uuid.MustParse(me.Source.ID)

	project, err := dataStore.CreateProject(ctx, workspaceID, "Regenerate Test", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateDiscussion(ctx, workspaceID, project.ID, sourceID)
	if err != nil {
		t.Fatal(err)
	}

	settings, err := dataStore.SaveAgentSettings(ctx, workspaceID, store.AgentSettingsInput{
		Routes: []store.ModelRouteInput{
			{
				ProviderType:    "openai_compatible",
				ProviderBaseURL: "https://api.openai.com/v1",
				Model:           "gpt-4o",
				Enabled:         true,
			},
			{
				ProviderType:    "openai_compatible",
				ProviderBaseURL: "https://api.openai.com/v1",
				Model:           "claude-sonnet-4",
				Enabled:         true,
			},
			{
				ProviderType:    "openai_compatible",
				ProviderBaseURL: "https://api.openai.com/v1",
				Model:           "disabled-route",
				Enabled:         false,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	route1 := settings.Routes[0]
	route2 := settings.Routes[1]
	disabledRoute := settings.Routes[2]

	// 第 1 轮问答
	q1, err := dataStore.SubmitSessionComment(ctx, store.SubmitSessionCommentParams{
		WorkspaceID: workspaceID, CardID: card.ID, AuthorSourceID: sourceID,
		Content: "Explain Go interfaces", IdempotencyKey: "http-regen-q1", DispatchMode: domain.DispatchModeEnqueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	claim1, err := dataStore.ClaimCardTurnAttempt(ctx, workspaceID, card.ID, settings.SourceID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	a1EventID, _, err := dataStore.PublishReply(ctx, store.PublishReplyParams{
		WorkspaceID: workspaceID, CardID: card.ID, AttemptID: claim1.Attempt.ID, RunnerSourceID: settings.SourceID,
		Model:   "gpt-4o",
		Message: store.ReplyMessage{Type: "text", Content: "Answer by gpt-4o", EndTurn: true},
		IdempotencyKey: "http-regen-a1", FinishOnEnd: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1) 非法 UUID 返回 400
	badUUIDRes := doJSON(t, handler, http.MethodPost,
		"/api/v1/cards/"+card.ID.String()+"/messages/"+a1EventID.String()+"/regenerate",
		map[string]string{"preferred_model_route_id": "not-a-uuid"}, cookie, nil,
	)
	if badUUIDRes.Code != http.StatusBadRequest {
		t.Fatalf("bad uuid status=%d want 400 body=%s", badUUIDRes.Code, badUUIDRes.Body.String())
	}

	// 2) 禁用路由返回 409
	disabledRes := doJSON(t, handler, http.MethodPost,
		"/api/v1/cards/"+card.ID.String()+"/messages/"+a1EventID.String()+"/regenerate",
		map[string]string{"preferred_model_route_id": disabledRoute.ID.String()}, cookie, nil,
	)
	if disabledRes.Code != http.StatusConflict {
		t.Fatalf("disabled route status=%d want 409 body=%s", disabledRes.Code, disabledRes.Body.String())
	}

	// 3) 不存在的路由返回 404
	missingRes := doJSON(t, handler, http.MethodPost,
		"/api/v1/cards/"+card.ID.String()+"/messages/"+a1EventID.String()+"/regenerate",
		map[string]string{"preferred_model_route_id": uuid.NewString()}, cookie, nil,
	)
	if missingRes.Code != http.StatusNotFound {
		t.Fatalf("missing route status=%d want 404 body=%s", missingRes.Code, missingRes.Body.String())
	}

	// 4) 指定 route2 重新生成成功，并验证下一次领取命中 route2
	okRes := doJSON(t, handler, http.MethodPost,
		"/api/v1/cards/"+card.ID.String()+"/messages/"+a1EventID.String()+"/regenerate",
		map[string]string{"preferred_model_route_id": route2.ID.String()}, cookie, nil,
	)
	if okRes.Code != http.StatusOK {
		t.Fatalf("regenerate status=%d want 200 body=%s", okRes.Code, okRes.Body.String())
	}
	var regenResp commentResultResponse
	decodeRecorder(t, okRes, &regenResp)
	if regenResp.TurnID != q1.TurnID.String() {
		t.Fatalf("regenerate turn_id=%s want %s", regenResp.TurnID, q1.TurnID)
	}

	claim2, err := dataStore.ClaimCardTurnAttempt(ctx, workspaceID, card.ID, settings.SourceID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cardCtx, err := dataStore.LoadAgentCardContext(ctx, workspaceID, claim2.Attempt.ID, settings.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	if !cardCtx.PreferredModelRouteID.Valid || cardCtx.PreferredModelRouteID.UUID != route2.ID {
		t.Fatalf("claimed preferred route=%+v want %s", cardCtx.PreferredModelRouteID, route2.ID)
	}

	// 5) 让该 attempt 失败，再通过 POST /attempts/{attemptID}/retry 指定 route1 重试
	if err := dataStore.FinishAttempt(ctx, workspaceID, card.ID, claim2.Attempt.ID, domain.AttemptStatusFailed, "upstream 502", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	retryRes := doJSON(t, handler, http.MethodPost,
		"/api/v1/cards/"+card.ID.String()+"/attempts/"+claim2.Attempt.ID.String()+"/retry",
		map[string]string{"preferred_model_route_id": route1.ID.String()}, cookie, nil,
	)
	if retryRes.Code != http.StatusCreated {
		t.Fatalf("retry attempt status=%d want 201 body=%s", retryRes.Code, retryRes.Body.String())
	}
	claim3, err := dataStore.ClaimCardTurnAttempt(ctx, workspaceID, card.ID, settings.SourceID, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cardCtx3, err := dataStore.LoadAgentCardContext(ctx, workspaceID, claim3.Attempt.ID, settings.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	if !cardCtx3.PreferredModelRouteID.Valid || cardCtx3.PreferredModelRouteID.UUID != route1.ID {
		t.Fatalf("retried preferred route=%+v want %s", cardCtx3.PreferredModelRouteID, route1.ID)
	}
}
