package sqldb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SQLiteTimeLayout 是 SQLite 中所有时间戳列统一使用的定长 UTC 文本格式（30 字节）。
// 固定 9 位小数秒与 Z 时区后缀保证任意两个时间戳的字符串字典序比较与真实时间先后严格一致。
const SQLiteTimeLayout = "2006-01-02T15:04:05.000000000Z"

// jsonParam 显式标记一个 JSON 文档参数，使其在 PostgreSQL（jsonb）和 SQLite（TEXT）下均以
// UTF-8 字符串传递，避免被驱动误判为 bytea / BLOB 导致类型冲突或 json_valid 失败。
type jsonParam struct {
	text  string
	valid bool
}

// JSON 将字节切片包装为非空 JSON 参数；为空时回退为 "{}"。
func JSON(raw []byte) any {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		trimmed = "{}"
	}
	return jsonParam{text: trimmed, valid: true}
}

// JSONString 将字符串包装为非空 JSON 参数；为空时回退为 "{}"。
func JSONString(raw string) any {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		trimmed = "{}"
	}
	return jsonParam{text: trimmed, valid: true}
}

// JSONOrNull 将字节切片包装为可空 JSON 参数；为空时传递 SQL NULL。
func JSONOrNull(raw []byte) any {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return jsonParam{valid: false}
	}
	return jsonParam{text: trimmed, valid: true}
}

// FormatSQLiteTime 将 time.Time 规范化为 SQLite 定长 UTC 时间字符串。
func FormatSQLiteTime(t time.Time) string {
	return t.UTC().Format(SQLiteTimeLayout)
}

// ParseTime 兼容解析 PostgreSQL time.Time 与 SQLite 各类 ISO8601 / RFC3339 时间文本。
func ParseTime(raw string) (time.Time, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty timestamp")
	}
	layouts := []string{
		SQLiteTimeLayout,
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999-07",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, s); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("parse timestamp %q failed", raw)
}

// normalizeArgs 按目标方言将 Go 业务参数规范化为 database/sql 驱动可直接绑定的值。
func normalizeArgs(dialect Dialect, args []any) []any {
	if len(args) == 0 {
		return nil
	}
	out := make([]any, len(args))
	for i, arg := range args {
		out[i] = normalizeArg(dialect, arg)
	}
	return out
}

func normalizeArg(dialect Dialect, arg any) any {
	switch v := arg.(type) {
	case nil:
		return nil
	case jsonParam:
		if !v.valid {
			return nil
		}
		return v.text
	case json.RawMessage:
		if len(v) == 0 {
			return nil
		}
		return string(v)
	case []string:
		if dialect == DialectPostgres {
			return formatPGTextArray(v)
		}
		return formatJSONStringArray(v)
	case []uuid.UUID:
		strs := make([]string, len(v))
		for i, id := range v {
			strs[i] = id.String()
		}
		if dialect == DialectPostgres {
			return formatPGTextArray(strs)
		}
		return formatJSONStringArray(strs)
	case uuid.UUID:
		return v.String()
	case *uuid.UUID:
		if v == nil {
			return nil
		}
		return v.String()
	case uuid.NullUUID:
		if !v.Valid {
			return nil
		}
		return v.UUID.String()
	case time.Time:
		if dialect == DialectSQLite {
			return FormatSQLiteTime(v)
		}
		return v.UTC()
	case *time.Time:
		if v == nil {
			return nil
		}
		if dialect == DialectSQLite {
			return FormatSQLiteTime(*v)
		}
		t := v.UTC()
		return t
	case sql.NullTime:
		if !v.Valid {
			return nil
		}
		if dialect == DialectSQLite {
			return FormatSQLiteTime(v.Time)
		}
		return v.Time.UTC()
	case bool:
		if dialect == DialectSQLite {
			if v {
				return int64(1)
			}
			return int64(0)
		}
		return v
	case *bool:
		if v == nil {
			return nil
		}
		if dialect == DialectSQLite {
			if *v {
				return int64(1)
			}
			return int64(0)
		}
		return *v
	case sql.NullBool:
		if !v.Valid {
			return nil
		}
		if dialect == DialectSQLite {
			if v.Bool {
				return int64(1)
			}
			return int64(0)
		}
		return v.Bool
	case Text:
		if !v.Valid {
			return nil
		}
		return v.String
	case Timestamptz:
		if !v.Valid {
			return nil
		}
		if dialect == DialectSQLite {
			return FormatSQLiteTime(v.Time)
		}
		return v.Time.UTC()
	case Int2:
		if !v.Valid {
			return nil
		}
		return int64(v.Int16)
	case Int4:
		if !v.Valid {
			return nil
		}
		return int64(v.Int32)
	case Int8:
		if !v.Valid {
			return nil
		}
		return v.Int64
	case Bool:
		if !v.Valid {
			return nil
		}
		if dialect == DialectSQLite {
			if v.Bool {
				return int64(1)
			}
			return int64(0)
		}
		return v.Bool
	case Time:
		if !v.Valid {
			return nil
		}
		return v.FormatHHMMSS()
	default:
		return arg
	}
}

