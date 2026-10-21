package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store/db"
)

const (
	defaultWorkspaceName = "Personal Workspace"
	defaultAdminName     = "Administrator"
)

var (
	ErrUnauthorized       = errors.New("unauthorized")
	ErrLoginDisabled      = errors.New("self-hosted login is disabled")
	ErrInvalidConfig      = errors.New("invalid auth configuration")
	ErrMultipleWorkspaces = errors.New("self-hosted login requires a single workspace")
	ErrInvalidHumanSource = errors.New("session source is not human")
	ErrWorkspaceAccess     = errors.New("workspace access denied")
)

type Config struct {
	AdminPassword string
	SessionTTL    time.Duration
	OIDC          OIDCConfig
}

type Identity struct {
	HumanAccountID uuid.UUID
	WorkspaceID    uuid.UUID
	SourceID       uuid.UUID
	DisplayName    string
	Email          string
	WorkspaceName  string
	SourceName     string
	Role           string
}

type Session struct {
	Token     string
	ExpiresAt time.Time
	Identity  Identity
}

type WorkspaceMembership struct {
	ID         uuid.UUID
	Name       string
	Role       string
	IsPersonal bool
}

type Service struct {
	pool          *pgxpool.Pool
	queries       *db.Queries
	adminPassword []byte
	sessionTTL    time.Duration
	oidc          oidcRuntime
}

func New(pool *pgxpool.Pool, config Config) (*Service, error) {
	if pool == nil {
		return nil, fmt.Errorf("%w: database pool is required", ErrInvalidConfig)
	}
	if config.AdminPassword != "" && len(config.AdminPassword) < 12 {
		return nil, fmt.Errorf("%w: CYCLEDOCK_ADMIN_PASSWORD must be at least 12 characters", ErrInvalidConfig)
	}
	if config.SessionTTL <= 0 {
		config.SessionTTL = 7 * 24 * time.Hour
	}

	service := &Service{
		pool:          pool,
		queries:       db.New(pool),
		adminPassword: []byte(config.AdminPassword),
		sessionTTL:    config.SessionTTL,
	}
	if err := service.configureOIDC(config.OIDC); err != nil {
		return nil, err
	}
	return service, nil
}

func (s *Service) SelfHostedLoginEnabled() bool {
	return len(s.adminPassword) > 0 && !s.oidc.enabled()
}

