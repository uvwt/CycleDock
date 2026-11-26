package sqldb

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pressly/goose/v3"

	"github.com/uvwt/CycleDock/db/migrations"
)

// MigrateUp 将数据库迁移至最新版本。
// 当 migrationsDir 为空时直接使用二进制内嵌的对应方言迁移脚本；
// 当 migrationsDir 非空时支持传入根目录（自动定位 postgres/ 或 sqlite/ 子目录）或直接传入方言目录。
func MigrateUp(ctx context.Context, db *DB, migrationsDir string) error {
	provider, err := newGooseProvider(db, migrationsDir)
	if err != nil {
		return err
	}
	_, err = provider.Up(ctx)
	return err
}

// MigrateUpTo 将数据库迁移到指定版本（主要用于历史迁移演进回归测试）。
func MigrateUpTo(ctx context.Context, db *DB, migrationsDir string, version int64) error {
	provider, err := newGooseProvider(db, migrationsDir)
	if err != nil {
		return err
	}
	_, err = provider.UpTo(ctx, version)
	return err
}

// MigrateDown 回退一步迁移。
func MigrateDown(ctx context.Context, db *DB, migrationsDir string) error {
	provider, err := newGooseProvider(db, migrationsDir)
	if err != nil {
		return err
	}
	_, err = provider.Down(ctx)
	return err
}

// MigrateDownTo 回退到指定版本（version=0 表示回退全部迁移）。
func MigrateDownTo(ctx context.Context, db *DB, migrationsDir string, version int64) error {
	provider, err := newGooseProvider(db, migrationsDir)
	if err != nil {
		return err
	}
	_, err = provider.DownTo(ctx, version)
	return err
}

// ResolveMigrationsDir 根据数据库方言解析实际迁移目录路径，兼容指向父目录 db/migrations 的配置。
func ResolveMigrationsDir(dialect Dialect, dir string) string {
	cleaned := strings.TrimSpace(dir)
	if cleaned == "" {
		return filepath.Join("db", "migrations", string(dialect))
	}
	sub := filepath.Join(cleaned, string(dialect))
	if info, err := os.Stat(sub); err == nil && info.IsDir() {
		return sub
	}
	return cleaned
}

func newGooseProvider(db *DB, migrationsDir string) (*goose.Provider, error) {
	if db == nil {
		return nil, fmt.Errorf("db is nil")
	}
	var gooseDialect goose.Dialect
	switch db.Dialect() {
	case DialectPostgres:
		gooseDialect = goose.DialectPostgres
	case DialectSQLite:
		gooseDialect = goose.DialectSQLite3
	default:
		return nil, fmt.Errorf("unsupported dialect for migrations: %s", db.Dialect())
	}

	var fsys fs.FS
	if strings.TrimSpace(migrationsDir) == "" {
		sub, err := fs.Sub(migrations.FS, string(db.Dialect()))
		if err != nil {
			return nil, fmt.Errorf("open embedded migrations for %s: %w", db.Dialect(), err)
		}
		fsys = sub
	} else {
		resolved := ResolveMigrationsDir(db.Dialect(), migrationsDir)
		fsys = os.DirFS(resolved)
	}

	return goose.NewProvider(gooseDialect, db.StdDB(), fsys)
}
