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
	"github.com/uvwt/CycleDock/internal/store"
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

	saveWithDirectKey := doJSON(t, handler, http.MethodPut, "/api/v1/agent/settings", map[string]any{
		"routes": []map[string]any{{
			"id": uuid.NewString(), "enabled": true, "provider_type": "openai_compatible",
			"provider_base_url": "http://127.0.0.1:18090/v1", "model": "test",
			"api_key":            "sk-ant-api03-test-direct-key",
			"api_key_configured": false,
		}},
		"system_instructions": "",
	}, cookie, nil)
	if saveWithDirectKey.Code != http.StatusOK {
		t.Fatalf("direct api_key was rejected: %d %s", saveWithDirectKey.Code, saveWithDirectKey.Body.String())
	}
	if strings.Contains(saveWithDirectKey.Body.String(), "sk-ant-api03-test-direct-key") {
		t.Fatalf("plaintext api_key leaked into response: %s", saveWithDirectKey.Body.String())
	}
	var directResp agentSettingsResponse
	decodeRecorder(t, saveWithDirectKey, &directResp)
	if len(directResp.Routes) != 1 || !directResp.Routes[0].APIKeyConfigured {
		t.Fatalf("expected api_key_configured=true, got %+v", directResp)
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

	// 验证直接传入 API Key（包含破折号等非 POSIX 变量名格式）无需环境变量也能正常请求模型列表。
	directKeyProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-custom-direct-api-key" {
			t.Fatalf("direct authorization=%q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"custom-model"}]}`))
	}))
	defer directKeyProvider.Close()

	directResponse := doJSON(t, handler, http.MethodPost, "/api/v1/agent/provider-models", map[string]any{
		"provider_base_url": directKeyProvider.URL + "/v1",
		"api_key":           "sk-custom-direct-api-key",
	}, cookie, nil)
	if directResponse.Code != http.StatusOK {
		t.Fatalf("direct list models status=%d body=%s", directResponse.Code, directResponse.Body.String())
	}
	var directModels []modelclient.ModelInfo
	decodeRecorder(t, directResponse, &directModels)
	if len(directModels) != 1 || directModels[0].ID != "custom-model" {
		t.Fatalf("directModels=%+v", directModels)
	}
}

func TestSaveAgentSettings_AcceptsAPIKeyConfiguredField(t *testing.T) {
	api := &API{}
	body := strings.NewReader(`{
		"routes": [{
			"id": "11111111-1111-1111-1111-111111111111",
			"enabled": true,
			"provider_type": "openai_compatible",
			"provider_base_url": "http://127.0.0.1:18090/v1",
			"model": "test",
			"api_key_configured": true
		}],
		"system_instructions": ""
	}`)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/agent/settings", body)
	request = request.WithContext(context.WithValue(request.Context(), identityContextKey{}, auth.Identity{
		Role:        "owner",
		WorkspaceID: uuid.New(),
	}))
	response := httptest.NewRecorder()

	defer func() {
		// store 为 nil 会在进入数据库层时触发 panic，这是预期的，只要在进入前没有因 invalid_json 返回即可
		_ = recover()
	}()
	api.saveAgentSettings(response, request)
	if response.Code == http.StatusBadRequest && strings.Contains(response.Body.String(), "invalid_json") {
		t.Fatalf("api_key_configured was rejected as invalid_json: %s", response.Body.String())
	}
}

func TestAgentStatusReportsConfigurationWithoutExposingSettings(t *testing.T) {
	_, _, dataStore := newHTTPIntegration(t)
	ctx := context.Background()
	workspace, err := dataStore.CreateWorkspace(ctx, "Model status")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := dataStore.CreateWorkspace(ctx, "Other workspace")
	if err != nil {
		t.Fatal(err)
	}
	api := &API{store: dataStore}
	check := func(workspaceID uuid.UUID, want bool) {
		t.Helper()
		// 普通成员也能查询本工作区状态，但不应取得路由、指令和密钥。
		request := httptest.NewRequest(http.MethodGet, "/api/v1/agent/status", nil)
		request = request.WithContext(context.WithValue(ctx, identityContextKey{}, auth.Identity{WorkspaceID: workspaceID, Role: "member"}))
		response := httptest.NewRecorder()
		api.getAgentStatus(response, request)
		var body map[string]bool
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		configured, exists := body["model_configured"]
		if !exists || len(body) != 1 || configured != want {
			t.Fatalf("status response=%v want configured=%v", body, want)
		}
	}
	check(workspace.ID, false)
	for _, enabled := range []bool{false, true, false} {
		_, err := dataStore.SaveAgentSettings(ctx, workspace.ID, store.AgentSettingsInput{
			Routes:             []store.ModelRouteInput{{Enabled: enabled, ProviderType: "openai_compatible", ProviderBaseURL: "https://example.com/v1", Model: "test-model"}},
			SystemInstructions: "private instructions",
		})
		if err != nil {
			t.Fatal(err)
		}
		check(workspace.ID, enabled)
		check(foreign.ID, false)
	}
	if _, err := dataStore.SaveAgentSettings(ctx, workspace.ID, store.AgentSettingsInput{}); err != nil {
		t.Fatal(err)
	}
	check(workspace.ID, false)
}