func (s *Service) EnsureSelfHostedIdentity(ctx context.Context) (Identity, error) {
	if !s.SelfHostedLoginEnabled() {
		return Identity{}, ErrLoginDisabled
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Identity{}, fmt.Errorf("begin self-hosted identity bootstrap: %w", err)
	}
	defer tx.Rollback(ctx)

	// Self-hosted fallback 只有一个管理员身份。事务锁让多进程启动或并发首登
	// 不会各自创建一套 Workspace / Human Source。
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext('cycledock/local-admin-bootstrap'))"); err != nil {
		return Identity{}, fmt.Errorf("lock self-hosted identity bootstrap: %w", err)
	}

	q := db.New(tx)
	row, err := q.GetLocalAdminIdentity(ctx)
	if err == nil {
		identity, err := identityFromLocalRow(row)
		if err != nil {
			return Identity{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return Identity{}, fmt.Errorf("commit existing self-hosted identity: %w", err)
		}
		return identity, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, fmt.Errorf("load self-hosted identity: %w", err)
	}

	count, err := q.CountWorkspaces(ctx)
	if err != nil {
		return Identity{}, fmt.Errorf("count self-hosted workspaces: %w", err)
	}

	var workspace db.Workspace
	switch count {
	case 0:
		workspace, err = q.CreateWorkspace(ctx, db.CreateWorkspaceParams{
			ID:   uuid.New(),
			Name: defaultWorkspaceName,
		})
	case 1:
		workspace, err = q.GetFirstWorkspace(ctx)
	default:
		return Identity{}, ErrMultipleWorkspaces
	}
	if err != nil {
		return Identity{}, fmt.Errorf("resolve self-hosted workspace: %w", err)
	}

	account, err := q.CreateHumanAccount(ctx, db.CreateHumanAccountParams{
		ID:           uuid.New(),
		DisplayName:  defaultAdminName,
		IsLocalAdmin: true,
	})
	if err != nil {
		return Identity{}, fmt.Errorf("create self-hosted human account: %w", err)
	}
	source, err := q.CreateSource(ctx, db.CreateSourceParams{
		ID:          uuid.New(),
		WorkspaceID: workspace.ID,
		Kind:        string(domain.SourceKindHuman),
		Name:        defaultAdminName,
	})
	if err != nil {
		return Identity{}, fmt.Errorf("create self-hosted human source: %w", err)
	}
	if err := q.CreateWorkspaceMembership(ctx, db.CreateWorkspaceMembershipParams{
		WorkspaceID:    workspace.ID,
		HumanAccountID: account.ID,
		SourceID:       source.ID,
		Role:           "owner",
		IsPersonal:     true,
	}); err != nil {
		return Identity{}, fmt.Errorf("create self-hosted membership: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Identity{}, fmt.Errorf("commit self-hosted identity bootstrap: %w", err)
	}
	return Identity{
		HumanAccountID: account.ID,
		WorkspaceID:    workspace.ID,
		SourceID:       source.ID,
		DisplayName:    account.DisplayName,
		WorkspaceName:  workspace.Name,
		SourceName:     source.Name,
		Role:           "owner",
	}, nil
}

func (s *Service) ensureOIDCPersonalIdentity(
	ctx context.Context,
	issuer, subject, displayName, email string,
) (Identity, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Identity{}, fmt.Errorf("begin OIDC identity bootstrap: %w", err)
	}
	defer tx.Rollback(ctx)

	// issuer + sub 是 Hosted 唯一稳定身份。只串行同一身份的首登，不阻塞其他用户。
	lockKey := "cycledock/oidc/" + issuer + "|" + subject
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
		return Identity{}, fmt.Errorf("lock OIDC identity bootstrap: %w", err)
	}

	q := db.New(tx)
	row, err := q.GetOIDCPersonalIdentity(ctx, db.GetOIDCPersonalIdentityParams{
		Issuer: issuer, Subject: subject,
	})
	if err == nil {
		if row.SourceKind != string(domain.SourceKindHuman) {
			return Identity{}, ErrInvalidHumanSource
		}
		if err := q.UpdateHumanAccountSnapshot(ctx, db.UpdateHumanAccountSnapshotParams{
			DisplayName:   displayName,
			EmailSnapshot: nullableText(email),
			ID:            row.HumanAccountID,
		}); err != nil {
			return Identity{}, fmt.Errorf("refresh OIDC account snapshot: %w", err)
		}
		if err := q.UpdateHumanSourceName(ctx, db.UpdateHumanSourceNameParams{
			Name: displayName, WorkspaceID: row.WorkspaceID, ID: row.SourceID,
		}); err != nil {
			return Identity{}, fmt.Errorf("refresh OIDC human source name: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return Identity{}, fmt.Errorf("commit existing OIDC identity: %w", err)
		}
		return identityFromOIDCRow(row, displayName, email), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, fmt.Errorf("load OIDC identity: %w", err)
	}

	account, err := q.CreateHumanAccount(ctx, db.CreateHumanAccountParams{
		ID:            uuid.New(),
		DisplayName:   displayName,
		EmailSnapshot: nullableText(email),
		IsLocalAdmin:  false,
	})
	if err != nil {
		return Identity{}, fmt.Errorf("create OIDC human account: %w", err)
	}
	workspace, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		ID: uuid.New(), Name: defaultWorkspaceName,
	})
	if err != nil {
		return Identity{}, fmt.Errorf("create OIDC personal workspace: %w", err)
	}
	source, err := q.CreateSource(ctx, db.CreateSourceParams{
		ID:          uuid.New(),
		WorkspaceID: workspace.ID,
		Kind:        string(domain.SourceKindHuman),
		Name:        displayName,
	})
	if err != nil {
		return Identity{}, fmt.Errorf("create OIDC human source: %w", err)
	}
	if err := q.CreateOIDCIdentity(ctx, db.CreateOIDCIdentityParams{
		Issuer: issuer, Subject: subject, HumanAccountID: account.ID,
	}); err != nil {
		return Identity{}, fmt.Errorf("create OIDC identity: %w", err)
	}
	if err := q.CreateWorkspaceMembership(ctx, db.CreateWorkspaceMembershipParams{
		WorkspaceID:    workspace.ID,
		HumanAccountID: account.ID,
		SourceID:       source.ID,
		Role:           "owner",
		IsPersonal:     true,
	}); err != nil {
		return Identity{}, fmt.Errorf("create OIDC personal membership: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Identity{}, fmt.Errorf("commit OIDC identity bootstrap: %w", err)
	}
	return Identity{
		HumanAccountID: account.ID,
		WorkspaceID:    workspace.ID,
		SourceID:       source.ID,
		DisplayName:    displayName,
		Email:          email,
		WorkspaceName:  workspace.Name,
		SourceName:     source.Name,
		Role:           "owner",
	}, nil
}

func (s *Service) Login(ctx context.Context, password string) (Session, error) {
	if !s.SelfHostedLoginEnabled() {
		return Session{}, ErrLoginDisabled
	}
	expected := sha256.Sum256(s.adminPassword)
	provided := sha256.Sum256([]byte(password))
	if subtle.ConstantTimeCompare(expected[:], provided[:]) != 1 {
		return Session{}, ErrUnauthorized
	}
	identity, err := s.EnsureSelfHostedIdentity(ctx)
	if err != nil {
		return Session{}, err
	}
	return s.createSession(ctx, identity)
}

