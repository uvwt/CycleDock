package sqldb

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// ErrNoRows 统一表示查询未命中任何行，与 database/sql.ErrNoRows 保持同一哨兵错误。
var ErrNoRows = sql.ErrNoRows

// IsNoRows 判断错误是否为“未找到记录”。
func IsNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

// ConstraintName 返回数据库错误携带的约束名（PG 返回 PgError.ConstraintName，SQLite 无独立约束名时返回空字符串）。
func ConstraintName(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}
	return ""
}

// IsUniqueViolation 判断错误是否为唯一约束或主键冲突（PG 23505 / SQLite UNIQUE or PRIMARYKEY）。
func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	var sqErr *sqlite.Error
	if errors.As(err, &sqErr) {
		code := sqErr.Code()
		return code == sqlite3.SQLITE_CONSTRAINT_UNIQUE || code == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
	}
	return false
}

// IsForeignKeyViolation 判断错误是否为外键约束冲突（PG 23503 / SQLite FOREIGNKEY）。
func IsForeignKeyViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23503"
	}
	var sqErr *sqlite.Error
	if errors.As(err, &sqErr) {
		return sqErr.Code() == sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY
	}
	return false
}

// IsCheckViolation 判断错误是否为 CHECK 约束冲突（PG 23514 / SQLite CHECK）。
func IsCheckViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23514"
	}
	var sqErr *sqlite.Error
	if errors.As(err, &sqErr) {
		return sqErr.Code() == sqlite3.SQLITE_CONSTRAINT_CHECK
	}
	return false
}

// IsNotNullViolation 判断错误是否为非空约束冲突（PG 23502 / SQLite NOTNULL）。
func IsNotNullViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23502"
	}
	var sqErr *sqlite.Error
	if errors.As(err, &sqErr) {
		return sqErr.Code() == sqlite3.SQLITE_CONSTRAINT_NOTNULL
	}
	return false
}

// IsBusyOrLocked 判断错误是否为 SQLite 瞬时锁冲突（用于测试断言或极端情况退避）。
func IsBusyOrLocked(err error) bool {
	if err == nil {
		return false
	}
	var sqErr *sqlite.Error
	if errors.As(err, &sqErr) {
		primary := sqErr.Code() & 0xff
		return primary == sqlite3.SQLITE_BUSY || primary == sqlite3.SQLITE_LOCKED
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "sqlite_busy") || strings.Contains(msg, "database is locked")
}
