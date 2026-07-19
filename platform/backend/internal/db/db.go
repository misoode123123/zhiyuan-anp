// Package db 负责数据库连接与迁移。
// 双驱动：SQLite（modernc.org/sqlite，纯 Go 无 CGO，测试/回退）+ PostgreSQL（pgx/v5，主力）。
// 按 DATABASE_URL 前缀自动选择（sqlite:// / postgres://）。
package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
	_ "github.com/jackc/pgx/v5/stdlib" // PG 驱动（副作用注册 database/sql，驱动名 "pgx"）
	_ "modernc.org/sqlite"             // SQLite 驱动（保留，测试/回退）
)

// Open 按 DATABASE_URL 打开数据库连接。
// PostgreSQL 用较大连接池；SQLite 写锁宜小。
func Open(databaseURL string) (*sqlx.DB, error) {
	driver, dsn, err := parseDSN(databaseURL)
	if err != nil {
		return nil, err
	}
	db, err := sqlx.Connect(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", driver, err)
	}
	if driver == "pgx" {
		db.SetMaxOpenConns(20)
		db.SetMaxIdleConns(5)
	} else {
		// SQLite 写锁，连接数宜小
		db.SetMaxOpenConns(10)
		db.SetMaxIdleConns(2)
	}
	return db, nil
}

// parseDSN 按 URL 前缀解析 (driver, dsn)。
//   - sqlite://path                       → ("sqlite", "path")
//   - postgres://... / postgresql://...   → ("pgx", 原样 URL)
//
// pgx stdlib 接受标准 postgres:// URL（含 scheme）或 key=value DSN。
func parseDSN(u string) (driver, dsn string, err error) {
	if strings.HasPrefix(u, "sqlite://") {
		return "sqlite", strings.TrimPrefix(u, "sqlite://"), nil
	}
	if strings.HasPrefix(u, "postgres://") || strings.HasPrefix(u, "postgresql://") {
		return "pgx", u, nil
	}
	return "", "", fmt.Errorf("不支持的 DATABASE_URL（仅 sqlite:// 或 postgres://），收到 %q", u)
}

// Ping 健康检查。
func Ping(ctx context.Context, db *sqlx.DB) error {
	return db.PingContext(ctx)
}
