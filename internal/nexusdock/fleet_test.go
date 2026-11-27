package nexusdock

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestFleetCache_缓存有效期内直接复用(t *testing.T) {
	wsID := uuid.New()
	svc := &Service{
		fleetCache: make(map[uuid.UUID]fleetCacheEntry),
	}

	cachedResp := &FleetResponse{
		Available: true,
		Connected: true,
		Devices: []FleetDevice{
			{ID: "node-cached", Name: "Cached Node", Online: true},
		},
	}

	svc.fleetCache[wsID] = fleetCacheEntry{
		data:      cachedResp,
		fetchedAt: time.Now(),
	}

	// 不传强制刷新时，应直接返回缓存对象
	got, err := svc.Fleet(context.Background(), wsID, false)
	if err != nil {
		t.Fatalf("期望返回缓存，实际发生错误: %v", err)
	}
	if got != cachedResp || len(got.Devices) != 1 || got.Devices[0].ID != "node-cached" {
		t.Errorf("未命中缓存: got=%v", got)
	}

	// 失效缓存后不应再命中旧条目
	svc.invalidateWorkspaceCaches(wsID)
	if _, ok := svc.cachedFleet(wsID); ok {
		t.Fatalf("invalidateWorkspaceCaches 后不应继续命中旧缓存")
	}
}

