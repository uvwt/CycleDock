package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/oauth2"

	"github.com/uvwt/CycleDock/internal/store/db"
)

type LoginProvider string

const (
	ProviderNexusDock LoginProvider = "nexusdock"
	oidcFlowTTL                     = 10 * time.Minute
)

var (
	ErrOIDCDisabled     = errors.New("OIDC login is disabled")
	ErrInvalidOIDCState = errors.New("invalid or expired OIDC login state")
	ErrOIDCProvider     = errors.New("OIDC provider request failed")
)

type OIDCConfig struct {
	Issuer     string
	ClientID   string
	PublicURL  string
	HTTPClient *http.Client
}

type LoginMethods struct {
	Password  bool
	Providers []LoginProvider
}

type OIDCLoginStart struct {
	URL       string
	State     string
	ExpiresAt time.Time
}

type oidcRuntime struct {
	issuer      string
	clientID    string
	redirectURL string
	httpClient  *http.Client

	mu       sync.Mutex
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
}

func (o *oidcRuntime) enabled() bool {
	return o.clientID != ""
}

func (s *Service) configureOIDC(config OIDCConfig) error {
	config.Issuer = strings.TrimRight(strings.TrimSpace(config.Issuer), "/")
	config.ClientID = strings.TrimSpace(config.ClientID)
	config.PublicURL = strings.TrimRight(strings.TrimSpace(config.PublicURL), "/")
	if config.Issuer == "" && config.ClientID == "" && config.PublicURL == "" {
		s.oidc = oidcRuntime{}
		return nil
	}
	if config.Issuer == "" || config.ClientID == "" || config.PublicURL == "" {
		return fmt.Errorf("%w: CYCLEDOCK_OIDC_ISSUER, CYCLEDOCK_OIDC_CLIENT_ID and CYCLEDOCK_PUBLIC_URL must be configured together", ErrInvalidConfig)
	}

	issuer, err := url.Parse(config.Issuer)
	if err != nil || issuer.Scheme == "" || issuer.Host == "" || issuer.RawQuery != "" || issuer.Fragment != "" || issuer.User != nil {
		return fmt.Errorf("%w: CYCLEDOCK_OIDC_ISSUER must be an absolute issuer URL", ErrInvalidConfig)
	}
	if issuer.Scheme != "https" {
		host := issuer.Hostname()
		ip := net.ParseIP(host)
		if issuer.Scheme != "http" || (host != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return fmt.Errorf("%w: CYCLEDOCK_OIDC_ISSUER must use HTTPS except localhost/loopback development", ErrInvalidConfig)
		}
	}
	publicURL, err := url.Parse(config.PublicURL)
	if err != nil || publicURL.Scheme == "" || publicURL.Host == "" || publicURL.RawQuery != "" || publicURL.Fragment != "" ||
		publicURL.User != nil || (publicURL.Path != "" && publicURL.Path != "/") {
		return fmt.Errorf("%w: CYCLEDOCK_PUBLIC_URL must be an absolute origin", ErrInvalidConfig)
	}
	if publicURL.Scheme != "https" {
		host := publicURL.Hostname()
		ip := net.ParseIP(host)
		if publicURL.Scheme != "http" || (host != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return fmt.Errorf("%w: CYCLEDOCK_PUBLIC_URL must use HTTPS except localhost/loopback development", ErrInvalidConfig)
		}
	}

	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	s.oidc = oidcRuntime{
		issuer:      config.Issuer,
		clientID:    config.ClientID,
		redirectURL: config.PublicURL + "/auth/nexusdock/callback",
		httpClient:  client,
	}
	return nil
}

func (s *Service) LoginMethods() LoginMethods {
	methods := LoginMethods{Password: s.SelfHostedLoginEnabled()}
	if s.oidc.enabled() {
		methods.Providers = []LoginProvider{ProviderNexusDock}
	}
	return methods
}

func (s *Service) BeginOIDCLogin(ctx context.Context) (OIDCLoginStart, error) {
	config, _, err := s.oidcClient(ctx)
	if err != nil {
		return OIDCLoginStart{}, err
	}
	if err := s.queries.DeleteExpiredWebAuthFlows(ctx); err != nil {
		return OIDCLoginStart{}, fmt.Errorf("delete expired OIDC flows: %w", err)
	}

	state, err := randomURLValue(32)
	if err != nil {
		return OIDCLoginStart{}, err
	}
	nonce, err := randomURLValue(24)
	if err != nil {
		return OIDCLoginStart{}, err
	}
	verifier := oauth2.GenerateVerifier()
	digest := sha256.Sum256([]byte(state))
	expiresAt := time.Now().UTC().Add(oidcFlowTTL)
	if err := s.queries.CreateWebAuthFlow(ctx, db.CreateWebAuthFlowParams{
		StateDigest:  digest[:],
		CodeVerifier: verifier,
		Nonce:        nonce,
		ExpiresAt:    pgtype.Timestamptz{Time: expiresAt, Valid: true},
	}); err != nil {
		return OIDCLoginStart{}, fmt.Errorf("create OIDC web auth flow: %w", err)
	}

	return OIDCLoginStart{
		URL: config.AuthCodeURL(
			state,
			oauth2.S256ChallengeOption(verifier),
			oidc.Nonce(nonce),
		),
		State:     state,
		ExpiresAt: expiresAt,
	}, nil
}

