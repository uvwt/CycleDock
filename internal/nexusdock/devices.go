package nexusdock

import (
	"context"

	"github.com/google/uuid"
)

// Device 仅包含前端展示所需的公开设备摘要，不含连接凭据或工具上下文。
type Device struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Online       bool   `json:"online"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	AgentVersion string `json:"agent_version"`
}

// Devices 返回当前 Workspace 关联 NexusDock 设备的公开在线摘要。
// 复用 MCP Fleet 快照与短时缓存，避免拿 mcp OAuth token 请求 REST Integration 端点触发 resource_mismatch。
func (s *Service) Devices(ctx context.Context, workspaceID uuid.UUID, forceRefresh bool) ([]Device, error) {
	if s == nil {
		return nil, ErrDisabled
	}
	fleet, err := s.Fleet(ctx, workspaceID, forceRefresh)
	if err != nil {
		return nil, err
	}
	return devicesFromFleet(fleet), nil
}

func devicesFromFleet(fleet *FleetResponse) []Device {
	if fleet == nil || len(fleet.Devices) == 0 {
		return []Device{}
	}
	devices := make([]Device, 0, len(fleet.Devices))
	for _, d := range fleet.Devices {
		devices = append(devices, Device{
			ID:           d.ID,
			Name:         d.Name,
			Online:       d.Online,
			OS:           d.OS,
			Arch:         d.Arch,
			AgentVersion: d.AgentVersion,
		})
	}
	return devices
}
