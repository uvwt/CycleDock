package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// Dialect 表示底层关系数据库方言。CycleDock 仅支持 PostgreSQL 与 SQLite。
type Dialect string

const (
	DialectPostgres Dialect = "postgres"
	DialectSQLite   Dialect = "sqlite"
)

// Config 描述数据库连接配置。
type Config struct {
	Driver      Dialect
	DatabaseURL string
	SQLitePath  string
}

// ConfigFromEnv 从标准环境变量解析数据库配置。
// 优先级：
//  1. 显式设置 CYCLEDOCK_DB_DRIVER（postgres / sqlite）
//  2. 根据 DATABASE_URL 协议自动推断（sqlite://、sqlite:、file:、*.db -> sqlite；postgres:// -> postgres）
//  3. 未设 DATABASE_URL 但设置了 CYCLEDOCK_SQLITE_PATH -> sqlite
func ConfigFromEnv() (Config, error) {
	driverRaw := strings.ToLower(strings.TrimSpace(os.Getenv("CYCLEDOCK_DB_DRIVER")))
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	sqlitePath := strings.TrimSpace(os.Getenv("CYCLEDOCK_SQLITE_PATH"))

	if driverRaw != "" && driverRaw != string(DialectPostgres) && driverRaw != string(DialectSQLite) && driverRaw != "postgresql" && driverRaw != "sqlite3" {
		return Config{}, fmt.Errorf("unsupported CYCLEDOCK_DB_DRIVER %q (allowed: postgres, sqlite)", driverRaw)
	}
	if driverRaw == "postgresql" {
		driverRaw = string(DialectPostgres)
	}
	if driverRaw == "sqlite3" {
		driverRaw = string(DialectSQLite)
	}

	if databaseURL == "" && sqlitePath != "" {
		if driverRaw == string(DialectPostgres) {
			return Config{}, fmt.Errorf("DATABASE_URL is required when CYCLEDOCK_DB_DRIVER=postgres")
		}
		return Config{
			Driver:     DialectSQLite,
			SQLitePath: sqlitePath,
		}, nil
	}
	if databaseURL == "" && driverRaw == string(DialectSQLite) {
		return Config{
			Driver:     DialectSQLite,
			SQLitePath: "./data/cycledock.db",
		}, nil
	}
	if databaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL (or CYCLEDOCK_DB_DRIVER=sqlite / CYCLEDOCK_SQLITE_PATH) is required")
	}

	inferred, err := DetectDialect(databaseURL)
	if err != nil {
		return Config{}, err
	}
	if driverRaw != "" && Dialect(driverRaw) != inferred {
		return Config{}, fmt.Errorf("CYCLEDOCK_DB_DRIVER=%s conflicts with DATABASE_URL dialect %s", driverRaw, inferred)
	}
	return Config{
		Driver:      inferred,
		DatabaseURL: databaseURL,
		SQLitePath:  sqlitePath,
	}, nil
}

// DetectDialect 根据连接串或路径自动判断数据库方言。
func DetectDialect(databaseURL string) (Dialect, error) {
	raw := strings.TrimSpace(databaseURL)
	if raw == "" {
		return "", fmt.Errorf("empty database URL")
	}
	lower := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(lower, "postgres://"), strings.HasPrefix(lower, "postgresql://"):
		return DialectPostgres, nil
	case strings.HasPrefix(lower, "sqlite://"), strings.HasPrefix(lower, "sqlite:"), strings.HasPrefix(lower, "file:"), raw == ":memory:":
		return DialectSQLite, nil
	default:
		// 允许直接传入本地文件路径（如 /data/cycledock.db 或 C:\data\cycledock.db）。
		if strings.Contains(lower, "://") {
			return "", fmt.Errorf("unsupported database URL scheme in %q (only postgres and sqlite are supported)", raw)
		}
		return DialectSQLite, nil
	}
}

// Result 封装 Exec 的影响行数结果。
type Result struct {
	rowsAffected int64
}

func (r Result) RowsAffected() int64 {
	return r.rowsAffected
}

