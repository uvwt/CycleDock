package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/agentcheck"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/modelauth"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/store/db"
)

type agentSettingsResponse struct {
	Configured         bool                 `json:"configured"`
	Enabled            bool                 `json:"enabled"`
	Routes             []modelRouteResponse `json:"routes"`
	RequestTimeoutMS   int32                `json:"request_timeout_ms"`
	MaxToolRounds      int32                `json:"max_tool_rounds"`
	SystemInstructions string               `json:"system_instructions"`
	SourceID           string               `json:"source_id,omitempty"`
}

type modelRouteResponse struct {
	ID              string `json:"id"`
	Enabled         bool   `json:"enabled"`
	ProviderType    string `json:"provider_type"`
	CredentialID    string `json:"credential_id,omitempty"`
	ProviderBaseURL string `json:"provider_base_url"`
	Model           string `json:"model"`
	APIKeyEnv       string `json:"api_key_env"`
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
			Configured: false, Routes: []modelRouteResponse{},
			RequestTimeoutMS: 120000, MaxToolRounds: 12,
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
		Enabled bool `json:"enabled"`
		Routes  []struct {
			ID              string `json:"id"`
			Enabled         bool   `json:"enabled"`
			ProviderType    string `json:"provider_type"`
			CredentialID    string `json:"credential_id"`
			ProviderBaseURL string `json:"provider_base_url"`
			Model           string `json:"model"`
			APIKeyEnv       string `json:"api_key_env"`
		} `json:"routes"`
		RequestTimeoutMS   int32  `json:"request_timeout_ms"`
		MaxToolRounds      int32  `json:"max_tool_rounds"`
		SystemInstructions string `json:"system_instructions"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	routes := make([]store.ModelRouteInput, 0, len(request.Routes))
	for _, route := range request.Routes {
		var routeID uuid.UUID
		if strings.TrimSpace(route.ID) != "" {
			parsed, err := uuid.Parse(strings.TrimSpace(route.ID))
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_route_id", "Model route id is invalid.")
				return
			}
			routeID = parsed
		}
		var credentialID *uuid.UUID
		if strings.TrimSpace(route.CredentialID) != "" {
			parsed, err := uuid.Parse(strings.TrimSpace(route.CredentialID))
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_credential_id", "Model credential id is invalid.")
				return
			}
			credentialID = &parsed
		}
		providerType := strings.TrimSpace(route.ProviderType)
		if request.Enabled && route.Enabled &&
			(providerType == string(modelauth.ProviderChatGPT) || providerType == string(modelauth.ProviderGrok)) &&
			api.modelAuth == nil {
			writeError(w, http.StatusServiceUnavailable, "model_auth_unavailable", "Model account sign-in is not configured.")
			return
		}
		routes = append(routes, store.ModelRouteInput{
			ID: routeID, Enabled: route.Enabled, ProviderType: providerType,
			CredentialID: credentialID, ProviderBaseURL: route.ProviderBaseURL,
			Model: route.Model, APIKeyEnv: route.APIKeyEnv,
		})
	}
	settings, err := api.store.SaveAgentSettings(r.Context(), identity.WorkspaceID, store.AgentSettingsInput{
		Enabled: request.Enabled, Routes: routes,
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
		ProviderType     string `json:"provider_type"`
		CredentialID     string `json:"credential_id"`
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
	timeout := time.Duration(request.RequestTimeoutMS) * time.Millisecond
	providerType := strings.TrimSpace(request.ProviderType)
	if providerType == "" {
		providerType = "openai_compatible"
	}
	checker := agentcheck.NewWithModelClient(api.model)
	var result agentcheck.Result
	switch providerType {
	case "openai_compatible":
		result = checker.Provider(r.Context(), agentcheck.ProviderInput{
			BaseURL: request.ProviderBaseURL, Model: request.Model,
			APIKeyEnv: request.APIKeyEnv, Timeout: timeout,
		})
	case string(modelauth.ProviderChatGPT), string(modelauth.ProviderGrok):
		if api.modelAuth == nil {
			writeError(w, http.StatusServiceUnavailable, "model_auth_unavailable", "Model account sign-in is not configured.")
			return
		}
		credentialID, err := uuid.Parse(strings.TrimSpace(request.CredentialID))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_credential_id", "Select a connected model account.")
			return
		}
		config, err := api.modelAuth.ModelConfigForCredential(
			r.Context(), identity.WorkspaceID, modelauth.Provider(providerType), credentialID, request.Model, timeout,
		)
		if err != nil {
			api.writeModelAuthError(w, err)
			return
		}
		result = checker.ProviderConfig(r.Context(), config)
	default:
		writeError(w, http.StatusBadRequest, "invalid_provider", "Unsupported model provider.")
		return
	}
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

func agentSettingsToResponse(settings store.AgentSettings) agentSettingsResponse {
	routes := make([]modelRouteResponse, 0, len(settings.Routes))
	for _, route := range settings.Routes {
		credentialID := ""
		if route.CredentialID.Valid {
			credentialID = route.CredentialID.UUID.String()
		}
		routes = append(routes, modelRouteResponse{
			ID: route.ID.String(), Enabled: route.Enabled, ProviderType: route.ProviderType,
			CredentialID: credentialID, ProviderBaseURL: route.ProviderBaseUrl,
			Model: route.Model, APIKeyEnv: route.ApiKeyEnv,
		})
	}
	return agentSettingsResponse{
		Configured: true, Enabled: settings.Enabled,
		Routes:           routes,
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
