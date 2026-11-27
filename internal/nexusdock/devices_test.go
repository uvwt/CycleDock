package nexusdock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newMockMCPFleetHandler(payload string, isError bool) http.Handler {
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "nexus-device-test", Version: "1.0.0"}, nil)
	type emptyIn struct{}
	mcp.AddTool(mcpServer, &mcp.Tool{Name: "agentdock_context", Description: "test"},
		func(context.Context, *mcp.CallToolRequest, emptyIn) (*mcp.CallToolResult, map[string]any, error) {
			return &mcp.CallToolResult{
				IsError: isError,
				Content: []mcp.Content{&mcp.TextContent{Text: payload}},
			}, nil, nil
		},
	)
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil)
}

func TestDevicesFromFleet_仅返回公开设备摘要且不泄露凭据或上下文(t *testing.T) {
	payload := `{"nodes":[
		{"node_id":"node-b","name":"WinPC","online":false,"os":"windows","arch":"amd64","version":"1.0.0"},
		{"node_id":"node-a","name":"RN","online":true,"os":"linux","arch":"amd64","version":"1.1.1","access_token":"must-not-return","context":{"skills":[{"name":"secret-skill","description":"must-not-return"}]}}
	]}`
	mcpHandler := newMockMCPFleetHandler(payload, false)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" || r.Header.Get("Authorization") != "Bearer secret-token" {
			t.Errorf("MCP 请求路径或鉴权错误: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	}))
	defer server.Close()

	svc := &Service{client: server.Client()}
	fleet, err := svc.fetchFleetContext(context.Background(), RuntimeConnection{
		URL:         server.URL + "/mcp",
		AccessToken: "secret-token",
	})
	if err != nil {
		t.Fatalf("拉取 MCP Fleet 失败: %v", err)
	}
	devices := devicesFromFleet(fleet)
	if len(devices) != 2 || devices[0].ID != "node-a" || !devices[0].Online || devices[1].ID != "node-b" || devices[1].Online {
		t.Fatalf("设备摘要或排序错误: %+v", devices)
	}
	raw, err := json.Marshal(devices)
	if err != nil || strings.Contains(string(raw), "must-not-return") || strings.Contains(string(raw), "secret-skill") || strings.Contains(string(raw), "access_token") {
		t.Fatalf("设备摘要不应包含凭据或工具上下文: %s err=%v", raw, err)
	}
}

func TestDevices_共享Fleet短缓存不重复打上游(t *testing.T) {
	wsID := uuid.New()
	svc := &Service{
		fleetCache: map[uuid.UUID]fleetCacheEntry{
			wsID: {
				data: &FleetResponse{
					Available: true,
					Connected: true,
					Devices: []FleetDevice{
						{
							ID:           "node-cached",
							Name:         "Cached Node",
							Online:       true,
							OS:           "linux",
							Arch:         "amd64",
							AgentVersion: "1.1.1",
							Skills:       []DeviceSkill{{Name: "internal-skill"}},
						},
					},
				},
				fetchedAt: time.Now(),
			},
		},
	}

	devices, err := svc.Devices(context.Background(), wsID, false)
	if err != nil {
		t.Fatalf("期望命中共享缓存，实际错误: %v", err)
	}
	if len(devices) != 1 || devices[0].ID != "node-cached" || devices[0].Name != "Cached Node" {
		t.Fatalf("共享缓存投影结果不匹配: %+v", devices)
	}
	// 修改返回切片不应污染内部缓存
	devices[0].Name = "mutated"
	again, err := svc.Devices(context.Background(), wsID, false)
	if err != nil || len(again) != 1 || again[0].Name != "Cached Node" {
		t.Fatalf("返回切片修改污染了缓存: %+v err=%v", again, err)
	}
}

func TestFetchFleetContext_拒绝重定向且不发送凭据(t *testing.T) {
	var reached atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Store(true)
	}))
	defer destination.Close()

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/mcp", http.StatusFound)
	}))
	defer source.Close()

	svc := &Service{client: source.Client()}
	_, err := svc.fetchFleetContext(context.Background(), RuntimeConnection{
		URL:         source.URL + "/mcp",
		AccessToken: "secret-token",
	})
	if err == nil || !errors.Is(err, errFleetRedirectRefused) || reached.Load() {
		t.Fatalf("重定向必须在发送凭据前拒绝: err=%v reached=%v", err, reached.Load())
	}
}