// formatPGTextArray 将字符串切片编码为 PostgreSQL 数组字面量，供 text[] / uuid[] 绑定使用。
func formatPGTextArray(items []string) string {
	if len(items) == 0 {
		return "{}"
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, item := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		for j := 0; j < len(item); j++ {
			c := item[j]
			if c == '\\' || c == '"' {
				b.WriteByte('\\')
			}
			b.WriteByte(c)
		}
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

func formatJSONStringArray(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// parseStringArray 同时支持 PostgreSQL 数组字面量（{"a","b"}）与 SQLite JSON 数组（["a","b"]）。
func parseStringArray(raw string) ([]string, error) {
	s := strings.TrimSpace(raw)
	if s == "" || s == "{}" || s == "[]" {
		return []string{}, nil
	}
	if strings.HasPrefix(s, "[") {
		var items []string
		if err := json.Unmarshal([]byte(s), &items); err != nil {
			return nil, err
		}
		if items == nil {
			return []string{}, nil
		}
		return items, nil
	}
	if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
		return parsePGTextArray(s)
	}
	return nil, fmt.Errorf("unsupported array encoding: %q", raw)
}

func parsePGTextArray(s string) ([]string, error) {
	inner := s[1 : len(s)-1]
	if inner == "" {
		return []string{}, nil
	}
	var result []string
	i := 0
	n := len(inner)
	for i < n {
		if inner[i] == '"' {
			i++
			var elem strings.Builder
			for i < n {
				if inner[i] == '\\' && i+1 < n {
					elem.WriteByte(inner[i+1])
					i += 2
					continue
				}
				if inner[i] == '"' {
					i++
					break
				}
				elem.WriteByte(inner[i])
				i++
			}
			result = append(result, elem.String())
			if i < n && inner[i] == ',' {
				i++
			}
			continue
		}
		start := i
		for i < n && inner[i] != ',' {
			i++
		}
		token := inner[start:i]
		if token == "NULL" {
			result = append(result, "")
		} else {
			result = append(result, token)
		}
		if i < n && inner[i] == ',' {
			i++
		}
	}
	return result, nil
}

// Row 是对单行查询结果的薄封装，自动处理双库的时间、数组、JSON、可空指针扫描。
type Row struct {
	dialect Dialect
	row     *sql.Row
	err     error
}

// Scan 将当前行扫描到目标指针中。
func (r *Row) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if r.row == nil {
		return sql.ErrNoRows
	}
	adapted, finishers := adaptScanTargets(r.dialect, dest)
	if err := r.row.Scan(adapted...); err != nil {
		return err
	}
	for _, fn := range finishers {
		if err := fn(); err != nil {
			return err
		}
	}
	return nil
}

// Rows 是对多行查询结果的薄封装。
type Rows struct {
	dialect Dialect
	rows    *sql.Rows
}

func (r *Rows) Next() bool {
	if r == nil || r.rows == nil {
		return false
	}
	return r.rows.Next()
}

func (r *Rows) Err() error {
	if r == nil || r.rows == nil {
		return nil
	}
	return r.rows.Err()
}

func (r *Rows) Close() error {
	if r == nil || r.rows == nil {
		return nil
	}
	return r.rows.Close()
}

func (r *Rows) Columns() ([]string, error) {
	if r == nil || r.rows == nil {
		return nil, nil
	}
	return r.rows.Columns()
}

func (r *Rows) Scan(dest ...any) error {
	if r == nil || r.rows == nil {
		return sql.ErrNoRows
	}
	adapted, finishers := adaptScanTargets(r.dialect, dest)
	if err := r.rows.Scan(adapted...); err != nil {
		return err
	}
	for _, fn := range finishers {
		if err := fn(); err != nil {
			return err
		}
	}
	return nil
}

func adaptScanTargets(dialect Dialect, dest []any) ([]any, []func() error) {
	adapted := make([]any, len(dest))
	var finishers []func() error

	for i, target := range dest {
		switch ptr := target.(type) {
		case *time.Time:
			var raw any
			adapted[i] = &raw
			finishers = append(finishers, func() error {
				t, ok, err := decodeTime(raw)
				if err != nil {
					return err
				}
				if !ok {
					*ptr = time.Time{}
					return nil
				}
				*ptr = t
				return nil
			})
		case **time.Time:
			var raw any
			adapted[i] = &raw
			finishers = append(finishers, func() error {
				t, ok, err := decodeTime(raw)
				if err != nil {
					return err
				}
				if !ok {
					*ptr = nil
					return nil
				}
				*ptr = &t
				return nil
			})
		case *sql.NullTime:
			var raw any
			adapted[i] = &raw
			finishers = append(finishers, func() error {
				t, ok, err := decodeTime(raw)
				if err != nil {
					return err
				}
				if !ok {
					*ptr = sql.NullTime{}
					return nil
				}
				*ptr = sql.NullTime{Time: t, Valid: true}
				return nil
			})
		case **uuid.UUID:
			var nu uuid.NullUUID
			adapted[i] = &nu
			finishers = append(finishers, func() error {
				if !nu.Valid {
					*ptr = nil
					return nil
				}
				id := nu.UUID
				*ptr = &id
				return nil
			})
		case *[]string:
			var raw any
			adapted[i] = &raw
			finishers = append(finishers, func() error {
				items, err := decodeStringSlice(raw)
				if err != nil {
					return err
				}
				*ptr = items
				return nil
			})
		case *[]uuid.UUID:
			var raw any
			adapted[i] = &raw
			finishers = append(finishers, func() error {
				strs, err := decodeStringSlice(raw)
				if err != nil {
					return err
				}
				ids := make([]uuid.UUID, 0, len(strs))
				for _, s := range strs {
					id, err := uuid.Parse(strings.TrimSpace(s))
					if err != nil {
						return fmt.Errorf("parse uuid array element %q: %w", s, err)
					}
					ids = append(ids, id)
				}
				*ptr = ids
				return nil
			})
		case *[]byte:
			var raw any
			adapted[i] = &raw
			finishers = append(finishers, func() error {
				b, err := decodeBytes(raw)
				if err != nil {
					return err
				}
				*ptr = b
				return nil
			})
		case *json.RawMessage:
			var raw any
			adapted[i] = &raw
			finishers = append(finishers, func() error {
				b, err := decodeBytes(raw)
				if err != nil {
					return err
				}
				if b == nil {
					*ptr = nil
					return nil
				}
				*ptr = json.RawMessage(b)
				return nil
			})
		case *bool:
			if dialect == DialectSQLite {
				var raw any
				adapted[i] = &raw
				finishers = append(finishers, func() error {
					b, ok, err := decodeBool(raw)
					if err != nil {
						return err
					}
					if !ok {
						*ptr = false
						return nil
					}
					*ptr = b
					return nil
				})
			} else {
				adapted[i] = ptr
			}
		case **bool:
			var raw any
			adapted[i] = &raw
			finishers = append(finishers, func() error {
				b, ok, err := decodeBool(raw)
				if err != nil {
					return err
				}
				if !ok {
					*ptr = nil
					return nil
				}
				*ptr = &b
				return nil
			})
		case *sql.NullBool:
			var raw any
			adapted[i] = &raw
			finishers = append(finishers, func() error {
				b, ok, err := decodeBool(raw)
				if err != nil {
					return err
				}
				if !ok {
					*ptr = sql.NullBool{}
					return nil
				}
				*ptr = sql.NullBool{Bool: b, Valid: true}
				return nil
			})
		case *Text:
			var ns sql.NullString
			adapted[i] = &ns
			finishers = append(finishers, func() error {
				if !ns.Valid {
					*ptr = Text{}
					return nil
				}
				*ptr = Text{String: ns.String, Valid: true}
				return nil
			})
		case *Timestamptz:
			var raw any
			adapted[i] = &raw
			finishers = append(finishers, func() error {
				t, ok, err := decodeTime(raw)
				if err != nil {
					return err
				}
				if !ok {
					*ptr = Timestamptz{}
					return nil
				}
				*ptr = Timestamptz{Time: t, Valid: true}
				return nil
			})
		case *Int2:
			var raw any
			adapted[i] = &raw
			finishers = append(finishers, func() error {
				n, ok, err := decodeInt64(raw)
				if err != nil {
					return err
				}
				if !ok {
					*ptr = Int2{}
					return nil
				}
				*ptr = Int2{Int16: int16(n), Valid: true}
				return nil
			})
		case *Int4:
			var raw any
			adapted[i] = &raw
			finishers = append(finishers, func() error {
				n, ok, err := decodeInt64(raw)
				if err != nil {
					return err
				}
				if !ok {
					*ptr = Int4{}
					return nil
				}
				*ptr = Int4{Int32: int32(n), Valid: true}
				return nil
			})
		case *Int8:
			var raw any
			adapted[i] = &raw
			finishers = append(finishers, func() error {
				n, ok, err := decodeInt64(raw)
				if err != nil {
					return err
				}
				if !ok {
					*ptr = Int8{}
					return nil
				}
				*ptr = Int8{Int64: n, Valid: true}
				return nil
			})
		case *Bool:
			var raw any
			adapted[i] = &raw
			finishers = append(finishers, func() error {
				b, ok, err := decodeBool(raw)
				if err != nil {
					return err
				}
				if !ok {
					*ptr = Bool{}
					return nil
				}
				*ptr = Bool{Bool: b, Valid: true}
				return nil
			})
		case *Time:
			var raw any
			adapted[i] = &raw
			finishers = append(finishers, func() error {
				tod, err := decodeTimeOfDay(raw)
				if err != nil {
					return err
				}
				*ptr = tod
				return nil
			})
		default:
			adapted[i] = target
		}
	}
	return adapted, finishers
}

func decodeTime(raw any) (time.Time, bool, error) {
	switch v := raw.(type) {
	case nil:
		return time.Time{}, false, nil
	case time.Time:
		return v.UTC(), true, nil
	case string:
		t, err := ParseTime(v)
		return t, err == nil, err
	case []byte:
		t, err := ParseTime(string(v))
		return t, err == nil, err
	default:
		return time.Time{}, false, fmt.Errorf("cannot scan %T into time.Time", raw)
	}
}

func decodeBool(raw any) (bool, bool, error) {
	switch v := raw.(type) {
	case nil:
		return false, false, nil
	case bool:
		return v, true, nil
	case int64:
		return v != 0, true, nil
	case int32:
		return v != 0, true, nil
	case int:
		return v != 0, true, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "t", "yes":
			return true, true, nil
		case "0", "false", "f", "no", "":
			return false, true, nil
		default:
			return false, false, fmt.Errorf("cannot scan %q into bool", v)
		}
	case []byte:
		return decodeBool(string(v))
	default:
		return false, false, fmt.Errorf("cannot scan %T into bool", raw)
	}
}

func decodeBytes(raw any) ([]byte, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case []byte:
		cp := make([]byte, len(v))
		copy(cp, v)
		return cp, nil
	case string:
		return []byte(v), nil
	default:
		return nil, fmt.Errorf("cannot scan %T into []byte", raw)
	}
}

func decodeStringSlice(raw any) ([]string, error) {
	switch v := raw.(type) {
	case nil:
		return []string{}, nil
	case []string:
		cp := make([]string, len(v))
		copy(cp, v)
		return cp, nil
	case string:
		return parseStringArray(v)
	case []byte:
		return parseStringArray(string(v))
	default:
		return nil, fmt.Errorf("cannot scan %T into []string", raw)
	}
}
