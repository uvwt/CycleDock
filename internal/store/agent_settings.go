package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/uvwt/CycleDock/internal/agentconfig"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/store/db"
)

const maxModelRoutes = 16

type ModelRouteInput struct {
	ID                  uuid.UUID
	Enabled             bool
	ProviderType        string
	CredentialID        *uuid.UUID
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
	DiscussionModelRouteID *uuid.UUID
	PlanModelRouteID       *uuid.UUID
}

type AgentSettings struct {
	db.WorkspaceAgentSetting
	Routes []db.WorkspaceModelRoute
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
	return AgentSettings{WorkspaceAgentSetting: settings, Routes: routes}, nil
}

func normalizeModelRoutes(routes []ModelRouteInput) ([]ModelRouteInput, error) {
	if len(routes) > maxModelRoutes {
		return nil, fmt.Errorf("%w: at most %d model routes are allowed", domain.ErrConflict, maxModelRoutes)
	}
	normalized := make([]ModelRouteInput, len(routes))
	seenIDs := make(map[uuid.UUID]struct{}, len(routes))
	for i, route := range routes {
		route.ProviderType = strings.TrimSpace(route.ProviderType)
		route.ProviderBaseURL = strings.TrimRight(strings.TrimSpace(route.ProviderBaseURL), "/")
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
			if route.ProviderBaseURL == "" {
				return nil, fmt.Errorf("%w: provider_base_url is required for OpenAI-compatible routes", domain.ErrConflict)
			}
			if err := agentconfig.ValidateEndpoint(route.ProviderBaseURL); err != nil {
				return nil, fmt.Errorf("%w: %v", domain.ErrConflict, err)
			}
			if len(route.APIKey) > 8192 || strings.ContainsAny(route.APIKey, "\r\n") {
				return nil, fmt.Errorf("%w: invalid API key", domain.ErrConflict)
			}
			if route.APIKeyEnv != "" && !agentconfig.ValidEnvName(route.APIKeyEnv) {
				return nil, fmt.Errorf("%w: api_key_env must be an environment variable name", domain.ErrConflict)
			}
		case "chatgpt", "grok":
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

	existingRoutes, err := q.ListWorkspaceModelRoutes(ctx, workspaceID)
	if err != nil {
		return AgentSettings{}, err
	}
	existingByID := make(map[uuid.UUID]db.WorkspaceModelRoute, len(existingRoutes))
	for _, r := range existingRoutes {
		existingByID[r.ID] = r
	}
	if err := q.DeleteWorkspaceModelRoutes(ctx, workspaceID); err != nil {
		return AgentSettings{}, err
	}
	storedRoutes := make([]db.WorkspaceModelRoute, 0, len(routes))
	for position, route := range routes {
		credentialID := uuid.NullUUID{}
		if route.CredentialID != nil {
			credentialID = uuid.NullUUID{UUID: *route.CredentialID, Valid: true}
		}
		var ciphertext []byte
		if route.APIKey != "" {
			var err error
			ciphertext, err = s.encryptModelAPIKey(workspaceID.String(), route.ID.String(), route.APIKey)
			if err != nil {
				return AgentSettings{}, err
			}
		} else if !route.ClearAPIKey {
			if existing, ok := existingByID[route.ID]; ok && len(existing.ApiKeyCiphertext) > 0 {
				ciphertext = existing.ApiKeyCiphertext
			}
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
		stored, err := q.CreateWorkspaceModelRoute(ctx, db.CreateWorkspaceModelRouteParams{
			ID: route.ID, WorkspaceID: workspaceID, Position: int32(position),
			Enabled: route.Enabled, ProviderType: route.ProviderType,
			CredentialID: credentialID, ProviderBaseUrl: route.ProviderBaseURL,
			Model: route.Model, BaseModel: route.BaseModel, ApiKeyEnv: route.APIKeyEnv,
			ApiKeyCiphertext:    ciphertext,
			ContextWindowTokens: pgtype.Int4{Int32: int32(resolvedLimits.ContextWindowTokens), Valid: resolvedLimits.ContextWindowTokens > 0},
			MaxInputTokens:      pgtype.Int4{Int32: int32(resolvedLimits.MaxInputTokens), Valid: resolvedLimits.MaxInputTokens > 0},
			MaxOutputTokens:     pgtype.Int4{Int32: int32(resolvedLimits.MaxOutputTokens), Valid: resolvedLimits.MaxOutputTokens > 0},
			LimitsSource:        resolvedLimits.Source,
			LimitsVerifiedAt:    verifiedAt,
		})
		if err != nil {
			return AgentSettings{}, mapConstraintError(err)
		}
		storedRoutes = append(storedRoutes, stored)
	}

	validRouteIDs := make(map[uuid.UUID]bool, len(storedRoutes))
	for _, r := range storedRoutes {
		validRouteIDs[r.ID] = true
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
	if err := tx.Commit(ctx); err != nil {
		return AgentSettings{}, err
	}
	return AgentSettings{WorkspaceAgentSetting: settings, Routes: storedRoutes}, nil
}

type MCPConnectionInput struct {
	ID               uuid.UUID
	Name             string
	URL              string
	BearerToken      string
	ClearBearerToken bool
	Enabled          bool
	TimeoutMS        int32
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
