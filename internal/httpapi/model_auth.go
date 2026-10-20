package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/modelauth"
)

func (api *API) getModelAuthStatus(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	if api.modelAuth == nil {
		writeJSON(w, http.StatusOK, []modelauth.Status{
			{Provider: modelauth.ProviderChatGPT, Reason: "server_not_configured"},
			{Provider: modelauth.ProviderGrok, Reason: "server_not_configured"},
		})
		return
	}

	statuses := make([]modelauth.Status, 0, 2)
	for _, provider := range []modelauth.Provider{modelauth.ProviderChatGPT, modelauth.ProviderGrok} {
		status, err := api.modelAuth.Status(r.Context(), identity.WorkspaceID, provider)
		if err != nil {
			api.handleError(w, err)
			return
		}
		statuses = append(statuses, status)
	}
	writeJSON(w, http.StatusOK, statuses)
}

func (api *API) startChatGPTModelAuth(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	if api.modelAuth == nil {
		writeError(w, http.StatusServiceUnavailable, "model_auth_unavailable", "Model account sign-in is not configured.")
		return
	}
	authorizationURL, err := api.modelAuth.StartChatGPT(r.Context(), identity.WorkspaceID)
	if err != nil {
		api.writeModelAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"authorization_url": authorizationURL})
}

func (api *API) modelChatGPTCallback(w http.ResponseWriter, r *http.Request) {
	if api.modelAuth == nil {
		http.Redirect(w, r, "/?model_auth=chatgpt_error", http.StatusFound)
		return
	}
	query := r.URL.Query()
	if query.Get("error") != "" {
		http.Redirect(w, r, "/?model_auth=chatgpt_denied", http.StatusFound)
		return
	}
	_, err := api.modelAuth.CompleteChatGPT(
		r.Context(),
		query.Get("state"),
		query.Get("code"),
		query.Get("client_id"),
	)
	if err != nil {
		api.logger.Warn("ChatGPT model sign-in failed", "error", err)
		http.Redirect(w, r, "/?model_auth=chatgpt_error", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/?model_auth=chatgpt_connected", http.StatusFound)
}

func (api *API) startGrokModelAuth(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	if api.modelAuth == nil {
		writeError(w, http.StatusServiceUnavailable, "model_auth_unavailable", "Model account sign-in is not configured.")
		return
	}
	flow, err := api.modelAuth.StartGrok(r.Context(), identity.WorkspaceID)
	if err != nil {
		api.writeModelAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, flow)
}

func (api *API) pollGrokModelAuth(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	if api.modelAuth == nil {
		writeError(w, http.StatusServiceUnavailable, "model_auth_unavailable", "Model account sign-in is not configured.")
		return
	}
	var request struct {
		FlowID string `json:"flow_id"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	flowID, err := uuid.Parse(strings.TrimSpace(request.FlowID))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_flow_id", "flow_id is invalid.")
		return
	}
	result, err := api.modelAuth.PollGrok(r.Context(), identity.WorkspaceID, flowID)
	if err != nil {
		api.writeModelAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (api *API) disconnectModelAuth(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	if api.modelAuth == nil {
		writeError(w, http.StatusServiceUnavailable, "model_auth_unavailable", "Model account sign-in is not configured.")
		return
	}
	provider, ok := parseModelAuthProvider(chi.URLParam(r, "provider"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_provider", "Unsupported model account provider.")
		return
	}
	if err := api.modelAuth.Disconnect(r.Context(), identity.WorkspaceID, provider); err != nil {
		api.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (api *API) listAgentModels(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	if api.modelAuth == nil {
		writeError(w, http.StatusServiceUnavailable, "model_auth_unavailable", "Model account sign-in is not configured.")
		return
	}
	provider, ok := parseModelAuthProvider(r.URL.Query().Get("provider"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_provider", "Unsupported model account provider.")
		return
	}
	config, err := api.modelAuth.ModelConfig(r.Context(), identity.WorkspaceID, provider, "", 30*time.Second)
	if err != nil {
		api.writeModelAuthError(w, err)
		return
	}
	// 模型目录与正式推理使用同一份 OAuth access token，避免 UI 展示账号实际不可用的模型。
	if provider == modelauth.ProviderGrok {
		delete(config.Headers, "x-grok-model-override")
	}
	models, err := api.model.ListModels(r.Context(), config)
	if err != nil {
		api.logger.Warn("list subscription models failed", "provider", provider, "error", err)
		writeError(w, http.StatusBadGateway, "model_catalog_unavailable", "Could not load models for the connected account.")
		return
	}
	writeJSON(w, http.StatusOK, models)
}

func parseModelAuthProvider(raw string) (modelauth.Provider, bool) {
	switch modelauth.Provider(strings.TrimSpace(raw)) {
	case modelauth.ProviderChatGPT:
		return modelauth.ProviderChatGPT, true
	case modelauth.ProviderGrok:
		return modelauth.ProviderGrok, true
	default:
		return "", false
	}
}

func (api *API) writeModelAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, modelauth.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "model_auth_unavailable", "Model account sign-in is not configured.")
	case errors.Is(err, modelauth.ErrNotConnected), errors.Is(err, modelauth.ErrReauthRequired):
		writeError(w, http.StatusConflict, "model_account_not_connected", "Connect the model account again.")
	case errors.Is(err, modelauth.ErrFlowNotFound):
		writeError(w, http.StatusGone, "model_auth_expired", "The model sign-in attempt expired. Start again.")
	default:
		api.logger.Warn("model account operation failed", "error", err)
		writeError(w, http.StatusBadGateway, "model_auth_failed", "Model account operation failed.")
	}
}