func TestFetchFleetContext_错误响应不伪装成空列表且不泄露正文(t *testing.T) {
	t.Run("空设备列表正常返回", func(t *testing.T) {
		server := httptest.NewServer(newMockMCPFleetHandler(`{"nodes":[]}`, false))
		defer server.Close()
		svc := &Service{client: server.Client()}
		fleet, err := svc.fetchFleetContext(context.Background(), RuntimeConnection{
			URL:         server.URL + "/mcp",
			AccessToken: "token",
		})
		if err != nil || !fleet.Connected || len(fleet.Devices) != 0 {
			t.Fatalf("空节点数组应成功返回空列表: fleet=%+v err=%v", fleet, err)
		}
	})

	httpCases := []struct {
		name        string
		status      int
		body        string
		wantUnauth  bool
		wantExpired bool
	}{
		{"未授权401", http.StatusUnauthorized, "upstream-secret", true, false},
		{"权限撤销403", http.StatusForbidden, "upstream-secret", false, true},
		{"服务不可用503", http.StatusServiceUnavailable, "upstream-secret", false, false},
		{"无效JSON", http.StatusOK, "upstream-secret", false, false},
	}
	for _, tc := range httpCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprint(w, tc.body)
			}))
			defer server.Close()

			svc := &Service{client: server.Client()}
			_, err := svc.fetchFleetContext(context.Background(), RuntimeConnection{
				URL:         server.URL + "/mcp",
				AccessToken: "token",
			})
			if err == nil {
				t.Fatalf("期望失败，实际返回 nil")
			}
			if errors.Is(err, errUpstreamUnauthorized) != tc.wantUnauth {
				t.Errorf("errUpstreamUnauthorized 映射不匹配: err=%v want=%v", err, tc.wantUnauth)
			}
			if errors.Is(err, ErrConnectionExpired) != tc.wantExpired {
				t.Errorf("ErrConnectionExpired 映射不匹配: err=%v want=%v", err, tc.wantExpired)
			}
			if strings.Contains(err.Error(), "upstream-secret") {
				t.Errorf("错误消息不应泄露上游响应正文: %v", err)
			}
		})
	}

	t.Run("MCP响应超限", func(t *testing.T) {
		oversized := `{"nodes":[{"node_id":"node-big","name":"` + strings.Repeat("x", maxFleetResponseBytes+1024) + `","online":true}]}`
		server := httptest.NewServer(newMockMCPFleetHandler(oversized, false))
		defer server.Close()
		svc := &Service{client: server.Client()}
		_, err := svc.fetchFleetContext(context.Background(), RuntimeConnection{
			URL:         server.URL + "/mcp",
			AccessToken: "token",
		})
		if !errors.Is(err, errFleetResponseTooLarge) {
			t.Fatalf("期望 errFleetResponseTooLarge，实际: %v", err)
		}
	})

	toolCases := []struct {
		name    string
		payload string
		isError bool
	}{
		{"工具返回IsError", "upstream-secret", true},
		{"工具正文非JSON", "upstream-secret", false},
		{"工具缺少nodes字段", `{"other":"upstream-secret"}`, false},
	}
	for _, tc := range toolCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(newMockMCPFleetHandler(tc.payload, tc.isError))
			defer server.Close()
			svc := &Service{client: server.Client()}
			_, err := svc.fetchFleetContext(context.Background(), RuntimeConnection{
				URL:         server.URL + "/mcp",
				AccessToken: "token",
			})
			if err == nil || errors.Is(err, ErrConnectionExpired) || strings.Contains(err.Error(), "upstream-secret") {
				t.Fatalf("异常工具响应不应伪装成空列表或泄露正文: %v", err)
			}
		})
	}
}
