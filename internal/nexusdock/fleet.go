package nexusdock

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// DeviceSkill 表示设备上安装的 Skill 摘要。
type DeviceSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SourceType  string `json:"source_type,omitempty"`
	PluginName  string `json:"plugin_name,omitempty"`
}

// DeviceDynamicMCP 表示设备上运行的动态 MCP 服务。
type DeviceDynamicMCP struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status,omitempty"`
	ToolCount   int    `json:"tool_count,omitempty"`
	SourceType  string `json:"source_type,omitempty"`
	PluginName  string `json:"plugin_name,omitempty"`
}

// DevicePlugin 表示设备上安装的 Plugin 概览。
type DevicePlugin struct {
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Enabled     bool   `json:"enabled"`
	Description string `json:"description,omitempty"`
	SkillsCount int    `json:"skills_count,omitempty"`
	MCPCount    int    `json:"mcp_count,omitempty"`
	Format      string `json:"format,omitempty"`
}

// FleetDevice 包含 NexusDock 单台设备的完整运行能力快照。
type FleetDevice struct {
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	Online       bool               `json:"online"`
	OS           string             `json:"os,omitempty"`
	Arch         string             `json:"arch,omitempty"`
	AgentVersion string             `json:"agent_version,omitempty"`
	Capabilities []string           `json:"capabilities,omitempty"`
	Plugins      []DevicePlugin     `json:"plugins,omitempty"`
	Skills       []DeviceSkill      `json:"skills"`
	DynamicMCP   []DeviceDynamicMCP `json:"dynamic_mcp"`
	Error        string             `json:"error,omitempty"`
}

// FleetResponse 包含当前 Workspace 关联的全部设备、在线状态和能力。
type FleetResponse struct {
	Available bool          `json:"available"`
	Connected bool          `json:"connected"`
	Devices   []FleetDevice `json:"devices"`
}

type fleetCacheEntry struct {
	data      *FleetResponse
	fetchedAt time.Time
}

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (b *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(req)
}

type rawFleetPayload struct {
	Nodes []rawFleetNode `json:"nodes"`
}

type rawFleetNode struct {
	NodeID       string   `json:"node_id"`
	Name         string   `json:"name"`
	Online       bool     `json:"online"`
	OS           string   `json:"os"`
	Arch         string   `json:"arch"`
	Version      string   `json:"version"`
	Error        string   `json:"error"`
	Capabilities []string `json:"capabilities"`
	Context      struct {
		Skills []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			SourceType  string `json:"source_type"`
			PluginName  string `json:"plugin_name"`
		} `json:"skills"`
		CommonSkills struct {
			Items []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				SourceType  string `json:"source_type"`
			} `json:"items"`
		} `json:"common_skills"`
		Plugins []struct {
			Name        string `json:"name"`
			Version     string `json:"version"`
			Enabled     bool   `json:"enabled"`
			Description string `json:"description"`
			SkillsCount int    `json:"skills_count"`
			MCPCount    int    `json:"mcp_count"`
			Format      string `json:"format"`
		} `json:"plugins"`
		DynamicMCP []struct {
			Name        string `json:"name"`
			DisplayName string `json:"display_name"`
			Description string `json:"description"`
			Status      string `json:"status"`
			ToolCount   int    `json:"tool_count"`
			SourceType  string `json:"source_type"`
			PluginName  string `json:"plugin_name"`
		} `json:"dynamic_mcp"`
	} `json:"context"`
}

