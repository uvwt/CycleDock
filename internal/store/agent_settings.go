package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/uvwt/CycleDock/internal/agentconfig"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/store/db"
)

const maxModelRoutes = 16

// ModelRoute 表示携带关联自定义服务商凭据信息的模型路由读模型。
type ModelRoute = db.ListWorkspaceModelRoutesRow

type APICredentialInput struct {
	ID          uuid.UUID
	Name        string
	BaseURL     string
	APIKey      string
	APIKeyEnv   string
	ClearAPIKey bool
}

type ModelRouteInput struct {
	ID                  uuid.UUID
	Enabled             bool
	ProviderType        string
	CredentialID        *uuid.UUID
	APICredentialID     *uuid.UUID
	ProviderBaseURL     string
	Model               string
	BaseModel           string
	APIKey              string
	APIKeyEnv           string
	ClearAPIKey         bool
	ContextWindowTokens int
	MaxInputTokens      int
	MaxOutputTokens     int
	LimitsSource        string
	LimitsVerifiedAt    *time.Time
}

type AgentSettingsInput struct {
	SystemInstructions      string
	Routes                  []ModelRouteInput
	DefaultModelRouteID     *uuid.UUID
	DiscussionModelRouteID  *uuid.UUID
	PlanModelRouteID        *uuid.UUID
}

// ToolGuardSettings 描述工作区全局工具安全守卫的统一评估配置。
type ToolGuardSettings struct {
	Mode           string
	ModelRouteID   *uuid.UUID
	TimeoutSeconds int32
	CustomRules    string
	UpdatedAt      *time.Time
}

// ToolGuardSettingsInput 用于单独更新工作区全局工具守卫配置，与模型路由/全局指令保存隔离。
type ToolGuardSettingsInput struct {
	Mode           string
	ModelRouteID   *uuid.UUID
	TimeoutSeconds int32
	CustomRules    string
}

type AgentSettings struct {
	db.WorkspaceAgentSetting
	Routes         []ModelRoute
	APICredentials []db.WorkspaceApiCredential
}

// NormalizeAPIBaseURL 统一裁剪首尾空白与末尾斜杠，避免同一服务商地址因尾部 '/' 不一致导致匹配失败。
func NormalizeAPIBaseURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

func defaultAPICredentialName(baseURL string) string {
	name := baseURL
	if parsed, err := url.Parse(baseURL); err == nil && parsed.Hostname() != "" {
		name = parsed.Hostname()
	}
	if utf8.RuneCountInString(name) > 80 {
		runes := []rune(name)
		name = string(runes[:80])
	}
	return name
}

func (s *Store) ListAPICredentials(ctx context.Context, workspaceID uuid.UUID) ([]db.WorkspaceApiCredential, error) {
	return s.queries.ListWorkspaceAPICredentials(ctx, workspaceID)
}

