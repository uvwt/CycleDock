package modelauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/uvwt/CycleDock/internal/modelclient"
)

type Provider string

const (
	ProviderChatGPT Provider = "chatgpt"
	ProviderGrok    Provider = "grok"
)

// xAI 的 subscription inference proxy 会做 Grok Build 协议版本门禁。
// 该值跟随本次实现验证过的官方 Grok Build 公开协议版本；升级代理协议时需要一起验证。
const grokProxyClientVersion = "1.0.50"

var (
	ErrUnavailable       = errors.New("model subscription authentication is unavailable")
	ErrNotConnected      = errors.New("model subscription account is not connected")
	ErrReauthRequired    = errors.New("model subscription account must be connected again")
	ErrCredentialRefresh = errors.New("model subscription credential refresh failed")
	ErrCredentialInUse   = errors.New("model subscription credential is used by a model route")
	ErrFlowNotFound      = errors.New("model authentication flow not found")
)

type Config struct {
	PublicURL       string
	SecretKey       string
	ChatGPTClientID string
	// 仅用于小规模 Hosted DEV 验证；正式远程部署应使用 OpenAI 批准的固定 Client ID。
	AllowHostedChatGPTDynamicRegistration bool
	HTTPClient                            *http.Client
}

type Status struct {
	Provider     Provider        `json:"provider"`
	Available    bool            `json:"available"`
	Connected    bool            `json:"connected"`
	Reason       string          `json:"reason,omitempty"`
	Warning      string          `json:"warning,omitempty"`
	DisplayLabel string          `json:"display_label,omitempty"`
	ExpiresAt    *time.Time      `json:"expires_at,omitempty"`
	Accounts     []AccountStatus `json:"accounts"`
}

type AccountStatus struct {
	ID           uuid.UUID `json:"id"`
	DisplayLabel string    `json:"display_label"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type RuntimeCredential struct {
	ID          uuid.UUID
	Provider    Provider
	AccessToken string
	ExpiresAt   time.Time
	UserID      string
	Email       string
}

type credentialPayload struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ClientID     string `json:"client_id"`
	TokenURL     string `json:"token_url"`
	Scope        string `json:"scope,omitempty"`
	UserID       string `json:"user_id,omitempty"`
	Email        string `json:"email,omitempty"`
}

type Service struct {
	pool                                  *pgxpool.Pool
	httpClient                            *http.Client
	vault                                 *vault
	publicURL                             string
	chatGPTClientID                       string
	allowHostedChatGPTDynamicRegistration bool
	hostID                                string
}

func New(pool *pgxpool.Pool, config Config) (*Service, error) {
	if pool == nil {
		return nil, errors.New("model auth database pool is required")
	}
	service := &Service{
		pool:                                  pool,
		httpClient:                            config.HTTPClient,
		publicURL:                             strings.TrimRight(strings.TrimSpace(config.PublicURL), "/"),
		chatGPTClientID:                       strings.TrimSpace(config.ChatGPTClientID),
		allowHostedChatGPTDynamicRegistration: config.AllowHostedChatGPTDynamicRegistration,
	}
	if service.httpClient == nil {
		service.httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	if strings.TrimSpace(config.SecretKey) == "" {
		return service, nil
	}

	vault, key, err := newVault(strings.TrimSpace(config.SecretKey))
	if err != nil {
		return nil, err
	}
	service.vault = vault
	digest := sha256.Sum256(append([]byte("cycledock-model-host:"), key...))
	service.hostID = "urn:cycledock:" + base64.RawURLEncoding.EncodeToString(digest[:])
	return service, nil
}

func (s *Service) Status(ctx context.Context, workspaceID uuid.UUID, provider Provider) (Status, error) {
	status := Status{Provider: provider, Accounts: []AccountStatus{}}
	switch provider {
	case ProviderChatGPT:
		switch {
		case s.vault == nil:
			status.Reason = "secret_key_required"
		case s.publicURL == "":
			status.Reason = "public_url_required"
		case s.chatGPTClientID == "" && !s.allowsDynamicChatGPTRegistration():
			status.Reason = "chatgpt_client_id_required"
		default:
			status.Available = true
			if s.chatGPTClientID == "" && !isLoopbackURL(s.publicURL) {
				status.Warning = "hosted_dynamic_registration_test_mode"
			}
		}
	case ProviderGrok:
		status.Available = s.vault != nil
		if !status.Available {
			status.Reason = "secret_key_required"
		}
	default:
		return Status{}, fmt.Errorf("unsupported model auth provider %q", provider)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, expires_at, display_label
		FROM model_oauth_credentials
		WHERE workspace_id=$1 AND provider=$2
		ORDER BY created_at, id
	`, workspaceID, string(provider))
	if err != nil {
		return Status{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var account AccountStatus
		if err := rows.Scan(&account.ID, &account.ExpiresAt, &account.DisplayLabel); err != nil {
			return Status{}, err
		}
		status.Accounts = append(status.Accounts, account)
	}
	if err := rows.Err(); err != nil {
		return Status{}, err
	}
	status.Connected = len(status.Accounts) > 0
	if status.Connected {
		status.DisplayLabel = status.Accounts[0].DisplayLabel
		expiresAt := status.Accounts[0].ExpiresAt
		status.ExpiresAt = &expiresAt
	}
	return status, nil
}

