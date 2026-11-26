package sqldb_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/sqldb"
)

func TestRebind(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "basic and repeated placeholders",
			in:   "SELECT * FROM cards WHERE workspace_id = $1 AND (id = $2 OR source_discussion_id = $1) LIMIT $10",
			want: "SELECT * FROM cards WHERE workspace_id = ?1 AND (id = ?2 OR source_discussion_id = ?1) LIMIT ?10",
		},
		{
			name: "skip string literals and escaped quotes",
			in:   "SELECT '$1 is not a param', 'it''s $2' FROM cards WHERE id = $1",
			want: "SELECT '$1 is not a param', 'it''s $2' FROM cards WHERE id = ?1",
		},
		{
			name: "skip comments and quoted identifiers",
			in:   "SELECT \"$1col\" -- $2 comment\n/* $3 block */\nFROM cards WHERE id = $1",
			want: "SELECT \"$1col\" -- $2 comment\n/* $3 block */\nFROM cards WHERE id = ?1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sqldb.Rebind(sqldb.DialectPostgres, tc.in); got != tc.in {
				t.Fatalf("postgres rebind mutated query:\ngot:  %s\nwant: %s", got, tc.in)
			}
			if got := sqldb.Rebind(sqldb.DialectSQLite, tc.in); got != tc.want {
				t.Fatalf("sqlite rebind mismatch:\ngot:  %s\nwant: %s", got, tc.want)
			}
		})
	}
}