// Querier 统一 *DB 与 *Tx 的最小查询契约，便于 store 与各服务共享写读辅助方法。
type Querier interface {
	Dialect() Dialect
	Exec(ctx context.Context, query string, args ...any) (Result, error)
	Query(ctx context.Context, query string, args ...any) (*Rows, error)
	QueryRow(ctx context.Context, query string, args ...any) *Row
}

// DB 是 CycleDock 统一的数据库连接池封装，同时支持 PostgreSQL 与 SQLite。
//
// SQLite 采用生产级读写分离连接池 + 单写连接 + BEGIN IMMEDIATE 事务模型：
//   - readDB 多连接并发只读，不阻塞读请求；
//   - writeDB 单连接配合 writeMu 串行化所有写操作和写事务，从根源避免多协程写锁升级引发 SQLITE_BUSY。
type DB struct {
	dialect Dialect

	// PostgreSQL 资源
	pgPool *pgxpool.Pool
	pgDB   *sql.DB

	// SQLite 资源
	writeDB     *sql.DB
	readDB      *sql.DB
	writeMu     sync.Mutex
	sqlitePath  string
	inMemory    bool
	sessionLock *keyedLocks
}

// OpenURL 通过单一连接串打开 PostgreSQL 或 SQLite 数据库。
func OpenURL(ctx context.Context, databaseURL string) (*DB, error) {
	dialect, err := DetectDialect(databaseURL)
	if err != nil {
		return nil, err
	}
	return Open(ctx, Config{
		Driver:      dialect,
		DatabaseURL: databaseURL,
	})
}

// Open 根据配置打开数据库并执行健康检查与 SQLite WAL 初始化。
func Open(ctx context.Context, cfg Config) (*DB, error) {
	dialect := cfg.Driver
	if dialect == "" {
		if cfg.DatabaseURL != "" {
			var err error
			dialect, err = DetectDialect(cfg.DatabaseURL)
			if err != nil {
				return nil, err
			}
		} else if cfg.SQLitePath != "" {
			dialect = DialectSQLite
		} else {
			return nil, fmt.Errorf("database configuration is empty")
		}
	}

	switch dialect {
	case DialectPostgres:
		return openPostgres(ctx, cfg.DatabaseURL)
	case DialectSQLite:
		target := cfg.DatabaseURL
		if target == "" {
			target = cfg.SQLitePath
		}
		return openSQLite(ctx, target)
	default:
		return nil, fmt.Errorf("unsupported database dialect %q", dialect)
	}
}

func openPostgres(ctx context.Context, databaseURL string) (*DB, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	stdDB := stdlib.OpenDBFromPool(pool)
	return &DB{
		dialect: DialectPostgres,
		pgPool:  pool,
		pgDB:    stdDB,
	}, nil
}

func openSQLite(ctx context.Context, rawTarget string) (*DB, error) {
	filePath, inMemory, err := parseSQLiteTarget(rawTarget)
	if err != nil {
		return nil, err
	}
	if !inMemory {
		dir := filepath.Dir(filePath)
		if dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("create sqlite directory %q: %w", dir, err)
			}
		}
	}

	writeDSN := buildSQLiteDSN(filePath, inMemory, false)
	writeDB, err := sql.Open("sqlite", writeDSN)
	if err != nil {
		return nil, fmt.Errorf("open sqlite write connection: %w", err)
	}
	// 单写连接确保同一进程内所有写语句与写事务串行进入 SQLite，彻底消除并发写锁升级死锁。
	writeDB.SetMaxOpenConns(1)
	writeDB.SetMaxIdleConns(1)
	writeDB.SetConnMaxLifetime(0)

	if err := writeDB.PingContext(ctx); err != nil {
		_ = writeDB.Close()
		return nil, fmt.Errorf("ping sqlite write connection: %w", err)
	}
	if !inMemory {
		// 显式启用 WAL 并验证；仅写连接设置一次，文件级持久化生效。
		if _, err := writeDB.ExecContext(ctx, "PRAGMA journal_mode = WAL"); err != nil {
			_ = writeDB.Close()
			return nil, fmt.Errorf("enable sqlite WAL: %w", err)
		}
	}

	var readDB *sql.DB
	if inMemory {
		// 内存库各连接独立，读与写必须共享唯一连接。
		readDB = writeDB
	} else {
		readDSN := buildSQLiteDSN(filePath, false, true)
		readDB, err = sql.Open("sqlite", readDSN)
		if err != nil {
			_ = writeDB.Close()
			return nil, fmt.Errorf("open sqlite read pool: %w", err)
		}
		maxReads := runtime.NumCPU()
		if maxReads < 4 {
			maxReads = 4
		}
		if maxReads > 16 {
			maxReads = 16
		}
		readDB.SetMaxOpenConns(maxReads)
		readDB.SetMaxIdleConns(maxReads)
		readDB.SetConnMaxLifetime(0)
		if err := readDB.PingContext(ctx); err != nil {
			_ = readDB.Close()
			_ = writeDB.Close()
			return nil, fmt.Errorf("ping sqlite read pool: %w", err)
		}
	}

	return &DB{
		dialect:     DialectSQLite,
		writeDB:     writeDB,
		readDB:      readDB,
		sqlitePath:  filePath,
		inMemory:    inMemory,
		sessionLock: newKeyedLocks(),
	}, nil
}

