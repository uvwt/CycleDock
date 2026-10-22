package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/uvwt/CycleDock/internal/agentconfig"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store/db"
)

const maxModelRoutes = 16

type ModelRouteInput struct {
	ID              uuid.UUID
	Enabled         bool
	ProviderType    string
	CredentialID    *uuid.UUID
	ProviderBaseURL string
	Model           string
	APIKeyEnv       string
}

type AgentSettingsInput struct {
	Enabled            bool
	RequestTimeoutMS   int32
	MaxToolRounds      int32
	SystemInstructions string
	Routes             []ModelRouteInput
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
			if route.APIKeyEnv != "" && !agentconfig.ValidEnvName(route.APIKeyEnv) {
				return nil, fmt.Errorf("%w: api_key_env must be an environment variable name", domain.ErrConflict)
			}
		case "chatgpt", "grok":
			route.ProviderBaseURL = ""
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
	if input.RequestTimeoutMS == 0 {
		input.RequestTimeoutMS = 120000
	}
	if input.MaxToolRounds == 0 {
		input.MaxToolRounds = 12
	}
	if input.RequestTimeoutMS < 1000 || input.RequestTimeoutMS > 600000 {
		return AgentSettings{}, fmt.Errorf("%w: request_timeout_ms must be between 1000 and 600000", domain.ErrConflict)
	}
	if input.MaxToolRounds < 1 || input.MaxToolRounds > 32 {
		return AgentSettings{}, fmt.Errorf("%w: max_tool_rounds must be between 1 and 32", domain.ErrConflict)
	}
	routes, err := normalizeModelRoutes(input.Routes)
	if err != nil {
		return AgentSettings{}, err
	}
	if input.Enabled {
		hasEnabled := false
		for _, route := range routes {
			if route.Enabled {
				hasEnabled = true
				break
			}
		}
		if !hasEnabled {
			return AgentSettings{}, fmt.Errorf("%w: at least one enabled model route is required", domain.ErrConflict)
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AgentSettings{}, err
	}
	defer tx.Rollback(ctx)

	// Settings 与内置 Agent Source 是 1:1。锁住 Workspace 后创建，避免两个并发
	// 保存请求产生两条 cycle_agent Source。
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

	// Subscription route 必须引用同一 Workspace、同一 Provider 的账号。
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

	settings, err := q.UpsertWorkspaceAgentSettings(ctx, db.UpsertWorkspaceAgentSettingsParams{
		WorkspaceID: workspaceID, SourceID: sourceID, Enabled: input.Enabled,
		RequestTimeoutMs: input.RequestTimeoutMS, MaxToolRounds: input.MaxToolRounds,
		SystemInstructions: input.SystemInstructions,
	})
	if err != nil {
		return AgentSettings{}, mapConstraintError(err)
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
		stored, err := q.CreateWorkspaceModelRoute(ctx, db.CreateWorkspaceModelRouteParams{
			ID: route.ID, WorkspaceID: workspaceID, Position: int32(position),
			Enabled: route.Enabled, ProviderType: route.ProviderType,
			CredentialID: credentialID, ProviderBaseUrl: route.ProviderBaseURL,
			Model: route.Model, ApiKeyEnv: route.APIKeyEnv,
		})
		if err != nil {
			return AgentSettings{}, mapConstraintError(err)
		}
		storedRoutes = append(storedRoutes, stored)
	}
	if err := tx.Commit(ctx); err != nil {
		return AgentSettings{}, err
	}
	return AgentSettings{WorkspaceAgentSetting: settings, Routes: storedRoutes}, nil
}

type MCPConnectionInput struct {
	ID             uuid.UUID
	Name           string
	URL            string
	BearerTokenEnv string
	Enabled        bool
	TimeoutMS      int32
}

func (s *Store) ListMCPConnections(ctx context.Context, workspaceID uuid.UUID) ([]db.WorkspaceMcpConnection, error) {
	return s.queries.ListWorkspaceMCPConnections(ctx, workspaceID)
}

func (s *Store) ListEnabledMCPConnections(ctx context.Context, workspaceID uuid.UUID) ([]db.WorkspaceMcpConnection, error) {
	return s.queries.ListEnabledWorkspaceMCPConnections(ctx, workspaceID)
}

func (s *Store) SaveMCPConnection(
	ctx context.Context,
	workspaceID uuid.UUID,
	input MCPConnectionInput,
) (db.WorkspaceMcpConnection, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.URL = strings.TrimSpace(input.URL)
	input.BearerTokenEnv = strings.TrimSpace(input.BearerTokenEnv)
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
	if input.BearerTokenEnv != "" && !agentconfig.ValidEnvName(input.BearerTokenEnv) {
		return db.WorkspaceMcpConnection{}, fmt.Errorf("%w: bearer_token_env must be an environment variable name", domain.ErrConflict)
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
		if _, err := s.queries.GetWorkspaceMCPConnection(ctx, db.GetWorkspaceMCPConnectionParams{
			WorkspaceID: workspaceID, ID: input.ID,
		}); errors.Is(err, pgx.ErrNoRows) {
			return db.WorkspaceMcpConnection{}, domain.ErrNotFound
		} else if err != nil {
			return db.WorkspaceMcpConnection{}, err
		}
	}
	connection, err := s.queries.UpsertWorkspaceMCPConnection(ctx, db.UpsertWorkspaceMCPConnectionParams{
		ID: input.ID, WorkspaceID: workspaceID, Name: input.Name, Url: input.URL,
		BearerTokenEnv: input.BearerTokenEnv, Enabled: input.Enabled, TimeoutMs: input.TimeoutMS,
	})
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
