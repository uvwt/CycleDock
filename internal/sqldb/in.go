package sqldb

import (
	"strconv"
	"strings"
)

// AppendInPlaceholders 生成跨 PostgreSQL 与 SQLite 的 IN ($k, $k+1, ...) 占位符列表，
// 并将对应参数追加到 args，避免在双库 SQL 中使用 PostgreSQL 专属的 = ANY($n::type[])。
// 调用方需确保 items 非空。
func AppendInPlaceholders[T any](args *[]any, items []T) string {
	parts := make([]string, len(items))
	for i, item := range items {
		*args = append(*args, item)
		parts[i] = "$" + strconv.Itoa(len(*args))
	}
	return strings.Join(parts, ", ")
}
