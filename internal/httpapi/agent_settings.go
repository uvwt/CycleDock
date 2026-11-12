package httpapi

import (
	"context"
	"errors"
	"fmt"
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
	Configured             bool                    `json:"configured"`
	Routes                 []modelRouteResponse    `json:"routes"`
	APICredentials         []apiCredentialResponse `json:"api_credentials"`
	SystemInstructions     string                  `json:"system_instructions"`
	SourceID               string                  `json:"source_id,omitempty"`
	DefaultModelRouteID    string                  `json:"default_model_route_id,omitempty"`
	DiscussionModelRouteID string                  `json:"discussion_model_route_id,omitempty"`
	PlanModelRouteID       string                  `json:"plan_model_route_id,omitempty"`
}

type apiCredentialResponse struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	BaseURL          string `json:"base_url"`
	APIKeyEnv        string `json:"api_key_env"`
	APIKeyConfigured bool   `json:"api_key_configured"`
}

type modelRouteResponse struct {
	ID                  string     `json:"id"`
	Enabled             bool       `json:"enabled"`
	ProviderType        string     `json:"provider_type"`
	CredentialID        string     `json:"credential_id,omitempty"`
	APICredentialID     string     `json:"api_credential_id,omitempty"`
	APICredentialName   string     `json:"api_credential_name,omitempty"`
	ProviderBaseURL     string     `json:"provider_base_url"`
	Model               string     `json:"model"`
	BaseModel           string     `json:"base_model,omitempty"`
	APIKeyEnv           string     `json:"api_key_env"`
	APIKeyConfigured    bool       `json:"api_key_configured"`
	ContextWindowTokens int        `json:"context_window_tokens,omitempty"`
	MaxInputTokens      int        `json:"max_input_tokens,omitempty"`
	MaxOutputTokens     int        `json:"max_output_tokens,omitempty"`
	LimitsSource        string     `json:"limits_source"`
	LimitsVerifiedAt    *time.Time `json:"limits_verified_at,omitempty"`
}

type mcpConnectionResponse struct {
	ID                      string `json:"id"`
	Name                    string `json:"name"`
	URL                     string `json:"url"`
	TokenConfigured         bool   `json:"token_configured"`
	Enabled                 bool   `json:"enabled"`
	TimeoutMS               int32  `json:"timeout_ms"`
	ToolGuardMode           string `json:"tool_guard_mode"`
	ToolGuardModelRouteID   string `json:"tool_guard_model_route_id,omitempty"`
	ToolGuardTimeoutSeconds int32  `json:"tool_guard_timeout_seconds"`
	ToolGuardCustomRules    string `json:"tool_guard_custom_rules"`
}

// 所有工作区成员都需要知道 Agent 是否可执行；这里只返回配置状态，不暴露路由或凭据。
func (api *API) getAgentStatus(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	settings, err := api.store.GetAgentSettings(r.Context(), identity.WorkspaceID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		api.handleError(w, err)
		return
	}
	configured := false
	for _, route := range settings.Routes {
		if route.Enabled {
			configured = true
			break
		}
	}
	writeJSON(w, http.StatusOK, struct {
		ModelConfigured bool `json:"model_configured"`
	}{ModelConfigured: configured})
}

func (api *API) getAgentSettings(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	// 读取已持久化的模型路由不依赖外网模型目录；改为后台异步预热，避免首屏因外网超时卡顿。
	if api.model != nil {
		api.model.WarmOnlineRegistryAsync()
	}
	settings, err := api.store.GetAgentSettings(r.Context(), identity.WorkspaceID)
	if errors.Is(err, domain.ErrNotFound) {
		creds, credErr := api.store.ListAPICredentials(r.Context(), identity.WorkspaceID)
		if credErr != nil {
			api.handleError(w, credErr)
			return
		}
		writeJSON(w, http.StatusOK, agentSettingsResponse{
			Configured:     false,
			Routes:         []modelRouteResponse{},
			APICredentials: apiCredentialsToResponse(creds),
		})
		return
	}
	if err != nil {
		api.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agentSettingsToResponse(settings))
}

