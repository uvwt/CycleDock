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

type AgentSettingsInput struct {
	Enabled            bool
	ProviderBaseURL    string
	Model              string
	APIKeyEnv          string
	RequestTimeoutMS   int32
	MaxToolRounds      int32
	SystemInstructions string
}

func (s *Store) GetAgentSettings(ctx context.Context, workspaceID uuid.UUID) (db.WorkspaceAgentSetting, error) {
	settings, err := s.queries.GetWorkspaceAgentSettings(ctx, workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.WorkspaceAgentSetting{}, domain.ErrNotFound
	}
	return settings, err
}

func (s *Store) SaveAgentSettings(
	ctx context.Context,
	workspaceID uuid.UUID,
	input AgentSettingsInput,
) (db.WorkspaceAgentSetting, error) {
	input.ProviderBaseURL = strings.TrimRight(strings.TrimSpace(input.ProviderBaseURL), "/")
	input.Model = strings.TrimSpace(input.Model)
	input.APIKeyEnv = strings.TrimSpace(input.APIKeyEnv)
	input.SystemInstructions = strings.TrimSpace(input.SystemInstructions)
	if input.RequestTimeoutMS == 0 {
		input.RequestTimeoutMS = 120000
	}
	if input.MaxToolRounds == 0 {
		input.MaxToolRounds = 12
	}
	if input.Enabled && (input.ProviderBaseURL == "" || input.Model == "") {
		return db.WorkspaceAgentSetting{}, fmt.Errorf("%w: provider_base_url and model are required when built-in agent is enabled", domain.ErrConflict)
	}
	if input.ProviderBaseURL != "" {
		if err := agentconfig.ValidateEndpoint(input.ProviderBaseURL); err != nil {
			return db.WorkspaceAgentSetting{}, fmt.Errorf("%w: %v", domain.ErrConflict, err)
		}
	}
	if input.APIKeyEnv != "" && !agentconfig.ValidEnvName(input.APIKeyEnv) {
		return db.WorkspaceAgentSetting{}, fmt.Errorf("%w: api_key_env must be an environment variable name", domain.ErrConflict)
	}
	if input.RequestTimeoutMS < 1000 || input.RequestTimeoutMS > 600000 {
		return db.WorkspaceAgentSetting{}, fmt.Errorf("%w: request_timeout_ms must be between 1000 and 600000", domain.ErrConflict)
	}
	if input.MaxToolRounds < 1 || input.MaxToolRounds > 32 {
		return db.WorkspaceAgentSetting{}, fmt.Errorf("%w: max_tool_rounds must be between 1 and 32", domain.ErrConflict)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return db.WorkspaceAgentSetting{}, err
	}
	defer tx.Rollback(ctx)

	// Settings 与内置 Agent Source 是 1:1。锁住 Workspace 后创建，避免两个并发
	// 保存请求产生两条 cycle_agent Source。
	var lockedWorkspace uuid.UUID
	if err := tx.QueryRow(ctx, "SELECT id FROM workspaces WHERE id=$1 FOR UPDATE", workspaceID).Scan(&lockedWorkspace); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.WorkspaceAgentSetting{}, domain.ErrNotFound
		}
		return db.WorkspaceAgentSetting{}, err
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
			return db.WorkspaceAgentSetting{}, err
		}
		if kind != string(domain.SourceKindCycleAgent) {
			return db.WorkspaceAgentSetting{}, fmt.Errorf("%w: configured agent source is not cycle_agent", domain.ErrConflict)
		}
	case errors.Is(err, pgx.ErrNoRows):
		source, err := q.CreateSource(ctx, db.CreateSourceParams{
			ID: uuid.New(), WorkspaceID: workspaceID,
			Kind: string(domain.SourceKindCycleAgent), Name: "Cycle Agent",
		})
		if err != nil {
			return db.WorkspaceAgentSetting{}, err
		}
		sourceID = source.ID
	default:
		return db.WorkspaceAgentSetting{}, err
	}

	settings, err := q.UpsertWorkspaceAgentSettings(ctx, db.UpsertWorkspaceAgentSettingsParams{
		WorkspaceID: workspaceID, SourceID: sourceID, Enabled: input.Enabled,
		ProviderBaseUrl: input.ProviderBaseURL, Model: input.Model, ApiKeyEnv: input.APIKeyEnv,
		RequestTimeoutMs: input.RequestTimeoutMS, MaxToolRounds: input.MaxToolRounds,
		SystemInstructions: input.SystemInstructions,
	})
	if err != nil {
		return db.WorkspaceAgentSetting{}, mapConstraintError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return db.WorkspaceAgentSetting{}, err
	}
	return settings, nil
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
