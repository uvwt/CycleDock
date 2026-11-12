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
		if r.URL.Path == "/v1/models" {
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
			return
		}
		if r.URL.Path == "/v1/chat/completions" {
			var body struct {
				Model string `json:"model"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Model == "cpa-claude-sonnet-4-6-thinking" {
				_, _ = w.Write([]byte(`{
					"id": "chatcmpl-llm-id",
					"choices": [
						{
							"index": 0,
							"message": {
								"role": "assistant",
								"content": "{\"base_model\":\"claude-sonnet-4-6\"}"
							}
						}
					]
				}`))
				return
			}
			_, _ = w.Write([]byte(`{
				"id": "chatcmpl-llm-empty",
				"choices": [
					{
						"index": 0,
						"message": {
							"role": "assistant",
							"content": "{\"base_model\":\"\"}"
						}
					}
				]
			}`))
			return
		}
		http.NotFound(w, r)
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
				"id":                uuid.NewString(),
				"enabled":           true,
				"provider_type":     "openai_compatible",
				"provider_base_url": provider.URL + "/v1",
				"model":             "cpa-claude-sonnet-4-6-thinking",
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
	if len(got.Routes) != 6 {
		t.Fatalf("expected 6 routes, got %d", len(got.Routes))
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
	// Route 4: custom alias automatically identified by LLM -> base_model=claude-sonnet-4-6 + registry limits
	if got.Routes[4].LimitsSource != "registry" || got.Routes[4].BaseModel != "claude-sonnet-4-6" || got.Routes[4].ContextWindowTokens != 1000000 || got.Routes[4].MaxOutputTokens != 128000 {
		t.Fatalf("route[4] LLM auto-identified base_model mismatch: %+v", got.Routes[4])
	}
	// Route 5: user manual override wins over registry
	if got.Routes[5].LimitsSource != "user" || got.Routes[5].ContextWindowTokens != 64000 || got.Routes[5].MaxInputTokens != 56000 || got.Routes[5].MaxOutputTokens != 8000 {
		t.Fatalf("route[5] user override mismatch: %+v", got.Routes[5])
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

func TestCustomAPICredentialLifecycleAndMultiRouteKeySharing(t *testing.T) {
	var lastAuthHeader string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastAuthHeader = r.Header.Get("Authorization")
		if lastAuthHeader == "" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"Missing API key"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"cpa-claude-sonnet"},{"id":"cpa-gpt-5"}]}`))
		case "/v1/chat/completions":
			_, _ = w.Write([]byte(`{
				"id":"chatcmpl-test",
				"choices":[{"index":0,"message":{"role":"assistant","content":"OK"}}]
			}`))
		default:
			http.NotFound(w, r)
		}
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

	// 1. 创建服务商级统一凭据（输入带尾部斜杠，落库应自动归一化且不回显明文密钥）
	createCred := doJSON(t, handler, http.MethodPost, "/api/v1/agent/api-credentials", map[string]any{
		"name":     "CPA 主力网关",
		"base_url": provider.URL + "/v1/",
		"api_key":  "sk-cpa-shared-v1",
	}, cookie, nil)
	if createCred.Code != http.StatusCreated {
		t.Fatalf("create credential status=%d body=%s", createCred.Code, createCred.Body.String())
	}
	if strings.Contains(createCred.Body.String(), "sk-cpa-shared-v1") {
		t.Fatalf("plaintext api_key leaked: %s", createCred.Body.String())
	}
	var credA apiCredentialResponse
	decodeRecorder(t, createCred, &credA)
	if credA.BaseURL != provider.URL+"/v1" || credA.Name != "CPA 主力网关" || !credA.APIKeyConfigured {
		t.Fatalf("unexpected created credential: %+v", credA)
	}

	// 2. 通过 api_credential_id 或带末尾斜杠的同地址 URL 读取模型列表，均应携带已保存 Bearer Key
	for _, payload := range []map[string]any{
		{"api_credential_id": credA.ID},
		{"provider_base_url": provider.URL + "/v1/"},
	} {
		lastAuthHeader = ""
		modelsResp := doJSON(t, handler, http.MethodPost, "/api/v1/agent/provider-models", payload, cookie, nil)
		if modelsResp.Code != http.StatusOK {
			t.Fatalf("provider-models payload=%v status=%d body=%s", payload, modelsResp.Code, modelsResp.Body.String())
		}
		if lastAuthHeader != "Bearer sk-cpa-shared-v1" {
			t.Fatalf("expected Bearer sk-cpa-shared-v1, got %q", lastAuthHeader)
		}
	}

	// 3. 先保存模型 A，再新增第二个模型 B（api_key 为空）：验证刷新后及连通性测试时模型 B 不会丢失密钥
	routeAID := uuid.NewString()
	saveFirst := doJSON(t, handler, http.MethodPut, "/api/v1/agent/settings", map[string]any{
		"routes": []map[string]any{{
			"id":                routeAID,
			"enabled":           true,
			"provider_type":     "openai_compatible",
			"api_credential_id": credA.ID,
			"model":             "cpa-claude-sonnet",
		}},
		"system_instructions": "",
	}, cookie, nil)
	if saveFirst.Code != http.StatusOK {
		t.Fatalf("save first route status=%d body=%s", saveFirst.Code, saveFirst.Body.String())
	}

	routeBID := uuid.NewString()
	saveSecond := doJSON(t, handler, http.MethodPut, "/api/v1/agent/settings", map[string]any{
		"routes": []map[string]any{
			{
				"id":                routeAID,
				"enabled":           true,
				"provider_type":     "openai_compatible",
				"api_credential_id": credA.ID,
				"model":             "cpa-claude-sonnet",
			},
			{
				"id":                routeBID,
				"enabled":           true,
				"provider_type":     "openai_compatible",
				"api_credential_id": credA.ID,
				"model":             "cpa-gpt-5",
				"api_key":           "",
			},
		},
		"system_instructions": "",
	}, cookie, nil)
	if saveSecond.Code != http.StatusOK {
		t.Fatalf("save second route status=%d body=%s", saveSecond.Code, saveSecond.Body.String())
	}

	reloaded := doJSON(t, handler, http.MethodGet, "/api/v1/agent/settings", nil, cookie, nil)
	if reloaded.Code != http.StatusOK {
		t.Fatalf("reload settings status=%d body=%s", reloaded.Code, reloaded.Body.String())
	}
	var reloadedBody agentSettingsResponse
	decodeRecorder(t, reloaded, &reloadedBody)
	if len(reloadedBody.Routes) != 2 || !reloadedBody.Routes[1].APIKeyConfigured || reloadedBody.Routes[1].APICredentialID != credA.ID {
		t.Fatalf("route B lost credential binding after reload: %+v", reloadedBody)
	}

	lastAuthHeader = ""
	testRouteB := doJSON(t, handler, http.MethodPost, "/api/v1/agent/provider-test", map[string]any{
		"provider_type":     "openai_compatible",
		"api_credential_id": credA.ID,
		"route_id":          routeBID,
		"model":             "cpa-gpt-5",
	}, cookie, nil)
	if testRouteB.Code != http.StatusOK || !strings.Contains(testRouteB.Body.String(), `"ok":true`) {
		t.Fatalf("test route B failed: status=%d body=%s", testRouteB.Code, testRouteB.Body.String())
	}
	if lastAuthHeader != "Bearer sk-cpa-shared-v1" {
		t.Fatalf("route B test auth=%q, want Bearer sk-cpa-shared-v1", lastAuthHeader)
	}

	// 4. 在凭据卡片上原地轮换 API Key，无需逐条重建路由即可立即对所有关联路由生效
	rotateCred := doJSON(t, handler, http.MethodPut, "/api/v1/agent/api-credentials/"+credA.ID, map[string]any{
		"name":     "CPA 主力网关",
		"base_url": provider.URL + "/v1",
		"api_key":  "sk-cpa-shared-v2",
	}, cookie, nil)
	if rotateCred.Code != http.StatusOK {
		t.Fatalf("rotate credential status=%d body=%s", rotateCred.Code, rotateCred.Body.String())
	}
	lastAuthHeader = ""
	testRotated := doJSON(t, handler, http.MethodPost, "/api/v1/agent/provider-test", map[string]any{
		"provider_type": "openai_compatible",
		"route_id":      routeBID,
		"model":         "cpa-gpt-5",
	}, cookie, nil)
	if testRotated.Code != http.StatusOK || lastAuthHeader != "Bearer sk-cpa-shared-v2" {
		t.Fatalf("expected rotated key on route B, status=%d auth=%q body=%s", testRotated.Code, lastAuthHeader, testRotated.Body.String())
	}

	// 5. 同一网关地址支持创建第二个独立命名凭据，互不窜用密钥
	createCredB := doJSON(t, handler, http.MethodPost, "/api/v1/agent/api-credentials", map[string]any{
		"name":     "CPA 备用分组 Key",
		"base_url": provider.URL + "/v1",
		"api_key":  "sk-cpa-secondary-key",
	}, cookie, nil)
	if createCredB.Code != http.StatusCreated {
		t.Fatalf("create second credential status=%d body=%s", createCredB.Code, createCredB.Body.String())
	}
	var credB apiCredentialResponse
	decodeRecorder(t, createCredB, &credB)
	lastAuthHeader = ""
	modelsCredB := doJSON(t, handler, http.MethodPost, "/api/v1/agent/provider-models", map[string]any{
		"api_credential_id": credB.ID,
	}, cookie, nil)
	if modelsCredB.Code != http.StatusOK || lastAuthHeader != "Bearer sk-cpa-secondary-key" {
		t.Fatalf("second credential models auth=%q status=%d", lastAuthHeader, modelsCredB.Code)
	}

	// 6. 修改服务商地址但未替换/清空密钥时必须拦截，防止把已有密钥转发到其他地址
	changeURLWithoutKey := doJSON(t, handler, http.MethodPut, "/api/v1/agent/api-credentials/"+credA.ID, map[string]any{
		"name":     "CPA 主力网关",
		"base_url": "http://127.0.0.1:19999/v1",
	}, cookie, nil)
	if changeURLWithoutKey.Code != http.StatusConflict {
		t.Fatalf("expected 409 when changing base_url without replacing key, got %d %s", changeURLWithoutKey.Code, changeURLWithoutKey.Body.String())
	}
	probeChangedURL := doJSON(t, handler, http.MethodPost, "/api/v1/agent/provider-models", map[string]any{
		"api_credential_id": credA.ID,
		"provider_base_url": "http://127.0.0.1:19999/v1",
	}, cookie, nil)
	if probeChangedURL.Code != http.StatusBadRequest || !strings.Contains(probeChangedURL.Body.String(), "api_url_changed") {
		t.Fatalf("expected 400 api_url_changed, got %d %s", probeChangedURL.Code, probeChangedURL.Body.String())
	}

	// 7. 删除凭据 A 时级联清理其关联路由，保留凭据 B
	deleteCredA := doJSON(t, handler, http.MethodDelete, "/api/v1/agent/api-credentials/"+credA.ID, nil, cookie, nil)
	if deleteCredA.Code != http.StatusNoContent {
		t.Fatalf("delete credential A status=%d body=%s", deleteCredA.Code, deleteCredA.Body.String())
	}
	afterDelete := doJSON(t, handler, http.MethodGet, "/api/v1/agent/settings", nil, cookie, nil)
	var afterDeleteBody agentSettingsResponse
	decodeRecorder(t, afterDelete, &afterDeleteBody)
	if len(afterDeleteBody.Routes) != 0 || len(afterDeleteBody.APICredentials) != 1 || afterDeleteBody.APICredentials[0].ID != credB.ID {
		t.Fatalf("unexpected state after deleting credential A: %+v", afterDeleteBody)
	}
}

