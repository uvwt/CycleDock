package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/auth"
)

func TestSaveDeviceToolGuard_RequestValidationWithoutDB(t *testing.T) {
	newReq := func(nodeID, body string) *http.Request {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/agent/device-tool-guards/"+nodeID, strings.NewReader(body))
		req.SetPathValue("nodeID", nodeID)
		return req.WithContext(context.WithValue(req.Context(), identityContextKey{}, auth.Identity{
			Role:        "owner",
			WorkspaceID: uuid.New(),
		}))
	}

	t.Run("rejects unknown field with invalid_json", func(t *testing.T) {
		api := &API{}
		resp := httptest.NewRecorder()
		api.saveDeviceToolGuard(resp, newReq("node_1", `{"device_node_id":"node_1","enabled":true,"unexpected":1}`))
		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "invalid JSON request") {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
	})

	t.Run("rejects legacy mode field with invalid_json", func(t *testing.T) {
		api := &API{}
		resp := httptest.NewRecorder()
		api.saveDeviceToolGuard(resp, newReq("node_1", `{"device_node_id":"node_1","mode":"strict"}`))
		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "invalid JSON request") {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
	})

	t.Run("rejects wrong enabled type with invalid_json", func(t *testing.T) {
		api := &API{}
		resp := httptest.NewRecorder()
		api.saveDeviceToolGuard(resp, newReq("node_1", `{"device_node_id":"node_1","enabled":"true"}`))
		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "invalid JSON request") {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
	})

	t.Run("rejects mismatched device_node_id", func(t *testing.T) {
		api := &API{}
		resp := httptest.NewRecorder()
		api.saveDeviceToolGuard(resp, newReq("node_1", `{"device_node_id":"node_2","enabled":false}`))
		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "invalid_node_id") {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
	})
}