func (s *Store) GetAPICredential(ctx context.Context, workspaceID, id uuid.UUID) (db.WorkspaceApiCredential, error) {
	credential, err := s.queries.GetWorkspaceAPICredential(ctx, db.GetWorkspaceAPICredentialParams{
		WorkspaceID: workspaceID, ID: id,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.WorkspaceApiCredential{}, domain.ErrNotFound
	}
	return credential, err
}

func (s *Store) resolveAPICredentialWrite(
	workspaceID uuid.UUID,
	input APICredentialInput,
	existing *db.WorkspaceApiCredential,
) (db.UpsertWorkspaceAPICredentialParams, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.BaseURL = NormalizeAPIBaseURL(input.BaseURL)
	input.APIKey = strings.TrimSpace(input.APIKey)
	input.APIKeyEnv = strings.TrimSpace(input.APIKeyEnv)

	if input.ID == uuid.Nil {
		input.ID = uuid.New()
	}
	if input.BaseURL == "" {
		return db.UpsertWorkspaceAPICredentialParams{}, fmt.Errorf("%w: base_url is required", domain.ErrConflict)
	}
	if err := agentconfig.ValidateEndpoint(input.BaseURL); err != nil {
		return db.UpsertWorkspaceAPICredentialParams{}, fmt.Errorf("%w: %v", domain.ErrConflict, err)
	}
	if input.Name == "" {
		if existing != nil && existing.Name != "" {
			input.Name = existing.Name
		} else {
			input.Name = defaultAPICredentialName(input.BaseURL)
		}
	}
	if utf8.RuneCountInString(input.Name) > 80 || strings.ContainsAny(input.Name, "\r\n") {
		return db.UpsertWorkspaceAPICredentialParams{}, fmt.Errorf("%w: API credential name must contain 1-80 characters", domain.ErrConflict)
	}
	if len(input.APIKey) > 8192 || strings.ContainsAny(input.APIKey, "\r\n") {
		return db.UpsertWorkspaceAPICredentialParams{}, fmt.Errorf("%w: invalid API key", domain.ErrConflict)
	}
	if input.APIKey != "" && input.ClearAPIKey {
		return db.UpsertWorkspaceAPICredentialParams{}, fmt.Errorf("%w: cannot set and clear API key together", domain.ErrConflict)
	}
	if input.APIKeyEnv != "" && !agentconfig.ValidEnvName(input.APIKeyEnv) {
		return db.UpsertWorkspaceAPICredentialParams{}, fmt.Errorf("%w: api_key_env must be an environment variable name", domain.ErrConflict)
	}

	if existing != nil {
		// 不允许编辑服务商地址时悄悄把原服务的已加密 API Key 带到另一个地址。
		if existing.BaseUrl != input.BaseURL && len(existing.ApiKeyCiphertext) > 0 && input.APIKey == "" && !input.ClearAPIKey {
			return db.UpsertWorkspaceAPICredentialParams{}, fmt.Errorf("%w: changing provider base URL requires replacing or clearing its API key", domain.ErrConflict)
		}
	}

	var ciphertext []byte
	var apiKeyEnv string
	var replaceAPIKey bool

	switch {
	case input.APIKey != "":
		var err error
		ciphertext, err = s.encryptAPICredentialKey(workspaceID.String(), input.ID.String(), input.APIKey)
		if err != nil {
			return db.UpsertWorkspaceAPICredentialParams{}, err
		}
		apiKeyEnv = input.APIKeyEnv
		replaceAPIKey = true
	case input.ClearAPIKey:
		ciphertext = nil
		apiKeyEnv = ""
		replaceAPIKey = true
	case input.APIKeyEnv != "":
		apiKeyEnv = input.APIKeyEnv
		if existing == nil || existing.ApiKeyEnv != input.APIKeyEnv {
			ciphertext = nil
			replaceAPIKey = true
		}
	default:
		if existing != nil {
			apiKeyEnv = existing.ApiKeyEnv
		}
	}

	return db.UpsertWorkspaceAPICredentialParams{
		ID:               input.ID,
		WorkspaceID:      workspaceID,
		Name:             input.Name,
		BaseUrl:          input.BaseURL,
		ApiKeyEnv:        apiKeyEnv,
		ApiKeyCiphertext: ciphertext,
		ReplaceApiKey:    replaceAPIKey,
	}, nil
}

func (s *Store) SaveAPICredential(
	ctx context.Context,
	workspaceID uuid.UUID,
	input APICredentialInput,
) (db.WorkspaceApiCredential, error) {
	isCreate := input.ID == uuid.Nil
	var exists bool
	if err := s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM workspaces WHERE id=$1)", workspaceID).Scan(&exists); err != nil {
		return db.WorkspaceApiCredential{}, err
	}
	if !exists {
		return db.WorkspaceApiCredential{}, domain.ErrNotFound
	}
	var existing *db.WorkspaceApiCredential
	if !isCreate {
		found, err := s.queries.GetWorkspaceAPICredential(ctx, db.GetWorkspaceAPICredentialParams{
			WorkspaceID: workspaceID, ID: input.ID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return db.WorkspaceApiCredential{}, domain.ErrNotFound
		}
		if err != nil {
			return db.WorkspaceApiCredential{}, err
		}
		existing = &found
	}
	params, err := s.resolveAPICredentialWrite(workspaceID, input, existing)
	if err != nil {
		return db.WorkspaceApiCredential{}, err
	}
	credential, err := s.queries.UpsertWorkspaceAPICredential(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.WorkspaceApiCredential{}, fmt.Errorf("%w: changing provider base URL requires replacing or clearing its API key", domain.ErrConflict)
	}
	if err != nil {
		return db.WorkspaceApiCredential{}, mapConstraintError(err)
	}
	return credential, nil
}

func (s *Store) DeleteAPICredential(ctx context.Context, workspaceID, credentialID uuid.UUID) error {
	affected, err := s.queries.DeleteWorkspaceAPICredential(ctx, db.DeleteWorkspaceAPICredentialParams{
		WorkspaceID: workspaceID, ID: credentialID,
	})
	if err != nil {
		return err
	}
	if affected != 1 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) GetAgentSettings(ctx context.Context, workspaceID uuid.UUID) (AgentSettings, error) {
	settings, err := s.queries.GetWorkspaceAgentSettings(ctx, workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentSettings{}, domain.ErrNotFound
	}
	if err != nil {
		return AgentSettings{}, err
	}
	routes, err := s.queries.ListWorkspaceModelRoutes(ctx, workspaceID)
	if err != nil {
		return AgentSettings{}, err
	}
	apiCredentials, err := s.queries.ListWorkspaceAPICredentials(ctx, workspaceID)
	if err != nil {
		return AgentSettings{}, err
	}
	return AgentSettings{
		WorkspaceAgentSetting: settings,
		Routes:                routes,
		APICredentials:        apiCredentials,
	}, nil
}

