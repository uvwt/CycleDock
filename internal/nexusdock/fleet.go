package nexusdock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	fleetCacheTTL         = 15 * time.Second
	maxFleetResponseBytes = 4 << 20
)

var (
	errUpstreamUnauthorized  = errors.New("NexusDock MCP unauthorized")
	errFleetRedirectRefused  = errors.New("NexusDock MCP redirect refused")
	errFleetResponseTooLarge = errors.New("NexusDock MCP response too large")
)

type fleetHTTPStatusError struct {
	code int
}

func (e fleetHTTPStatusError) Error() string {
	return fmt.Sprintf("NexusDock MCP HTTP %d", e.code)
}

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

type fleetTransport struct {
	token         string
	allowedOrigin string
	base          http.RoundTripper
	maxBytes      int64

	mu         sync.Mutex
	recordedErr error
}

func (t *fleetTransport) recordError(err error) {
	if err == nil {
		return
	}
	t.mu.Lock()
	if t.recordedErr == nil {
		t.recordedErr = err
	}
	t.mu.Unlock()
}

func (t *fleetTransport) lastError() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.recordedErr
}

func fleetEndpointOrigin(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return strings.ToLower(parsed.Scheme + "://" + parsed.Host)
}

func (t *fleetTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	// 仅向已校验的同 origin 附加 Bearer，避免意外跳出授权目标泄露凭据。
	if t.allowedOrigin != "" && fleetEndpointOrigin(clone.URL.String()) == t.allowedOrigin {
		clone.Header.Set("Authorization", "Bearer "+t.token)
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(clone)
	if err != nil {
		return nil, err
	}
	// 拦截鉴权与上游错误状态码，避免上游响应正文（可能含内部细节）渗入错误消息。
	// 404/405 透传给 MCP SDK 处理会话关闭与协议协商。
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		_ = resp.Body.Close()
		t.recordError(errUpstreamUnauthorized)
		return nil, errUpstreamUnauthorized
	case resp.StatusCode == http.StatusForbidden:
		_ = resp.Body.Close()
		t.recordError(ErrConnectionExpired)
		return nil, ErrConnectionExpired
	case resp.StatusCode >= http.StatusBadRequest &&
		resp.StatusCode != http.StatusNotFound &&
		resp.StatusCode != http.StatusMethodNotAllowed:
		_ = resp.Body.Close()
		statusErr := fleetHTTPStatusError{code: resp.StatusCode}
		t.recordError(statusErr)
		return nil, statusErr
	}
	if t.maxBytes > 0 {
		if resp.ContentLength > t.maxBytes {
			_ = resp.Body.Close()
			t.recordError(errFleetResponseTooLarge)
			return nil, errFleetResponseTooLarge
		}
		resp.Body = &boundedFleetReadCloser{
			ReadCloser: resp.Body,
			remaining:  t.maxBytes,
			onExceeded: func() { t.recordError(errFleetResponseTooLarge) },
		}
	}
	return resp, nil
}

type boundedFleetReadCloser struct {
	io.ReadCloser
	remaining  int64
	onExceeded func()
}

func (b *boundedFleetReadCloser) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		var extra [1]byte
		n, err := b.ReadCloser.Read(extra[:])
		if n > 0 {
			if b.onExceeded != nil {
				b.onExceeded()
			}
			return 0, errFleetResponseTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	return n, err
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
// 统一走 MCP agentdock_context，并在内存短缓存与 singleflight 上与 Devices 共享结果，避免小窗和页面重复打上游。
func (s *Service) Fleet(ctx context.Context, workspaceID uuid.UUID, forceRefresh bool) (*FleetResponse, error) {
	if s == nil {
		return &FleetResponse{Available: false, Connected: false, Devices: []FleetDevice{}}, nil
	}

	if !forceRefresh {
		if cached, ok := s.cachedFleet(workspaceID); ok {
			return cached, nil
		}
	}

	groupKey := workspaceID.String()
	if forceRefresh {
		groupKey = "refresh:" + groupKey
	}

	ch := s.fleetGroup.DoChan(groupKey, func() (any, error) {
		if !forceRefresh {
			if cached, ok := s.cachedFleet(workspaceID); ok {
				return cached, nil
			}
		}

		s.fleetMu.RLock()
		epoch := s.fleetEpoch[workspaceID]
		s.fleetMu.RUnlock()

		// 使用与单个请求解耦的超时上下文，避免并发共享 singleflight 时某个请求取消中断其他调用方。
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cancel()

		fleetResp, err := s.loadFleetFromUpstream(fetchCtx, workspaceID)
		if err != nil {
			return nil, err
		}

		// 若拉取期间发生了断开或重新授权，则丢弃旧 epoch 的写回，防止过期快照复活。
		s.fleetMu.Lock()
		if s.fleetEpoch[workspaceID] == epoch {
			if s.fleetCache == nil {
				s.fleetCache = make(map[uuid.UUID]fleetCacheEntry)
			}
			s.fleetCache[workspaceID] = fleetCacheEntry{
				data:      fleetResp,
				fetchedAt: time.Now(),
			}
		}
		s.fleetMu.Unlock()
		return fleetResp, nil
	})

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err
		}
		return res.Val.(*FleetResponse), nil
	}
}

