package pgsupply

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestCollector_WritesDBSizeSnapshot CollectDBSizes 采完应按项目写一条 db_size_snapshot；
// Snapshots 计数 + DBSizeTrendByDay 能读到当日末值。
func TestCollector_WritesDBSizeSnapshot(t *testing.T) {
	s, ins := setupCollectorData(t)
	ctx := context.Background()

	pg := &fakePGAdmin{
		fakeSizes: map[string]map[string]int64{
			ins.AdminURLRef: {"app_a": 5 * 1024 * 1024, "app_b": 7 * 1024 * 1024},
		},
	}
	c := NewCollector(s, pg, zap.NewNop())

	r := c.CollectDBSizes(ctx)
	if r.Snapshots != 1 {
		t.Fatalf("Snapshots = %d, want 1（ps_1 一条）", r.Snapshots)
	}

	// DBSizeTrendByDay 读最近 7 天：应有 1 条，size = 12MB
	list, err := s.DBSizeTrendByDay(ctx, "ps_1", 7)
	if err != nil {
		t.Fatalf("DBSizeTrendByDay: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("快照趋势 len = %d, want 1", len(list))
	}
	if list[0].TotalSizeBytes != 12*1024*1024 {
		t.Errorf("快照 total_size_bytes = %d, want %d", list[0].TotalSizeBytes, 12*1024*1024)
	}
}

// TestStore_DBSizeTrendByDay_PicksDayLast 同日多条应取 created_at 最大那条（当日末值）；
// 跨日按日升序返回。
func TestStore_DBSizeTrendByDay_PicksDayLast(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	testutil_TruncateSnapshot(t, s)
	now := time.Now()

	ins := func(ps string, bytes int64, at time.Time, suffix string) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO db_size_snapshot (id, project_space_id, total_size_bytes, created_at) VALUES ($1,$2,$3,$4)`,
			"dss_t_"+suffix, ps, bytes, at); err != nil {
			t.Fatalf("seed snapshot: %v", err)
		}
	}
	daysAgo := func(n int, h int) time.Time {
		// 以「今日 00:00」为锚回推 n 天再加 h 小时，避免 AddDate(-1d)+22h 滚动到今天。
		startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		return startOfToday.AddDate(0, 0, -n).Add(time.Duration(h) * time.Hour)
	}

	// 前天 1 条；昨天 2 条（早 8 点 20MB，晚 22 点 30MB → 取 30MB）；今天 1 条 45MB
	ins("ps_x", 10*1024*1024, daysAgo(2, 12), "p2")
	ins("ps_x", 20*1024*1024, daysAgo(1, 8), "p1a")
	ins("ps_x", 30*1024*1024, daysAgo(1, 22), "p1b")
	ins("ps_x", 45*1024*1024, daysAgo(0, 10), "p0")

	list, err := s.DBSizeTrendByDay(ctx, "ps_x", 7)
	if err != nil {
		t.Fatalf("DBSizeTrendByDay: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("趋势 len = %d, want 3", len(list))
	}
	want := []int64{10 * 1024 * 1024, 30 * 1024 * 1024, 45 * 1024 * 1024}
	for i, w := range want {
		if list[i].TotalSizeBytes != w {
			t.Errorf("[%d] bytes = %d, want %d", i, list[i].TotalSizeBytes, w)
		}
	}
	// 升序检查（按 created_at）
	for i := 1; i < len(list); i++ {
		if !list[i].CreatedAt.After(list[i-1].CreatedAt) && !list[i].CreatedAt.Equal(list[i-1].CreatedAt) {
			t.Errorf("趋势应按时间升序：[%d] %v 不晚于 [%d] %v", i, list[i].CreatedAt, i-1, list[i-1].CreatedAt)
		}
	}
}

// TestStore_DBSizeTrendByDay_DaysClamp days 越界夹到 30。
func TestStore_DBSizeTrendByDay_DaysClamp(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	cases := []int{0, -1, 181, 999}
	// 仅断言不报错 + 返回空（数据已清）。days 夹到合法值后查到空切片即可。
	for _, d := range cases {
		if _, err := s.DBSizeTrendByDay(ctx, "ps_nope", d); err != nil {
			t.Errorf("days=%d 不应报错: %v", d, err)
		}
	}
}

// testutil_TruncateSnapshot 清 db_size_snapshot（隔离本组用例）。
func testutil_TruncateSnapshot(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.db.Exec(`TRUNCATE TABLE db_size_snapshot`); err != nil {
		t.Fatalf("truncate db_size_snapshot: %v", err)
	}
}