func parseSQLiteTarget(raw string) (string, bool, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false, fmt.Errorf("empty sqlite path")
	}
	if s == ":memory:" || strings.EqualFold(s, "sqlite://:memory:") || strings.EqualFold(s, "sqlite::memory:") || strings.EqualFold(s, "file::memory:") {
		return ":memory:", true, nil
	}
	if strings.HasPrefix(strings.ToLower(s), "sqlite://") {
		rest := s[len("sqlite://"):]
		// 去掉可能附带的查询参数，由 buildSQLiteDSN 统一注入标准 PRAGMA。
		if idx := strings.IndexByte(rest, '?'); idx >= 0 {
			rest = rest[:idx]
		}
		if rest == "" || rest == ":memory:" {
			return ":memory:", true, nil
		}
		// Windows 路径 sqlite:///C:/foo.db 解析时去掉前导多余斜杠。
		if len(rest) >= 3 && rest[0] == '/' && rest[2] == ':' {
			rest = rest[1:]
		}
		return filepath.Clean(rest), false, nil
	}
	if strings.HasPrefix(strings.ToLower(s), "sqlite:") {
		rest := s[len("sqlite:"):]
		if idx := strings.IndexByte(rest, '?'); idx >= 0 {
			rest = rest[:idx]
		}
		if rest == "" || rest == ":memory:" {
			return ":memory:", true, nil
		}
		return filepath.Clean(rest), false, nil
	}
	if strings.HasPrefix(strings.ToLower(s), "file:") {
		rest := s[len("file:"):]
		if idx := strings.IndexByte(rest, '?'); idx >= 0 {
			rest = rest[:idx]
		}
		if rest == "" || rest == ":memory:" {
			return ":memory:", true, nil
		}
		return filepath.Clean(rest), false, nil
	}
	return filepath.Clean(s), false, nil
}

func buildSQLiteDSN(filePath string, inMemory, readOnly bool) string {
	// 通过连接串 _pragma 确保连接池中的每条新连接都强制开启外键、busy_timeout 和低内存缓存策略。
	params := url.Values{}
	params.Add("_pragma", "foreign_keys(1)")
	params.Add("_pragma", "busy_timeout(10000)")
	params.Add("_pragma", "temp_store(MEMORY)")
	params.Add("_pragma", "cache_size(-16000)")
	if !inMemory {
		params.Add("_pragma", "synchronous(NORMAL)")
	}
	if readOnly {
		params.Add("_pragma", "query_only(1)")
	}
	if inMemory {
		return "file::memory:?" + params.Encode()
	}
	// 使用标准 file: URI 统一兼容 Windows（file:///C:/...）与 Linux（file:///...）。
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		absPath = filePath
	}
	slashed := filepath.ToSlash(absPath)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	u := &url.URL{
		Scheme:   "file",
		Path:     slashed,
		RawQuery: params.Encode(),
	}
	return u.String()
}

func (db *DB) Dialect() Dialect {
	return db.dialect
}

func (db *DB) IsSQLite() bool {
	return db.dialect == DialectSQLite
}

func (db *DB) IsPostgres() bool {
	return db.dialect == DialectPostgres
}

