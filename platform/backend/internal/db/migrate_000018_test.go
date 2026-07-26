package db_test

import (
	"context"
	"testing"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

// TestMigrate_000018_PerformanceSchema 验证 000018 应用后：
// 三表有 user_id、codews_session 存在、attendance_record 已删。
// 用 testutil.TestDB（跑全量真实迁移到 public schema，golang-migrate 幂等跳过已应用、应用新增 000018）。
func TestMigrate_000018_PerformanceSchema(t *testing.T) {
	db := testutil.TestDB(t)
	ctx := context.Background()

	for _, c := range []struct{ tbl, col string }{
		{"code_task", "user_id"},
		{"change_request", "user_id"},
		{"conversation", "user_id"},
	} {
		var n int
		err := db.GetContext(ctx, &n,
			`SELECT COUNT(*) FROM information_schema.columns WHERE table_name=$1 AND column_name=$2`,
			c.tbl, c.col)
		if err != nil || n != 1 {
			t.Fatalf("%s.%s 应存在: n=%d err=%v", c.tbl, c.col, n, err)
		}
	}

	var tbl int
	if err := db.GetContext(ctx, &tbl, `SELECT COUNT(*) FROM information_schema.tables WHERE table_name='codews_session'`); err != nil || tbl != 1 {
		t.Fatalf("codews_session 表应存在: n=%d err=%v", tbl, err)
	}
	if err := db.GetContext(ctx, &tbl, `SELECT COUNT(*) FROM information_schema.tables WHERE table_name='attendance_record'`); err != nil || tbl != 0 {
		t.Fatalf("attendance_record 表应已删除: n=%d err=%v", tbl, err)
	}
}
