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
	ErrUnavailable    = errors.New("model subscription authentication is unavailable")
	ErrNotConnected   = errors.New("model subscription account is not connected")
	ErrReauthRequired = errors.New("model subscription account must be connected again")
	ErrFlowNotFound   = errors.New("model authentication flow not found")
)

type Config struct {
	PublicURL       string
	SecretKey       string
	ChatGPTClientID string
	HTTPClient      *http.Client
}

type Status struct {
	Provider     Provider   `json:"provider"`
	Available    bool       `json:"available"`
	Connected    bool       `json:"connected"`
	Reason       string     `json:"reason,omitempty"`
	DisplayLabel string     `json:"display_label,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

type RuntimeCredential struct {
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
	pool            *pgxpool.Pool
	httpClient      *http.Client
	vault           *vault
	publicURL       string
	chatGPTClientID string
	hostID          string
}

func New(pool *pgxpool.Pool, config Config) (*Service, error) {
	if pool == nil {
		return nil, errors.New("model auth database pool is required")
	}
	service := &Service{
		pool:            pool,
		httpClient:      config.HTTPClient,
		publicURL:       strings.TrimRight(strings.TrimSpace(config.PublicURL), "/"),
		chatGPTClientID: strings.TrimSpace(config.ChatGPTClientID),
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
	status := Status{Provider: provider}
	switch provider {
	case ProviderChatGPT:
		switch {
		case s.vault == nil:
			status.Reason = "secret_key_required"
		case s.publicURL == "":
			status.Reason = "public_url_required"
		case s.chatGPTClientID == "" && !isLoopbackURL(s.publicURL):
			status.Reason = "chatgpt_client_id_required"
		default:
			status.Available = true
		}
	case ProviderGrok:
		status.Available = s.vault != nil
		if !status.Available {
			status.Reason = "secret_key_required"
		}
	default:
		return Status{}, fmt.Errorf("unsupported model auth provider %q", provider)
	}

	var expiresAt time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT expires_at, display_label
		FROM model_oauth_credentials
		WHERE workspace_id=$1 AND provider=$2
	`, workspaceID, string(provider)).Scan(&expiresAt, &status.DisplayLabel)
	if errors.Is(err, pgx.ErrNoRows) {
		return status, nil
	}
	if err != nil {
		return Status{}, err
	}
	status.Connected = true
	status.ExpiresAt = &expiresAt
	return status, nil
}

func (s *Service) Disconnect(ctx context.Context, workspaceID uuid.UUID, provider Provider) error {
	if provider != ProviderChatGPT && provider != ProviderGrok {
		return fmt.Errorf("unsupported model auth provider %q", provider)
	}
	_, err := s.pool.Exec(ctx, `
		DELETE FROM model_oauth_credentials
		WHERE workspace_id=$1 AND provider=$2
	`, workspaceID, string(provider))
	return err
}

func (s *Service) ModelConfig(
	ctx context.Context,
	workspaceID uuid.UUID,
	provider Provider,
	model string,
	timeout time.Duration,
) (modelclient.Config, error) {
	credential, err := s.credential(ctx, workspaceID, provider, timeout+time.Minute)
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

	var ciphertext []byte
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT credential_ciphertext, expires_at
		FROM model_oauth_credentials
		WHERE workspace_id=$1 AND provider=$2
		FOR UPDATE
	`, workspaceID, string(provider)).Scan(&ciphertext, &expiresAt)
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
			Provider: provider, AccessToken: payload.AccessToken, ExpiresAt: expiresAt,
			UserID: payload.UserID, Email: payload.Email,
		}, nil
	}
	if payload.RefreshToken == "" {
		return RuntimeCredential{}, ErrReauthRequired
	}

	refreshed, refreshedExpiry, err := s.refreshCredential(ctx, provider, payload)
	if err != nil {
		return RuntimeCredential{}, err
	}
	encrypted, err := s.vault.seal(refreshed, credentialAAD(workspaceID, provider))
	if err != nil {
		return RuntimeCredential{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE model_oauth_credentials
		SET credential_ciphertext=$3, expires_at=$4, updated_at=now()
		WHERE workspace_id=$1 AND provider=$2
	`, workspaceID, string(provider), encrypted, refreshedExpiry); err != nil {
		return RuntimeCredential{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RuntimeCredential{}, err
	}
	return RuntimeCredential{
		Provider: provider, AccessToken: refreshed.AccessToken, ExpiresAt: refreshedExpiry,
		UserID: refreshed.UserID, Email: refreshed.Email,
	}, nil
}

func (s *Service) saveCredential(
	ctx context.Context,
	workspaceID uuid.UUID,
	provider Provider,
	payload credentialPayload,
	expiresAt time.Time,
) error {
	encrypted, err := s.vault.seal(payload, credentialAAD(workspaceID, provider))
	if err != nil {
		return err
	}
	label := "Connected"
	_, err = s.pool.Exec(ctx, `
		INSERT INTO model_oauth_credentials (
			workspace_id, provider, credential_ciphertext, expires_at, display_label
		)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (workspace_id, provider) DO UPDATE
		SET credential_ciphertext=EXCLUDED.credential_ciphertext,
		    expires_at=EXCLUDED.expires_at,
		    display_label=EXCLUDED.display_label,
		    updated_at=now()
	`, workspaceID, string(provider), encrypted, expiresAt, label)
	return err
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