func TestAgentSettingsModelLimitsResolutionAndOverride(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [
				{
					"id": "relay-pro-max",
					"name": "Relay Pro Max",
					"context_window": 256000,
					"max_input_tokens": 240000,
					"max_output_tokens": 32000
				}
			]
		}`))
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

	// 先拉取服务商目录，将 relay-pro-max 的容量信息写入短时缓存
	catalogResp := doJSON(t, handler, http.MethodPost, "/api/v1/agent/provider-models", map[string]any{
		"provider_base_url": provider.URL + "/v1",
	}, cookie, nil)
	if catalogResp.Code != http.StatusOK {
		t.Fatalf("provider-models status=%d body=%s", catalogResp.Code, catalogResp.Body.String())
	}

	save := doJSON(t, handler, http.MethodPut, "/api/v1/agent/settings", map[string]any{
		"routes": []map[string]any{
			{
				"id":                uuid.NewString(),
				"enabled":           true,
				"provider_type":     "openai_compatible",
				"provider_base_url": provider.URL + "/v1",
				"model":             "relay-pro-max",
			},
			{
				"id":                uuid.NewString(),
				"enabled":           true,
				"provider_type":     "openai_compatible",
				"provider_base_url": provider.URL + "/v1",
				"model":             "gpt-4o",
			},
			{
				"id":                uuid.NewString(),
				"enabled":           true,
				"provider_type":     "openai_compatible",
				"provider_base_url": provider.URL + "/v1",
				"model":             "gemini-3.8-flash-high",
			},
			{
				"id":                uuid.NewString(),
				"enabled":           true,
				"provider_type":     "openai_compatible",
				"provider_base_url": provider.URL + "/v1",
				"model":             "gemini-3.8-flash-high",
				"base_model":        "gemini-2.5-flash",
			},
			{
				"id":                    uuid.NewString(),
				"enabled":               true,
				"provider_type":         "openai_compatible",
				"provider_base_url":     provider.URL + "/v1",
				"model":                 "gpt-4o",
				"context_window_tokens": 64000,
				"max_input_tokens":      56000,
				"max_output_tokens":     8000,
				"limits_source":         "user",
			},
		},
		"system_instructions": "",
	}, cookie, nil)
	if save.Code != http.StatusOK {
		t.Fatalf("save settings status=%d body=%s", save.Code, save.Body.String())
	}

	var got agentSettingsResponse
	decodeRecorder(t, save, &got)
	if len(got.Routes) != 5 {
		t.Fatalf("expected 5 routes, got %d", len(got.Routes))
	}

	// Route 0: provider metadata
	if got.Routes[0].LimitsSource != "provider" || got.Routes[0].ContextWindowTokens != 256000 || got.Routes[0].MaxInputTokens != 240000 || got.Routes[0].MaxOutputTokens != 32000 {
		t.Fatalf("route[0] provider limits mismatch: %+v", got.Routes[0])
	}
	// Route 1: registry exact match
	if got.Routes[1].LimitsSource != "registry" || got.Routes[1].ContextWindowTokens != 128000 || got.Routes[1].MaxOutputTokens != 16384 {
		t.Fatalf("route[1] registry limits mismatch: %+v", got.Routes[1])
	}
	// Route 2: unknown custom alias -> 0 (not fabricated)
	if got.Routes[2].LimitsSource != "unknown" || got.Routes[2].ContextWindowTokens != 0 || got.Routes[2].MaxInputTokens != 0 || got.Routes[2].MaxOutputTokens != 0 {
		t.Fatalf("route[2] unknown alias should not fabricate limits: %+v", got.Routes[2])
	}
	// Route 3: custom alias with base_model -> registry match
	if got.Routes[3].LimitsSource != "registry" || got.Routes[3].BaseModel != "gemini-2.5-flash" || got.Routes[3].ContextWindowTokens != 1048576 {
		t.Fatalf("route[3] base_model registry limits mismatch: %+v", got.Routes[3])
	}
	// Route 4: user manual override wins over registry
	if got.Routes[4].LimitsSource != "user" || got.Routes[4].ContextWindowTokens != 64000 || got.Routes[4].MaxInputTokens != 56000 || got.Routes[4].MaxOutputTokens != 8000 {
		t.Fatalf("route[4] user override mismatch: %+v", got.Routes[4])
	}

	// 非法配置（max_input_tokens > context_window_tokens）必须被拒绝
	invalid := doJSON(t, handler, http.MethodPut, "/api/v1/agent/settings", map[string]any{
		"routes": []map[string]any{{
			"id":                    uuid.NewString(),
			"enabled":               true,
			"provider_type":         "openai_compatible",
			"provider_base_url":     provider.URL + "/v1",
			"model":                 "gpt-4o",
			"context_window_tokens": 32000,
			"max_input_tokens":      64000,
			"limits_source":         "user",
		}},
		"system_instructions": "",
	}, cookie, nil)
	if invalid.Code != http.StatusConflict {
		t.Fatalf("expected 409 for invalid token bounds, got %d %s", invalid.Code, invalid.Body.String())
	}
}
