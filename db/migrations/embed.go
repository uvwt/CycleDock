package migrations

import "embed"

// FS 嵌入 PostgreSQL 与 SQLite 两套完整迁移脚本，供单二进制启动与测试直接使用。
//
//go:embed postgres/*.sql sqlite/*.sql
var FS embed.FS