func (s *Service) Disconnect(ctx context.Context, workspaceID uuid.UUID, provider Provider, credentialID uuid.UUID) error {
	if provider != ProviderChatGPT && provider != ProviderGrok {
		return fmt.Errorf("unsupported model auth provider %q", provider)
	}
	if credentialID == uuid.Nil {
		return errors.New("model credential id is required")
	}
	result, err := s.pool.Exec(ctx, `
		DELETE FROM model_oauth_credentials
		WHERE workspace_id=$1 AND provider=$2 AND id=$3
	`, workspaceID, string(provider), credentialID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return ErrCredentialInUse
		}
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotConnected
	}
	return nil
}

func (s *Service) ModelConfig(
	ctx context.Context,
	workspaceID uuid.UUID,
	provider Provider,
	model string,
	timeout time.Duration,
) (modelclient.Config, error) {
	return s.modelConfig(ctx, workspaceID, provider, uuid.Nil, model, timeout)
}

func (s *Service) ModelConfigForCredential(
	ctx context.Context,
	workspaceID uuid.UUID,
	provider Provider,
	credentialID uuid.UUID,
	model string,
	timeout time.Duration,
) (modelclient.Config, error) {
	if credentialID == uuid.Nil {
		return modelclient.Config{}, ErrNotConnected
	}
	return s.modelConfig(ctx, workspaceID, provider, credentialID, model, timeout)
}

func (s *Service) modelConfig(
	ctx context.Context,
	workspaceID uuid.UUID,
	provider Provider,
	credentialID uuid.UUID,
	model string,
	timeout time.Duration,
) (modelclient.Config, error) {
	credential, err := s.credential(ctx, workspaceID, provider, credentialID, timeout+time.Minute)
	if err != nil {
		return modelclient.Config{}, err
	}
	config := modelclient.Config{
		Model: model, APIKey: credential.AccessToken, Timeout: timeout,
		Transport: modelclient.TransportResponses,
	}
	switch provider {
	case ProviderChatGPT:
		config.BaseURL = "https://api.openai.com/v1"
	case ProviderGrok:
		config.BaseURL = "https://cli-chat-proxy.grok.com/v1"
		config.Headers = grokProxyHeaders(model, credential.UserID, credential.Email)
	default:
		return modelclient.Config{}, fmt.Errorf("unsupported model auth provider %q", provider)
	}
	return config, nil
}

func grokProxyHeaders(model, userID, email string) map[string]string {
	headers := map[string]string{
		"X-XAI-Token-Auth":         "xai-grok-cli",
		"x-authenticateresponse":   "authenticate-response",
		"x-grok-client-version":    grokProxyClientVersion,
		"x-grok-client-identifier": "cycledock",
		"x-grok-client-mode":       "headless",
		"x-grok-model-override":    model,
	}
	if userID != "" {
		headers["x-userid"] = userID
	}
	if email != "" {
		headers["x-email"] = email
	}
	return headers
}

