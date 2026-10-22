package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/agentcheck"
	"github.com/uvwt/CycleDock/internal/agentconfig"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/modelauth"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/store/db"
)

type agentSettingsResponse struct {
	Configured         bool   `json:"configured"`
	ProviderType       string `json:"provider_type"`
	ProviderBaseURL    string `json:"provider_base_url"`
	Model              string `json:"model"`
	APIKeyEnv          string `json:"api_key_env"`
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
			Configured: false, ProviderType: "openai_compatible",
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
		ProviderType       string `json:"provider_type"`
		ProviderBaseURL    string `json:"provider_base_url"`
		Model              string `json:"model"`
		APIKeyEnv          string `json:"api_key_env"`
		SystemInstructions string `json:"system_instructions"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	request.ProviderType = strings.TrimSpace(request.ProviderType)
	if request.ProviderType == "" {
		request.ProviderType = "openai_compatible"
	}
	settings, err := api.store.SaveAgentSettings(r.Context(), identity.WorkspaceID, store.AgentSettingsInput{
		ProviderType:    request.ProviderType,
		ProviderBaseURL: request.ProviderBaseURL,
		Model:           request.Model, APIKeyEnv: request.APIKeyEnv,
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
		ProviderType    string `json:"provider_type"`
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
		config, err := api.modelAuth.ModelConfig(
			r.Context(), identity.WorkspaceID, modelauth.Provider(providerType), request.Model, timeout,
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

func agentSettingsToResponse(settings db.WorkspaceAgentSetting) agentSettingsResponse {
	return agentSettingsResponse{
		Configured:      true,
		ProviderType:    settings.ProviderType,
		ProviderBaseURL: settings.ProviderBaseUrl, Model: settings.Model, APIKeyEnv: settings.ApiKeyEnv,
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