func normalizeModelRoutes(routes []ModelRouteInput) ([]ModelRouteInput, error) {
	if len(routes) > maxModelRoutes {
		return nil, fmt.Errorf("%w: at most %d model routes are allowed", domain.ErrConflict, maxModelRoutes)
	}
	normalized := make([]ModelRouteInput, len(routes))
	seenIDs := make(map[uuid.UUID]struct{}, len(routes))
	for i, route := range routes {
		route.ProviderType = strings.TrimSpace(route.ProviderType)
		route.ProviderBaseURL = NormalizeAPIBaseURL(route.ProviderBaseURL)
		route.Model = strings.TrimSpace(route.Model)
		route.BaseModel = strings.TrimSpace(route.BaseModel)
		route.APIKey = strings.TrimSpace(route.APIKey)
		route.APIKeyEnv = strings.TrimSpace(route.APIKeyEnv)
		if route.ID == uuid.Nil {
			route.ID = uuid.New()
		}
		if _, exists := seenIDs[route.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate model route id", domain.ErrConflict)
		}
		seenIDs[route.ID] = struct{}{}
		if route.Model == "" {
			return nil, fmt.Errorf("%w: model is required for every model route", domain.ErrConflict)
		}
		if len(route.BaseModel) > 160 || strings.ContainsAny(route.BaseModel, "\r\n") {
			return nil, fmt.Errorf("%w: invalid base_model", domain.ErrConflict)
		}
		validatedLimits, err := modelclient.ValidateUserModelLimits(modelclient.ModelLimits{
			ContextWindowTokens: route.ContextWindowTokens,
			MaxInputTokens:      route.MaxInputTokens,
			MaxOutputTokens:     route.MaxOutputTokens,
			Source:              route.LimitsSource,
			VerifiedAt:          route.LimitsVerifiedAt,
		})
		if err != nil {
			return nil, fmt.Errorf("%w: %v", domain.ErrConflict, err)
		}
		route.ContextWindowTokens = validatedLimits.ContextWindowTokens
		route.MaxInputTokens = validatedLimits.MaxInputTokens
		route.MaxOutputTokens = validatedLimits.MaxOutputTokens
		route.LimitsSource = validatedLimits.Source
		route.LimitsVerifiedAt = validatedLimits.VerifiedAt
		switch route.ProviderType {
		case "openai_compatible":
			if route.CredentialID != nil {
				return nil, fmt.Errorf("%w: OpenAI-compatible routes cannot use subscription credentials", domain.ErrConflict)
			}
			if route.APICredentialID == nil && route.ProviderBaseURL == "" {
				return nil, fmt.Errorf("%w: api_credential_id or provider_base_url is required for OpenAI-compatible routes", domain.ErrConflict)
			}
			if route.ProviderBaseURL != "" {
				if err := agentconfig.ValidateEndpoint(route.ProviderBaseURL); err != nil {
					return nil, fmt.Errorf("%w: %v", domain.ErrConflict, err)
				}
			}
			if len(route.APIKey) > 8192 || strings.ContainsAny(route.APIKey, "\r\n") {
				return nil, fmt.Errorf("%w: invalid API key", domain.ErrConflict)
			}
			if route.APIKey != "" && route.ClearAPIKey {
				return nil, fmt.Errorf("%w: cannot set and clear API key together", domain.ErrConflict)
			}
			if route.APIKeyEnv != "" && !agentconfig.ValidEnvName(route.APIKeyEnv) {
				return nil, fmt.Errorf("%w: api_key_env must be an environment variable name", domain.ErrConflict)
			}
		case "chatgpt", "grok":
			route.APICredentialID = nil
			route.ProviderBaseURL = ""
			route.APIKey = ""
			route.APIKeyEnv = ""
			if route.Enabled && route.CredentialID == nil {
				return nil, fmt.Errorf("%w: enabled subscription routes require a connected account", domain.ErrConflict)
			}
		default:
			return nil, fmt.Errorf("%w: unsupported provider_type", domain.ErrConflict)
		}
		normalized[i] = route
	}
	return normalized, nil
}

func apiCredentialHasSecret(c db.WorkspaceApiCredential) bool {
	return len(c.ApiKeyCiphertext) > 0 || c.ApiKeyEnv != ""
}