func (s *Service) credential(
	ctx context.Context,
	workspaceID uuid.UUID,
	provider Provider,
	credentialID uuid.UUID,
	minimumValidity time.Duration,
) (RuntimeCredential, error) {
	if s.vault == nil {
		return RuntimeCredential{}, ErrUnavailable
	}
	if provider != ProviderChatGPT && provider != ProviderGrok {
		return RuntimeCredential{}, fmt.Errorf("unsupported model auth provider %q", provider)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RuntimeCredential{}, err
	}
	defer tx.Rollback(ctx)

	var id uuid.UUID
	var ciphertext []byte
	var expiresAt time.Time
	if credentialID == uuid.Nil {
		err = tx.QueryRow(ctx, `
			SELECT id, credential_ciphertext, expires_at
			FROM model_oauth_credentials
			WHERE workspace_id=$1 AND provider=$2
			ORDER BY updated_at DESC, id
			LIMIT 1
			FOR UPDATE
		`, workspaceID, string(provider)).Scan(&id, &ciphertext, &expiresAt)
	} else {
		err = tx.QueryRow(ctx, `
			SELECT id, credential_ciphertext, expires_at
			FROM model_oauth_credentials
			WHERE workspace_id=$1 AND provider=$2 AND id=$3
			FOR UPDATE
		`, workspaceID, string(provider), credentialID).Scan(&id, &ciphertext, &expiresAt)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeCredential{}, ErrNotConnected
	}
	if err != nil {
		return RuntimeCredential{}, err
	}

	var payload credentialPayload
	if err := s.vault.open(ciphertext, credentialAAD(workspaceID, provider), &payload); err != nil {
		return RuntimeCredential{}, err
	}
	if payload.AccessToken == "" {
		return RuntimeCredential{}, ErrReauthRequired
	}

	if minimumValidity < 2*time.Minute {
		minimumValidity = 2 * time.Minute
	}
	if expiresAt.After(time.Now().Add(minimumValidity)) {
		if err := tx.Commit(ctx); err != nil {
			return RuntimeCredential{}, err
		}
		return RuntimeCredential{
			ID: id, Provider: provider, AccessToken: payload.AccessToken, ExpiresAt: expiresAt,
			UserID: payload.UserID, Email: payload.Email,
		}, nil
	}
	if payload.RefreshToken == "" {
		return RuntimeCredential{}, ErrReauthRequired
	}

	refreshed, refreshedExpiry, err := s.refreshCredential(ctx, provider, payload)
	if err != nil {
		return RuntimeCredential{}, fmt.Errorf("%w: %v", ErrCredentialRefresh, err)
	}
	encrypted, err := s.vault.seal(refreshed, credentialAAD(workspaceID, provider))
	if err != nil {
		return RuntimeCredential{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE model_oauth_credentials
		SET credential_ciphertext=$3, expires_at=$4, updated_at=now()
		WHERE workspace_id=$1 AND provider=$2 AND id=$5
	`, workspaceID, string(provider), encrypted, refreshedExpiry, id); err != nil {
		return RuntimeCredential{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RuntimeCredential{}, err
	}
	return RuntimeCredential{
		ID: id, Provider: provider, AccessToken: refreshed.AccessToken, ExpiresAt: refreshedExpiry,
		UserID: refreshed.UserID, Email: refreshed.Email,
	}, nil
}

func (s *Service) saveCredential(
	ctx context.Context,
	workspaceID uuid.UUID,
	provider Provider,
	payload credentialPayload,
	expiresAt time.Time,
	accountSubject string,
	displayLabel string,
) (uuid.UUID, error) {
	encrypted, err := s.vault.seal(payload, credentialAAD(workspaceID, provider))
	if err != nil {
		return uuid.Nil, err
	}
	accountSubject = strings.TrimSpace(accountSubject)
	displayLabel = strings.TrimSpace(displayLabel)
	if displayLabel == "" {
		displayLabel = "Connected"
	}
	id := uuid.New()
	if accountSubject == "" {
		err = s.pool.QueryRow(ctx, `
			INSERT INTO model_oauth_credentials (
				id, workspace_id, provider, account_subject,
				credential_ciphertext, expires_at, display_label
			)
			VALUES ($1, $2, $3, '', $4, $5, $6)
			RETURNING id
		`, id, workspaceID, string(provider), encrypted, expiresAt, displayLabel).Scan(&id)
		return id, err
	}
	err = s.pool.QueryRow(ctx, `
		INSERT INTO model_oauth_credentials (
			id, workspace_id, provider, account_subject,
			credential_ciphertext, expires_at, display_label
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (workspace_id, provider, account_subject)
		WHERE btrim(account_subject) <> ''
		DO UPDATE
		SET credential_ciphertext=EXCLUDED.credential_ciphertext,
		    expires_at=EXCLUDED.expires_at,
		    display_label=EXCLUDED.display_label,
		    updated_at=now()
		RETURNING id
	`, id, workspaceID, string(provider), accountSubject, encrypted, expiresAt, displayLabel).Scan(&id)
	return id, err
}

func credentialAAD(workspaceID uuid.UUID, provider Provider) string {
	return workspaceID.String() + "\x00" + string(provider) + "\x00credential"
}

func flowAAD(flowID, workspaceID uuid.UUID, provider Provider) string {
	return flowID.String() + "\x00" + workspaceID.String() + "\x00" + string(provider) + "\x00flow"
}

func isLoopbackURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func (s *Service) allowsDynamicChatGPTRegistration() bool {
	return isLoopbackURL(s.publicURL) || s.allowHostedChatGPTDynamicRegistration
}
