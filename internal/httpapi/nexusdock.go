package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/uvwt/CycleDock/internal/auth"
	"github.com/uvwt/CycleDock/internal/nexusdock"
)

type nexusDockConnectionResponse struct {
	Available   bool       `json:"available"`
	Connected   bool       `json:"connected"`
	ManageURL   string     `json:"manage_url,omitempty"`
	ConnectedAt *time.Time `json:"connected_at,omitempty"`
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
		Available: status.Available,
		Connected: status.Connected,
		ManageURL: status.ManageURL,
	}
	if !status.ConnectedAt.IsZero() {
		connectedAt := status.ConnectedAt.UTC()
		response.ConnectedAt = &connectedAt
	}
	writeJSON(w, http.StatusOK, response)
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
	start, err := api.startNexusDockAuthorization(r.Context(), identity, false, "/")
	if err != nil {
		api.handleNexusDockError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nexusDockConnectResponse{
		AuthorizationURL: start.URL,
		ExpiresAt:        start.ExpiresAt.UTC(),
	})
}

// startNexusDockAuthorization 在登录回调与手动设置中复用同一条授权链。
func (api *API) startNexusDockAuthorization(ctx context.Context, identity auth.Identity, automatic bool, returnTo string) (nexusdock.AuthorizationStart, error) {
	subject, err := api.auth.SubjectForHuman(ctx, identity.HumanAccountID)
	if err != nil {
		return nexusdock.AuthorizationStart{}, err
	}
	return api.nexusDock.BeginAuthorization(ctx, identity.WorkspaceID, identity.HumanAccountID, identity.SourceID,
		nexusdock.AuthorizationOptions{Automatic: automatic, ReturnTo: safeInternalReturnTo(returnTo), ExpectedSubject: subject})
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
			outcome, err := api.nexusDock.DenyAuthorization(r.Context(), identity.WorkspaceID, identity.HumanAccountID, identity.SourceID, state)
			if err == nil && outcome.Automatic {
				http.Redirect(w, r, safeInternalReturnTo(outcome.ReturnTo), http.StatusFound)
				return
			}
			if err != nil {
				api.logger.Warn("NexusDock consent denial callback rejected", "error", err)
			}
		}
		api.redirectNexusDockConnection(w, r, "denied")
		return
	}
	outcome, err := api.nexusDock.CompleteAuthorization(
		r.Context(), identity.WorkspaceID, identity.HumanAccountID, identity.SourceID, state, r.URL.Query().Get("code"),
	)
	if err != nil {
		status := "failed"
		if errors.Is(err, nexusdock.ErrInvalidState) {
			status = "invalid-state"
		}
		if errors.Is(err, nexusdock.ErrIdentityMismatch) {
			status = "account-mismatch"
		}
		api.logger.Warn("NexusDock device authorization callback failed", slog.String("code", status))
		api.redirectNexusDockConnection(w, r, status)
		return
	}
	if outcome.Automatic {
		http.Redirect(w, r, safeInternalReturnTo(outcome.ReturnTo), http.StatusFound)
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