func (s *Service) ConsumeOIDCState(ctx context.Context, state string) error {
	_, err := s.consumeOIDCFlow(ctx, state)
	return err
}

func (s *Service) CompleteOIDCLogin(ctx context.Context, state, code string) (Session, error) {
	flow, err := s.consumeOIDCFlow(ctx, state)
	if err != nil {
		return Session{}, err
	}
	if strings.TrimSpace(code) == "" {
		return Session{}, ErrOIDCProvider
	}

	config, verifier, err := s.oidcClient(ctx)
	if err != nil {
		return Session{}, err
	}
	providerCtx := oidc.ClientContext(ctx, s.oidc.httpClient)
	token, err := config.Exchange(providerCtx, code, oauth2.VerifierOption(flow.CodeVerifier))
	if err != nil {
		return Session{}, fmt.Errorf("%w: exchange authorization code: %v", ErrOIDCProvider, err)
	}
	rawIDToken, _ := token.Extra("id_token").(string)
	if rawIDToken == "" {
		return Session{}, fmt.Errorf("%w: token response does not contain id_token", ErrOIDCProvider)
	}
	idToken, err := verifier.Verify(providerCtx, rawIDToken)
	if err != nil {
		return Session{}, fmt.Errorf("%w: verify id_token: %v", ErrOIDCProvider, err)
	}
	if idToken.Nonce != flow.Nonce {
		return Session{}, fmt.Errorf("%w: id_token nonce mismatch", ErrOIDCProvider)
	}

	var claims struct {
		Name              string `json:"name"`
		PreferredUsername string `json:"preferred_username"`
		Email             string `json:"email"`
		EmailVerified     bool   `json:"email_verified"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return Session{}, fmt.Errorf("%w: decode id_token claims: %v", ErrOIDCProvider, err)
	}
	subject := strings.TrimSpace(idToken.Subject)
	if subject == "" {
		return Session{}, fmt.Errorf("%w: id_token subject is empty", ErrOIDCProvider)
	}

	displayName := strings.TrimSpace(claims.Name)
	if displayName == "" {
		displayName = strings.TrimSpace(claims.PreferredUsername)
	}
	email := ""
	if claims.EmailVerified {
		email = strings.TrimSpace(claims.Email)
	}
	if displayName == "" && email != "" {
		displayName = email
	}
	if displayName == "" {
		displayName = "NexusDock User"
	}

	identity, err := s.ensureOIDCPersonalIdentity(ctx, s.oidc.issuer, subject, displayName, email)
	if err != nil {
		return Session{}, err
	}
	return s.createSession(ctx, identity)
}

func (s *Service) consumeOIDCFlow(ctx context.Context, state string) (db.WebAuthFlow, error) {
	state = strings.TrimSpace(state)
	if state == "" || !s.oidc.enabled() {
		return db.WebAuthFlow{}, ErrInvalidOIDCState
	}
	digest := sha256.Sum256([]byte(state))
	flow, err := s.queries.ConsumeWebAuthFlow(ctx, digest[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return db.WebAuthFlow{}, ErrInvalidOIDCState
	}
	if err != nil {
		return db.WebAuthFlow{}, fmt.Errorf("consume OIDC web auth flow: %w", err)
	}
	return flow, nil
}

func (s *Service) oidcClient(ctx context.Context) (oauth2.Config, *oidc.IDTokenVerifier, error) {
	if !s.oidc.enabled() {
		return oauth2.Config{}, nil, ErrOIDCDisabled
	}
	s.oidc.mu.Lock()
	defer s.oidc.mu.Unlock()

	if s.oidc.provider == nil {
		provider, err := oidc.NewProvider(oidc.ClientContext(ctx, s.oidc.httpClient), s.oidc.issuer)
		if err != nil {
			return oauth2.Config{}, nil, fmt.Errorf("%w: discovery: %v", ErrOIDCProvider, err)
		}
		s.oidc.provider = provider
		s.oidc.verifier = provider.Verifier(&oidc.Config{ClientID: s.oidc.clientID})
	}
	return oauth2.Config{
		ClientID:    s.oidc.clientID,
		Endpoint:    s.oidc.provider.Endpoint(),
		RedirectURL: s.oidc.redirectURL,
		Scopes:      []string{oidc.ScopeOpenID, "profile", "email"},
	}, s.oidc.verifier, nil
}

func randomURLValue(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate OIDC random value: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
