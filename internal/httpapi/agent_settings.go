package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/agentcheck"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/store/db"
)

type agentSettingsResponse struct {
	Configured         bool   `json:"configured"`
	Enabled            bool   `json:"enabled"`
	ProviderBaseURL    string `json:"provider_base_url"`
	Model              string `json:"model"`
	APIKeyEnv          string `json:"api_key_env"`
	RequestTimeoutMS   int32  `json:"request_timeout_ms"`
	MaxToolRounds      int32  `json:"max_tool_rounds"`
	SystemInstructions string `json:"system_instructions"`
	SourceID           string `json:"source_id,omitempty"`
}

type mcpConnectionResponse struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	URL            string `json:"url"`
	BearerTokenEnv string `json:"bearer_token_env"`
	Enabled        bool   `json:"enabled"`
	TimeoutMS      int32  `json:"timeout_ms"`
}

func (api *API) getAgentSettings(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	settings, err := api.store.GetAgentSettings(r.Context(), identity.WorkspaceID)
	if errors.Is(err, domain.ErrNotFound) {
		writeJSON(w, http.StatusOK, agentSettingsResponse{
			Configured: false, RequestTimeoutMS: 120000, MaxToolRounds: 12,
		})
		return
	}
	if err != nil {
		api.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agentSettingsToResponse(settings))
}

func (api *API) saveAgentSettings(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	var request struct {
		Enabled            bool   `json:"enabled"`
		ProviderBaseURL    string `json:"provider_base_url"`
		Model              string `json:"model"`
		APIKeyEnv          string `json:"api_key_env"`
		RequestTimeoutMS   int32  `json:"request_timeout_ms"`
		MaxToolRounds      int32  `json:"max_tool_rounds"`
		SystemInstructions string `json:"system_instructions"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	settings, err := api.store.SaveAgentSettings(r.Context(), identity.WorkspaceID, store.AgentSettingsInput{
		Enabled: request.Enabled, ProviderBaseURL: request.ProviderBaseURL,
		Model: request.Model, APIKeyEnv: request.APIKeyEnv,
		RequestTimeoutMS: request.RequestTimeoutMS, MaxToolRounds: request.MaxToolRounds,
		SystemInstructions: request.SystemInstructions,
	})
	if err != nil {
		api.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agentSettingsToResponse(settings))
}

func (api *API) testAgentProvider(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	var request struct {
		ProviderBaseURL  string `json:"provider_base_url"`
		Model            string `json:"model"`
		APIKeyEnv        string `json:"api_key_env"`
		RequestTimeoutMS int32  `json:"request_timeout_ms"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.RequestTimeoutMS == 0 {
		request.RequestTimeoutMS = 120000
	}
	if request.RequestTimeoutMS < 1000 || request.RequestTimeoutMS > 600000 {
		writeError(w, http.StatusBadRequest, "invalid_timeout", "request_timeout_ms must be between 1000 and 600000")
		return
	}
	result := agentcheck.New().Provider(r.Context(), agentcheck.ProviderInput{
		BaseURL:   request.ProviderBaseURL,
		Model:     request.Model,
		APIKeyEnv: request.APIKeyEnv,
		Timeout:   time.Duration(request.RequestTimeoutMS) * time.Millisecond,
	})
	writeJSON(w, http.StatusOK, result)
}

func (api *API) testAgentMCP(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	var request struct {
		Name           string `json:"name"`
		URL            string `json:"url"`
		BearerTokenEnv string `json:"bearer_token_env"`
		TimeoutMS      int32  `json:"timeout_ms"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.TimeoutMS == 0 {
		request.TimeoutMS = 30000
	}
	if request.TimeoutMS < 1000 || request.TimeoutMS > 300000 {
		writeError(w, http.StatusBadRequest, "invalid_timeout", "timeout_ms must be between 1000 and 300000")
		return
	}
	result := agentcheck.New().MCP(r.Context(), agentcheck.MCPInput{
		Name:           request.Name,
		URL:            request.URL,
		BearerTokenEnv: request.BearerTokenEnv,
		Timeout:        time.Duration(request.TimeoutMS) * time.Millisecond,
	})
	writeJSON(w, http.StatusOK, result)
}

func (api *API) listAgentMCPConnections(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	connections, err := api.store.ListMCPConnections(r.Context(), identity.WorkspaceID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	response := make([]mcpConnectionResponse, 0, len(connections))
	for _, connection := range connections {
		response = append(response, mcpConnectionToResponse(connection))
	}
	writeJSON(w, http.StatusOK, response)
}

func (api *API) createAgentMCPConnection(w http.ResponseWriter, r *http.Request) {
	api.saveAgentMCPConnection(w, r, uuid.Nil)
}

func (api *API) updateAgentMCPConnection(w http.ResponseWriter, r *http.Request) {
	connectionID, ok := parseUUIDParam(w, r, "connectionID")
	if !ok {
		return
	}
	api.saveAgentMCPConnection(w, r, connectionID)
}

func (api *API) saveAgentMCPConnection(w http.ResponseWriter, r *http.Request, connectionID uuid.UUID) {
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	var request struct {
		Name           string `json:"name"`
		URL            string `json:"url"`
		BearerTokenEnv string `json:"bearer_token_env"`
		Enabled        *bool  `json:"enabled"`
		TimeoutMS      int32  `json:"timeout_ms"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	connection, err := api.store.SaveMCPConnection(r.Context(), identity.WorkspaceID, store.MCPConnectionInput{
		ID: connectionID, Name: strings.TrimSpace(request.Name), URL: strings.TrimSpace(request.URL),
		BearerTokenEnv: strings.TrimSpace(request.BearerTokenEnv), Enabled: enabled,
		TimeoutMS: request.TimeoutMS,
	})
	if err != nil {
		api.handleError(w, err)
		return
	}
	status := http.StatusOK
	if connectionID == uuid.Nil {
		status = http.StatusCreated
	}
	writeJSON(w, status, mcpConnectionToResponse(connection))
}

func (api *API) deleteAgentMCPConnection(w http.ResponseWriter, r *http.Request) {
	connectionID, ok := parseUUIDParam(w, r, "connectionID")
	if !ok {
		return
	}
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	if err := api.store.DeleteMCPConnection(r.Context(), identity.WorkspaceID, connectionID); err != nil {
		api.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func requireWorkspaceOwner(w http.ResponseWriter, role string) bool {
	if role == "owner" {
		return true
	}
	writeError(w, http.StatusForbidden, "owner_required", "workspace owner permission is required")
	return false
}

func agentSettingsToResponse(settings db.WorkspaceAgentSetting) agentSettingsResponse {
	return agentSettingsResponse{
		Configured: true, Enabled: settings.Enabled,
		ProviderBaseURL: settings.ProviderBaseUrl, Model: settings.Model, APIKeyEnv: settings.ApiKeyEnv,
		RequestTimeoutMS: settings.RequestTimeoutMs, MaxToolRounds: settings.MaxToolRounds,
		SystemInstructions: settings.SystemInstructions, SourceID: settings.SourceID.String(),
	}
}

func mcpConnectionToResponse(connection db.WorkspaceMcpConnection) mcpConnectionResponse {
	return mcpConnectionResponse{
		ID: connection.ID.String(), Name: connection.Name, URL: connection.Url,
		BearerTokenEnv: connection.BearerTokenEnv, Enabled: connection.Enabled,
		TimeoutMS: connection.TimeoutMs,
	}
}