func TestFetchFleetContext_透传插件信息并按来源与插件复合去重(t *testing.T) {
	rawPayload := `{
		"nodes": [
			{
				"node_id": "node-tianyi",
				"name": "TianYi",
				"online": true,
				"os": "windows",
				"arch": "amd64",
				"version": "1.1.1",
				"capabilities": ["exec_command", "plugin_manage"],
				"context": {
					"plugins": [
						{
							"name": "telegram",
							"version": "0.0.7",
							"enabled": true,
							"description": "Telegram messaging plugin",
							"skills_count": 2,
							"mcp_count": 1,
							"format": "portable"
						},
						{
							"name": "cloudflare",
							"version": "0.1.2",
							"enabled": true,
							"description": "Cloudflare Workers and Agents SDK",
							"skills_count": 2,
							"mcp_count": 1,
							"format": "portable"
						},
						{
							"name": "cloudflare",
							"version": "0.1.2",
							"enabled": true
						}
					],
					"skills": [
						{
							"name": "deploy",
							"description": "Managed deploy skill",
							"source_type": "managed"
						},
						{
							"name": "deploy",
							"description": "Cloudflare plugin deploy skill",
							"source_type": "plugin",
							"plugin_name": "cloudflare"
						},
						{
							"name": "deploy",
							"description": "Telegram plugin deploy skill",
							"source_type": "plugin",
							"plugin_name": "telegram"
						},
						{
							"name": "agents-sdk",
							"description": "Build AI agents on Cloudflare",
							"source_type": "plugin",
							"plugin_name": "cloudflare"
						},
						{
							"name": "agents-sdk",
							"description": "Duplicate entry should be ignored",
							"source_type": "plugin",
							"plugin_name": "cloudflare"
						}
					],
					"common_skills": {
						"items": [
							{
								"name": "deploy",
								"description": "Shared deploy skill",
								"source_type": "shared"
							}
						]
					},
					"dynamic_mcp": [
						{
							"name": "plugin.cloudflare.cloudflare-api",
							"display_name": "cloudflare-api",
							"description": "Official Cloudflare API MCP",
							"status": "active",
							"tool_count": 8,
							"source_type": "plugin",
							"plugin_name": "cloudflare"
						},
						{
							"name": "browseros-neo",
							"display_name": "browseros-neo",
							"description": "Standalone browser MCP",
							"status": "running",
							"tool_count": 12,
							"source_type": "standalone"
						}
					]
				}
			},
			{
				"node_id": "node-legacy",
				"name": "Legacy Phone",
				"online": true,
				"os": "android",
				"arch": "arm64-v8a",
				"version": "0.1.1",
				"capabilities": ["phone_device_status"],
				"context": {
					"skills": [
						{
							"name": "phone-helper",
							"description": "Legacy skill without plugin metadata",
							"source_type": "managed"
						}
					],
					"dynamic_mcp": []
				}
			}
		]
	}`

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "nexus-fleet-test", Version: "1.0.0"}, nil)
	type emptyIn struct{}
	mcp.AddTool(mcpServer, &mcp.Tool{Name: "agentdock_context", Description: "test"},
		func(context.Context, *mcp.CallToolRequest, emptyIn) (*mcp.CallToolResult, map[string]any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: rawPayload}},
			}, nil, nil
		},
	)
	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-access-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		streamable.ServeHTTP(w, r)
	}))
	defer httpServer.Close()

	svc := &Service{}
	resp, err := svc.fetchFleetContext(context.Background(), RuntimeConnection{
		URL:         httpServer.URL,
		AccessToken: "test-access-token",
	})
	if err != nil {
		t.Fatalf("期望成功拉取 Fleet 快照，实际失败: %v", err)
	}
	if len(resp.Devices) != 2 {
		t.Fatalf("期望 2 台设备，实际 %d", len(resp.Devices))
	}

	// 在线设备按名称排序：Legacy Phone 在前，TianYi 在后
	legacy := resp.Devices[0]
	tianyi := resp.Devices[1]

	if legacy.ID != "node-legacy" {
		t.Fatalf("期望第一台设备为 node-legacy，实际: %s", legacy.ID)
	}
	if len(legacy.Plugins) != 0 {
		t.Fatalf("旧版节点不应包含 plugins，实际: %+v", legacy.Plugins)
	}
	legacyJSON, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("序列化旧版节点失败: %v", err)
	}
	if strings.Contains(string(legacyJSON), `"plugins"`) || strings.Contains(string(legacyJSON), `"plugin_name"`) {
		t.Fatalf("旧版节点序列化后应省略空的 plugins 与 plugin_name，实际: %s", string(legacyJSON))
	}

	if tianyi.ID != "node-tianyi" {
		t.Fatalf("期望第二台设备为 node-tianyi，实际: %s", tianyi.ID)
	}
	if len(tianyi.Plugins) != 2 {
		t.Fatalf("期望去重后保留 2 个 Plugin 概览，实际: %+v", tianyi.Plugins)
	}
	if tianyi.Plugins[0].Name != "cloudflare" || tianyi.Plugins[0].Version != "0.1.2" || tianyi.Plugins[0].SkillsCount != 2 || tianyi.Plugins[0].MCPCount != 1 {
		t.Errorf("cloudflare 插件概览字段不匹配: %+v", tianyi.Plugins[0])
	}
	if tianyi.Plugins[1].Name != "telegram" || tianyi.Plugins[1].Version != "0.0.7" {
		t.Errorf("telegram 插件概览字段不匹配: %+v", tianyi.Plugins[1])
	}

	// agents-sdk 重复项被去重（1 个），deploy 来自 managed / plugin(cloudflare) / plugin(telegram) / shared 共 4 个候选均应保留
	if len(tianyi.Skills) != 5 {
		t.Fatalf("期望保留 5 个区分来源与插件的 Skill，实际 %d: %+v", len(tianyi.Skills), tianyi.Skills)
	}
	if tianyi.Skills[0].Name != "agents-sdk" || tianyi.Skills[0].SourceType != "plugin" || tianyi.Skills[0].PluginName != "cloudflare" {
		t.Errorf("agents-sdk 插件归属不正确: %+v", tianyi.Skills[0])
	}
	wantDeploy := []struct {
		source string
		plugin string
	}{
		{source: "managed", plugin: ""},
		{source: "plugin", plugin: "cloudflare"},
		{source: "plugin", plugin: "telegram"},
		{source: "shared", plugin: ""},
	}
	for i, want := range wantDeploy {
		got := tianyi.Skills[i+1]
		if got.Name != "deploy" || got.SourceType != want.source || got.PluginName != want.plugin {
			t.Errorf("deploy[%d] 期望 source=%q plugin=%q，实际: %+v", i, want.source, want.plugin, got)
		}
	}

	if len(tianyi.DynamicMCP) != 2 {
		t.Fatalf("期望 2 个动态 MCP，实际: %+v", tianyi.DynamicMCP)
	}
	if tianyi.DynamicMCP[0].DisplayName != "browseros-neo" || tianyi.DynamicMCP[0].SourceType != "standalone" || tianyi.DynamicMCP[0].PluginName != "" {
		t.Errorf("独立 MCP 字段不正确: %+v", tianyi.DynamicMCP[0])
	}
	if tianyi.DynamicMCP[1].DisplayName != "cloudflare-api" || tianyi.DynamicMCP[1].SourceType != "plugin" || tianyi.DynamicMCP[1].PluginName != "cloudflare" {
		t.Errorf("插件 MCP 字段不正确: %+v", tianyi.DynamicMCP[1])
	}
}
