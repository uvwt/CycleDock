package httpapi

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/agentcheck"
	"github.com/uvwt/CycleDock/internal/agentconfig"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/modelauth"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/store/db"
)

type agentSettingsResponse struct {
	Configured         bool                 `json:"configured"`
	Routes             []modelRouteResponse `json:"routes"`
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
	ID              string `json:"id"`
	Name            string `json:"name"`
	URL             string `json:"url"`
	TokenConfigured bool   `json:"token_configured"`
	Enabled         bool   `json:"enabled"`
	TimeoutMS       int32  `json:"timeout_ms"`
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
		Routes []struct {
			ID              string `json:"id"`
			Enabled         bool   `json:"enabled"`
			ProviderType    string `json:"provider_type"`
			CredentialID    string `json:"credential_id"`
			ProviderBaseURL string `json:"provider_base_url"`
			Model           string `json:"model"`
			APIKeyEnv       string `json:"api_key_env"`
		} `json:"routes"`
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
		routes = append(routes, store.ModelRouteInput{
			ID: routeID, Enabled: route.Enabled,
			ProviderType: strings.TrimSpace(route.ProviderType),
			CredentialID: credentialID, ProviderBaseURL: route.ProviderBaseURL,
			Model: route.Model, APIKeyEnv: route.APIKeyEnv,
		})
	}
	settings, err := api.store.SaveAgentSettings(r.Context(), identity.WorkspaceID, store.AgentSettingsInput{
		Routes: routes, SystemInstructions: request.SystemInstructions,
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
		ProviderType    string `json:"provider_type"`
		CredentialID    string `json:"credential_id"`
		ProviderBaseURL string `json:"provider_base_url"`
		Model           string `json:"model"`
		APIKeyEnv       string `json:"api_key_env"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	timeout := agentconfig.DefaultModelRequestTimeout
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

func (api *API) listAgentProviderModels(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	var request struct {
		ProviderBaseURL string `json:"provider_base_url"`
		APIKeyEnv       string `json:"api_key_env"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}

	baseURL := strings.TrimSpace(request.ProviderBaseURL)
	if err := agentconfig.ValidateEndpoint(baseURL); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_endpoint", err.Error())
		return
	}
	envName := strings.TrimSpace(request.APIKeyEnv)
	if envName != "" && !agentconfig.ValidEnvName(envName) {
		writeError(w, http.StatusBadRequest, "invalid_secret_env", "Model API key environment variable name is invalid.")
		return
	}

	apiKey := ""
	if envName != "" {
		value, ok := os.LookupEnv(envName)
		if !ok || strings.TrimSpace(value) == "" {
			writeError(w, http.StatusBadRequest, "secret_env_missing", "Configured model API key environment variable is not set.")
			return
		}
		apiKey = value
	}

	models, err := api.model.ListModels(r.Context(), modelclient.Config{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		api.logger.Warn("list custom provider models failed", "error", err)
		writeError(w, http.StatusBadGateway, "model_catalog_unavailable", "Could not load models from the provider.")
		return
	}
	writeJSON(w, http.StatusOK, models)
}

func (api *API) testAgentMCP(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	var request struct {
		Name             string `json:"name"`
		URL              string `json:"url"`
		ConnectionID     string `json:"connection_id"`
		BearerToken      string `json:"bearer_token"`
		ClearBearerToken bool   `json:"clear_bearer_token"`
		TimeoutMS        int32  `json:"timeout_ms"`
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
	if request.ClearBearerToken && strings.TrimSpace(request.BearerToken) != "" {
		writeError(w, http.StatusBadRequest, "invalid_bearer_token", "Cannot set and clear MCP bearer token together.")
		return
	}
	if strings.TrimSpace(request.BearerToken) == "" && !request.ClearBearerToken && request.ConnectionID != "" {
		connectionID, err := uuid.Parse(request.ConnectionID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_connection_id", "MCP connection id is invalid.")
			return
		}
		connection, err := api.store.GetMCPConnection(r.Context(), identity.WorkspaceID, connectionID)
		if err != nil {
			api.handleError(w, err)
			return
		}
		// 已保存的密钥只能用于原连接地址，不能借测试接口转发到攻击者控制的 URL。
		if len(connection.BearerTokenCiphertext) != 0 && strings.TrimSpace(request.URL) != connection.Url {
			writeError(w, http.StatusBadRequest, "mcp_url_changed", "Enter a new bearer token to test a different MCP URL.")
			return
		}
		request.BearerToken, err = api.store.MCPBearerToken(connection)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "mcp_token_unavailable", "Saved MCP bearer token cannot be decrypted.")
			return
		}
	}
	result := agentcheck.New().MCP(r.Context(), agentcheck.MCPInput{
		Name:        request.Name,
		URL:         request.URL,
		BearerToken: request.BearerToken,
		Timeout:     time.Duration(request.TimeoutMS) * time.Millisecond,
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
		Name             string `json:"name"`
		URL              string `json:"url"`
		BearerToken      string `json:"bearer_token"`
		ClearBearerToken bool   `json:"clear_bearer_token"`
		Enabled          *bool  `json:"enabled"`
		TimeoutMS        int32  `json:"timeout_ms"`
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
		BearerToken: request.BearerToken, ClearBearerToken: request.ClearBearerToken, Enabled: enabled,
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
		Configured: true, Routes: routes,
		SystemInstructions: settings.SystemInstructions, SourceID: settings.SourceID.String(),
	}
}

func mcpConnectionToResponse(connection db.WorkspaceMcpConnection) mcpConnectionResponse {
	return mcpConnectionResponse{
		ID: connection.ID.String(), Name: connection.Name, URL: connection.Url,
		TokenConfigured: len(connection.BearerTokenCiphertext) != 0, Enabled: connection.Enabled,
		TimeoutMS: connection.TimeoutMs,
	}
}
