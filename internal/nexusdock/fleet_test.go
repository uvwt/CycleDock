package nexusdock

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestFallbackDevicesToFleet_平滑转换且在线优先(t *testing.T) {
	devices := []Device{
		{ID: "node-1", Name: "Device B", Online: false, OS: "linux", Arch: "amd64", AgentVersion: "1.0.0"},
		{ID: "node-2", Name: "Device A", Online: true, OS: "darwin", Arch: "arm64", AgentVersion: "1.1.0"},
	}

	fleet := fallbackDevicesToFleet(devices)
	if !fleet.Available || !fleet.Connected {
		t.Fatalf("期望 Available 和 Connected 均为 true")
	}
	if len(fleet.Devices) != 2 {
		t.Fatalf("期望 2 台设备，实际 %d", len(fleet.Devices))
	}
	// 在线设备必须排在前面
	if fleet.Devices[0].ID != "node-2" || !fleet.Devices[0].Online {
		t.Errorf("第一台必须是在线设备 node-2，实际: %s", fleet.Devices[0].ID)
	}
	if fleet.Devices[1].ID != "node-1" || fleet.Devices[1].Online {
		t.Errorf("第二台必须是离线设备 node-1，实际: %s", fleet.Devices[1].ID)
	}
}

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
}