func TestSQLiteMigrationsUpDownUpAndConstraints(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "nested", "cycledock.db")
	db, err := sqldb.OpenURL(ctx, "sqlite://"+dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	// 1. 内嵌迁移 Up -> Down -> Up 必须全部成功。
	if err := sqldb.MigrateUp(ctx, db, ""); err != nil {
		t.Fatalf("sqlite migrate up: %v", err)
	}
	if err := sqldb.MigrateDownTo(ctx, db, "", 0); err != nil {
		t.Fatalf("sqlite migrate down to 0: %v", err)
	}
	if err := sqldb.MigrateUp(ctx, db, ""); err != nil {
		t.Fatalf("sqlite migrate re-up: %v", err)
	}

	// 2. 验证外键约束、唯一约束、CHECK 约束、自增 sequence、复合外键 BEFORE DELETE SET NULL 触发器。
	wsID := uuid.New()
	projID := uuid.New()
	srcID := uuid.New()
	card1 := uuid.New()
	card2 := uuid.New()

	if _, err := db.Exec(ctx, `INSERT INTO workspaces (id, name) VALUES ($1, $2)`, wsID, "WS"); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	// 唯一冲突分类测试
	if _, err := db.Exec(ctx, `INSERT INTO workspaces (id, name) VALUES ($1, $2)`, wsID, "WS2"); !sqldb.IsUniqueViolation(err) {
		t.Fatalf("expected unique violation, got %v", err)
	}
	// 外键冲突分类测试
	if _, err := db.Exec(ctx, `INSERT INTO projects (id, workspace_id, name, description) VALUES ($1, $2, 'P', '')`, projID, uuid.New()); !sqldb.IsForeignKeyViolation(err) {
		t.Fatalf("expected foreign key violation, got %v", err)
	}
	// CHECK 约束分类测试
	if _, err := db.Exec(ctx, `INSERT INTO workspaces (id, name) VALUES ($1, '   ')`, uuid.New()); !sqldb.IsCheckViolation(err) {
		t.Fatalf("expected check violation, got %v", err)
	}

	if _, err := db.Exec(ctx, `INSERT INTO projects (id, workspace_id, name, description) VALUES ($1, $2, 'P', '')`, projID, wsID); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO sources (id, workspace_id, kind, name) VALUES ($1, $2, 'human', 'u')`, srcID, wsID); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO cards (id, workspace_id, project_id, kind, discussion_state, created_by_source_id)
		VALUES ($1, $2, $3, 'discussion', 'active', $4)
	`, card1, wsID, projID, srcID); err != nil {
		t.Fatalf("insert card1: %v", err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO cards (id, workspace_id, project_id, kind, title, todo_state, source_discussion_id, created_by_source_id)
		VALUES ($1, $2, $3, 'todo', 'Implement SQLite', 'todo', $4, $5)
	`, card2, wsID, projID, card1, srcID); err != nil {
		t.Fatalf("insert card2: %v", err)
	}

	// 验证 events.sequence AUTOINCREMENT + RETURNING
	var seq1, seq2 int64
	if err := db.QueryRow(ctx, `
		INSERT INTO events (id, workspace_id, card_id, actor_source_id, event_type, content)
		VALUES ($1, $2, $3, $4, 'comment', 'hello')
		RETURNING sequence
	`, uuid.New(), wsID, card2, srcID).Scan(&seq1); err != nil {
		t.Fatalf("insert event 1: %v", err)
	}
	if err := db.QueryRow(ctx, `
		INSERT INTO events (id, workspace_id, card_id, actor_source_id, event_type, content)
		VALUES ($1, $2, $3, $4, 'comment', 'world')
		RETURNING sequence
	`, uuid.New(), wsID, card2, srcID).Scan(&seq2); err != nil {
		t.Fatalf("insert event 2: %v", err)
	}
	if seq2 <= seq1 {
		t.Fatalf("expected strictly increasing sequence, got %d then %d", seq1, seq2)
	}

	// 验证附件关联 card1 后删除 card1：workspace_id 保持非空，card_id 置空，card2.source_discussion_id 置空！
	attID := uuid.New()
	if _, err := db.Exec(ctx, `
		INSERT INTO workspace_attachments (
			id, workspace_id, card_id, uploaded_by_source_id, mime_type,
			size_bytes, stored_size_bytes, width, height, sha256, relative_path, expires_at, kind, file_name
		) VALUES ($1, $2, $3, $4, 'image/png', 10, 10, 1, 1, 'abc', 'rel/a.png', $5, 'image', 'a.png')
	`, attID, wsID, card1, srcID, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("insert attachment: %v", err)
	}
	if _, err := db.Exec(ctx, `DELETE FROM cards WHERE id = $1`, card1); err != nil {
		t.Fatalf("delete card1 with composite set-null refs failed: %v", err)
	}
	var gotCardID uuid.NullUUID
	if err := db.QueryRow(ctx, `SELECT card_id FROM workspace_attachments WHERE id = $1`, attID).Scan(&gotCardID); err != nil {
		t.Fatalf("query attachment after card delete: %v", err)
	}
	if gotCardID.Valid {
		t.Fatalf("expected attachment.card_id to be NULL after card1 delete, got %s", gotCardID.UUID)
	}
	var gotSourceDisc uuid.NullUUID
	if err := db.QueryRow(ctx, `SELECT source_discussion_id FROM cards WHERE id = $1`, card2).Scan(&gotSourceDisc); err != nil {
		t.Fatalf("query card2 after card1 delete: %v", err)
	}
	if gotSourceDisc.Valid {
		t.Fatalf("expected card2.source_discussion_id to be NULL after card1 delete, got %s", gotSourceDisc.UUID)
	}
}

func TestSQLiteConcurrentWritesAndReadsNoBusy(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "concurrent.db")
	db, err := sqldb.OpenURL(ctx, "sqlite://"+dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	if err := sqldb.MigrateUp(ctx, db, ""); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	const writers = 8
	const writesPerWorker = 25
	var wg sync.WaitGroup
	errCh := make(chan error, writers*writesPerWorker+10)

	// 并发写事务 + 并发读，验证 WAL + 单写连接 + BEGIN IMMEDIATE 零 SQLITE_BUSY。
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < writesPerWorker; i++ {
				tx, err := db.Begin(ctx)
				if err != nil {
					errCh <- fmt.Errorf("worker %d begin: %w", worker, err)
					return
				}
				wsID := uuid.New()
				if _, err := tx.Exec(ctx, `INSERT INTO workspaces (id, name) VALUES ($1, $2)`, wsID, fmt.Sprintf("w-%d-%d", worker, i)); err != nil {
					_ = tx.Rollback(ctx)
					errCh <- fmt.Errorf("worker %d insert: %w", worker, err)
					return
				}
				if err := tx.Commit(ctx); err != nil {
					errCh <- fmt.Errorf("worker %d commit: %w", worker, err)
					return
				}
			}
		}(w)
	}

	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				var count int
				if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM workspaces`).Scan(&count); err != nil {
					errCh <- fmt.Errorf("reader query: %w", err)
					return
				}
			}
		}()
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent sqlite operation failed: %v", err)
	}

	var total int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM workspaces`).Scan(&total); err != nil {
		t.Fatalf("count workspaces: %v", err)
	}
	if total != writers*writesPerWorker {
		t.Fatalf("expected %d workspaces, got %d", writers*writesPerWorker, total)
	}
}

func TestDualDBRoundTripTypesAndSchemaContract(t *testing.T) {
	ctx := context.Background()
	sqliteDB, err := sqldb.OpenURL(ctx, "sqlite://"+filepath.Join(t.TempDir(), "contract.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqliteDB.Close()
	if err := sqldb.MigrateUp(ctx, sqliteDB, ""); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}

	verifyTypes := func(t *testing.T, db *sqldb.DB) {
		t.Helper()
		clientID := "cli-" + uuid.NewString()
		uris := []string{"http://127.0.0.1:8080/cb", `https://example.com/a"b,c`}
		now := time.Now().UTC().Truncate(time.Microsecond)

		if _, err := db.Exec(ctx, `
			INSERT INTO oauth_clients (client_id, client_name, redirect_uris, created_at)
			VALUES ($1, $2, $3, $4)
		`, clientID, "Test", uris, now); err != nil {
			t.Fatalf("insert oauth_client (%s): %v", db.Dialect(), err)
		}
		var gotURIs []string
		var gotTime time.Time
		if err := db.QueryRow(ctx, `
			SELECT redirect_uris, created_at FROM oauth_clients WHERE client_id = $1
		`, clientID).Scan(&gotURIs, &gotTime); err != nil {
			t.Fatalf("select oauth_client (%s): %v", db.Dialect(), err)
		}
		if len(gotURIs) != len(uris) || gotURIs[0] != uris[0] || gotURIs[1] != uris[1] {
			t.Fatalf("redirect_uris mismatch (%s): got %#v want %#v", db.Dialect(), gotURIs, uris)
		}
		if !gotTime.UTC().Equal(now) {
			t.Fatalf("timestamp mismatch (%s): got %s want %s", db.Dialect(), gotTime.UTC(), now)
		}

		// JSONB / TEXT 往返测试
		appName := "app-" + uuid.NewString()
		payload := json.RawMessage(`{"k":"v","n":42}`)
		if _, err := db.Exec(ctx, `
			INSERT INTO agent_app_state (app_name, state, updated_at)
			VALUES ($1, $2, $3)
		`, appName, sqldb.JSON(payload), now); err != nil {
			t.Fatalf("insert agent_app_state (%s): %v", db.Dialect(), err)
		}
		var gotRaw []byte
		if err := db.QueryRow(ctx, `SELECT state FROM agent_app_state WHERE app_name = $1`, appName).Scan(&gotRaw); err != nil {
			t.Fatalf("select agent_app_state (%s): %v", db.Dialect(), err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(gotRaw, &decoded); err != nil || decoded["k"] != "v" {
			t.Fatalf("json roundtrip failed (%s): %s err=%v", db.Dialect(), string(gotRaw), err)
		}

		// 清理
		_, _ = db.Exec(ctx, `DELETE FROM oauth_clients WHERE client_id = $1`, clientID)
		_, _ = db.Exec(ctx, `DELETE FROM agent_app_state WHERE app_name = $1`, appName)
	}

	t.Run("sqlite-types", func(t *testing.T) {
		verifyTypes(t, sqliteDB)
	})

	pgURL := os.Getenv("TEST_DATABASE_URL")
	if pgURL == "" {
		return
	}
	pgDB, err := sqldb.OpenURL(ctx, pgURL)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer pgDB.Close()
	if err := sqldb.MigrateUp(ctx, pgDB, ""); err != nil {
		t.Fatalf("migrate postgres: %v", err)
	}

	t.Run("postgres-types", func(t *testing.T) {
		verifyTypes(t, pgDB)
	})

	// 双库 Schema 契约一致性测试：确保所有表名与列名 100% 对齐，绝不漂移！
	t.Run("schema-contract-parity", func(t *testing.T) {
		pgSchema := loadPGSchema(t, pgDB)
		sqSchema := loadSQLiteSchema(t, sqliteDB)

		if len(pgSchema) != len(sqSchema) {
			t.Fatalf("table count mismatch: postgres=%d sqlite=%d", len(pgSchema), len(sqSchema))
		}
		for table, pgCols := range pgSchema {
			sqCols, ok := sqSchema[table]
			if !ok {
				t.Fatalf("table %q exists in postgres but missing in sqlite", table)
			}
			if len(pgCols) != len(sqCols) {
				t.Fatalf("table %q column count mismatch: pg=%v sqlite=%v", table, pgCols, sqCols)
			}
			for i := range pgCols {
				if pgCols[i] != sqCols[i] {
					t.Fatalf("table %q columns mismatch: pg=%v sqlite=%v", table, pgCols, sqCols)
				}
			}
		}
	})
}