func (api *API) listAgentAPICredentials(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	credentials, err := api.store.ListAPICredentials(r.Context(), identity.WorkspaceID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiCredentialsToResponse(credentials))
}

func (api *API) createAgentAPICredential(w http.ResponseWriter, r *http.Request) {
	api.saveAgentAPICredential(w, r, uuid.Nil)
}

func (api *API) updateAgentAPICredential(w http.ResponseWriter, r *http.Request) {
	credentialID, ok := parseUUIDParam(w, r, "credentialID")
	if !ok {
		return
	}
	api.saveAgentAPICredential(w, r, credentialID)
}

func (api *API) saveAgentAPICredential(w http.ResponseWriter, r *http.Request, credentialID uuid.UUID) {
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	var request struct {
		Name        string `json:"name"`
		BaseURL     string `json:"base_url"`
		APIKey      string `json:"api_key"`
		APIKeyEnv   string `json:"api_key_env"`
		ClearAPIKey bool   `json:"clear_api_key"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	credential, err := api.store.SaveAPICredential(r.Context(), identity.WorkspaceID, store.APICredentialInput{
		ID:          credentialID,
		Name:        request.Name,
		BaseURL:     request.BaseURL,
		APIKey:      request.APIKey,
		APIKeyEnv:   request.APIKeyEnv,
		ClearAPIKey: request.ClearAPIKey,
	})
	if err != nil {
		api.handleError(w, err)
		return
	}
	status := http.StatusOK
	if credentialID == uuid.Nil {
		status = http.StatusCreated
	}
	writeJSON(w, status, apiCredentialToResponse(credential))
}

func (api *API) deleteAgentAPICredential(w http.ResponseWriter, r *http.Request) {
	credentialID, ok := parseUUIDParam(w, r, "credentialID")
	if !ok {
		return
	}
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	if err := api.store.DeleteAPICredential(r.Context(), identity.WorkspaceID, credentialID); err != nil {
		api.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (api *API) saveAgentSettings(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	var request struct {
		Routes []struct {
			ID                  string `json:"id"`
			Enabled             bool   `json:"enabled"`
			ProviderType        string `json:"provider_type"`
			CredentialID        string `json:"credential_id"`
			APICredentialID     string `json:"api_credential_id"`
			APICredentialName   string `json:"api_credential_name"`
			ProviderBaseURL     string `json:"provider_base_url"`
			Model               string `json:"model"`
			BaseModel           string `json:"base_model"`
			APIKey              string `json:"api_key"`
			APIKeyEnv           string `json:"api_key_env"`
			ClearAPIKey         bool   `json:"clear_api_key"`
			APIKeyConfigured    bool   `json:"api_key_configured"`
			ContextWindowTokens int    `json:"context_window_tokens"`
			MaxInputTokens      int    `json:"max_input_tokens"`
			MaxOutputTokens     int    `json:"max_output_tokens"`
			LimitsSource        string `json:"limits_source"`
		} `json:"routes"`
		SystemInstructions     string `json:"system_instructions"`
		DefaultModelRouteID    string `json:"default_model_route_id"`
		DiscussionModelRouteID string `json:"discussion_model_route_id"`
		PlanModelRouteID       string `json:"plan_model_route_id"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if api.model != nil {
		api.model.EnsureOnlineRegistry(r.Context())
	}
	existingSettings, _ := api.store.GetAgentSettings(r.Context(), identity.WorkspaceID)
	existingRoutesByID := make(map[uuid.UUID]store.ModelRoute, len(existingSettings.Routes))
	for _, er := range existingSettings.Routes {
		existingRoutesByID[er.ID] = er
	}
	existingCreds, _ := api.store.ListAPICredentials(r.Context(), identity.WorkspaceID)
	existingCredsByID := make(map[uuid.UUID]db.WorkspaceApiCredential, len(existingCreds))
	existingCredsByURL := make(map[string]db.WorkspaceApiCredential, len(existingCreds))
	for _, cred := range existingCreds {
		existingCredsByID[cred.ID] = cred
		prev, exists := existingCredsByURL[cred.BaseUrl]
		if !exists || (len(prev.ApiKeyCiphertext) == 0 && prev.ApiKeyEnv == "" && (len(cred.ApiKeyCiphertext) > 0 || cred.ApiKeyEnv != "")) {
			existingCredsByURL[cred.BaseUrl] = cred
		}
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
		var apiCredentialID *uuid.UUID
		if strings.TrimSpace(route.APICredentialID) != "" {
			parsed, err := uuid.Parse(strings.TrimSpace(route.APICredentialID))
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_api_credential_id", "API credential id is invalid.")
				return
			}
			apiCredentialID = &parsed
		}

		providerType := strings.TrimSpace(route.ProviderType)
		if providerType == "" {
			providerType = "openai_compatible"
		}
		baseModel := strings.TrimSpace(route.BaseModel)
		if prev, ok := existingRoutesByID[routeID]; ok && !strings.EqualFold(strings.TrimSpace(prev.Model), strings.TrimSpace(route.Model)) {
			// 模型 ID 变更且未显式传入新底层模型时，清空旧底层模型关联，重新触发自动识别。
			if strings.EqualFold(baseModel, strings.TrimSpace(prev.BaseModel)) {
				baseModel = ""
			}
		}

		limitsSource := strings.TrimSpace(route.LimitsSource)
		contextWindow := route.ContextWindowTokens
		maxInput := route.MaxInputTokens
		maxOutput := route.MaxOutputTokens
		var limitsVerifiedAt *time.Time

		// 当非用户手动覆盖时，按优先级自动发现模型容量：
		// 1. 服务商 /models 缓存 -> 2. 实时注册表 / 内置注册表精确匹配 -> 3. 调用路由自身 LLM 从实时注册表候选列表中识别底层模型
		if limitsSource != modelclient.LimitsSourceUser {
			var routeCfg modelclient.Config
			lookupBaseURL := store.NormalizeAPIBaseURL(route.ProviderBaseURL)
			if providerType != "openai_compatible" && api.modelAuth != nil && credentialID != nil {
				if cfg, err := api.modelAuth.ModelConfigForCredential(
					r.Context(),
					identity.WorkspaceID,
					modelauth.Provider(providerType),
					*credentialID,
					route.Model,
					10*time.Second,
				); err == nil {
					routeCfg = cfg
					lookupBaseURL = cfg.BaseURL
				}
			} else if providerType == "openai_compatible" {
				apiKey := strings.TrimSpace(route.APIKey)
				envName := strings.TrimSpace(route.APIKeyEnv)
				var matchedCred *db.WorkspaceApiCredential
				if apiCredentialID != nil {
					if c, ok := existingCredsByID[*apiCredentialID]; ok {
						matchedCred = &c
						if lookupBaseURL == "" {
							lookupBaseURL = c.BaseUrl
						}
					}
				} else if lookupBaseURL != "" {
					if prev, ok := existingRoutesByID[routeID]; ok && prev.ProviderBaseUrl == lookupBaseURL && prev.ApiCredentialID.Valid {
						if c, ok := existingCredsByID[prev.ApiCredentialID.UUID]; ok {
							matchedCred = &c
						}
					}
					if matchedCred == nil {
						if c, ok := existingCredsByURL[lookupBaseURL]; ok {
							matchedCred = &c
						}
					}
				}
				if apiKey == "" && !route.ClearAPIKey && matchedCred != nil && lookupBaseURL == matchedCred.BaseUrl {
					if k, err := api.store.APICredentialKey(*matchedCred); err == nil && k != "" {
						apiKey = k
					} else if envName == "" {
						envName = matchedCred.ApiKeyEnv
					}
				}
				if apiKey == "" && envName != "" && agentconfig.ValidEnvName(envName) {
					apiKey = strings.TrimSpace(os.Getenv(envName))
				}
				if lookupBaseURL != "" {
					routeCfg = modelclient.Config{
						BaseURL: lookupBaseURL,
						APIKey:  apiKey,
						Model:   strings.TrimSpace(route.Model),
						Timeout: 8 * time.Second,
					}
				}
			}

			if api.model != nil && lookupBaseURL != "" {
				if cached, ok := api.model.CachedProviderModelLimits(lookupBaseURL, route.Model); ok && cached != nil {
					contextWindow = cached.ContextWindowTokens
					maxInput = cached.MaxInputTokens
					maxOutput = cached.MaxOutputTokens
					limitsSource = modelclient.LimitsSourceProvider
					limitsVerifiedAt = cached.VerifiedAt
				}
			}
			if limitsSource != modelclient.LimitsSourceProvider {
				if reg, canonical, ok := modelclient.LookupRegistryModelLimitsWithCanonical(route.Model, baseModel); ok {
					contextWindow = reg.ContextWindowTokens
					maxInput = reg.MaxInputTokens
					maxOutput = reg.MaxOutputTokens
					limitsSource = reg.Source
					now := time.Now().UTC()
					limitsVerifiedAt = &now
					if baseModel == "" && canonical != "" && !strings.EqualFold(canonical, strings.TrimSpace(route.Model)) {
						baseModel = canonical
					}
				} else if api.model != nil && routeCfg.BaseURL != "" && strings.TrimSpace(route.Model) != "" {
					if identifiedBase, llmLimits, ok := api.model.IdentifyBaseModelWithLLM(r.Context(), routeCfg, route.Model); ok {
						baseModel = identifiedBase
						contextWindow = llmLimits.ContextWindowTokens
						maxInput = llmLimits.MaxInputTokens
						maxOutput = llmLimits.MaxOutputTokens
						limitsSource = llmLimits.Source
						limitsVerifiedAt = llmLimits.VerifiedAt
					} else {
						contextWindow = 0
						maxInput = 0
						maxOutput = 0
						limitsSource = modelclient.LimitsSourceUnknown
					}
				}
			}
		}

		routes = append(routes, store.ModelRouteInput{
			ID:                  routeID,
			Enabled:             route.Enabled,
			ProviderType:        providerType,
			CredentialID:        credentialID,
			APICredentialID:     apiCredentialID,
			ProviderBaseURL:     route.ProviderBaseURL,
			Model:               route.Model,
			BaseModel:           baseModel,
			APIKey:              route.APIKey,
			APIKeyEnv:           route.APIKeyEnv,
			ClearAPIKey:         route.ClearAPIKey,
			ContextWindowTokens: contextWindow,
			MaxInputTokens:      maxInput,
			MaxOutputTokens:     maxOutput,
			LimitsSource:        limitsSource,
			LimitsVerifiedAt:    limitsVerifiedAt,
		})
	}
	parseRouteID := func(raw, field string) (*uuid.UUID, error) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return nil, nil
		}
		u, err := uuid.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid_%s", field)
		}
		return &u, nil
	}
	defaultRouteID, err := parseRouteID(request.DefaultModelRouteID, "default_model_route_id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_default_model_route_id", "Default model route id is invalid.")
		return
	}
	discussionRouteID, err := parseRouteID(request.DiscussionModelRouteID, "discussion_model_route_id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_discussion_model_route_id", "Discussion model route id is invalid.")
		return
	}
	planRouteID, err := parseRouteID(request.PlanModelRouteID, "plan_model_route_id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_plan_model_route_id", "Plan model route id is invalid.")
		return
	}
	settings, err := api.store.SaveAgentSettings(r.Context(), identity.WorkspaceID, store.AgentSettingsInput{
		Routes:                 routes,
		SystemInstructions:     request.SystemInstructions,
		DefaultModelRouteID:    defaultRouteID,
		DiscussionModelRouteID: discussionRouteID,
		PlanModelRouteID:       planRouteID,
	})
	if err != nil {
		api.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agentSettingsToResponse(settings))
}

