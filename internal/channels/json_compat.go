package channels

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// flexString 用于外部 IM 协议边界解析可能混用 JSON 字符串、数字或 null 的字段
// （例如微信 iLink 的 message_id 实测为 64 位数字，但也可能以字符串或 null 返回）。
// 数值类型通过 json.Number 保留原始字面量，避免大整数经 float64 转换丢失精度。
type flexString string

func (s *flexString) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*s = ""
		return nil
	}
	if trimmed[0] == '"' {
		var str string
		if err := json.Unmarshal(trimmed, &str); err != nil {
			return err
		}
		*s = flexString(str)
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var val any
	if err := dec.Decode(&val); err != nil {
		return err
	}
	switch v := val.(type) {
	case json.Number:
		*s = flexString(v.String())
		return nil
	case bool:
		*s = flexString(strconv.FormatBool(v))
		return nil
	default:
		return fmt.Errorf("flexString: unsupported JSON value %s", string(trimmed))
	}
}

func (s flexString) String() string {
	return string(s)
}

// flexInt64 用于外部 IM 协议边界解析可能混用 JSON 数字、数字字符串或 null 的整型字段
// （例如 ret、errcode、seq、create_time_ms、longpolling_timeout_ms、expires_in 等）。
type flexInt64 int64

func (n *flexInt64) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*n = 0
		return nil
	}
	if trimmed[0] == '"' {
		var str string
		if err := json.Unmarshal(trimmed, &str); err != nil {
			return err
		}
		str = strings.TrimSpace(str)
		if str == "" {
			*n = 0
			return nil
		}
		if v, err := strconv.ParseInt(str, 10, 64); err == nil {
			*n = flexInt64(v)
			return nil
		}
		f, err := strconv.ParseFloat(str, 64)
		if err != nil {
			return fmt.Errorf("flexInt64: cannot parse %q: %w", str, err)
		}
		*n = flexInt64(int64(f))
		return nil
	}
	if v, err := strconv.ParseInt(string(trimmed), 10, 64); err == nil {
		*n = flexInt64(v)
		return nil
	}
	var f float64
	if err := json.Unmarshal(trimmed, &f); err != nil {
		return fmt.Errorf("flexInt64: cannot unmarshal %s: %w", string(trimmed), err)
	}
	*n = flexInt64(int64(f))
	return nil
}

func (n flexInt64) Int64() int64 {
	return int64(n)
}

func (n flexInt64) Int() int {
	return int(n)
}
