package main

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/uvwt/CycleDock/internal/attachments"
	"github.com/uvwt/CycleDock/internal/auth"
	"github.com/uvwt/CycleDock/internal/httpapi"
	"github.com/uvwt/CycleDock/internal/mcpserver"
	"github.com/uvwt/CycleDock/internal/modelauth"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/nexusdock"
	cycledockoauth "github.com/uvwt/CycleDock/internal/oauth"
	"github.com/uvwt/CycleDock/internal/realtime"
	"github.com/uvwt/CycleDock/internal/retention"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/webpush"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	maxConcurrent, err := envPositiveInt("CYCLEDOCK_AGENT_MAX_CONCURRENT", agent.DefaultMaxConcurrent)
	if err != nil {
		logger.Error("configure agent concurrency", "error", err)
		os.Exit(1)
	}
	maxPerWorkspace, err := envPositiveInt("CYCLEDOCK_AGENT_MAX_CONCURRENT_PER_WORKSPACE", agent.DefaultMaxConcurrentPerWorkspace)
	if err != nil {
		logger.Error("configure workspace agent concurrency", "error", err)
		os.Exit(1)
	}
	retentionCfg, err := loadRetentionConfigFromEnv()
	if err != nil {
		logger.Error("configure db retention cleaner", "error", err)
		os.Exit(1)
	}
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
	if err := dataStore.ConfigureMCPEncryption(os.Getenv("CYCLEDOCK_CONNECTION_ENCRYPTION_KEY")); err != nil {
		logger.Error("configure MCP token encryption", "error", err)
		os.Exit(1)
	}
	modelKey := os.Getenv("CYCLEDOCK_MODEL_SECRET_KEY")
	if modelKey == "" {
		modelKey = os.Getenv("CYCLEDOCK_CONNECTION_ENCRYPTION_KEY")
	}
	if err := dataStore.ConfigureModelEncryption(modelKey); err != nil {
		logger.Error("configure model API key encryption", "error", err)
		os.Exit(1)
	}
	triggerKey := os.Getenv("CYCLEDOCK_TRIGGER_ENCRYPTION_KEY")
	if triggerKey == "" {
		triggerKey = modelKey
	}
	if err := dataStore.ConfigureTriggerEncryption(triggerKey); err != nil {
		logger.Error("configure webhook trigger encryption", "error", err)
		os.Exit(1)
	}
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
	pushSubject := strings.TrimSpace(os.Getenv("CYCLEDOCK_VAPID_SUBJECT"))
	if pushSubject == "" && strings.HasPrefix(strings.ToLower(publicURL), "https://") {
		pushSubject = publicURL
	}
	pushSender, err := webpush.New(os.Getenv("CYCLEDOCK_VAPID_PUBLIC_KEY"), os.Getenv("CYCLEDOCK_VAPID_PRIVATE_KEY"), pushSubject)
	if err != nil {
		logger.Error("configure web push", "error", err)
		os.Exit(1)
	}

	uploadTTLHours, err := envPositiveInt("CYCLEDOCK_UPLOAD_TTL_HOURS", int(attachments.DefaultTTL/time.Hour))
	if err != nil {
		logger.Error("configure upload ttl", "error", err)
		os.Exit(1)
	}
	uploadMaxMB, err := envPositiveInt("CYCLEDOCK_UPLOAD_MAX_MB", int(attachments.DefaultMaxTotalBytes>>20))
	if err != nil {
		logger.Error("configure upload max size", "error", err)
		os.Exit(1)
	}
	attachmentsService, err := attachments.New(pool, attachments.Config{
		Dir:           os.Getenv("CYCLEDOCK_UPLOAD_DIR"),
		TTL:           time.Duration(uploadTTLHours) * time.Hour,
		MaxTotalBytes: int64(uploadMaxMB) << 20,
		Logger:        logger,
	})
	if err != nil {
		logger.Error("configure attachment storage", "error", err)
		os.Exit(1)
	}

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
		Attachments:  attachmentsService,
		MCP:          mcpHandlers.MCP,
		MCPMetadata:  mcpHandlers.ProtectedResourceMetadata,
		ModelAuth:    modelAuthService,
		ModelClient:  modelClient,
		Live:         live,
		Push:         pushSender,
	})

	server := &http.Server{
		Addr:              envOr("HTTP_ADDR", ":8080"),
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go attachmentsService.RunBackgroundCleanup(ctx)
	sessionService := agentsession.NewPostgresService(pool)
	artifactService := agentartifact.NewPostgresService(pool)
	dispatcher := agent.NewDispatcher(dataStore, modelClient, logger, agent.DispatcherConfig{
		MaxConcurrent:             maxConcurrent,
		MaxConcurrentPerWorkspace: maxPerWorkspace,
		SessionService:            sessionService,
		ArtifactService:           artifactService,
		Attachments:               attachmentsService,
		ModelAuth:                 modelAuthService,
		NexusDock:                 nexusDockService,
		Live:                      live,
	})
	dataStore.SetWorkReadyNotifier(dispatcher.Wake)
	dataStore.SetWorkspaceChangedNotifier(live.NotifyWorkspace)
	retentionCleaner := retention.NewCleaner(pool, retentionCfg, logger)
	go retentionCleaner.Run(ctx)
	if pushSender != nil {
		go httpapi.DeliverPushes(ctx, pool, dataStore, pushSender, live, logger)
	}
	dispatcherDone := make(chan struct{})
	go func() {
		defer close(dispatcherDone)
		if err := dispatcher.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("Cycle Agent dispatcher stopped", "error", err)
		}
	}()
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("server shutdown failed", "error", err)
		}
	}()

	logger.Info("cycledock server starting", "addr", server.Addr,
		"agent_max_concurrent", maxConcurrent, "agent_max_concurrent_per_workspace", maxPerWorkspace)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
	// 等待 HTTP 服务关闭以及调度器完成运行中 Attempt 的优雅交接，
	// 避免主函数提前 return 触发 pool.Close() 导致交接写库失败。
	<-shutdownDone
	select {
	case <-dispatcherDone:
	case <-time.After(15 * time.Second):
		logger.Warn("timed out waiting for Cycle Agent dispatcher graceful shutdown")
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

func envPositiveInt(key string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return parsed, nil
}

func envPositiveDuration(key string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return parsed, nil
}

func loadRetentionConfigFromEnv() (retention.Config, error) {
	enabled := true
	if parsed, ok, err := envBool("CYCLEDOCK_DB_RETENTION_ENABLED"); err != nil {
		return retention.Config{}, fmt.Errorf("invalid CYCLEDOCK_DB_RETENTION_ENABLED: %w", err)
	} else if ok {
		enabled = parsed
	}

	dryRun := false
	if parsed, ok, err := envBool("CYCLEDOCK_DB_RETENTION_DRY_RUN"); err != nil {
		return retention.Config{}, fmt.Errorf("invalid CYCLEDOCK_DB_RETENTION_DRY_RUN: %w", err)
	} else if ok {
		dryRun = parsed
	}

	retentionDays, err := envPositiveInt("CYCLEDOCK_DB_RETENTION_DAYS", retention.DefaultRetentionDays)
	if err != nil {
		return retention.Config{}, err
	}
	interval, err := envPositiveDuration("CYCLEDOCK_DB_RETENTION_INTERVAL", retention.DefaultInterval)
	if err != nil {
		return retention.Config{}, err
	}
	batchSize, err := envPositiveInt("CYCLEDOCK_DB_RETENTION_BATCH_SIZE", retention.DefaultBatchSize)
	if err != nil {
		return retention.Config{}, err
	}
	maxBatches, err := envPositiveInt("CYCLEDOCK_DB_RETENTION_MAX_BATCHES", retention.DefaultMaxBatchesPerRun)
	if err != nil {
		return retention.Config{}, err
	}

	return retention.Config{
		Enabled:       enabled,
		DryRun:        dryRun,
		RetentionDays: retentionDays,
		Interval:      interval,
		BatchSize:     batchSize,
		MaxBatches:    maxBatches,
		StartupDelay:  2 * time.Minute,
	}, nil
}