func (s *Store) SaveAgentSettings(
	ctx context.Context,
	workspaceID uuid.UUID,
	input AgentSettingsInput,
) (AgentSettings, error) {
	input.SystemInstructions = strings.TrimSpace(input.SystemInstructions)
	routes, err := normalizeModelRoutes(input.Routes)
	if err != nil {
		return AgentSettings{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AgentSettings{}, err
	}
	defer tx.Rollback(ctx)

	var lockedWorkspace uuid.UUID
	if err := tx.QueryRow(ctx, "SELECT id FROM workspaces WHERE id=$1 FOR UPDATE", workspaceID).Scan(&lockedWorkspace); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AgentSettings{}, domain.ErrNotFound
		}
		return AgentSettings{}, err
	}

	q := db.New(tx)
	var sourceID uuid.UUID
	existing, err := q.GetWorkspaceAgentSettings(ctx, workspaceID)
	switch {
	case err == nil:
		sourceID = existing.SourceID
		var kind string
		if err := tx.QueryRow(ctx,
			"SELECT kind FROM sources WHERE workspace_id=$1 AND id=$2",
			workspaceID, sourceID,
		).Scan(&kind); err != nil {
			return AgentSettings{}, err
		}
		if kind != string(domain.SourceKindCycleAgent) {
			return AgentSettings{}, fmt.Errorf("%w: configured agent source is not cycle_agent", domain.ErrConflict)
		}
	case errors.Is(err, pgx.ErrNoRows):
		source, err := q.CreateSource(ctx, db.CreateSourceParams{
			ID: uuid.New(), WorkspaceID: workspaceID,
			Kind: string(domain.SourceKindCycleAgent), Name: "Cycle Agent",
		})
		if err != nil {
			return AgentSettings{}, err
		}
		sourceID = source.ID
	default:
		return AgentSettings{}, err
	}

	for _, route := range routes {
		if route.CredentialID == nil {
			continue
		}
		var provider string
		if err := tx.QueryRow(ctx,
			"SELECT provider FROM model_oauth_credentials WHERE workspace_id=$1 AND id=$2",
			workspaceID, *route.CredentialID,
		).Scan(&provider); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return AgentSettings{}, fmt.Errorf("%w: model credential does not exist in this workspace", domain.ErrConflict)
			}
			return AgentSettings{}, err
		}
		if provider != route.ProviderType {
			return AgentSettings{}, fmt.Errorf("%w: model credential provider does not match route", domain.ErrConflict)
		}
	}

	existingCreds, err := q.ListWorkspaceAPICredentials(ctx, workspaceID)
	if err != nil {
		return AgentSettings{}, err
	}
	credByID := make(map[uuid.UUID]db.WorkspaceApiCredential, len(existingCreds))
	credByURL := make(map[string]db.WorkspaceApiCredential, len(existingCreds))
	for _, cred := range existingCreds {
		credByID[cred.ID] = cred
		prev, exists := credByURL[cred.BaseUrl]
		if !exists || (!apiCredentialHasSecret(prev) && apiCredentialHasSecret(cred)) {
			credByURL[cred.BaseUrl] = cred
		}
	}

	existingRoutes, err := q.ListWorkspaceModelRoutes(ctx, workspaceID)
	if err != nil {
		return AgentSettings{}, err
	}
	existingByID := make(map[uuid.UUID]ModelRoute, len(existingRoutes))
	for _, r := range existingRoutes {
		existingByID[r.ID] = r
	}

	// 解析并收敛每条 openai_compatible 路由的统一凭据引用：
	// 1. 显式传入 api_credential_id 时校验归属，并允许在同一事务内更新凭据密钥；
	// 2. 未传 api_credential_id 时按归一化 base_url 自动复用已有凭据或创建新凭据，保证多模型共享同一份加密密钥。
	for i := range routes {
		route := &routes[i]
		if route.ProviderType != "openai_compatible" {
			continue
		}
		if route.APICredentialID != nil {
			cred, ok := credByID[*route.APICredentialID]
			if !ok {
				return AgentSettings{}, fmt.Errorf("%w: API credential does not exist in this workspace", domain.ErrConflict)
			}
			targetURL := cred.BaseUrl
			if route.ProviderBaseURL != "" {
				targetURL = route.ProviderBaseURL
			}
			if targetURL != cred.BaseUrl || route.APIKey != "" || route.ClearAPIKey || (route.APIKeyEnv != "" && route.APIKeyEnv != cred.ApiKeyEnv) {
				params, err := s.resolveAPICredentialWrite(workspaceID, APICredentialInput{
					ID:          cred.ID,
					Name:        cred.Name,
					BaseURL:     targetURL,
					APIKey:      route.APIKey,
					APIKeyEnv:   route.APIKeyEnv,
					ClearAPIKey: route.ClearAPIKey,
				}, &cred)
				if err != nil {
					return AgentSettings{}, err
				}
				cred, err = q.UpsertWorkspaceAPICredential(ctx, params)
				if errors.Is(err, pgx.ErrNoRows) {
					return AgentSettings{}, fmt.Errorf("%w: changing provider base URL requires replacing or clearing its API key", domain.ErrConflict)
				}
				if err != nil {
					return AgentSettings{}, mapConstraintError(err)
				}
				credByID[cred.ID] = cred
				credByURL[cred.BaseUrl] = cred
			}
			route.ProviderBaseURL = cred.BaseUrl
			continue
		}

		// 兼容未显式指定 api_credential_id 的写入：若原路由已绑定同地址凭据则优先沿用，否则按 base_url 归并。
		var matched *db.WorkspaceApiCredential
		if prevRoute, ok := existingByID[route.ID]; ok && prevRoute.ApiCredentialID.Valid {
			if c, ok := credByID[prevRoute.ApiCredentialID.UUID]; ok && c.BaseUrl == route.ProviderBaseURL {
				copied := c
				matched = &copied
			}
		}
		if matched == nil {
			if c, ok := credByURL[route.ProviderBaseURL]; ok {
				copied := c
				matched = &copied
			}
		}

		if matched != nil {
			cred := *matched
			if route.APIKey != "" || route.ClearAPIKey || (route.APIKeyEnv != "" && route.APIKeyEnv != cred.ApiKeyEnv) {
				params, err := s.resolveAPICredentialWrite(workspaceID, APICredentialInput{
					ID:          cred.ID,
					Name:        cred.Name,
					BaseURL:     route.ProviderBaseURL,
					APIKey:      route.APIKey,
					APIKeyEnv:   route.APIKeyEnv,
					ClearAPIKey: route.ClearAPIKey,
				}, &cred)
				if err != nil {
					return AgentSettings{}, err
				}
				cred, err = q.UpsertWorkspaceAPICredential(ctx, params)
				if err != nil {
					return AgentSettings{}, mapConstraintError(err)
				}
				credByID[cred.ID] = cred
				credByURL[cred.BaseUrl] = cred
			}
			id := cred.ID
			route.APICredentialID = &id
			continue
		}

		params, err := s.resolveAPICredentialWrite(workspaceID, APICredentialInput{
			BaseURL:     route.ProviderBaseURL,
			APIKey:      route.APIKey,
			APIKeyEnv:   route.APIKeyEnv,
			ClearAPIKey: route.ClearAPIKey,
		}, nil)
		if err != nil {
			return AgentSettings{}, err
		}
		created, err := q.UpsertWorkspaceAPICredential(ctx, params)
		if err != nil {
			return AgentSettings{}, mapConstraintError(err)
		}
		credByID[created.ID] = created
		credByURL[created.BaseUrl] = created
		id := created.ID
		route.APICredentialID = &id
	}

	// 仅删除本次真正移除的 Route，保留既有 Route 记录原地更新，
	// 防止全表 DELETE 触发 cards / card_turns / tool_guard 外键 ON DELETE SET NULL 清空已绑定模型。
	keepIDs := make([]uuid.UUID, 0, len(routes))
	for _, route := range routes {
		keepIDs = append(keepIDs, route.ID)
	}
	if err := q.DeleteRemovedWorkspaceModelRoutes(ctx, db.DeleteRemovedWorkspaceModelRoutesParams{
		WorkspaceID: workspaceID,
		KeepIds:     keepIDs,
	}); err != nil {
		return AgentSettings{}, err
	}
	if len(routes) > 0 {
		if err := q.ShiftWorkspaceModelRoutePositions(ctx, workspaceID); err != nil {
			return AgentSettings{}, err
		}
	}

	validRouteIDs := make(map[uuid.UUID]bool, len(routes))
	for position, route := range routes {
		credentialID := uuid.NullUUID{}
		if route.CredentialID != nil {
			credentialID = uuid.NullUUID{UUID: *route.CredentialID, Valid: true}
		}
		apiCredentialID := uuid.NullUUID{}
		if route.APICredentialID != nil {
			apiCredentialID = uuid.NullUUID{UUID: *route.APICredentialID, Valid: true}
		}
		now := time.Now().UTC()
		var userOverride modelclient.ModelLimits
		var providerLimits *modelclient.ModelLimits
		switch route.LimitsSource {
		case modelclient.LimitsSourceUser:
			userOverride = modelclient.ModelLimits{
				ContextWindowTokens: route.ContextWindowTokens,
				MaxInputTokens:      route.MaxInputTokens,
				MaxOutputTokens:     route.MaxOutputTokens,
				Source:              modelclient.LimitsSourceUser,
				VerifiedAt:          route.LimitsVerifiedAt,
			}
		case modelclient.LimitsSourceProvider:
			providerLimits = &modelclient.ModelLimits{
				ContextWindowTokens: route.ContextWindowTokens,
				MaxInputTokens:      route.MaxInputTokens,
				MaxOutputTokens:     route.MaxOutputTokens,
				Source:              modelclient.LimitsSourceProvider,
				VerifiedAt:          route.LimitsVerifiedAt,
			}
		default:
			if existing, ok := existingByID[route.ID]; ok &&
				existing.LimitsSource == modelclient.LimitsSourceProvider &&
				existing.ProviderType == route.ProviderType &&
				existing.ProviderBaseUrl == route.ProviderBaseURL &&
				existing.Model == route.Model {
				var existingVerified *time.Time
				if existing.LimitsVerifiedAt.Valid {
					ts := existing.LimitsVerifiedAt.Time.UTC()
					existingVerified = &ts
				}
				providerLimits = &modelclient.ModelLimits{
					ContextWindowTokens: int(existing.ContextWindowTokens.Int32),
					MaxInputTokens:      int(existing.MaxInputTokens.Int32),
					MaxOutputTokens:     int(existing.MaxOutputTokens.Int32),
					Source:              modelclient.LimitsSourceProvider,
					VerifiedAt:          existingVerified,
				}
			}
		}
		resolvedLimits := modelclient.ResolveModelLimits(
			userOverride,
			providerLimits,
			route.Model,
			route.BaseModel,
			now,
		)
		var verifiedAt pgtype.Timestamptz
		if resolvedLimits.VerifiedAt != nil && !resolvedLimits.VerifiedAt.IsZero() {
			verifiedAt = pgtype.Timestamptz{Time: resolvedLimits.VerifiedAt.UTC(), Valid: true}
		}
		stored, err := q.UpsertWorkspaceModelRoute(ctx, db.UpsertWorkspaceModelRouteParams{
			ID:                  route.ID,
			WorkspaceID:         workspaceID,
			Position:            int32(position),
			Enabled:             route.Enabled,
			ProviderType:        route.ProviderType,
			CredentialID:        credentialID,
			ApiCredentialID:     apiCredentialID,
			Model:               route.Model,
			BaseModel:           route.BaseModel,
			ContextWindowTokens: pgtype.Int4{Int32: int32(resolvedLimits.ContextWindowTokens), Valid: resolvedLimits.ContextWindowTokens > 0},
			MaxInputTokens:      pgtype.Int4{Int32: int32(resolvedLimits.MaxInputTokens), Valid: resolvedLimits.MaxInputTokens > 0},
			MaxOutputTokens:     pgtype.Int4{Int32: int32(resolvedLimits.MaxOutputTokens), Valid: resolvedLimits.MaxOutputTokens > 0},
			LimitsSource:        resolvedLimits.Source,
			LimitsVerifiedAt:    verifiedAt,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return AgentSettings{}, fmt.Errorf("%w: model route id belongs to another workspace", domain.ErrConflict)
		}
		if err != nil {
			return AgentSettings{}, mapConstraintError(err)
		}
		validRouteIDs[stored.ID] = true
	}

	toNullUUID := func(u *uuid.UUID) (uuid.NullUUID, error) {
		if u == nil || *u == uuid.Nil {
			return uuid.NullUUID{}, nil
		}
		if !validRouteIDs[*u] {
			return uuid.NullUUID{}, fmt.Errorf("%w: default model route does not exist in workspace routes", domain.ErrConflict)
		}
		return uuid.NullUUID{UUID: *u, Valid: true}, nil
	}

	defaultRouteID, err := toNullUUID(input.DefaultModelRouteID)
	if err != nil {
		return AgentSettings{}, err
	}
	discussionRouteID, err := toNullUUID(input.DiscussionModelRouteID)
	if err != nil {
		return AgentSettings{}, err
	}
	planRouteID, err := toNullUUID(input.PlanModelRouteID)
	if err != nil {
		return AgentSettings{}, err
	}
	settings, err := q.UpsertWorkspaceAgentSettings(ctx, db.UpsertWorkspaceAgentSettingsParams{
		WorkspaceID:            workspaceID,
		SourceID:               sourceID,
		SystemInstructions:     input.SystemInstructions,
		DefaultModelRouteID:    defaultRouteID,
		DiscussionModelRouteID: discussionRouteID,
		PlanModelRouteID:       planRouteID,
	})
	if err != nil {
		return AgentSettings{}, mapConstraintError(err)
	}
	storedRoutes, err := q.ListWorkspaceModelRoutes(ctx, workspaceID)
	if err != nil {
		return AgentSettings{}, err
	}
	storedCreds, err := q.ListWorkspaceAPICredentials(ctx, workspaceID)
	if err != nil {
		return AgentSettings{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AgentSettings{}, err
	}
	return AgentSettings{
		WorkspaceAgentSetting: settings,
		Routes:                storedRoutes,
		APICredentials:        storedCreds,
	}, nil
}

// GetToolGuardSettings 读取工作区全局工具安全守卫配置；未初始化记录时返回安全默认值。
func (s *Store) GetToolGuardSettings(ctx context.Context, workspaceID uuid.UUID) (ToolGuardSettings, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM workspaces WHERE id=$1)", workspaceID).Scan(&exists); err != nil {
		return ToolGuardSettings{}, err
	}
	if !exists {
		return ToolGuardSettings{}, domain.ErrNotFound
	}

	settings, err := s.queries.GetWorkspaceAgentSettings(ctx, workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ToolGuardSettings{
			Mode:           "block_high_risk",
			TimeoutSeconds: 10,
		}, nil
	}
	if err != nil {
		return ToolGuardSettings{}, err
	}
	return toolGuardSettingsFromRow(settings), nil
}

