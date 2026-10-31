package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/uvwt/CycleDock/internal/nexusdock"
)

type nexusDockConnectionResponse struct {
	Available         bool       `json:"available"`
	Connected         bool       `json:"connected"`
	ManageURL         string     `json:"manage_url,omitempty"`
	ConnectedAt       *time.Time `json:"connected_at,omitempty"`
	AuthorizedSubject string     `json:"authorized_subject,omitempty"`
}

type nexusDockConnectResponse struct {
	AuthorizationURL string    `json:"authorization_url"`
	ExpiresAt        time.Time `json:"expires_at"`
}

func (api *API) getNexusDockConnection(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	if api.nexusDock == nil {
		writeJSON(w, http.StatusOK, nexusDockConnectionResponse{})
		return
	}
	status, err := api.nexusDock.Status(r.Context(), identity.WorkspaceID)
	if err != nil {
		api.handleNexusDockError(w, err)
		return
	}
	response := nexusDockConnectionResponse{
		Available:         status.Available,
		Connected:         status.Connected,
		ManageURL:         status.ManageURL,
		AuthorizedSubject: status.AuthorizedSubject,
	}
	if !status.ConnectedAt.IsZero() {
		connectedAt := status.ConnectedAt.UTC()
		response.ConnectedAt = &connectedAt
	}
	writeJSON(w, http.StatusOK, response)
}

type nexusDockDevicesResponse struct {
	Available bool               `json:"available"`
	Connected bool               `json:"connected"`
	Devices   []nexusdock.Device `json:"devices"`
}

func (api *API) listNexusDockDevices(w http.ResponseWriter, r *http.Request) {
	// 成员可以查看设备摘要；授权管理仍由独立的 Owner 接口负责。
	response := nexusDockDevicesResponse{Available: api.nexusDock != nil, Devices: []nexusdock.Device{}}
	if api.nexusDock == nil {
		writeJSON(w, http.StatusOK, response)
		return
	}
	devices, err := api.nexusDock.Devices(r.Context(), identityFromRequest(r).WorkspaceID)
	if errors.Is(err, nexusdock.ErrNotConnected) {
		writeJSON(w, http.StatusOK, response)
		return
	}
	if err != nil {
		api.handleNexusDockError(w, err)
		return
	}
	response.Connected, response.Devices = true, devices
	writeJSON(w, http.StatusOK, response)
}

func (api *API) getNexusDockFleet(w http.ResponseWriter, r *http.Request) {
	// 成员可以只读查看 NexusDock 设备及其 Skill、MCP 能力全貌。
	response := nexusdock.FleetResponse{Available: api.nexusDock != nil, Connected: false, Devices: []nexusdock.FleetDevice{}}
	if api.nexusDock == nil {
		writeJSON(w, http.StatusOK, response)
		return
	}
	forceRefresh := r.URL.Query().Get("refresh") == "true" || r.URL.Query().Get("refresh") == "1"
	fleet, err := api.nexusDock.Fleet(r.Context(), identityFromRequest(r).WorkspaceID, forceRefresh)
	if errors.Is(err, nexusdock.ErrNotConnected) {
		writeJSON(w, http.StatusOK, response)
		return
	}
	if err != nil {
		api.handleNexusDockError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, fleet)
}

func (api *API) connectNexusDock(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	if api.nexusDock == nil {
		writeError(w, http.StatusNotFound, "nexusdock_unavailable", "NexusDock remote devices are not available on this deployment")
		return
	}
	start, err := api.nexusDock.BeginAuthorization(r.Context(), identity.WorkspaceID, identity.HumanAccountID, identity.SourceID)
	if err != nil {
		api.handleNexusDockError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nexusDockConnectResponse{
		AuthorizationURL: start.URL,
		ExpiresAt:        start.ExpiresAt.UTC(),
	})
}

func (api *API) disconnectNexusDock(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	if api.nexusDock == nil {
		writeError(w, http.StatusNotFound, "nexusdock_unavailable", "NexusDock remote devices are not available on this deployment")
		return
	}
	if err := api.nexusDock.Disconnect(r.Context(), identity.WorkspaceID); err != nil {
		api.handleNexusDockError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (api *API) nexusDockDeviceCallback(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	if api.nexusDock == nil || identity.Role != "owner" {
		api.redirectNexusDockConnection(w, r, "unavailable")
		return
	}
	state := r.URL.Query().Get("state")
	if r.URL.Query().Get("error") != "" {
		if r.URL.Query().Get("error") == "access_denied" {
			if err := api.nexusDock.DenyAuthorization(r.Context(), identity.WorkspaceID, identity.HumanAccountID, identity.SourceID, state); err != nil {
				api.logger.Warn("NexusDock consent denial callback rejected", "error", err)
			}
		}
		api.redirectNexusDockConnection(w, r, "denied")
		return
	}
	err := api.nexusDock.CompleteAuthorization(
		r.Context(), identity.WorkspaceID, identity.HumanAccountID, identity.SourceID, state, r.URL.Query().Get("code"),
	)
	if err != nil {
		status := "failed"
		if errors.Is(err, nexusdock.ErrInvalidState) {
			status = "invalid-state"
		}
		api.logger.Warn("NexusDock device authorization callback failed", slog.String("code", status))
		api.redirectNexusDockConnection(w, r, status)
		return
	}
	api.redirectNexusDockConnection(w, r, "connected")
}

func (api *API) redirectNexusDockConnection(w http.ResponseWriter, r *http.Request, result string) {
	values := url.Values{"agent_connections": {"nexusdock-" + result}}
	http.Redirect(w, r, "/?"+values.Encode(), http.StatusFound)
}

func (api *API) handleNexusDockError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, nexusdock.ErrDisabled):
		writeError(w, http.StatusNotFound, "nexusdock_unavailable", "NexusDock remote devices are not available on this deployment")
	case errors.Is(err, nexusdock.ErrNotConnected):
		writeError(w, http.StatusNotFound, "nexusdock_not_connected", "NexusDock remote devices are not connected")
	case errors.Is(err, nexusdock.ErrInvalidState):
		writeError(w, http.StatusBadRequest, "invalid_nexusdock_state", "NexusDock authorization state is invalid or expired")
	case errors.Is(err, nexusdock.ErrConnectionExpired), errors.Is(err, nexusdock.ErrAuthorization):
		writeError(w, http.StatusConflict, "nexusdock_reconnect_required", "NexusDock authorization must be reconnected")
	default:
		api.logger.Error("NexusDock connection error", slog.Any("error", err))
		writeError(w, http.StatusBadGateway, "nexusdock_unavailable", "NexusDock is temporarily unavailable")
	}
}
