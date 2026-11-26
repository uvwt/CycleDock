package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/uvwt/CycleDock/internal/sqldb"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := sqldb.ConfigFromEnv()
	if err != nil {
		logger.Error("configure database", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()
	database, err := sqldb.Open(ctx, cfg)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	if err := database.Ping(ctx); err != nil {
		logger.Error("database ping failed", "error", err)
		os.Exit(1)
	}
	// MIGRATIONS_DIR 留空时默认使用二进制内嵌的对应方言迁移脚本，也可指定外部目录。
	if err := sqldb.MigrateUp(ctx, database, os.Getenv("MIGRATIONS_DIR")); err != nil {
		logger.Error("database migration failed", "error", err)
		os.Exit(1)
	}
	logger.Info("database migrations are current", "dialect", database.Dialect())
}