func toolGuardSettingsFromRow(row db.WorkspaceAgentSetting) ToolGuardSettings {
	mode := strings.TrimSpace(row.ToolGuardMode)
	if mode != "strict" {
		mode = "block_high_risk"
	}
	timeout := row.ToolGuardTimeoutSeconds
	if timeout < 2 || timeout > 60 {
		timeout = 10
	}
	var routeID *uuid.UUID
	if row.ToolGuardModelRouteID.Valid {
		id := row.ToolGuardModelRouteID.UUID
		routeID = &id
	}
	var updatedAt *time.Time
	if row.UpdatedAt.Valid {
		ts := row.UpdatedAt.Time.UTC()
		updatedAt = &ts
	}
	return ToolGuardSettings{
		Mode:           mode,
		ModelRouteID:   routeID,
		TimeoutSeconds: timeout,
		CustomRules:    row.ToolGuardCustomRules,
		UpdatedAt:      updatedAt,
	}
}

// SaveToolGuardSettings 独立保存工作区全局工具安全守卫配置，不会覆盖模型路由、默认模型或全局指令。
func (s *Store) SaveToolGuardSettings(
	ctx context.Context,
	workspaceID uuid.UUID,
	input ToolGuardSettingsInput,
) (ToolGuardSettings, error) {
	mode := strings.TrimSpace(input.Mode)
	if mode == "" {
		mode = "block_high_risk"
	}
	switch mode {
	case "block_high_risk", "strict":
	default:
		return ToolGuardSettings{}, fmt.Errorf("%w: invalid tool_guard_mode %q", domain.ErrConflict, mode)
	}

	timeout := input.TimeoutSeconds
	if timeout == 0 {
		timeout = 10
	}
	if timeout < 2 || timeout > 60 {
		return ToolGuardSettings{}, fmt.Errorf("%w: tool_guard_timeout_seconds must be between 2 and 60", domain.ErrConflict)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ToolGuardSettings{}, err
	}
	defer tx.Rollback(ctx)

	var lockedWorkspace uuid.UUID
	if err := tx.QueryRow(ctx, "SELECT id FROM workspaces WHERE id=$1 FOR UPDATE", workspaceID).Scan(&lockedWorkspace); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ToolGuardSettings{}, domain.ErrNotFound
		}
		return ToolGuardSettings{}, err
	}

	q := db.New(tx)
	var sourceID uuid.UUID
	existing, err := q.GetWorkspaceAgentSettings(ctx, workspaceID)
	switch {
	case err == nil:
		sourceID = existing.SourceID
	case errors.Is(err, pgx.ErrNoRows):
		// 如果工作区尚未创建过 Cycle Agent Source，则一并创建，确保外键完整。
		var existingSourceID uuid.UUID
		findErr := tx.QueryRow(ctx,
			"SELECT id FROM sources WHERE workspace_id=$1 AND kind=$2 ORDER BY created_at ASC LIMIT 1",
			workspaceID, string(domain.SourceKindCycleAgent),
		).Scan(&existingSourceID)
		switch {
		case findErr == nil:
			sourceID = existingSourceID
		case errors.Is(findErr, pgx.ErrNoRows):
			source, createErr := q.CreateSource(ctx, db.CreateSourceParams{
				ID: uuid.New(), WorkspaceID: workspaceID,
				Kind: string(domain.SourceKindCycleAgent), Name: "Cycle Agent",
			})
			if createErr != nil {
				return ToolGuardSettings{}, createErr
			}
			sourceID = source.ID
		default:
			return ToolGuardSettings{}, findErr
		}
	default:
		return ToolGuardSettings{}, err
	}

	var routeID uuid.NullUUID
	if input.ModelRouteID != nil && *input.ModelRouteID != uuid.Nil {
		var routeEnabled bool
		routeErr := tx.QueryRow(ctx,
			"SELECT enabled FROM workspace_model_routes WHERE workspace_id=$1 AND id=$2",
			workspaceID, *input.ModelRouteID,
		).Scan(&routeEnabled)
		if errors.Is(routeErr, pgx.ErrNoRows) {
			return ToolGuardSettings{}, fmt.Errorf("%w: tool guard model route does not exist in workspace routes", domain.ErrConflict)
		}
		if routeErr != nil {
			return ToolGuardSettings{}, routeErr
		}
		if !routeEnabled {
			return ToolGuardSettings{}, fmt.Errorf("%w: tool guard model route is disabled", domain.ErrConflict)
		}
		routeID = uuid.NullUUID{UUID: *input.ModelRouteID, Valid: true}
	}

	saved, err := q.UpsertWorkspaceToolGuardSettings(ctx, db.UpsertWorkspaceToolGuardSettingsParams{
		WorkspaceID:             workspaceID,
		SourceID:                sourceID,
		ToolGuardMode:           mode,
		ToolGuardModelRouteID:   routeID,
		ToolGuardTimeoutSeconds: timeout,
		ToolGuardCustomRules:    strings.TrimSpace(input.CustomRules),
	})
	if err != nil {
		return ToolGuardSettings{}, mapConstraintError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ToolGuardSettings{}, err
	}
	return toolGuardSettingsFromRow(saved), nil
}

