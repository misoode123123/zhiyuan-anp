// Package db 负责数据库连接与迁移。
// M0：SQLite（modernc.org/sqlite，纯 Go 无 CGO）；后续切 PostgreSQL+pgvector。
package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite" // 纯 Go SQLite 驱动
)

// Open 按 DATABASE_URL 打开数据库连接。
func Open(databaseURL string) (*sqlx.DB, error) {
	driver, dsn, err := parseDSN(databaseURL)
	if err != nil {
		return nil, err
	}
	db, err := sqlx.Connect(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", driver, err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(2)
	return db, nil
}

func parseDSN(u string) (driver, dsn string, err error) {
	if strings.HasPrefix(u, "sqlite://") {
		return "sqlite", strings.TrimPrefix(u, "sqlite://"), nil
	}
	// TODO(切 PG): 装 Docker 后支持 postgres:// (pgx)
	return "", "", fmt.Errorf("M0 仅支持 sqlite://，收到 %q", u)
}

// Ping 健康检查。
func Ping(ctx context.Context, db *sqlx.DB) error {
	return db.PingContext(ctx)
}
