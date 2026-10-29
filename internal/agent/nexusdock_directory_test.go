package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/uvwt/CycleDock/internal/nexusdock"
)

func TestNexusDockDirectoryTargets_使用授权设备目录并聚合能力(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer oauth-token" || r.Method != http.MethodGet {
			t.Errorf("授权或方法错误: %s %s", r.Method, r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/integrations/devices":
			w.Write([]byte(`{"devices":[{"id":"node_a","name":"TianYi","online":true,"os":"windows","agent_version":"1.1.1"},{"id":"node_b","name":"RN","online":false}]}`))
		case "/v1/integrations/capabilities":
			w.Write([]byte(`{"items":[{"node_id":"node_a","name":"browser","type":"skill"},{"node_id":"node_a","name":"neo","type":"mcp"},{"node_id":"node_a","name":"screenshots","type":"tool"},{"node_id":"node_a","name":"pluginX","type":"plugin"},{"node_id":"node_b","name":"docker","type":"tool"},{"node_id":"other","name":"forbidden","type":"tool"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	text, supported, err := nexusDockDirectoryTargets(context.Background(), nexusdock.RuntimeConnection{URL: server.URL + "/mcp", AccessToken: "oauth-token"})
	if err != nil || !supported || calls != 2 {
		t.Fatalf("REST 目录失败: supported=%v calls=%d err=%v", supported, calls, err)
	}
	for _, want := range []string{"node_a", "TianYi", "node_b", "browser", "neo", "screenshots", "pluginX", "docker"} {
		if !strings.Contains(text, want) {
			t.Fatalf("目录缺少 %s: %s", want, text)
		}
	}
	if strings.Contains(text, "forbidden") || strings.Contains(text, "oauth-token") {
		t.Fatalf("上下文包含越权信息或密钥: %s", text)
	}
}

func TestNexusDockDirectoryTargets_旧Cloud返回404允许回退MCP(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	_, supported, err := nexusDockDirectoryTargets(context.Background(), nexusdock.RuntimeConnection{URL: server.URL + "/mcp", AccessToken: "token"})
	if supported || err != nil {
		t.Fatalf("旧版应允许回退: supported=%v err=%v", supported, err)
	}
}

func TestNexusDockDirectoryTargets_拒绝HTTP重定向凭据(t *testing.T) {
	var redirected atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Store(true) }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	defer server.Close()
	_, supported, err := nexusDockDirectoryTargets(context.Background(), nexusdock.RuntimeConnection{URL: server.URL + "/mcp", AccessToken: "sensitive"})
	if !supported || err == nil || redirected.Load() {
		t.Fatalf("重定向不得携带凭据，supported=%v err=%v redirected=%v", supported, err, redirected.Load())
	}
}