type MCPConnectionInput struct {
	ID                      uuid.UUID
	Name                    string
	URL                     string
	BearerToken             string
	ClearBearerToken        bool
	Enabled                 bool
	TimeoutMS               int32
	ToolGuardEnabled        bool
}

func (s *Store) ListMCPConnections(ctx context.Context, workspaceID uuid.UUID) ([]db.WorkspaceMcpConnection, error) {
	return s.queries.ListWorkspaceMCPConnections(ctx, workspaceID)
}

func (s *Store) ListEnabledMCPConnections(ctx context.Context, workspaceID uuid.UUID) ([]db.WorkspaceMcpConnection, error) {
	return s.queries.ListEnabledWorkspaceMCPConnections(ctx, workspaceID)
}

func (s *Store) GetMCPConnection(ctx context.Context, workspaceID, id uuid.UUID) (db.WorkspaceMcpConnection, error) {
	connection, err := s.queries.GetWorkspaceMCPConnection(ctx, db.GetWorkspaceMCPConnectionParams{
		WorkspaceID: workspaceID, ID: id,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.WorkspaceMcpConnection{}, domain.ErrNotFound
	}
	return connection, err
}

func (s *Store) SaveMCPConnection(
	ctx context.Context,
	workspaceID uuid.UUID,
	input MCPConnectionInput,
) (db.WorkspaceMcpConnection, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.URL = strings.TrimSpace(input.URL)
	input.BearerToken = strings.TrimSpace(input.BearerToken)
	isCreate := input.ID == uuid.Nil
	if isCreate {
		input.ID = uuid.New()
	}
	if input.Name == "" || len(input.Name) > 80 {
		return db.WorkspaceMcpConnection{}, fmt.Errorf("%w: MCP connection name must contain 1-80 characters", domain.ErrConflict)
	}
	if err := agentconfig.ValidateEndpoint(input.URL); err != nil {
		return db.WorkspaceMcpConnection{}, fmt.Errorf("%w: %v", domain.ErrConflict, err)
	}
	if len(input.BearerToken) > 8192 || strings.ContainsAny(input.BearerToken, "\r\n") {
		return db.WorkspaceMcpConnection{}, fmt.Errorf("%w: invalid MCP bearer token", domain.ErrConflict)
	}
	if input.BearerToken != "" && input.ClearBearerToken {
		return db.WorkspaceMcpConnection{}, fmt.Errorf("%w: cannot set and clear MCP bearer token together", domain.ErrConflict)
	}
	if input.TimeoutMS == 0 {
		input.TimeoutMS = 30000
	}
	if input.TimeoutMS < 1000 || input.TimeoutMS > 300000 {
		return db.WorkspaceMcpConnection{}, fmt.Errorf("%w: timeout_ms must be between 1000 and 300000", domain.ErrConflict)
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM workspaces WHERE id=$1)", workspaceID).Scan(&exists); err != nil {
		return db.WorkspaceMcpConnection{}, err
	}
	if !exists {
		return db.WorkspaceMcpConnection{}, domain.ErrNotFound
	}
	if !isCreate {
		existing, err := s.queries.GetWorkspaceMCPConnection(ctx, db.GetWorkspaceMCPConnectionParams{
			WorkspaceID: workspaceID, ID: input.ID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return db.WorkspaceMcpConnection{}, domain.ErrNotFound
		} else if err != nil {
			return db.WorkspaceMcpConnection{}, err
		}
		// 不允许编辑 URL 时悄悄将原服务的密钥带到另一个服务。
		if existing.Url != input.URL && len(existing.BearerTokenCiphertext) != 0 && input.BearerToken == "" && !input.ClearBearerToken {
			return db.WorkspaceMcpConnection{}, fmt.Errorf("%w: changing MCP URL requires replacing or clearing its bearer token", domain.ErrConflict)
		}
	}
	var ciphertext []byte
	if input.BearerToken != "" {
		var err error
		ciphertext, err = s.encryptMCPBearerToken(workspaceID.String(), input.ID.String(), input.BearerToken)
		if err != nil {
			return db.WorkspaceMcpConnection{}, err
		}
	}

	connection, err := s.queries.UpsertWorkspaceMCPConnection(ctx, db.UpsertWorkspaceMCPConnectionParams{
		ID: input.ID, WorkspaceID: workspaceID, Name: input.Name, Url: input.URL,
		BearerTokenCiphertext: ciphertext, ReplaceBearerToken: input.BearerToken != "" || input.ClearBearerToken,
		Enabled: input.Enabled, TimeoutMs: input.TimeoutMS,
		ToolGuardEnabled: input.ToolGuardEnabled,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.WorkspaceMcpConnection{}, fmt.Errorf("%w: changing MCP URL requires replacing or clearing its bearer token", domain.ErrConflict)
	}
	if err != nil {
		return db.WorkspaceMcpConnection{}, mapConstraintError(err)
	}
	return connection, nil
}

func (s *Store) DeleteMCPConnection(ctx context.Context, workspaceID, connectionID uuid.UUID) error {
	affected, err := s.queries.DeleteWorkspaceMCPConnection(ctx, db.DeleteWorkspaceMCPConnectionParams{
		WorkspaceID: workspaceID, ID: connectionID,
	})
	if err != nil {
		return err
	}
	if affected != 1 {
		return domain.ErrNotFound
	}
	return nil
}