func loadPGSchema(t *testing.T, db *sqldb.DB) map[string][]string {
	t.Helper()
	rows, err := db.Query(context.Background(), `
		SELECT table_name, column_name
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name <> 'goose_db_version'
		ORDER BY table_name, column_name
	`)
	if err != nil {
		t.Fatalf("query pg schema: %v", err)
	}
	defer rows.Close()
	out := make(map[string][]string)
	for rows.Next() {
		var tbl, col string
		if err := rows.Scan(&tbl, &col); err != nil {
			t.Fatalf("scan pg schema: %v", err)
		}
		out[tbl] = append(out[tbl], col)
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}

func loadSQLiteSchema(t *testing.T, db *sqldb.DB) map[string][]string {
	t.Helper()
	rows, err := db.Query(context.Background(), `
		SELECT name FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name <> 'goose_db_version'
		ORDER BY name
	`)
	if err != nil {
		t.Fatalf("query sqlite tables: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan sqlite table: %v", err)
		}
		tables = append(tables, name)
	}
	rows.Close()

	out := make(map[string][]string, len(tables))
	for _, tbl := range tables {
		crows, err := db.Query(context.Background(), fmt.Sprintf(`PRAGMA table_info(%s)`, tbl))
		if err != nil {
			t.Fatalf("pragma table_info(%s): %v", tbl, err)
		}
		for crows.Next() {
			var cid int
			var name, ctype string
			var notnull, pk int
			var dflt sql.NullString
			if err := crows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
				crows.Close()
				t.Fatalf("scan pragma table_info(%s): %v", tbl, err)
			}
			out[tbl] = append(out[tbl], name)
		}
		crows.Close()
		sort.Strings(out[tbl])
	}
	return out
}
