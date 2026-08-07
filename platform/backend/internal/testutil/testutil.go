// Package testutil 提供 PG 测试辅助：连 anp_test 库 + 跑迁移建表 + 清表隔离。
// 替代 sqlite :memory:（sqlite 单测漏 PG 类型 bug，见 memory sqlite-test-pg-type-trap）。
package testutil

import (
	"context"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib" // 驱动名 "pgx"
	"github.com/jmoiron/sqlx"

	"zhiyuan-anp/platform/backend/internal/db"
)

// DefaultTestDBURL 默认测试库（.28 anp_test，与 anp_dev 隔离）。
const DefaultTestDBURL = "postgres://anp:anp_dev_pwd@10.10.0.28:5432/anp_test?sslmode=disable"

// TestDB 返回 anp_test 连接，首次跑迁移建平台全表（幂等）。
// 用 ANP_TEST_DATABASE_URL 环境变量覆盖连接串。调用方负责 Truncate 清表隔离。
func TestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	url := os.Getenv("ANP_TEST_DATABASE_URL")
	if url == "" {
		url = DefaultTestDBURL
	}
	d, err := sqlx.Connect("pgx", url)
	if err != nil {
		t.Fatalf("连 anp_test 失败: %v（确认 .28 可达或设 ANP_TEST_DATABASE_URL）", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := db.Migrate(context.Background(), d); err != nil {
		t.Fatalf("anp_test 迁移失败: %v", err)
	}
	return d
}

// Truncate 清空指定表（RESTART IDENTITY CASCADE），测试间数据隔离。
func Truncate(t *testing.T, d *sqlx.DB, tables ...string) {
	t.Helper()
	for _, tb := range tables {
		if _, err := d.Exec("TRUNCATE TABLE " + tb + " RESTART IDENTITY CASCADE"); err != nil {
			t.Fatalf("truncate %s: %v", tb, err)
		}
	}
}
