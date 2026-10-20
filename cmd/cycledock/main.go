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

	"github.com/uvwt/CycleDock/internal/auth"
	"github.com/uvwt/CycleDock/internal/httpapi"
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
	router := httpapi.NewRouter(pool, store.New(pool), authService, httpapi.Config{
		SecureCookie: secureCookie,
		WebDir:       os.Getenv("WEB_DIR"),
		Logger:       logger,
	})

	server := &http.Server{
		Addr:              envOr("HTTP_ADDR", ":8080"),
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
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
