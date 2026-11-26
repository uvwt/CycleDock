package sqldb

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// Rebind 将统一书写的 PostgreSQL 编号占位符（$1, $2, ...）按目标方言转换。
//
// 在 SQLite 中转换为原生支持的编号占位符（?1, ?2, ...），避免因同一参数被重复引用
// 或乱序引用而重排 args 切片。转换过程按 SQL 词法扫描，正确跳过字符串字面量、
// 双引号标识符、单行注释与块注释，不会误改正文内容。
func Rebind(dialect Dialect, query string) string {
	if dialect != DialectSQLite {
		return query
	}
	if !strings.ContainsRune(query, '$') {
		return normalizeSQLiteSQL(query)
	}

	var b strings.Builder
	b.Grow(len(query))
	n := len(query)
	i := 0
	for i < n {
		ch := query[i]

		// 单引号字符串字面量：支持 '' 转义。
		if ch == '\'' {
			b.WriteByte(ch)
			i++
			for i < n {
				c := query[i]
				b.WriteByte(c)
				i++
				if c == '\'' {
					if i < n && query[i] == '\'' {
						b.WriteByte(query[i])
						i++
						continue
					}
					break
				}
			}
			continue
		}

		// 双引号标识符：支持 "" 转义。
		if ch == '"' {
			b.WriteByte(ch)
			i++
			for i < n {
				c := query[i]
				b.WriteByte(c)
				i++
				if c == '"' {
					if i < n && query[i] == '"' {
						b.WriteByte(query[i])
						i++
						continue
					}
					break
				}
			}
			continue
		}

		// 单行注释 -- ... \n
		if ch == '-' && i+1 < n && query[i+1] == '-' {
			b.WriteString("--")
			i += 2
			for i < n {
				c := query[i]
				b.WriteByte(c)
				i++
				if c == '\n' {
					break
				}
			}
			continue
		}

		// 块注释 /* ... */
		if ch == '/' && i+1 < n && query[i+1] == '*' {
			b.WriteString("/*")
			i += 2
			for i < n {
				if query[i] == '*' && i+1 < n && query[i+1] == '/' {
					b.WriteString("*/")
					i += 2
					break
				}
				b.WriteByte(query[i])
				i++
			}
			continue
		}

		// PostgreSQL 编号占位符 $1 .. $99999（前面不能紧邻标识符字符，避免误伤标识符内 $）。
		if ch == '$' && i+1 < n && query[i+1] >= '1' && query[i+1] <= '9' {
			prevIsIdent := i > 0 && isIdentChar(query[i-1])
			if !prevIsIdent {
				j := i + 1
				for j < n && query[j] >= '0' && query[j] <= '9' {
					j++
				}
				// 如果紧随字母或下划线，说明不是纯占位符。
				if j == n || !isIdentChar(query[j]) {
					b.WriteByte('?')
					b.WriteString(query[i+1 : j])
					i = j
					continue
				}
			}
		}

		b.WriteByte(ch)
		i++
	}
	return normalizeSQLiteSQL(b.String())
}

const sqliteNowExpr = "strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')"