type resolvedOpenAIAuth struct {
	baseURL   string
	apiKey    string
	apiKeyEnv string
}

// resolveOpenAICompatibleAuth 统一解析自定义 API 的连通性测试与模型列表鉴权参数：
// 1. 优先采用请求直传的新密钥；
// 2. 否则按 api_credential_id -> route_id -> 归一化 base_url 查找服务端加密凭据；
// 3. 严禁在未提供新密钥时把已保存密钥转发到不同的 Base URL；
// 4. 若服务端存在密文但解密失败，显式报错而非静默降级为空密钥。
func (api *API) resolveOpenAICompatibleAuth(
	ctx context.Context,
	w http.ResponseWriter,
	workspaceID uuid.UUID,
	rawAPICredentialID, rawRouteID, rawBaseURL, rawAPIKey, rawAPIKeyEnv string,
) (resolvedOpenAIAuth, bool) {
	baseURL := store.NormalizeAPIBaseURL(rawBaseURL)
	apiKey := strings.TrimSpace(rawAPIKey)
	apiKeyEnv := strings.TrimSpace(rawAPIKeyEnv)
	boundToCredential := false

	if trimmedCredID := strings.TrimSpace(rawAPICredentialID); trimmedCredID != "" {
		credUUID, err := uuid.Parse(trimmedCredID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_api_credential_id", "API credential id is invalid.")
			return resolvedOpenAIAuth{}, false
		}
		cred, err := api.store.GetAPICredential(ctx, workspaceID, credUUID)
		if err != nil {
			api.handleError(w, err)
			return resolvedOpenAIAuth{}, false
		}
		boundToCredential = true
		if baseURL == "" {
			baseURL = cred.BaseUrl
		}
		hasStoredSecret := len(cred.ApiKeyCiphertext) > 0 || cred.ApiKeyEnv != ""
		if apiKey == "" && apiKeyEnv == "" && hasStoredSecret && baseURL != cred.BaseUrl {
			writeError(w, http.StatusBadRequest, "api_url_changed", "Enter a new API key to access a different Base URL.")
			return resolvedOpenAIAuth{}, false
		}
		if apiKey == "" && apiKeyEnv == "" {
			if len(cred.ApiKeyCiphertext) > 0 {
				decrypted, err := api.store.APICredentialKey(cred)
				if err != nil {
					if api.logger != nil {
						api.logger.Warn("decrypt custom provider API credential failed", "credential_id", cred.ID, "error", err)
					}
					writeError(w, http.StatusServiceUnavailable, "api_key_unavailable", "Saved model API key cannot be decrypted.")
					return resolvedOpenAIAuth{}, false
				}
				apiKey = decrypted
			} else if cred.ApiKeyEnv != "" {
				apiKeyEnv = cred.ApiKeyEnv
			}
		}
	} else if apiKey == "" && apiKeyEnv == "" && strings.TrimSpace(rawRouteID) != "" {
		if routeUUID, err := uuid.Parse(strings.TrimSpace(rawRouteID)); err == nil {
			if route, err := api.store.GetModelRoute(ctx, workspaceID, routeUUID); err == nil {
				boundToCredential = route.ApiCredentialID.Valid
				if baseURL == "" {
					baseURL = route.ProviderBaseUrl
				}
				hasStoredSecret := len(route.ApiKeyCiphertext) > 0 || route.ApiKeyEnv != ""
				if hasStoredSecret && baseURL != route.ProviderBaseUrl {
					writeError(w, http.StatusBadRequest, "api_url_changed", "Enter a new API key to access a different Base URL.")
					return resolvedOpenAIAuth{}, false
				}
				if len(route.ApiKeyCiphertext) > 0 {
					decrypted, err := api.store.ModelRouteAPIKey(route)
					if err != nil {
						if api.logger != nil {
							api.logger.Warn("decrypt model route API key failed", "route_id", route.ID, "error", err)
						}
						writeError(w, http.StatusServiceUnavailable, "api_key_unavailable", "Saved model API key cannot be decrypted.")
						return resolvedOpenAIAuth{}, false
					}
					apiKey = decrypted
				} else if route.ApiKeyEnv != "" {
					apiKeyEnv = route.ApiKeyEnv
				}
			}
		}
	}

	// 仅在请求未绑定明确凭据或已有路由时，才按归一化 Base URL 兜底复用同地址凭据，
	// 防止同一网关下多个独立凭据之间窜用密钥。
	if !boundToCredential && apiKey == "" && apiKeyEnv == "" && baseURL != "" {
		if creds, err := api.store.ListAPICredentials(ctx, workspaceID); err == nil {
			for _, cred := range creds {
				if cred.BaseUrl != baseURL {
					continue
				}
				if len(cred.ApiKeyCiphertext) > 0 {
					decrypted, err := api.store.APICredentialKey(cred)
					if err != nil {
						if api.logger != nil {
							api.logger.Warn("decrypt custom provider API credential failed", "credential_id", cred.ID, "error", err)
						}
						writeError(w, http.StatusServiceUnavailable, "api_key_unavailable", "Saved model API key cannot be decrypted.")
						return resolvedOpenAIAuth{}, false
					}
					apiKey = decrypted
					break
				}
				if apiKeyEnv == "" && cred.ApiKeyEnv != "" {
					apiKeyEnv = cred.ApiKeyEnv
				}
			}
		}
	}

	return resolvedOpenAIAuth{
		baseURL:   baseURL,
		apiKey:    apiKey,
		apiKeyEnv: apiKeyEnv,
	}, true
}