func TestSaveGlobalToolGuard_RequestValidationWithoutDB(t *testing.T) {
	newReq := func(role, body string) *http.Request {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/agent/tool-guard", strings.NewReader(body))
		return req.WithContext(context.WithValue(req.Context(), identityContextKey{}, auth.Identity{
			Role:        role,
			WorkspaceID: uuid.New(),
		}))
	}

	t.Run("rejects non-owner with forbidden", func(t *testing.T) {
		api := &API{}
		resp := httptest.NewRecorder()
		api.saveToolGuardSettings(resp, newReq("member", `{"mode":"strict","timeout_seconds":10}`))
		if resp.Code != http.StatusForbidden {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
	})

	t.Run("rejects invalid mode enum", func(t *testing.T) {
		api := &API{}
		resp := httptest.NewRecorder()
		api.saveToolGuardSettings(resp, newReq("owner", `{"mode":"disabled","timeout_seconds":10}`))
		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "invalid_mode") {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
	})

	t.Run("rejects out of range timeout", func(t *testing.T) {
		api := &API{}
		resp := httptest.NewRecorder()
		api.saveToolGuardSettings(resp, newReq("owner", `{"mode":"block_high_risk","timeout_seconds":90}`))
		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "invalid_timeout") {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
	})

	t.Run("rejects non-uuid model_route_id", func(t *testing.T) {
		api := &API{}
		resp := httptest.NewRecorder()
		api.saveToolGuardSettings(resp, newReq("owner", `{"mode":"strict","model_route_id":"not-a-uuid","timeout_seconds":10}`))
		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "invalid_model_route_id") {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
	})
}

func TestGlobalAndDeviceToolGuardHTTPIntegration(t *testing.T) {
	handler, _, _ := newHTTPIntegration(t)
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"password": httpIntegrationPassword,
	}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	cookie := responseCookie(t, login, sessionCookieName)

	// 1. 未保存过设置前读取全局守卫，返回默认 block_high_risk + 10 秒。
	initialGuardResp := doJSON(t, handler, http.MethodGet, "/api/v1/agent/tool-guard", nil, cookie, nil)
	if initialGuardResp.Code != http.StatusOK {
		t.Fatalf("get initial global tool guard failed: %d %s", initialGuardResp.Code, initialGuardResp.Body.String())
	}
	var initialGuard toolGuardSettingsResponse
	decodeRecorder(t, initialGuardResp, &initialGuard)
	if initialGuard.Mode != "block_high_risk" || initialGuard.TimeoutSeconds != 10 || initialGuard.ModelRouteID != "" {
		t.Fatalf("unexpected initial global guard: %+v", initialGuard)
	}

	// 2. 配置模型路由后，保存全局工具守卫配置。
	routeID := uuid.NewString()
	settingsResp := doJSON(t, handler, http.MethodPut, "/api/v1/agent/settings", map[string]any{
		"routes": []map[string]any{{
			"id":                routeID,
			"enabled":           true,
			"provider_type":     "openai_compatible",
			"provider_base_url": "http://127.0.0.1:18090/v1",
			"model":             "guard-mini",
			"api_key":           "sk-test-guard-key",
		}},
		"system_instructions":     "always verify",
		"default_model_route_id":  routeID,
	}, cookie, nil)
	if settingsResp.Code != http.StatusOK {
		t.Fatalf("save settings status=%d body=%s", settingsResp.Code, settingsResp.Body.String())
	}

	saveGlobal := doJSON(t, handler, http.MethodPut, "/api/v1/agent/tool-guard", map[string]any{
		"mode":            "strict",
		"model_route_id":  routeID,
		"timeout_seconds": 25,
		"custom_rules":    "require approval for docker",
	}, cookie, nil)
	if saveGlobal.Code != http.StatusOK {
		t.Fatalf("save global tool guard failed: %d %s", saveGlobal.Code, saveGlobal.Body.String())
	}
	var savedGlobal toolGuardSettingsResponse
	decodeRecorder(t, saveGlobal, &savedGlobal)
	if savedGlobal.Mode != "strict" || savedGlobal.ModelRouteID != routeID || savedGlobal.TimeoutSeconds != 25 || savedGlobal.CustomRules != "require approval for docker" {
		t.Fatalf("unexpected saved global guard: %+v", savedGlobal)
	}

	// 3. 保存全局工具守卫不能冲掉模型路由与全局指令；反之在模型服务页再次保存也不能冲掉全局守卫配置。
	getSettingsAfterGuard := doJSON(t, handler, http.MethodGet, "/api/v1/agent/settings", nil, cookie, nil)
	if getSettingsAfterGuard.Code != http.StatusOK {
		t.Fatalf("get settings after guard save failed: %d %s", getSettingsAfterGuard.Code, getSettingsAfterGuard.Body.String())
	}
	var loadedSettings agentSettingsResponse
	decodeRecorder(t, getSettingsAfterGuard, &loadedSettings)
	if loadedSettings.SystemInstructions != "always verify" || loadedSettings.DefaultModelRouteID != routeID || len(loadedSettings.Routes) != 1 {
		t.Fatalf("saving global tool guard mutated agent settings: %+v", loadedSettings)
	}

	resaveModels := doJSON(t, handler, http.MethodPut, "/api/v1/agent/settings", map[string]any{
		"routes": []map[string]any{{
			"id":                routeID,
			"enabled":           true,
			"provider_type":     "openai_compatible",
			"provider_base_url": "http://127.0.0.1:18090/v1",
			"model":             "guard-mini",
		}},
		"system_instructions":    "updated instructions",
		"default_model_route_id": routeID,
	}, cookie, nil)
	if resaveModels.Code != http.StatusOK {
		t.Fatalf("resave models failed: %d %s", resaveModels.Code, resaveModels.Body.String())
	}
	getGlobalAfterModels := doJSON(t, handler, http.MethodGet, "/api/v1/agent/tool-guard", nil, cookie, nil)
	if getGlobalAfterModels.Code != http.StatusOK {
		t.Fatalf("get global tool guard after model save failed: %d %s", getGlobalAfterModels.Code, getGlobalAfterModels.Body.String())
	}
	decodeRecorder(t, getGlobalAfterModels, &savedGlobal)
	if savedGlobal.Mode != "strict" || savedGlobal.ModelRouteID != routeID || savedGlobal.TimeoutSeconds != 25 || savedGlobal.CustomRules != "require approval for docker" {
		t.Fatalf("resaving models wiped global tool guard settings: %+v", savedGlobal)
	}

	// 4. 设备开关接口收敛为纯布尔读写，未落库默认 false。
	nodeID := "node_tianyi_test"
	getMissingDevice := doJSON(t, handler, http.MethodGet, "/api/v1/agent/device-tool-guards/"+nodeID, nil, cookie, nil)
	if getMissingDevice.Code != http.StatusOK {
		t.Fatalf("get missing device guard failed: %d %s", getMissingDevice.Code, getMissingDevice.Body.String())
	}
	var missingDevice deviceToolGuardResponse
	decodeRecorder(t, getMissingDevice, &missingDevice)
	if missingDevice.DeviceNodeID != nodeID || missingDevice.Enabled {
		t.Fatalf("expected missing device guard enabled=false, got %+v", missingDevice)
	}

	enableDevice := doJSON(t, handler, http.MethodPut, "/api/v1/agent/device-tool-guards/"+nodeID, map[string]any{
		"device_node_id": nodeID,
		"device_name":    "TianYi",
		"enabled":        true,
	}, cookie, nil)
	if enableDevice.Code != http.StatusOK {
		t.Fatalf("enable device guard failed: %d %s", enableDevice.Code, enableDevice.Body.String())
	}
	var savedDevice deviceToolGuardResponse
	decodeRecorder(t, enableDevice, &savedDevice)
	if savedDevice.DeviceNodeID != nodeID || savedDevice.DeviceName != "TianYi" || !savedDevice.Enabled {
		t.Fatalf("unexpected enabled device guard: %+v", savedDevice)
	}

	disableDevice := doJSON(t, handler, http.MethodPut, "/api/v1/agent/device-tool-guards/"+nodeID, map[string]any{
		"device_node_id": nodeID,
		"device_name":    "TianYi",
		"enabled":        false,
	}, cookie, nil)
	if disableDevice.Code != http.StatusOK {
		t.Fatalf("disable device guard failed: %d %s", disableDevice.Code, disableDevice.Body.String())
	}
	decodeRecorder(t, disableDevice, &savedDevice)
	if savedDevice.Enabled {
		t.Fatalf("expected disabled device guard, got %+v", savedDevice)
	}

	listResp := doJSON(t, handler, http.MethodGet, "/api/v1/agent/device-tool-guards", nil, cookie, nil)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list guards status=%d body=%s", listResp.Code, listResp.Body.String())
	}
	var guards []deviceToolGuardResponse
	decodeRecorder(t, listResp, &guards)
	if len(guards) != 1 || guards[0].DeviceNodeID != nodeID || guards[0].Enabled {
		t.Fatalf("unexpected guard list: %+v", guards)
	}

	// 5. MCP 连接开关接口读写 tool_guard_enabled。
	createMCP := doJSON(t, handler, http.MethodPost, "/api/v1/agent/mcp-connections", map[string]any{
		"name":               "github",
		"url":                "https://mcp.example.com/github",
		"enabled":            true,
		"timeout_ms":         5000,
		"tool_guard_enabled": true,
	}, cookie, nil)
	if createMCP.Code != http.StatusCreated {
		t.Fatalf("create mcp failed: %d %s", createMCP.Code, createMCP.Body.String())
	}
	var mcpResp mcpConnectionResponse
	decodeRecorder(t, createMCP, &mcpResp)
	if !mcpResp.ToolGuardEnabled {
		t.Fatalf("expected tool_guard_enabled=true, got %+v", mcpResp)
	}

	// 6. 停用被引用的评估模型路由后，再显式清除路由可恢复为空；同时测试传空字符串清除 model_route_id。
	clearGlobalRoute := doJSON(t, handler, http.MethodPut, "/api/v1/agent/tool-guard", map[string]any{
		"mode":            "block_high_risk",
		"model_route_id":  "",
		"timeout_seconds": 10,
		"custom_rules":    "",
	}, cookie, nil)
	if clearGlobalRoute.Code != http.StatusOK {
		t.Fatalf("clear global route failed: %d %s", clearGlobalRoute.Code, clearGlobalRoute.Body.String())
	}
	var clearedGlobal toolGuardSettingsResponse
	decodeRecorder(t, clearGlobalRoute, &clearedGlobal)
	if clearedGlobal.Mode != "block_high_risk" || clearedGlobal.ModelRouteID != "" || clearedGlobal.TimeoutSeconds != 10 || clearedGlobal.CustomRules != "" {
		t.Fatalf("unexpected cleared global guard: %+v", clearedGlobal)
	}
}