// normalizeSQLiteSQL 在 SQLite 方言下做词法安全归一化：
//   - now() / now() ± interval '...' -> 定长 30 字节 UTC strftime 表达式
//   - FOR UPDATE [OF <alias>] [SKIP LOCKED|NOWAIT] -> ""（写事务已由 BEGIN IMMEDIATE 串行化）
//   - ::text / ::bigint / ::boolean / ::int / ::jsonb / ::timestamptz 等类型转换后缀 -> ""
//   - IS [NOT] DISTINCT FROM -> IS [NOT]
//   - GREATEST( / LEAST( -> MAX( / MIN(
func normalizeSQLiteSQL(query string) string {
	var b strings.Builder
	b.Grow(len(query) + 64)
	n := len(query)
	i := 0
	for i < n {
		ch := query[i]

		// 单引号字符串字面量原样保留。
		if ch == '\'' {
			b.WriteByte(ch)
			i++
			for i < n {
				c := query[i]
				b.WriteByte(c)
				i++
				if c == '\'' {
					if i < n && query[i] == '\'' {
						b.WriteByte(query[i])
						i++
						continue
					}
					break
				}
			}
			continue
		}

		// 双引号标识符原样保留。
		if ch == '"' {
			b.WriteByte(ch)
			i++
			for i < n {
				c := query[i]
				b.WriteByte(c)
				i++
				if c == '"' {
					if i < n && query[i] == '"' {
						b.WriteByte(query[i])
						i++
						continue
					}
					break
				}
			}
			continue
		}

		// 单行注释原样保留。
		if ch == '-' && i+1 < n && query[i+1] == '-' {
			b.WriteString("--")
			i += 2
			for i < n {
				c := query[i]
				b.WriteByte(c)
				i++
				if c == '\n' {
					break
				}
			}
			continue
		}

		// 块注释原样保留。
		if ch == '/' && i+1 < n && query[i+1] == '*' {
			b.WriteString("/*")
			i += 2
			for i < n {
				if query[i] == '*' && i+1 < n && query[i+1] == '/' {
					b.WriteString("*/")
					i += 2
					break
				}
				b.WriteByte(query[i])
				i++
			}
			continue
		}

		// 去掉 PostgreSQL 类型转换后缀 ::type。
		if ch == ':' && i+1 < n && query[i+1] == ':' {
			j := i + 2
			for j < n && isIdentChar(query[j]) {
				j++
			}
			if j+1 < n && query[j] == '[' && query[j+1] == ']' {
				j += 2
			}
			if j > i+2 {
				typeName := strings.ToLower(query[i+2 : j])
				switch typeName {
				case "text", "bigint", "boolean", "bool", "int", "int4", "int8", "integer", "smallint", "jsonb", "json", "timestamptz", "timestamp", "time", "date", "uuid", "text[]", "uuid[]":
					i = j
					continue
				}
			}
		}

		if isIdentStart(ch) && (i == 0 || !isIdentChar(query[i-1])) {
			if next, repl, ok := matchSQLiteNowExpr(query, i); ok {
				b.WriteString(repl)
				i = next
				continue
			}
			if next, ok := matchForUpdateClause(query, i); ok {
				i = next
				continue
			}
			if next, ok := matchKeywordSeq(query, i, "IS", "DISTINCT", "FROM"); ok {
				b.WriteString("IS NOT")
				i = next
				continue
			}
			if next, ok := matchKeywordSeq(query, i, "IS", "NOT", "DISTINCT", "FROM"); ok {
				b.WriteString("IS")
				i = next
				continue
			}
			if next, ok := matchFuncCall(query, i, "GREATEST"); ok {
				b.WriteString("MAX(")
				i = next
				continue
			}
			if next, ok := matchFuncCall(query, i, "LEAST"); ok {
				b.WriteString("MIN(")
				i = next
				continue
			}
			if next, ok := matchFuncCall(query, i, "BTRIM"); ok {
				b.WriteString("TRIM(")
				i = next
				continue
			}
		}

		b.WriteByte(ch)
		i++
	}
	return b.String()
}

func isIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func skipSpaces(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r' || s[i] == '\n') {
		i++
	}
	return i
}

func matchWord(s string, i int, word string) (int, bool) {
	end := i + len(word)
	if end > len(s) {
		return i, false
	}
	if !strings.EqualFold(s[i:end], word) {
		return i, false
	}
	if end < len(s) && isIdentChar(s[end]) {
		return i, false
	}
	return end, true
}

func matchKeywordSeq(s string, i int, words ...string) (int, bool) {
	pos := i
	for idx, w := range words {
		if idx > 0 {
			next := skipSpaces(s, pos)
			if next == pos {
				return i, false
			}
			pos = next
		}
		next, ok := matchWord(s, pos, w)
		if !ok {
			return i, false
		}
		pos = next
	}
	return pos, true
}

func matchFuncCall(s string, i int, name string) (int, bool) {
	pos, ok := matchWord(s, i, name)
	if !ok {
		return i, false
	}
	pos = skipSpaces(s, pos)
	if pos < len(s) && s[pos] == '(' {
		return pos + 1, true
	}
	return i, false
}

func matchSQLiteNowExpr(s string, i int) (int, string, bool) {
	pos, ok := matchWord(s, i, "NOW")
	if !ok {
		return i, "", false
	}
	pos = skipSpaces(s, pos)
	if pos >= len(s) || s[pos] != '(' {
		return i, "", false
	}
	pos = skipSpaces(s, pos+1)
	if pos >= len(s) || s[pos] != ')' {
		return i, "", false
	}
	pos++

	look := skipSpaces(s, pos)
	if look < len(s) && (s[look] == '-' || s[look] == '+') {
		sign := s[look]
		look2 := skipSpaces(s, look+1)
		if afterKw, ok := matchWord(s, look2, "INTERVAL"); ok {
			look3 := skipSpaces(s, afterKw)
			if look3 < len(s) && s[look3] == '\'' {
				endQuote := strings.IndexByte(s[look3+1:], '\'')
				if endQuote >= 0 {
					rawMod := strings.TrimSpace(s[look3+1 : look3+1+endQuote])
					mod := rawMod
					if sign == '-' {
						mod = "-" + strings.TrimPrefix(rawMod, "+")
					} else {
						mod = "+" + strings.TrimPrefix(rawMod, "+")
					}
					return look3 + 1 + endQuote + 1, fmt.Sprintf("strftime('%%Y-%%m-%%dT%%H:%%M:%%f000000Z', 'now', '%s')", mod), true
				}
			}
		}
	}
	return pos, sqliteNowExpr, true
}

