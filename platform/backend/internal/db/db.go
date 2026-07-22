// Package db 负责数据库连接与迁移。
// 仅支持 PostgreSQL（pgx/v5）。开发/生产禁 SQLite（见 memory no-sqlite-pg-only）；
// 测试统一走 testutil 连 anp_test（PG），不再保留 sqlite 驱动。
package db

import (
	"context"
	"fmt"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib" // PG 驱动（副作用注册 database/sql，驱动名 "pgx"）
	"github.com/jmoiron/sqlx"
)

// Open 按 DATABASE_URL 打开 PostgreSQL 连接（连接池调优）。
func Open(databaseURL string) (*sqlx.DB, error) {
	driver, dsn, err := parseDSN(databaseURL)
	if err != nil {
		return nil, err
	}
	db, err := sqlx.Connect(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", driver, err)
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	return db, nil
}

// parseDSN 按 URL 前缀解析 (driver, dsn)。
//   - postgres://... / postgresql://...   → ("pgx", 原样 URL)
//
// pgx stdlib 接受标准 postgres:// URL（含 scheme）或 key=value DSN。
// sqlite:// 已废弃（测试全迁 PG，驱动移除）。
func parseDSN(u string) (driver, dsn string, err error) {
	if strings.HasPrefix(u, "postgres://") || strings.HasPrefix(u, "postgresql://") {
		return "pgx", u, nil
	}
	return "", "", fmt.Errorf("不支持的 DATABASE_URL（仅 postgres://），收到 %q", u)
}

// Ping 健康检查。
func Ping(ctx context.Context, db *sqlx.DB) error {
	return db.PingContext(ctx)
}
