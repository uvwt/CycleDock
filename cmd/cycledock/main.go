package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/uvwt/CycleDock/internal/agent"
	"github.com/uvwt/CycleDock/internal/agentartifact"
	"github.com/uvwt/CycleDock/internal/agentsession"
	"github.com/uvwt/CycleDock/internal/auth"
	"github.com/uvwt/CycleDock/internal/httpapi"
	"github.com/uvwt/CycleDock/internal/mcpserver"
	"github.com/uvwt/CycleDock/internal/modelauth"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/nexusdock"
	cycledockoauth "github.com/uvwt/CycleDock/internal/oauth"
	"github.com/uvwt/CycleDock/internal/realtime"
	"github.com/uvwt/CycleDock/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		logger.Error("DATABASE_URL is required")
		os.Exit(1)
	}

	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		logger.Error("create database pool", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := pool.Ping(context.Background()); err != nil {
		logger.Error("database ping failed", "error", err)
		os.Exit(1)
	}

	publicURL := strings.TrimRight(strings.TrimSpace(os.Getenv("CYCLEDOCK_PUBLIC_URL")), "/")
	authService, err := auth.New(pool, auth.Config{
		AdminPassword: os.Getenv("CYCLEDOCK_ADMIN_PASSWORD"),
		OIDC: auth.OIDCConfig{
			Issuer:    os.Getenv("CYCLEDOCK_OIDC_ISSUER"),
			ClientID:  os.Getenv("CYCLEDOCK_OIDC_CLIENT_ID"),
			PublicURL: publicURL,
		},
	})
	if err != nil {
		logger.Error("configure authentication", "error", err)
		os.Exit(1)
	}
	secureCookie := strings.HasPrefix(strings.ToLower(publicURL), "https://")
	configured, ok, err := envBool("CYCLEDOCK_SECURE_COOKIE")
	if err != nil {
		logger.Error("invalid CYCLEDOCK_SECURE_COOKIE", "error", err)
		os.Exit(1)
	}
	if ok && configured {
		// HTTPS Public URL 永远不能被显式降级为不安全 Cookie。
		// 此开关只用于在无 Public URL 的 Self-hosted HTTPS 反代场景强制启用 Secure。
		secureCookie = true
	}
	dataStore := store.New(pool)
	nexusDockService, err := nexusdock.New(pool, nexusdock.Config{
		MCPURL:        os.Getenv("CYCLEDOCK_NEXUSDOCK_MCP_URL"),
		WebURL:        os.Getenv("CYCLEDOCK_NEXUSDOCK_WEB_URL"),
		PublicURL:     publicURL,
		EncryptionKey: os.Getenv("CYCLEDOCK_CONNECTION_ENCRYPTION_KEY"),
	})
	if err != nil {
		logger.Error("configure NexusDock first-party connection", "error", err)
		os.Exit(1)
	}
	allowHostedChatGPTDynamicRegistration, _, err := envBool("CYCLEDOCK_CHATGPT_ALLOW_HOSTED_DYNAMIC_REGISTRATION")
	if err != nil {
		logger.Error("invalid CYCLEDOCK_CHATGPT_ALLOW_HOSTED_DYNAMIC_REGISTRATION", "error", err)
		os.Exit(1)
	}
	modelAuthService, err := modelauth.New(pool, modelauth.Config{
		PublicURL:                             publicURL,
		SecretKey:                             os.Getenv("CYCLEDOCK_MODEL_SECRET_KEY"),
		ChatGPTClientID:                       os.Getenv("CYCLEDOCK_CHATGPT_CLIENT_ID"),
		AllowHostedChatGPTDynamicRegistration: allowHostedChatGPTDynamicRegistration,
	})
	if err != nil {
		logger.Error("configure model account authentication", "error", err)
		os.Exit(1)
	}
	modelClient := modelclient.New()
	live := realtime.NewHub()

	var oauthService *cycledockoauth.Service
	var mcpHandlers mcpserver.Handlers
	if publicURL != "" {
		oauthService, err = cycledockoauth.New(pool, cycledockoauth.Config{PublicURL: publicURL})
		if err != nil {
			logger.Error("configure MCP OAuth", "error", err)
			os.Exit(1)
		}
		mcpHandlers = mcpserver.New(dataStore, oauthService, logger)
	}
	router := httpapi.NewRouter(pool, dataStore, authService, httpapi.Config{
		SecureCookie: secureCookie,
		WebDir:       os.Getenv("WEB_DIR"),
		Logger:       logger,
		OAuth:        oauthService,
		NexusDock:    nexusDockService,
		MCP:          mcpHandlers.MCP,
		MCPMetadata:  mcpHandlers.ProtectedResourceMetadata,
		ModelAuth:    modelAuthService,
		ModelClient:  modelClient,
		Live:         live,
	})

	server := &http.Server{
		Addr:              envOr("HTTP_ADDR", ":8080"),
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	sessionService := agentsession.NewPostgresService(pool)
	artifactService := agentartifact.NewPostgresService(pool)
	dispatcher := agent.NewDispatcher(dataStore, modelClient, logger, agent.DispatcherConfig{
		SessionService:  sessionService,
		ArtifactService: artifactService,
		ModelAuth:       modelAuthService,
		NexusDock:       nexusDockService,
		Live:            live,
	})
	dataStore.SetWorkReadyNotifier(dispatcher.Wake)
	go func() {
		if err := dispatcher.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("Cycle Agent dispatcher stopped", "error", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("server shutdown failed", "error", err)
		}
	}()

	logger.Info("cycledock server starting", "addr", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envBool(key string) (bool, bool, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return false, false, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, false, err
	}
	return parsed, true, nil
}