func matchForUpdateClause(s string, i int) (int, bool) {
	pos, ok := matchKeywordSeq(s, i, "FOR", "UPDATE")
	if !ok {
		return i, false
	}
	for {
		next := skipSpaces(s, pos)
		if afterOf, ok := matchWord(s, next, "OF"); ok {
			// 解析逗号分隔的表别名列表：OF a [, b ...]
			p := skipSpaces(s, afterOf)
			for {
				start := p
				for p < len(s) && isIdentChar(s[p]) {
					p++
				}
				if p == start {
					break
				}
				afterIdent := skipSpaces(s, p)
				if afterIdent < len(s) && s[afterIdent] == ',' {
					p = skipSpaces(s, afterIdent+1)
					continue
				}
				break
			}
			pos = p
			continue
		}
		if afterSkip, ok := matchKeywordSeq(s, next, "SKIP", "LOCKED"); ok {
			pos = afterSkip
			continue
		}
		if afterNoWait, ok := matchWord(s, next, "NOWAIT"); ok {
			pos = afterNoWait
			continue
		}
		break
	}
	return pos, true
}

func isIdentChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

// ForUpdate 返回行级排他锁子句。
// SQLite 写事务已通过 BEGIN IMMEDIATE 串行化，因此返回空字符串。
func ForUpdate(dialect Dialect) string {
	if dialect == DialectPostgres {
		return " FOR UPDATE"
	}
	return ""
}

// ForUpdateSkipLocked 返回调度抢占使用的跳过已锁行子句。
// SQLite 写事务串行化且前序事务提交后即改变行状态，后续事务自然跳过已认领记录。
func ForUpdateSkipLocked(dialect Dialect) string {
	if dialect == DialectPostgres {
		return " FOR UPDATE SKIP LOCKED"
	}
	return ""
}

// InPlaceholders 生成从 startIdx（1-based）开始的 count 个编号占位符列表，例如 "$3, $4, $5"。
func InPlaceholders(startIdx, count int) string {
	if count <= 0 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < count; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('$')
		b.WriteString(strconv.Itoa(startIdx + i))
	}
	return b.String()
}

// AppendUUIDArgs 将 UUID 切片展开追加到 args，并返回对应的 IN 占位符列表（不含外层括号）。
func AppendUUIDArgs(args []any, ids []uuid.UUID) (string, []any) {
	start := len(args) + 1
	for _, id := range ids {
		args = append(args, id)
	}
	return InPlaceholders(start, len(ids)), args
}

// AppendStringArgs 将字符串切片展开追加到 args，并返回对应的 IN 占位符列表（不含外层括号）。
func AppendStringArgs(args []any, values []string) (string, []any) {
	start := len(args) + 1
	for _, v := range values {
		args = append(args, v)
	}
	return InPlaceholders(start, len(values)), args
}

// AppendInt64Args 将 int64 切片展开追加到 args，并返回对应的 IN 占位符列表（不含外层括号）。
func AppendInt64Args(args []any, values []int64) (string, []any) {
	start := len(args) + 1
	for _, v := range values {
		args = append(args, v)
	}
	return InPlaceholders(start, len(values)), args
}

// IsWriteQuery 判断顶层 SQL 语句是否可能写入数据库，用于 SQLite 读写分离路由。
// 任何非纯 SELECT（或 CTE 中含 INSERT/UPDATE/DELETE）的语句均路由到写连接池。
func IsWriteQuery(query string) bool {
	trimmed := stripLeadingSQLComments(query)
	if trimmed == "" {
		return false
	}
	upper := strings.ToUpper(trimmed)
	if strings.HasPrefix(upper, "SELECT") || strings.HasPrefix(upper, "EXPLAIN") || strings.HasPrefix(upper, "VALUES") {
		return false
	}
	if strings.HasPrefix(upper, "WITH") {
		// CTE 可能包含数据修改语句（INSERT / UPDATE / DELETE），含写关键字时统一走写池。
		for _, kw := range []string{"INSERT ", "INSERT\n", "INSERT\t", "UPDATE ", "UPDATE\n", "UPDATE\t", "DELETE ", "DELETE\n", "DELETE\t"} {
			if strings.Contains(upper, kw) {
				return true
			}
		}
		return false
	}
	return true
}

func stripLeadingSQLComments(query string) string {
	s := query
	for {
		s = strings.TrimSpace(s)
		if strings.HasPrefix(s, "--") {
			idx := strings.IndexByte(s, '\n')
			if idx < 0 {
				return ""
			}
			s = s[idx+1:]
			continue
		}
		if strings.HasPrefix(s, "/*") {
			idx := strings.Index(s[2:], "*/")
			if idx < 0 {
				return ""
			}
			s = s[2+idx+2:]
			continue
		}
		return s
	}
}

// CheckPlaceholderIndex 用于调试或测试辅助，校验 SQL 占位符编号。
func FormatArgIndex(idx int) string {
	return fmt.Sprintf("$%d", idx)
}