func (s *Service) cachedFleet(workspaceID uuid.UUID) (*FleetResponse, bool) {
	s.fleetMu.RLock()
	entry, found := s.fleetCache[workspaceID]
	s.fleetMu.RUnlock()
	if found && time.Since(entry.fetchedAt) < fleetCacheTTL {
		return entry.data, true
	}
	return nil, false
}

func (s *Service) loadFleetFromUpstream(ctx context.Context, workspaceID uuid.UUID) (*FleetResponse, error) {
	connection, err := s.Runtime(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	fleetResp, err := s.fetchFleetContext(ctx, connection)
	if errors.Is(err, errUpstreamUnauthorized) {
		// 上游 MCP 返回 401 时强制刷新一次 OAuth token 并重试；
		// 刷新失败（如 refresh_token 失效）或刷新后再次 401 才判定为授权过期。
		refreshed, refreshErr := s.ForceRefresh(ctx, workspaceID)
		if refreshErr != nil {
			return nil, refreshErr
		}
		fleetResp, err = s.fetchFleetContext(ctx, refreshed)
		if errors.Is(err, errUpstreamUnauthorized) {
			return nil, ErrConnectionExpired
		}
	}
	if err != nil {
		if errors.Is(err, ErrConnectionExpired) {
			return nil, ErrConnectionExpired
		}
		return nil, fmt.Errorf("fetch NexusDock fleet: %w", err)
	}
	return fleetResp, nil
}

func (s *Service) fetchFleetContext(ctx context.Context, connection RuntimeConnection) (*FleetResponse, error) {
	if err := validateServiceURL(connection.URL); err != nil {
		return nil, fmt.Errorf("invalid NexusDock MCP origin: %w", err)
	}

	baseTransport := http.DefaultTransport
	if s != nil && s.client != nil && s.client.Transport != nil {
		baseTransport = s.client.Transport
	}

	mcpClient := mcp.NewClient(&mcp.Implementation{
		Name:    "cycledock-fleet-viewer",
		Version: "1.0.0",
	}, nil)

	rt := &fleetTransport{
		token:         connection.AccessToken,
		allowedOrigin: fleetEndpointOrigin(connection.URL),
		base:          baseTransport,
		maxBytes:      maxFleetResponseBytes,
	}

	transport := &mcp.StreamableClientTransport{
		Endpoint: connection.URL,
		HTTPClient: &http.Client{
			Transport: rt,
			Timeout:   15 * time.Second,
			// 拒绝重定向，防止 Bearer 在跳转时泄露到其他地址。
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errFleetRedirectRefused
			},
		},
		DisableStandaloneSSE: true,
	}

	connectCtx, cancelConnect := context.WithTimeout(ctx, 10*time.Second)
	defer cancelConnect()

	session, err := mcpClient.Connect(connectCtx, transport, nil)
	if err != nil {
		return nil, classifyFleetMCPError("connect NexusDock MCP", err, rt.lastError())
	}
	defer session.Close()

	callCtx, cancelCall := context.WithTimeout(ctx, 15*time.Second)
	defer cancelCall()

	result, err := session.CallTool(callCtx, &mcp.CallToolParams{
		Name: "agentdock_context",
	})
	if err != nil {
		return nil, classifyFleetMCPError("call NexusDock agentdock_context", err, rt.lastError())
	}
	if result == nil || result.IsError {
		return nil, errors.New("NexusDock agentdock_context returned error")
	}

	var payload rawFleetPayload
	parsed := false
	for _, content := range result.Content {
		textContent, ok := content.(*mcp.TextContent)
		if !ok || textContent.Text == "" {
			continue
		}
		var candidate rawFleetPayload
		if err := json.Unmarshal([]byte(textContent.Text), &candidate); err == nil && candidate.Nodes != nil {
			payload = candidate
			parsed = true
			if len(candidate.Nodes) > 0 {
				break
			}
		}
	}
	if !parsed {
		return nil, errors.New("invalid NexusDock agentdock_context response")
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

func classifyFleetMCPError(action string, err, transportErr error) error {
	for _, candidate := range []error{transportErr, err} {
		if candidate == nil {
			continue
		}
		switch {
		case errors.Is(candidate, errUpstreamUnauthorized):
			return errUpstreamUnauthorized
		case errors.Is(candidate, ErrConnectionExpired):
			return ErrConnectionExpired
		case errors.Is(candidate, errFleetRedirectRefused):
			return errFleetRedirectRefused
		case errors.Is(candidate, errFleetResponseTooLarge):
			return errFleetResponseTooLarge
		case errors.Is(candidate, context.DeadlineExceeded), errors.Is(candidate, context.Canceled):
			return candidate
		}
		var statusErr fleetHTTPStatusError
		if errors.As(candidate, &statusErr) {
			return statusErr
		}
	}
	// 其余 MCP 传输/协议解析失败做脱敏收口，避免上游原始正文渗入错误消息。
	return fmt.Errorf("%s failed", action)
}
