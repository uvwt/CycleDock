package testdb

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/sqldb"
)

// ForEach 在 SQLite（始终运行）与 PostgreSQL（当设置 TEST_DATABASE_URL 时）上分别运行同一份集成测试。
// 可通过 TEST_DB_DIALECT=sqlite 或 TEST_DB_DIALECT=postgres 在 CI 或本地定向只跑其中一种后端。
func ForEach(t *testing.T, fn func(t *testing.T, db *sqldb.DB)) {
	t.Helper()
	only := strings.ToLower(strings.TrimSpace(os.Getenv("TEST_DB_DIALECT")))

	if only == "" || only == "sqlite" {
		t.Run("sqlite", func(t *testing.T) {
			db := OpenSQLite(t)
			fn(t, db)
		})
	}

	if only == "" || only == "postgres" {
		pgURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
		if pgURL != "" {
			t.Run("postgres", func(t *testing.T) {
				db := OpenPostgres(t, pgURL)
				fn(t, db)
			})
		} else if only == "postgres" {
			t.Skip("TEST_DATABASE_URL is not set")
		}
	}
}

// OpenSQLite 在测试临时目录中创建独立的 WAL SQLite 数据库并执行内嵌迁移。
func OpenSQLite(t *testing.T) *sqldb.DB {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sqldb.OpenURL(ctx, "sqlite://"+dbPath)
	if err != nil {
		t.Fatalf("open test sqlite: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := sqldb.MigrateUp(ctx, db, ""); err != nil {
		t.Fatalf("migrate test sqlite: %v", err)
	}
	return db
}

// OpenPostgres 在独立临时 PostgreSQL schema 中执行内嵌迁移并打开连接，
// 测试结束后自动 DROP SCHEMA CASCADE，严禁污染开发库 public schema。
func OpenPostgres(t *testing.T, pgURL string) *sqldb.DB {
	t.Helper()
	db, cleanup, err := openIsolatedPostgres(pgURL, "testdb")
	if err != nil {
		t.Fatalf("open isolated test postgres: %v", err)
	}
	t.Cleanup(cleanup)
	return db
}

// OpenDefault 按环境变量返回一个已迁移且隔离的测试库：
//   - 当 TEST_DB_DIALECT=sqlite 时，返回临时 SQLite；
//   - 当 TEST_DB_DIALECT=postgres 时，返回隔离 PostgreSQL schema（若未设置 TEST_DATABASE_URL 则跳过）；
//   - 未设 TEST_DB_DIALECT 时，如果 TEST_DATABASE_URL 非空则使用隔离 PostgreSQL，否则使用临时 SQLite。
func OpenDefault(t *testing.T) *sqldb.DB {
	t.Helper()
	only := strings.ToLower(strings.TrimSpace(os.Getenv("TEST_DB_DIALECT")))
	pgURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if only == "sqlite" || (only == "" && pgURL == "") {
		return OpenSQLite(t)
	}
	if pgURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	return OpenPostgres(t, pgURL)
}

// SetupPackagePool 供使用 TestMain 的集成测试包初始化包级共享测试库（支持 SQLite 与隔离 PG schema）。
// 返回打开的 *sqldb.DB 与退出时必须调用的清理函数。
func SetupPackagePool(prefix string) (*sqldb.DB, func(), error) {
	only := strings.ToLower(strings.TrimSpace(os.Getenv("TEST_DB_DIALECT")))
	pgURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if only == "sqlite" || (only == "" && pgURL == "") {
		dir, err := os.MkdirTemp("", "cycledock-test-"+prefix+"-*")
		if err != nil {
			return nil, nil, err
		}
		ctx := context.Background()
		db, err := sqldb.OpenURL(ctx, "sqlite://"+filepath.Join(dir, "test.db"))
		if err != nil {
			_ = os.RemoveAll(dir)
			return nil, nil, err
		}
		if err := sqldb.MigrateUp(ctx, db, ""); err != nil {
			_ = db.Close()
			_ = os.RemoveAll(dir)
			return nil, nil, err
		}
		return db, func() {
			_ = db.Close()
			_ = os.RemoveAll(dir)
		}, nil
	}
	if pgURL == "" {
		return nil, func() {}, nil
	}
	return openIsolatedPostgres(pgURL, prefix)
}

func openIsolatedPostgres(pgURL, prefix string) (*sqldb.DB, func(), error) {
	ctx := context.Background()
	adminDB, err := sqldb.OpenURL(ctx, pgURL)
	if err != nil {
		return nil, nil, fmt.Errorf("connect admin postgres: %w", err)
	}
	schema := prefix + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := `"` + strings.ReplaceAll(schema, `"`, `""`) + `"`
	if _, err := adminDB.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		_ = adminDB.Close()
		return nil, nil, fmt.Errorf("create schema %s: %w", schema, err)
	}
	scopedURL, err := withPostgresSearchPath(pgURL, schema)
	if err != nil {
		_, _ = adminDB.Exec(ctx, "DROP SCHEMA IF EXISTS "+quoted+" CASCADE")
		_ = adminDB.Close()
		return nil, nil, err
	}
	db, err := sqldb.OpenURL(ctx, scopedURL)
	if err != nil {
		_, _ = adminDB.Exec(ctx, "DROP SCHEMA IF EXISTS "+quoted+" CASCADE")
		_ = adminDB.Close()
		return nil, nil, fmt.Errorf("connect scoped postgres: %w", err)
	}
	if err := sqldb.MigrateUp(ctx, db, ""); err != nil {
		_ = db.Close()
		_, _ = adminDB.Exec(ctx, "DROP SCHEMA IF EXISTS "+quoted+" CASCADE")
		_ = adminDB.Close()
		return nil, nil, fmt.Errorf("migrate scoped postgres: %w", err)
	}
	cleanup := func() {
		_ = db.Close()
		_, _ = adminDB.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quoted+" CASCADE")
		_ = adminDB.Close()
	}
	return db, cleanup, nil
}

