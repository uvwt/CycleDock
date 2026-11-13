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
		api.saveDeviceToolGuard(resp, newReq("node_1", `{"device_node_id":"node_1","mode":"disabled","unexpected":1}`))
		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "invalid JSON request") {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
	})

	t.Run("rejects wrong timeout type with invalid_json", func(t *testing.T) {
		api := &API{}
		resp := httptest.NewRecorder()
		api.saveDeviceToolGuard(resp, newReq("node_1", `{"device_node_id":"node_1","mode":"disabled","timeout_seconds":"10"}`))
		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "invalid JSON request") {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
	})

	t.Run("rejects mismatched device_node_id", func(t *testing.T) {
		api := &API{}
		resp := httptest.NewRecorder()
		api.saveDeviceToolGuard(resp, newReq("node_1", `{"device_node_id":"node_2","mode":"disabled","timeout_seconds":10}`))
		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "invalid_node_id") {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
	})

	t.Run("rejects invalid mode enum", func(t *testing.T) {
		api := &API{}
		resp := httptest.NewRecorder()
		api.saveDeviceToolGuard(resp, newReq("node_1", `{"device_node_id":"node_1","mode":"unknown_mode","timeout_seconds":10}`))
		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "invalid_mode") {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
	})

	t.Run("rejects non-uuid model_route_id", func(t *testing.T) {
		api := &API{}
		resp := httptest.NewRecorder()
		api.saveDeviceToolGuard(resp, newReq("node_1", `{"device_node_id":"node_1","mode":"strict","model_route_id":"not-a-uuid","timeout_seconds":10}`))
		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "invalid_model_route_id") {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
	})
}

func TestDeviceToolGuardSaveHTTPIntegration(t *testing.T) {
	handler, _, _ := newHTTPIntegration(t)
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"password": httpIntegrationPassword,
	}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	cookie := responseCookie(t, login, sessionCookieName)

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
		"system_instructions": "",
	}, cookie, nil)
	if settingsResp.Code != http.StatusOK {
		t.Fatalf("save settings status=%d body=%s", settingsResp.Code, settingsResp.Body.String())
	}

	// 1. 复现前端 DeviceToolGuardSection 默认保存请求（携带 device_node_id、省略 model_route_id）。
	nodeID := "node_tianyi_test"
	saveDefault := doJSON(t, handler, http.MethodPut, "/api/v1/agent/device-tool-guards/"+nodeID, map[string]any{
		"device_node_id":  nodeID,
		"device_name":     "TianYi",
		"mode":            "block_high_risk",
		"timeout_seconds": 15,
		"custom_rules":    "allow /workspace",
	}, cookie, nil)
	if saveDefault.Code != http.StatusOK {
		t.Fatalf("save default device guard failed: %d %s", saveDefault.Code, saveDefault.Body.String())
	}
	var saved deviceToolGuardResponse
	decodeRecorder(t, saveDefault, &saved)
	if saved.DeviceNodeID != nodeID || saved.DeviceName != "TianYi" || saved.Mode != "block_high_risk" ||
		saved.ModelRouteID != "" || saved.TimeoutSeconds != 15 || saved.CustomRules != "allow /workspace" {
		t.Fatalf("unexpected saved guard: %+v", saved)
	}

	// 2. 绑定具体评估模型路由。
	saveWithRoute := doJSON(t, handler, http.MethodPut, "/api/v1/agent/device-tool-guards/"+nodeID, map[string]any{
		"device_node_id":  nodeID,
		"device_name":     "TianYi",
		"mode":            "strict",
		"model_route_id":  routeID,
		"timeout_seconds": 25,
		"custom_rules":    "require approval for docker",
	}, cookie, nil)
	if saveWithRoute.Code != http.StatusOK {
		t.Fatalf("save device guard with route failed: %d %s", saveWithRoute.Code, saveWithRoute.Body.String())
	}
	decodeRecorder(t, saveWithRoute, &saved)
	if saved.Mode != "strict" || saved.ModelRouteID != routeID || saved.TimeoutSeconds != 25 {
		t.Fatalf("unexpected updated guard: %+v", saved)
	}

	// 3. 传 null / 空字符串清空模型路由并使用默认超时。
	saveCleared := doJSON(t, handler, http.MethodPut, "/api/v1/agent/device-tool-guards/"+nodeID, map[string]any{
		"device_node_id":  nodeID,
		"device_name":     nil,
		"mode":            "",
		"model_route_id":  "",
		"timeout_seconds": nil,
		"custom_rules":    nil,
	}, cookie, nil)
	if saveCleared.Code != http.StatusOK {
		t.Fatalf("save cleared device guard failed: %d %s", saveCleared.Code, saveCleared.Body.String())
	}
	var cleared deviceToolGuardResponse
	decodeRecorder(t, saveCleared, &cleared)
	if cleared.Mode != "disabled" || cleared.ModelRouteID != "" || cleared.TimeoutSeconds != 10 || cleared.CustomRules != "" {
		t.Fatalf("unexpected cleared guard: %+v", cleared)
	}

	// 4. 查询列表确认持久化结果一致。
	listResp := doJSON(t, handler, http.MethodGet, "/api/v1/agent/device-tool-guards", nil, cookie, nil)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list guards status=%d body=%s", listResp.Code, listResp.Body.String())
	}
	var guards []deviceToolGuardResponse
	decodeRecorder(t, listResp, &guards)
	if len(guards) != 1 || guards[0].DeviceNodeID != nodeID || guards[0].Mode != "disabled" {
		t.Fatalf("unexpected guard list: %+v", guards)
	}

	// 5. 错误请求返回清晰校验错误而非静默吞掉。
	badMode := doJSON(t, handler, http.MethodPut, "/api/v1/agent/device-tool-guards/"+nodeID, map[string]any{
		"device_node_id": nodeID,
		"mode":           "bogus",
	}, cookie, nil)
	if badMode.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid mode, got %d %s", badMode.Code, badMode.Body.String())
	}

	mismatchNode := doJSON(t, handler, http.MethodPut, "/api/v1/agent/device-tool-guards/"+nodeID, map[string]any{
		"device_node_id": "other_node",
		"mode":           "disabled",
	}, cookie, nil)
	if mismatchNode.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for mismatched node id, got %d %s", mismatchNode.Code, mismatchNode.Body.String())
	}
}