func (s *Service) createSession(ctx context.Context, identity Identity) (Session, error) {
	if err := s.queries.DeleteExpiredWebSessions(ctx); err != nil {
		return Session{}, fmt.Errorf("delete expired web sessions: %w", err)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Session{}, fmt.Errorf("generate web session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(token))
	expiresAt := time.Now().UTC().Add(s.sessionTTL)
	if err := s.queries.CreateWebSession(ctx, db.CreateWebSessionParams{
		ID:             uuid.New(),
		HumanAccountID: identity.HumanAccountID,
		WorkspaceID:    identity.WorkspaceID,
		SourceID:       identity.SourceID,
		TokenDigest:    digest[:],
		ExpiresAt:      pgtype.Timestamptz{Time: expiresAt, Valid: true},
	}); err != nil {
		return Session{}, fmt.Errorf("create web session: %w", err)
	}
	return Session{Token: token, ExpiresAt: expiresAt, Identity: identity}, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (Identity, error) {
	if token == "" {
		return Identity{}, ErrUnauthorized
	}
	digest := sha256.Sum256([]byte(token))
	row, err := s.queries.GetWebSessionIdentity(ctx, digest[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, ErrUnauthorized
	}
	if err != nil {
		return Identity{}, fmt.Errorf("load web session: %w", err)
	}
	if row.SourceKind != string(domain.SourceKindHuman) {
		return Identity{}, ErrInvalidHumanSource
	}
	return Identity{
		HumanAccountID: row.HumanAccountID,
		WorkspaceID:    row.WorkspaceID,
		SourceID:       row.SourceID,
		DisplayName:    row.DisplayName,
		Email:          textValue(row.EmailSnapshot),
		WorkspaceName:  row.WorkspaceName,
		SourceName:     row.SourceName,
		Role:           row.Role,
	}, nil
}

func (s *Service) ListWorkspaces(ctx context.Context, humanAccountID uuid.UUID) ([]WorkspaceMembership, error) {
	rows, err := s.queries.ListHumanWorkspaces(ctx, humanAccountID)
	if err != nil {
		return nil, fmt.Errorf("list human workspaces: %w", err)
	}
	items := make([]WorkspaceMembership, 0, len(rows))
	for _, row := range rows {
		items = append(items, WorkspaceMembership{
			ID: row.WorkspaceID, Name: row.WorkspaceName, Role: row.Role, IsPersonal: row.IsPersonal,
		})
	}
	return items, nil
}

func (s *Service) SwitchWorkspace(ctx context.Context, token string, workspaceID uuid.UUID) (Identity, error) {
	if token == "" {
		return Identity{}, ErrUnauthorized
	}
	digest := sha256.Sum256([]byte(token))
	changed, err := s.queries.SwitchWebSessionWorkspace(ctx, db.SwitchWebSessionWorkspaceParams{
		TokenDigest: digest[:], WorkspaceID: workspaceID,
	})
	if err != nil {
		return Identity{}, fmt.Errorf("switch web session workspace: %w", err)
	}
	if changed != 1 {
		return Identity{}, ErrWorkspaceAccess
	}
	return s.Authenticate(ctx, token)
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	digest := sha256.Sum256([]byte(token))
	if err := s.queries.DeleteWebSession(ctx, digest[:]); err != nil {
		return fmt.Errorf("delete web session: %w", err)
	}
	return nil
}

func identityFromLocalRow(row db.GetLocalAdminIdentityRow) (Identity, error) {
	if row.SourceKind != string(domain.SourceKindHuman) {
		return Identity{}, ErrInvalidHumanSource
	}
	return Identity{
		HumanAccountID: row.HumanAccountID,
		WorkspaceID:    row.WorkspaceID,
		SourceID:       row.SourceID,
		DisplayName:    row.DisplayName,
		Email:          textValue(row.EmailSnapshot),
		WorkspaceName:  row.WorkspaceName,
		SourceName:     row.SourceName,
		Role:           row.Role,
	}, nil
}

func identityFromOIDCRow(row db.GetOIDCPersonalIdentityRow, displayName, email string) Identity {
	return Identity{
		HumanAccountID: row.HumanAccountID,
		WorkspaceID:    row.WorkspaceID,
		SourceID:       row.SourceID,
		DisplayName:    displayName,
		Email:          email,
		WorkspaceName:  row.WorkspaceName,
		SourceName:     displayName,
		Role:           row.Role,
	}
}

func nullableText(value string) pgtype.Text {
	if value == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: value, Valid: true}
}

func textValue(value pgtype.Text) string {
	if !value.Valid {
		return ""
	}
	return value.String
}
