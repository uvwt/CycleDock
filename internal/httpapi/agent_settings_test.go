package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/auth"
	"github.com/uvwt/CycleDock/internal/modelclient"
)

func TestAgentSettingsRequireWorkspaceOwner(t *testing.T) {
	api := &API{}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/agent/settings", nil)
	request = request.WithContext(context.WithValue(request.Context(), identityContextKey{}, auth.Identity{
		Role: "member",
	}))
	response := httptest.NewRecorder()

	api.getAgentSettings(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAgentSettingsHTTPAndMCPBearerToken(t *testing.T) {
	handler, _, _ := newHTTPIntegration(t)
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"password": httpIntegrationPassword,
	}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	cookie := responseCookie(t, login, sessionCookieName)

	unknownSecret := doJSON(t, handler, http.MethodPut, "/api/v1/agent/settings", map[string]any{
		"routes": []map[string]any{{
			"id": uuid.NewString(), "enabled": true, "provider_type": "openai_compatible",
			"provider_base_url": "http://127.0.0.1:18090/v1", "model": "test",
			"api_key": "must-never-be-accepted",
		}},
		"system_instructions": "",
	}, cookie, nil)
	if unknownSecret.Code != http.StatusBadRequest {
		t.Fatalf("plaintext api_key was accepted: %d %s", unknownSecret.Code, unknownSecret.Body.String())
	}

	lowLevelOption := doJSON(t, handler, http.MethodPut, "/api/v1/agent/settings", map[string]any{
		"routes": []map[string]any{{
			"id": uuid.NewString(), "enabled": true, "provider_type": "openai_compatible",
			"provider_base_url": "http://127.0.0.1:18090/v1", "model": "test",
		}},
		"max_tool_rounds": 4,
	}, cookie, nil)
	if lowLevelOption.Code != http.StatusBadRequest {
		t.Fatalf("runtime-only option was accepted: %d %s", lowLevelOption.Code, lowLevelOption.Body.String())
	}

	save := doJSON(t, handler, http.MethodPut, "/api/v1/agent/settings", map[string]any{
		"routes": []map[string]any{{
			"id": uuid.NewString(), "enabled": true, "provider_type": "openai_compatible",
			"provider_base_url": "http://127.0.0.1:18090/v1", "model": "test",
			"api_key_env": "CYCLEDOCK_TEST_MODEL_KEY",
		}},
		"system_instructions": "Keep durable facts only.",
	}, cookie, nil)
	if save.Code != http.StatusOK {
		t.Fatalf("save settings: %d %s", save.Code, save.Body.String())
	}
	if strings.Contains(save.Body.String(), "must-never-be-accepted") {
		t.Fatalf("plaintext secret leaked into response: %s", save.Body.String())
	}
	var responseBody map[string]any
	if err := json.Unmarshal(save.Body.Bytes(), &responseBody); err != nil {
		t.Fatal(err)
	}
	for _, hidden := range []string{"enabled", "request_timeout_ms", "max_tool_rounds"} {
		if _, exists := responseBody[hidden]; exists {
			t.Fatalf("top-level runtime-only setting %q leaked into response: %s", hidden, save.Body.String())
		}
	}

	connection := doJSON(t, handler, http.MethodPost, "/api/v1/agent/mcp-connections", map[string]any{
		"name": "tools", "url": "http://127.0.0.1:18091/mcp",
		"bearer_token": "test-sensitive-bearer-token", "enabled": true, "timeout_ms": 5000,
	}, cookie, nil)
	if connection.Code != http.StatusCreated {
		t.Fatalf("save MCP connection: %d %s", connection.Code, connection.Body.String())
	}
	var saved mcpConnectionResponse
	decodeRecorder(t, connection, &saved)
	if !saved.TokenConfigured || strings.Contains(connection.Body.String(), "test-sensitive-bearer-token") {
		t.Fatalf("connection=%+v", saved)
	}
}

func TestAgentProviderModelsUsesServerSideAPIKey(t *testing.T) {
	t.Setenv("CYCLEDOCK_TEST_CATALOG_KEY", "catalog-secret")
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("model catalog path=%q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer catalog-secret" {
			t.Fatalf("authorization=%q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"alpha","name":"Alpha"},{"id":"beta"}]}`))
	}))
	defer provider.Close()

	handler, _, _ := newHTTPIntegration(t)
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"password": httpIntegrationPassword,
	}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	cookie := responseCookie(t, login, sessionCookieName)

	response := doJSON(t, handler, http.MethodPost, "/api/v1/agent/provider-models", map[string]any{
		"provider_base_url": provider.URL + "/v1",
		"api_key_env":       "CYCLEDOCK_TEST_CATALOG_KEY",
	}, cookie, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("list models status=%d body=%s", response.Code, response.Body.String())
	}
	var models []modelclient.ModelInfo
	decodeRecorder(t, response, &models)
	if len(models) != 2 ||
		models[0].ID != "alpha" || models[0].Name != "Alpha" ||
		models[1].ID != "beta" || models[1].Name != "beta" {
		t.Fatalf("models=%+v", models)
	}
}