// SQLitePath 返回 SQLite 数据库文件路径（非 SQLite 或内存模式返回空或 ":memory:"）。
func (db *DB) SQLitePath() string {
	return db.sqlitePath
}

// StdDB 返回底层用于管理或迁移的 *sql.DB（PostgreSQL 返回 pgxpool stdlib 连接，SQLite 返回写连接）。
func (db *DB) StdDB() *sql.DB {
	if db.dialect == DialectPostgres {
		return db.pgDB
	}
	return db.writeDB
}

func (db *DB) Ping(ctx context.Context) error {
	if db.dialect == DialectPostgres {
		return db.pgPool.Ping(ctx)
	}
	return db.writeDB.PingContext(ctx)
}

func (db *DB) Close() error {
	if db.dialect == DialectPostgres {
		var err error
		if db.pgDB != nil {
			err = db.pgDB.Close()
		}
		if db.pgPool != nil {
			db.pgPool.Close()
		}
		return err
	}
	var errs []error
	if db.readDB != nil && db.readDB != db.writeDB {
		if err := db.readDB.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if db.writeDB != nil {
		if err := db.writeDB.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (db *DB) Exec(ctx context.Context, query string, args ...any) (Result, error) {
	bound := Rebind(db.dialect, query)
	normArgs := normalizeArgs(db.dialect, args)
	if db.dialect == DialectPostgres {
		res, err := db.pgDB.ExecContext(ctx, bound, normArgs...)
		if err != nil {
			return Result{}, err
		}
		n, _ := res.RowsAffected()
		return Result{rowsAffected: n}, nil
	}
	db.writeMu.Lock()
	defer db.writeMu.Unlock()
	res, err := db.writeDB.ExecContext(ctx, bound, normArgs...)
	if err != nil {
		return Result{}, err
	}
	n, _ := res.RowsAffected()
	return Result{rowsAffected: n}, nil
}

func (db *DB) Query(ctx context.Context, query string, args ...any) (*Rows, error) {
	bound := Rebind(db.dialect, query)
	normArgs := normalizeArgs(db.dialect, args)
	if db.dialect == DialectPostgres {
		rows, err := db.pgDB.QueryContext(ctx, bound, normArgs...)
		if err != nil {
			return nil, err
		}
		return &Rows{dialect: db.dialect, rows: rows}, nil
	}
	if IsWriteQuery(bound) {
		// SQLite 写语句带 RETURNING 时走写连接并受锁保护，防止与并发事务竞争。
		// 为避免持有锁跨越整个 Rows 迭代，写语句带多行 RETURNING 应放在事务内或立即消费。
		db.writeMu.Lock()
		rows, err := db.writeDB.QueryContext(ctx, bound, normArgs...)
		db.writeMu.Unlock()
		if err != nil {
			return nil, err
		}
		return &Rows{dialect: db.dialect, rows: rows}, nil
	}
	rows, err := db.readDB.QueryContext(ctx, bound, normArgs...)
	if err != nil {
		return nil, err
	}
	return &Rows{dialect: db.dialect, rows: rows}, nil
}

func (db *DB) QueryRow(ctx context.Context, query string, args ...any) *Row {
	bound := Rebind(db.dialect, query)
	normArgs := normalizeArgs(db.dialect, args)
	if db.dialect == DialectPostgres {
		return &Row{
			dialect: db.dialect,
			row:     db.pgDB.QueryRowContext(ctx, bound, normArgs...),
		}
	}
	if IsWriteQuery(bound) {
		db.writeMu.Lock()
		defer db.writeMu.Unlock()
		return &Row{
			dialect: db.dialect,
			row:     db.writeDB.QueryRowContext(ctx, bound, normArgs...),
		}
	}
	return &Row{
		dialect: db.dialect,
		row:     db.readDB.QueryRowContext(ctx, bound, normArgs...),
	}
}

// Tx 封装一次数据库事务。
// 在 PostgreSQL 上对应标准 *sql.Tx；在 SQLite 上独占写连接并显式开启 BEGIN IMMEDIATE。
type Tx struct {
	dialect Dialect
	pgTx    *sql.Tx

	// SQLite 显式事务连接
	db       *DB
	sqConn   *sql.Conn
	finished bool
}

// WithTx 在一次事务内执行 fn，返回 nil 时自动提交，出错或 panic 时回滚。
func (db *DB) WithTx(ctx context.Context, fn func(tx *Tx) error) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Begin 开启读写事务。SQLite 下自动使用 BEGIN IMMEDIATE，避免读事务升级写锁导致死锁。
func (db *DB) Begin(ctx context.Context) (*Tx, error) {
	return db.BeginTx(ctx, nil)
}

// BeginTx 开启数据库事务。
func (db *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*Tx, error) {
	if db.dialect == DialectPostgres {
		tx, err := db.pgDB.BeginTx(ctx, opts)
		if err != nil {
			return nil, err
		}
		return &Tx{dialect: DialectPostgres, pgTx: tx}, nil
	}

	// SQLite 只读事务走 readDB 即可，不抢写锁。
	if opts != nil && opts.ReadOnly && !db.inMemory {
		tx, err := db.readDB.BeginTx(ctx, opts)
		if err != nil {
			return nil, err
		}
		return &Tx{dialect: DialectSQLite, pgTx: tx}, nil
	}

	// SQLite 读写事务：先加进程写锁，再独占连接执行 BEGIN IMMEDIATE。
	db.writeMu.Lock()
	conn, err := db.writeDB.Conn(ctx)
	if err != nil {
		db.writeMu.Unlock()
		return nil, fmt.Errorf("acquire sqlite write connection: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		_ = conn.Close()
		db.writeMu.Unlock()
		return nil, fmt.Errorf("begin sqlite immediate transaction: %w", err)
	}
	return &Tx{
		dialect: DialectSQLite,
		db:      db,
		sqConn:  conn,
	}, nil
}

func (tx *Tx) Dialect() Dialect {
	return tx.dialect
}

func (tx *Tx) Exec(ctx context.Context, query string, args ...any) (Result, error) {
	bound := Rebind(tx.dialect, query)
	normArgs := normalizeArgs(tx.dialect, args)
	var (
		res sql.Result
		err error
	)
	if tx.sqConn != nil {
		res, err = tx.sqConn.ExecContext(ctx, bound, normArgs...)
	} else {
		res, err = tx.pgTx.ExecContext(ctx, bound, normArgs...)
	}
	if err != nil {
		return Result{}, err
	}
	n, _ := res.RowsAffected()
	return Result{rowsAffected: n}, nil
}

func (tx *Tx) Query(ctx context.Context, query string, args ...any) (*Rows, error) {
	bound := Rebind(tx.dialect, query)
	normArgs := normalizeArgs(tx.dialect, args)
	var (
		rows *sql.Rows
		err  error
	)
	if tx.sqConn != nil {
		rows, err = tx.sqConn.QueryContext(ctx, bound, normArgs...)
	} else {
		rows, err = tx.pgTx.QueryContext(ctx, bound, normArgs...)
	}
	if err != nil {
		return nil, err
	}
	return &Rows{dialect: tx.dialect, rows: rows}, nil
}

func (tx *Tx) QueryRow(ctx context.Context, query string, args ...any) *Row {
	bound := Rebind(tx.dialect, query)
	normArgs := normalizeArgs(tx.dialect, args)
	if tx.sqConn != nil {
		return &Row{
			dialect: tx.dialect,
			row:     tx.sqConn.QueryRowContext(ctx, bound, normArgs...),
		}
	}
	return &Row{
		dialect: tx.dialect,
		row:     tx.pgTx.QueryRowContext(ctx, bound, normArgs...),
	}
}

// ForUpdate 返回行级写锁子句：PostgreSQL 返回 " FOR UPDATE"，SQLite 返回空字符串
//（SQLite 写事务已通过 BEGIN IMMEDIATE 串行化）。
func (db *DB) ForUpdate() string {
	if db.dialect == DialectPostgres {
		return " FOR UPDATE"
	}
	return ""
}

// ForUpdate 返回行级写锁子句（与 DB.ForUpdate 一致）。
func (tx *Tx) ForUpdate() string {
	if tx.dialect == DialectPostgres {
		return " FOR UPDATE"
	}
	return ""
}

// AdvisoryLock 在事务范围内获取按 key 隔离的互斥锁。
// PostgreSQL 使用事务级 pg_advisory_xact_lock；SQLite 写事务已由 BEGIN IMMEDIATE 串行化。
func (tx *Tx) AdvisoryLock(ctx context.Context, key string) error {
	if tx.dialect != DialectPostgres {
		return nil
	}
	_, err := tx.pgTx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", key)
	return err
}

func (tx *Tx) Commit(ctx context.Context) error {
	if tx.finished {
		return sql.ErrTxDone
	}
	tx.finished = true
	if tx.sqConn != nil {
		_, err := tx.sqConn.ExecContext(ctx, "COMMIT")
		closeErr := tx.sqConn.Close()
		tx.db.writeMu.Unlock()
		if err != nil {
			return err
		}
		return closeErr
	}
	return tx.pgTx.Commit()
}

func (tx *Tx) Rollback(ctx context.Context) error {
	if tx.finished {
		return nil
	}
	tx.finished = true
	if tx.sqConn != nil {
		// 回滚必须使用未被取消的后台上下文，确保连接干净归还。
		_, err := tx.sqConn.ExecContext(context.Background(), "ROLLBACK")
		closeErr := tx.sqConn.Close()
		tx.db.writeMu.Unlock()
		if err != nil {
			return err
		}
		return closeErr
	}
	err := tx.pgTx.Rollback()
	if errors.Is(err, sql.ErrTxDone) {
		return nil
	}
	return err
}

// WithSessionLock 获取跨请求/跨实例的会话级互斥锁并执行 fn。
// PostgreSQL 持有独立连接上的 pg_advisory_lock；SQLite 使用进程内 keyed mutex。
func (db *DB) WithSessionLock(ctx context.Context, key string, fn func(context.Context) error) error {
	if db.dialect == DialectPostgres {
		conn, err := db.pgDB.Conn(ctx)
		if err != nil {
			return err
		}
		defer conn.Close()
		lockID := hashLockKey(key)
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", lockID); err != nil {
			return err
		}
		defer func() {
			_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", lockID)
		}()
		return fn(ctx)
	}
	unlock, err := db.sessionLock.lock(ctx, key)
	if err != nil {
		return err
	}
	defer unlock()
	return fn(ctx)
}

// TrySessionLock 尝试非阻塞获取会话级互斥锁（用于后台清理等单实例任务）。
func (db *DB) TrySessionLock(ctx context.Context, key string) (func(), bool, error) {
	if db.dialect == DialectPostgres {
		conn, err := db.pgDB.Conn(ctx)
		if err != nil {
			return nil, false, err
		}
		lockID := hashLockKey(key)
		var locked bool
		if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", lockID).Scan(&locked); err != nil {
			_ = conn.Close()
			return nil, false, err
		}
		if !locked {
			_ = conn.Close()
			return nil, false, nil
		}
		var once sync.Once
		unlock := func() {
			once.Do(func() {
				_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", lockID)
				_ = conn.Close()
			})
		}
		return unlock, true, nil
	}
	unlock, ok := db.sessionLock.tryLock(key)
	return unlock, ok, nil
}

func hashLockKey(key string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return int64(h.Sum64())
}

// keyedLocks 为单机 SQLite 提供按 key 隔离的互斥锁与非阻塞 TryLock。
type keyedLocks struct {
	mu    sync.Mutex
	locks map[string]chan struct{}
}

func newKeyedLocks() *keyedLocks {
	return &keyedLocks{locks: make(map[string]chan struct{})}
}

func (k *keyedLocks) get(key string) chan struct{} {
	k.mu.Lock()
	defer k.mu.Unlock()
	ch, ok := k.locks[key]
	if !ok {
		ch = make(chan struct{}, 1)
		k.locks[key] = ch
	}
	return ch
}

func (k *keyedLocks) lock(ctx context.Context, key string) (func(), error) {
	ch := k.get(key)
	select {
	case ch <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() { <-ch })
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (k *keyedLocks) tryLock(key string) (func(), bool) {
	ch := k.get(key)
	select {
	case ch <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() { <-ch })
		}, true
	default:
		return nil, false
	}
}