func withPostgresSearchPath(rawURL, schema string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse TEST_DATABASE_URL: %w", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// TruncateAll 清空当前测试库中除 goose_db_version 之外的全部业务表数据。
func TruncateAll(t *testing.T, db *sqldb.DB) {
	t.Helper()
	ctx := context.Background()
	if db.IsPostgres() {
		// 动态查询当前 schema 下的真实业务表，避免表名漂移导致 TRUNCATE 失败。
		rows, err := db.Query(ctx, `
			SELECT tablename
			FROM pg_tables
			WHERE schemaname = current_schema()
			  AND tablename <> 'goose_db_version'
			ORDER BY tablename
		`)
		if err != nil {
			t.Fatalf("list postgres tables: %v", err)
		}
		var tables []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				t.Fatalf("scan postgres table name: %v", err)
			}
			tables = append(tables, `"`+strings.ReplaceAll(name, `"`, `""`)+`"`)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate postgres tables: %v", err)
		}
		if len(tables) == 0 {
			return
		}
		if _, err := db.Exec(ctx, "TRUNCATE TABLE "+strings.Join(tables, ", ")+" RESTART IDENTITY CASCADE"); err != nil {
			t.Fatalf("truncate postgres tables: %v", err)
		}
		return
	}

	// SQLite：单连接上临时关闭外键约束逐表清空，并重置 sqlite_sequence。
	rows, err := db.Query(ctx, `
		SELECT name FROM sqlite_master
		WHERE type = 'table'
		  AND name NOT LIKE 'sqlite_%'
		  AND name <> 'goose_db_version'
	`)
	if err != nil {
		t.Fatalf("list sqlite tables: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatalf("scan sqlite table name: %v", err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate sqlite tables: %v", err)
	}

	if _, err := db.Exec(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatalf("disable sqlite foreign_keys: %v", err)
	}
	defer func() {
		_, _ = db.Exec(ctx, "PRAGMA foreign_keys = ON")
	}()
	for _, table := range tables {
		if _, err := db.Exec(ctx, `DELETE FROM "`+table+`"`); err != nil {
			t.Fatalf("delete sqlite table %s: %v", table, err)
		}
	}
	_, _ = db.Exec(ctx, "DELETE FROM sqlite_sequence")
}
