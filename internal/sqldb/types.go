package sqldb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Text 是跨数据库的可空文本类型，字段名与语义兼容原 pgtype.Text（String / Valid），
// 用于将全仓业务代码平滑解耦出 github.com/jackc/pgx/v5/pgtype。
type Text struct {
	String string
	Valid  bool
}

func (t Text) MarshalJSON() ([]byte, error) {
	if !t.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(t.String)
}

func (t *Text) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*t = Text{}
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*t = Text{String: s, Valid: true}
	return nil
}

// Timestamptz 是跨数据库的可空 UTC 时间戳类型，字段名与语义兼容原 pgtype.Timestamptz（Time / Valid）。
type Timestamptz struct {
	Time  time.Time
	Valid bool
}

func (t Timestamptz) MarshalJSON() ([]byte, error) {
	if !t.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(t.Time.UTC())
}

func (t *Timestamptz) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*t = Timestamptz{}
		return nil
	}
	var parsed time.Time
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	*t = Timestamptz{Time: parsed.UTC(), Valid: true}
	return nil
}

// Int2 是跨数据库的可空 16 位整数类型，字段名与语义兼容原 pgtype.Int2（Int16 / Valid）。
type Int2 struct {
	Int16 int16
	Valid bool
}

func (n Int2) MarshalJSON() ([]byte, error) {
	if !n.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(n.Int16)
}

func (n *Int2) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*n = Int2{}
		return nil
	}
	var v int16
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*n = Int2{Int16: v, Valid: true}
	return nil
}

// Int4 是跨数据库的可空 32 位整数类型，字段名与语义兼容原 pgtype.Int4（Int32 / Valid）。
type Int4 struct {
	Int32 int32
	Valid bool
}

func (n Int4) MarshalJSON() ([]byte, error) {
	if !n.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(n.Int32)
}

func (n *Int4) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*n = Int4{}
		return nil
	}
	var v int32
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*n = Int4{Int32: v, Valid: true}
	return nil
}

// Int8 是跨数据库的可空 64 位整数类型，字段名与语义兼容原 pgtype.Int8（Int64 / Valid）。
type Int8 struct {
	Int64 int64
	Valid bool
}

func (n Int8) MarshalJSON() ([]byte, error) {
	if !n.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(n.Int64)
}

func (n *Int8) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*n = Int8{}
		return nil
	}
	var v int64
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*n = Int8{Int64: v, Valid: true}
	return nil
}

// Bool 是跨数据库的可空布尔类型，字段名与语义兼容原 pgtype.Bool（Bool / Valid）。
type Bool struct {
	Bool  bool
	Valid bool
}

func (b Bool) MarshalJSON() ([]byte, error) {
	if !b.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(b.Bool)
}

func (b *Bool) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*b = Bool{}
		return nil
	}
	var v bool
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*b = Bool{Bool: v, Valid: true}
	return nil
}

// Time 是跨数据库的纯时刻（HH:MM[:SS]）类型，兼容 PostgreSQL TIME 与 SQLite TEXT，
// 保持 Microseconds / Valid 字段以平滑替代原 pgtype.Time。
type Time struct {
	Microseconds int64
	Valid        bool
}

// FormatHHMMSS 格式化为标准 HH:MM:SS 文本（如 "09:30:00"）。
func (t Time) FormatHHMMSS() string {
	totalSecs := t.Microseconds / 1_000_000
	if totalSecs < 0 {
		totalSecs = 0
	}
	hours := totalSecs / 3600
	mins := (totalSecs % 3600) / 60
	secs := totalSecs % 60
	return fmt.Sprintf("%02d:%02d:%02d", hours, mins, secs)
}

// ParseTimeOfDay 解析 HH:MM、HH:MM:SS 或 HH:MM:SS.ffffff 为 sqldb.Time。
func ParseTimeOfDay(raw string) (Time, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Time{}, fmt.Errorf("empty time_of_day")
	}
	// 兼容可能带日期前缀的值（如 database/sql 对某些驱动返回的 "0000-01-01T09:30:00Z"）。
	if idx := strings.IndexByte(s, 'T'); idx >= 0 {
		s = s[idx+1:]
	}
	if idx := strings.IndexAny(s, "Z+-"); idx >= 0 {
		s = s[:idx]
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return Time{}, fmt.Errorf("invalid time_of_day %q", raw)
	}
	var h, m, sec int64
	if _, err := fmt.Sscanf(parts[0], "%d", &h); err != nil {
		return Time{}, fmt.Errorf("parse hour in %q: %w", raw, err)
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &m); err != nil {
		return Time{}, fmt.Errorf("parse minute in %q: %w", raw, err)
	}
	var fracMicros int64
	if len(parts) == 3 {
		secPart := parts[2]
		if dot := strings.IndexByte(secPart, '.'); dot >= 0 {
			if _, err := fmt.Sscanf(secPart[:dot], "%d", &sec); err != nil {
				return Time{}, fmt.Errorf("parse second in %q: %w", raw, err)
			}
			frac := secPart[dot+1:]
			for len(frac) < 6 {
				frac += "0"
			}
			if len(frac) > 6 {
				frac = frac[:6]
			}
			_, _ = fmt.Sscanf(frac, "%d", &fracMicros)
		} else {
			if _, err := fmt.Sscanf(secPart, "%d", &sec); err != nil {
				return Time{}, fmt.Errorf("parse second in %q: %w", raw, err)
			}
		}
	}
	if h < 0 || h > 23 || m < 0 || m > 59 || sec < 0 || sec > 59 {
		return Time{}, fmt.Errorf("time_of_day out of range %q", raw)
	}
	micros := ((h*60+m)*60+sec)*1_000_000 + fracMicros
	return Time{Microseconds: micros, Valid: true}, nil
}

func decodeTimeOfDay(raw any) (Time, error) {
	switch v := raw.(type) {
	case nil:
		return Time{}, nil
	case string:
		return ParseTimeOfDay(v)
	case []byte:
		return ParseTimeOfDay(string(v))
	case time.Time:
		micros := int64(((v.Hour()*60+v.Minute())*60+v.Second()))*1_000_000 + int64(v.Nanosecond()/1000)
		return Time{Microseconds: micros, Valid: true}, nil
	default:
		return Time{}, fmt.Errorf("cannot scan %T into sqldb.Time", raw)
	}
}

func decodeInt64(raw any) (int64, bool, error) {
	switch v := raw.(type) {
	case nil:
		return 0, false, nil
	case int64:
		return v, true, nil
	case int32:
		return int64(v), true, nil
	case int:
		return int64(v), true, nil
	case sql.NullInt64:
		return v.Int64, v.Valid, nil
	default:
		return 0, false, fmt.Errorf("cannot scan %T into int64", raw)
	}
}
