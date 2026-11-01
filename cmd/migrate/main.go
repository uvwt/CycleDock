package main

import (
	"database/sql"
	"log/slog"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		logger.Error("DATABASE_URL is required")
		os.Exit(1)
	}

	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	if err := database.Ping(); err != nil {
		logger.Error("database ping failed", "error", err)
		os.Exit(1)
	}
	migrationsDir := os.Getenv("MIGRATIONS_DIR")
	if migrationsDir == "" {
		migrationsDir = "db/migrations"
	}
	if err := goose.Up(database, migrationsDir); err != nil {
		logger.Error("database migration failed", "error", err)
		os.Exit(1)
	}
	logger.Info("database migrations are current")
}
