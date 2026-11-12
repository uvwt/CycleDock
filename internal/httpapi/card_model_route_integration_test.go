package httpapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/uvwt/CycleDock/internal/store"
)

func TestCardModelRouteEndpointIntegration(t *testing.T) {
	handler, _, dataStore := newHTTPIntegration(t)
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]string{"password": httpIntegrationPassword}, nil, nil)
	cookie := responseCookie(t, login, sessionCookieName)
	var me identityResponse
	decodeRecorder(t, login, &me)
	ctx := context.Background()
	workspaceID := uuid.MustParse(me.Workspace.ID)
	sourceID := uuid.MustParse(me.Source.ID)

	project, err := dataStore.CreateProject(ctx, workspaceID, "Model Route Test", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateDiscussion(ctx, workspaceID, project.ID, sourceID)
	if err != nil {
		t.Fatal(err)
	}

	savedSettings, err := dataStore.SaveAgentSettings(ctx, workspaceID, store.AgentSettingsInput{
		Routes: []store.ModelRouteInput{{
			ProviderType:    "openai_compatible",
			ProviderBaseURL: "https://api.openai.com/v1",
			Model:           "gpt-4o",
			Enabled:         true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	route := savedSettings.Routes[0]

	// 1. 设置模型路由
	routeIDStr := route.ID.String()
	res := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+card.ID.String()+"/model-route", map[string]*string{
		"preferred_model_route_id": &routeIDStr,
	}, cookie, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("set model route status=%d body=%s", res.Code, res.Body.String())
	}
	var cardRes cardResponse
	decodeRecorder(t, res, &cardRes)
	if cardRes.PreferredModelRouteID == nil || *cardRes.PreferredModelRouteID != routeIDStr {
		t.Fatalf("expected preferred_model_route_id=%s, got %v", routeIDStr, cardRes.PreferredModelRouteID)
	}

	// 2. GET 读取卡片时包含 preferred_model_route_id
	getRes := doJSON(t, handler, http.MethodGet, "/api/v1/cards/"+card.ID.String(), nil, cookie, nil)
	if getRes.Code != http.StatusOK {
		t.Fatalf("get card status=%d", getRes.Code)
	}
	var getCardDetail struct {
		Card cardResponse `json:"card"`
	}
	decodeRecorder(t, getRes, &getCardDetail)
	if getCardDetail.Card.PreferredModelRouteID == nil || *getCardDetail.Card.PreferredModelRouteID != routeIDStr {
		t.Fatalf("get card preferred_model_route_id mismatch: %v", getCardDetail.Card.PreferredModelRouteID)
	}

	// 3. 清除模型路由
	clearRes := doJSON(t, handler, http.MethodPost, "/api/v1/cards/"+card.ID.String()+"/model-route", map[string]*string{
		"preferred_model_route_id": nil,
	}, cookie, nil)
	if clearRes.Code != http.StatusOK {
		t.Fatalf("clear model route status=%d", clearRes.Code)
	}
	var clearCardRes cardResponse
	decodeRecorder(t, clearRes, &clearCardRes)
	if clearCardRes.PreferredModelRouteID != nil {
		t.Fatalf("expected nil preferred_model_route_id, got %v", clearCardRes.PreferredModelRouteID)
	}

	// 4. 创建讨论时带 preferred_model_route_id
	createRes := doJSON(t, handler, http.MethodPost, "/api/v1/projects/"+project.ID.String()+"/discussions", map[string]*string{
		"preferred_model_route_id": &routeIDStr,
	}, cookie, nil)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("create discussion status=%d body=%s", createRes.Code, createRes.Body.String())
	}
	var createCardRes cardResponse
	decodeRecorder(t, createRes, &createCardRes)
	if createCardRes.PreferredModelRouteID == nil || *createCardRes.PreferredModelRouteID != routeIDStr {
		t.Fatalf("created card preferred_model_route_id mismatch: %v", createCardRes.PreferredModelRouteID)
	}
}