func (api *API) testAgentProvider(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	var request struct {
		ProviderType    string `json:"provider_type"`
		CredentialID    string `json:"credential_id"`
		APICredentialID string `json:"api_credential_id"`
		ProviderBaseURL string `json:"provider_base_url"`
		Model           string `json:"model"`
		APIKey          string `json:"api_key"`
		APIKeyEnv       string `json:"api_key_env"`
		RouteID         string `json:"route_id"`
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
		resolved, ok := api.resolveOpenAICompatibleAuth(
			r.Context(),
			w,
			identity.WorkspaceID,
			request.APICredentialID,
			request.RouteID,
			request.ProviderBaseURL,
			request.APIKey,
			request.APIKeyEnv,
		)
		if !ok {
			return
		}
		result = checker.Provider(r.Context(), agentcheck.ProviderInput{
			BaseURL:   resolved.baseURL,
			Model:     request.Model,
			APIKey:    resolved.apiKey,
			APIKeyEnv: resolved.apiKeyEnv,
			Timeout:   timeout,
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
		APICredentialID string `json:"api_credential_id"`
		ProviderBaseURL string `json:"provider_base_url"`
		APIKey          string `json:"api_key"`
		APIKeyEnv       string `json:"api_key_env"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}

	resolved, ok := api.resolveOpenAICompatibleAuth(
		r.Context(),
		w,
		identity.WorkspaceID,
		request.APICredentialID,
		"",
		request.ProviderBaseURL,
		request.APIKey,
		request.APIKeyEnv,
	)
	if !ok {
		return
	}
	if err := agentconfig.ValidateEndpoint(resolved.baseURL); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_endpoint", err.Error())
		return
	}

	apiKey := resolved.apiKey
	if apiKey == "" && resolved.apiKeyEnv != "" {
		if !agentconfig.ValidEnvName(resolved.apiKeyEnv) {
			writeError(w, http.StatusBadRequest, "invalid_secret_env", "Model API key environment variable name is invalid.")
			return
		}
		value, ok := os.LookupEnv(resolved.apiKeyEnv)
		if !ok || strings.TrimSpace(value) == "" {
			writeError(w, http.StatusBadRequest, "secret_env_missing", "Configured model API key environment variable is not set.")
			return
		}
		apiKey = value
	}

	models, err := api.model.ListModels(r.Context(), modelclient.Config{
		BaseURL: resolved.baseURL,
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
		Name                    string `json:"name"`
		URL                     string `json:"url"`
		BearerToken             string `json:"bearer_token"`
		ClearBearerToken        bool   `json:"clear_bearer_token"`
		Enabled                 *bool  `json:"enabled"`
		TimeoutMS               int32  `json:"timeout_ms"`
		ToolGuardMode           string `json:"tool_guard_mode"`
		ToolGuardModelRouteID   string `json:"tool_guard_model_route_id"`
		ToolGuardTimeoutSeconds int32  `json:"tool_guard_timeout_seconds"`
		ToolGuardCustomRules    string `json:"tool_guard_custom_rules"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	var guardRouteID *uuid.UUID
	if strings.TrimSpace(request.ToolGuardModelRouteID) != "" {
		if id, err := uuid.Parse(strings.TrimSpace(request.ToolGuardModelRouteID)); err == nil {
			guardRouteID = &id
		}
	}
	connection, err := api.store.SaveMCPConnection(r.Context(), identity.WorkspaceID, store.MCPConnectionInput{
		ID: connectionID, Name: strings.TrimSpace(request.Name), URL: strings.TrimSpace(request.URL),
		BearerToken: request.BearerToken, ClearBearerToken: request.ClearBearerToken, Enabled: enabled,
		TimeoutMS:               request.TimeoutMS,
		ToolGuardMode:           request.ToolGuardMode,
		ToolGuardModelRouteID:   guardRouteID,
		ToolGuardTimeoutSeconds: request.ToolGuardTimeoutSeconds,
		ToolGuardCustomRules:    request.ToolGuardCustomRules,
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

func apiCredentialToResponse(cred db.WorkspaceApiCredential) apiCredentialResponse {
	return apiCredentialResponse{
		ID:               cred.ID.String(),
		Name:             cred.Name,
		BaseURL:          cred.BaseUrl,
		APIKeyEnv:        cred.ApiKeyEnv,
		APIKeyConfigured: len(cred.ApiKeyCiphertext) > 0 || cred.ApiKeyEnv != "",
	}
}

func apiCredentialsToResponse(creds []db.WorkspaceApiCredential) []apiCredentialResponse {
	out := make([]apiCredentialResponse, 0, len(creds))
	for _, cred := range creds {
		out = append(out, apiCredentialToResponse(cred))
	}
	return out
}

func agentSettingsToResponse(settings store.AgentSettings) agentSettingsResponse {
	routes := make([]modelRouteResponse, 0, len(settings.Routes))
	now := time.Now().UTC()
	for _, route := range settings.Routes {
		credentialID := ""
		if route.CredentialID.Valid {
			credentialID = route.CredentialID.UUID.String()
		}
		apiCredentialID := ""
		if route.ApiCredentialID.Valid {
			apiCredentialID = route.ApiCredentialID.UUID.String()
		}
		contextWindow := int(route.ContextWindowTokens.Int32)
		maxInput := int(route.MaxInputTokens.Int32)
		maxOutput := int(route.MaxOutputTokens.Int32)
		limitsSource := route.LimitsSource
		if limitsSource == "" {
			limitsSource = modelclient.LimitsSourceUnknown
		}
		var verifiedAt *time.Time
		if route.LimitsVerifiedAt.Valid {
			ts := route.LimitsVerifiedAt.Time.UTC()
			verifiedAt = &ts
		}
		baseModel := route.BaseModel
		// 对存量尚未回填限制的记录，在读取时尝试按注册表规则补齐精确识别模型的限制展示。
		if limitsSource == modelclient.LimitsSourceUnknown && contextWindow == 0 && maxInput == 0 && maxOutput == 0 {
			if reg, canonical, ok := modelclient.LookupRegistryModelLimitsWithCanonical(route.Model, baseModel); ok {
				contextWindow = reg.ContextWindowTokens
				maxInput = reg.MaxInputTokens
				maxOutput = reg.MaxOutputTokens
				limitsSource = reg.Source
				if verifiedAt == nil {
					verifiedAt = &now
				}
				if baseModel == "" && canonical != "" && !strings.EqualFold(canonical, strings.TrimSpace(route.Model)) {
					baseModel = canonical
				}
			}
		}
		routes = append(routes, modelRouteResponse{
			ID:                  route.ID.String(),
			Enabled:             route.Enabled,
			ProviderType:        route.ProviderType,
			CredentialID:        credentialID,
			APICredentialID:     apiCredentialID,
			APICredentialName:   route.ApiCredentialName,
			ProviderBaseURL:     route.ProviderBaseUrl,
			Model:               route.Model,
			BaseModel:           baseModel,
			APIKeyEnv:           route.ApiKeyEnv,
			APIKeyConfigured:    len(route.ApiKeyCiphertext) > 0 || route.ApiKeyEnv != "",
			ContextWindowTokens: contextWindow,
			MaxInputTokens:      maxInput,
			MaxOutputTokens:     maxOutput,
			LimitsSource:        limitsSource,
			LimitsVerifiedAt:    verifiedAt,
		})
	}
	defaultRouteID := ""
	if settings.DefaultModelRouteID.Valid {
		defaultRouteID = settings.DefaultModelRouteID.UUID.String()
	}
	discussionRouteID := ""
	if settings.DiscussionModelRouteID.Valid {
		discussionRouteID = settings.DiscussionModelRouteID.UUID.String()
	}
	planRouteID := ""
	if settings.PlanModelRouteID.Valid {
		planRouteID = settings.PlanModelRouteID.UUID.String()
	}
	return agentSettingsResponse{
		Configured:             true,
		Routes:                 routes,
		APICredentials:         apiCredentialsToResponse(settings.APICredentials),
		SystemInstructions:     settings.SystemInstructions,
		SourceID:               settings.SourceID.String(),
		DefaultModelRouteID:    defaultRouteID,
		DiscussionModelRouteID: discussionRouteID,
		PlanModelRouteID:       planRouteID,
	}
}

func mcpConnectionToResponse(connection db.WorkspaceMcpConnection) mcpConnectionResponse {
	guardRouteID := ""
	if connection.ToolGuardModelRouteID.Valid {
		guardRouteID = connection.ToolGuardModelRouteID.UUID.String()
	}
	guardMode := connection.ToolGuardMode
	if guardMode == "" {
		guardMode = "block_high_risk"
	}
	guardTimeout := connection.ToolGuardTimeoutSeconds
	if guardTimeout <= 0 {
		guardTimeout = 10
	}
	return mcpConnectionResponse{
		ID:                      connection.ID.String(),
		Name:                    connection.Name,
		URL:                     connection.Url,
		TokenConfigured:         len(connection.BearerTokenCiphertext) != 0,
		Enabled:                 connection.Enabled,
		TimeoutMS:               connection.TimeoutMs,
		ToolGuardMode:           guardMode,
		ToolGuardModelRouteID:   guardRouteID,
		ToolGuardTimeoutSeconds: guardTimeout,
		ToolGuardCustomRules:    connection.ToolGuardCustomRules,
	}
}
