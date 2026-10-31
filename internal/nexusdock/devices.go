package nexusdock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

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

func (s *Service) Devices(ctx context.Context, workspaceID uuid.UUID) ([]Device, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	connection, err := s.Runtime(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	return s.fetchDevices(ctx, connection)
}

func (s *Service) fetchDevices(ctx context.Context, connection RuntimeConnection) ([]Device, error) {
	endpoint, err := url.Parse(connection.URL)
	if err != nil || validateServiceURL(connection.URL) != nil {
		return nil, errors.New("invalid NexusDock device directory origin")
	}
	endpoint.Path, endpoint.RawPath = "/v1/integrations/devices", ""
	endpoint.RawQuery, endpoint.Fragment = "", ""
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+connection.AccessToken)
	request.Header.Set("Accept", "application/json")

	// 设备目录只读当前授权的同 origin；拒绝重定向，避免 Bearer 被带到其他地址。
	client := *s.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("NexusDock device directory redirect refused")
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch NexusDock devices: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, ErrConnectionExpired
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("NexusDock device directory HTTP %d", response.StatusCode)
	}
	const maxDeviceResponseBytes = 2 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, maxDeviceResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read NexusDock devices: %w", err)
	}
	if len(body) > maxDeviceResponseBytes {
		return nil, errors.New("NexusDock device directory response too large")
	}
	var result struct {
		Devices []Device `json:"devices"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.Devices == nil {
		return nil, errors.New("invalid NexusDock device directory response")
	}
	return result.Devices, nil
}