// Fleet 查询当前 Workspace 所属 NexusDock 账号下的设备及各设备上的 Skill 与 MCP。
// 优先通过 MCP agentdock_context 读取完整运行能力；若 MCP 阶段性连接异常，平滑降级为 REST 设备摘要。
func (s *Service) Fleet(ctx context.Context, workspaceID uuid.UUID, forceRefresh bool) (*FleetResponse, error) {
	if s == nil {
		return &FleetResponse{Available: false, Connected: false, Devices: []FleetDevice{}}, nil
	}

	// 缓存命中检查：优先检查内存缓存，命中时无需查询 DB 与上游 MCP。
	if !forceRefresh {
		s.fleetMu.RLock()
		entry, found := s.fleetCache[workspaceID]
		s.fleetMu.RUnlock()
		if found && time.Since(entry.fetchedAt) < 20*time.Second {
			return entry.data, nil
		}
	}

	connection, err := s.Runtime(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	// 尝试通过 MCP agentdock_context 读取设备及能力。
	fleetResp, err := s.fetchFleetContext(ctx, connection)
	if err != nil {
		// MCP 不可用时平滑降级为 REST 设备列表，保证页面不整体报红。
		devices, restErr := s.fetchDevices(ctx, connection)
		if restErr == nil {
			fleetResp = fallbackDevicesToFleet(devices)
		} else {
			return nil, fmt.Errorf("fetch NexusDock fleet: %w (fallback failed: %v)", err, restErr)
		}
	}

	// 写入内存缓存
	s.fleetMu.Lock()
	if s.fleetCache == nil {
		s.fleetCache = make(map[uuid.UUID]fleetCacheEntry)
	}
	s.fleetCache[workspaceID] = fleetCacheEntry{
		data:      fleetResp,
		fetchedAt: time.Now(),
	}
	s.fleetMu.Unlock()

	return fleetResp, nil
}

func (s *Service) fetchFleetContext(ctx context.Context, connection RuntimeConnection) (*FleetResponse, error) {
	if err := validateServiceURL(connection.URL); err != nil {
		return nil, fmt.Errorf("invalid NexusDock MCP origin: %w", err)
	}

	mcpClient := mcp.NewClient(&mcp.Implementation{
		Name:    "cycledock-fleet-viewer",
		Version: "1.0.0",
	}, nil)

	transport := &mcp.StreamableClientTransport{
		Endpoint: connection.URL,
		HTTPClient: &http.Client{
			Transport: &bearerTransport{token: connection.AccessToken, base: http.DefaultTransport},
			Timeout:   15 * time.Second,
		},
		DisableStandaloneSSE: true,
	}

	connectCtx, cancelConnect := context.WithTimeout(ctx, 10*time.Second)
	defer cancelConnect()

	session, err := mcpClient.Connect(connectCtx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect MCP agentdock_context: %w", err)
	}
	defer session.Close()

	callCtx, cancelCall := context.WithTimeout(ctx, 15*time.Second)
	defer cancelCall()

	result, err := session.CallTool(callCtx, &mcp.CallToolParams{
		Name: "agentdock_context",
	})
	if err != nil {
		return nil, fmt.Errorf("call agentdock_context: %w", err)
	}

	var payload rawFleetPayload
	for _, content := range result.Content {
		textContent, ok := content.(*mcp.TextContent)
		if !ok || textContent.Text == "" {
			continue
		}
		if err := json.Unmarshal([]byte(textContent.Text), &payload); err == nil && len(payload.Nodes) > 0 {
			break
		}
	}

	devices := make([]FleetDevice, 0, len(payload.Nodes))
	for _, node := range payload.Nodes {
		id := strings.TrimSpace(node.NodeID)
		if id == "" {
			continue
		}
		name := strings.TrimSpace(node.Name)
		if name == "" {
			name = id
		}

		// 解析 Plugin 概览（旧版节点无此字段时保持为空，序列化时自动省略）
		var plugins []DevicePlugin
		if len(node.Context.Plugins) > 0 {
			seenPlugins := make(map[string]bool, len(node.Context.Plugins))
			plugins = make([]DevicePlugin, 0, len(node.Context.Plugins))
			for _, p := range node.Context.Plugins {
				pName := strings.TrimSpace(p.Name)
				if pName == "" || seenPlugins[pName] {
					continue
				}
				seenPlugins[pName] = true
				plugins = append(plugins, DevicePlugin{
					Name:        pName,
					Version:     strings.TrimSpace(p.Version),
					Enabled:     p.Enabled,
					Description: strings.TrimSpace(p.Description),
					SkillsCount: p.SkillsCount,
					MCPCount:    p.MCPCount,
					Format:      strings.TrimSpace(p.Format),
				})
			}
			sort.Slice(plugins, func(i, j int) bool {
				return plugins[i].Name < plugins[j].Name
			})
		}

		// AgentDock 允许不同来源（managed、shared 或不同 plugin）同时存在同名 Skill 候选，
		// 按 (source_type, plugin_name, name) 复合键去重，避免跨来源或跨插件静默覆盖丢失字段。
		type skillKey struct {
			sourceType string
			pluginName string
			name       string
		}
		seenSkills := make(map[skillKey]bool, len(node.Context.Skills)+len(node.Context.CommonSkills.Items))
		skills := make([]DeviceSkill, 0, len(node.Context.Skills)+len(node.Context.CommonSkills.Items))
		appendSkill := func(rawName, rawDesc, rawSource, rawPlugin string) {
			sName := strings.TrimSpace(rawName)
			if sName == "" {
				return
			}
			sSource := strings.TrimSpace(rawSource)
			sPlugin := strings.TrimSpace(rawPlugin)
			if sPlugin != "" && sSource == "" {
				sSource = "plugin"
			} else if sSource != "plugin" {
				sPlugin = ""
			}
			key := skillKey{sourceType: sSource, pluginName: sPlugin, name: sName}
			if seenSkills[key] {
				return
			}
			seenSkills[key] = true
			skills = append(skills, DeviceSkill{
				Name:        sName,
				Description: strings.TrimSpace(rawDesc),
				SourceType:  sSource,
				PluginName:  sPlugin,
			})
		}
		for _, s := range node.Context.Skills {
			appendSkill(s.Name, s.Description, s.SourceType, s.PluginName)
		}
		for _, s := range node.Context.CommonSkills.Items {
			appendSkill(s.Name, s.Description, s.SourceType, "")
		}
		sort.Slice(skills, func(i, j int) bool {
			if skills[i].Name != skills[j].Name {
				return skills[i].Name < skills[j].Name
			}
			if skills[i].SourceType != skills[j].SourceType {
				return skills[i].SourceType < skills[j].SourceType
			}
			return skills[i].PluginName < skills[j].PluginName
		})

		// 解析动态 MCP
		seenMCP := make(map[string]bool, len(node.Context.DynamicMCP))
		dynamicMCP := make([]DeviceDynamicMCP, 0, len(node.Context.DynamicMCP))
		for _, m := range node.Context.DynamicMCP {
			mName := strings.TrimSpace(m.Name)
			if mName == "" || seenMCP[mName] {
				continue
			}
			seenMCP[mName] = true
			displayName := strings.TrimSpace(m.DisplayName)
			if displayName == "" {
				displayName = mName
			}
			mSource := strings.TrimSpace(m.SourceType)
			mPlugin := strings.TrimSpace(m.PluginName)
			if mPlugin != "" && mSource == "" {
				mSource = "plugin"
			} else if mSource != "plugin" {
				mPlugin = ""
			}
			dynamicMCP = append(dynamicMCP, DeviceDynamicMCP{
				Name:        mName,
				DisplayName: displayName,
				Description: strings.TrimSpace(m.Description),
				Status:      strings.TrimSpace(m.Status),
				ToolCount:   m.ToolCount,
				SourceType:  mSource,
				PluginName:  mPlugin,
			})
		}
		sort.Slice(dynamicMCP, func(i, j int) bool {
			if dynamicMCP[i].DisplayName != dynamicMCP[j].DisplayName {
				return dynamicMCP[i].DisplayName < dynamicMCP[j].DisplayName
			}
			if dynamicMCP[i].PluginName != dynamicMCP[j].PluginName {
				return dynamicMCP[i].PluginName < dynamicMCP[j].PluginName
			}
			return dynamicMCP[i].Name < dynamicMCP[j].Name
		})

		devices = append(devices, FleetDevice{
			ID:           id,
			Name:         name,
			Online:       node.Online,
			OS:           strings.TrimSpace(node.OS),
			Arch:         strings.TrimSpace(node.Arch),
			AgentVersion: strings.TrimSpace(node.Version),
			Capabilities: node.Capabilities,
			Plugins:      plugins,
			Skills:       skills,
			DynamicMCP:   dynamicMCP,
			Error:        strings.TrimSpace(node.Error),
		})
	}

	// 排序：在线设备优先，同状态按设备名称排序
	sort.Slice(devices, func(i, j int) bool {
		if devices[i].Online != devices[j].Online {
			return devices[i].Online
		}
		return devices[i].Name < devices[j].Name
	})

	return &FleetResponse{
		Available: true,
		Connected: true,
		Devices:   devices,
	}, nil
}

func fallbackDevicesToFleet(devices []Device) *FleetResponse {
	fleetDevices := make([]FleetDevice, 0, len(devices))
	for _, d := range devices {
		fleetDevices = append(fleetDevices, FleetDevice{
			ID:           d.ID,
			Name:         d.Name,
			Online:       d.Online,
			OS:           d.OS,
			Arch:         d.Arch,
			AgentVersion: d.AgentVersion,
			Skills:       []DeviceSkill{},
			DynamicMCP:   []DeviceDynamicMCP{},
		})
	}
	sort.Slice(fleetDevices, func(i, j int) bool {
		if fleetDevices[i].Online != fleetDevices[j].Online {
			return fleetDevices[i].Online
		}
		return fleetDevices[i].Name < fleetDevices[j].Name
	})
	return &FleetResponse{
		Available: true,
		Connected: true,
		Devices:   fleetDevices,
	}
}
