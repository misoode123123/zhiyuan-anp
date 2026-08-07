package quota

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

// TestService_UsageTrend_AIAndAPI 验证 AI 调用 / 应用 API 调用按日聚合：
// 插 2 天数据（昨天 3 条 AI 调用 / 今天 2 条；昨天 2 条 API / 今天 1 条 4xx），
// 断言 ai_trend/api_trend 各 2 个点、calls/tokens/success_rate/error_count 正确。
func TestService_UsageTrend_AIAndAPI(t *testing.T) {
	psID, svc := setupPS(t)
	db := testutil.TestDB(t)
	ctx := context.Background()
	now := time.Now()
	yesterday := now.AddDate(0, 0, -1)

	// 清历史快照 / 用量 / appgw 日志（防其他用例残留污染同 psID 之外的行；本用例 psID 唯一所以多余但稳妥）
	testutil.Truncate(t, db, "capability_usage", "appgw_access_log", "db_size_snapshot")

	// AI 调用：昨天 3 条（2 成功 1 失败，tokens/latency 各异），今天 2 条全成功
	insAI := func(success bool, inT, outT, lat int, at time.Time) {
		t.Helper()
		if _, err := db.Exec(
			`INSERT INTO capability_usage (id, project_space_id, skill_id, success, input_tokens, output_tokens, latency_ms, created_at)
			 VALUES ($1,$2,'skl_t',$3,$4,$5,$6,$7)`,
			"usg_"+uuid.NewString()[:18], psID, success, inT, outT, lat, at); err != nil {
			t.Fatalf("seed capability_usage: %v", err)
		}
	}
	insAI(true, 100, 50, 200, yesterday)
	insAI(true, 200, 100, 400, yesterday)
	insAI(false, 50, 0, 900, yesterday)
	insAI(true, 300, 150, 300, now)
	insAI(true, 100, 50, 250, now)

	// 应用 API 调用：昨天 2 条（200/500），今天 1 条 200
	insAPI := func(status, lat int, at time.Time) {
		t.Helper()
		if _, err := db.Exec(
			`INSERT INTO appgw_access_log (id, project_space_id, app_id, app_code, env, method, path, status, latency_ms, created_at)
			 VALUES ($1,$2,'app_t','app_t','prod','GET','/x',$3,$4,$5)`,
			"al_"+uuid.NewString()[:18], psID, status, lat, at); err != nil {
			t.Fatalf("seed appgw_access_log: %v", err)
		}
	}
	insAPI(200, 50, yesterday)
	insAPI(500, 500, yesterday)
	insAPI(200, 80, now)

	tr, err := svc.UsageTrend(ctx, psID, 7)
	if err != nil {
		t.Fatalf("UsageTrend: %v", err)
	}
	if tr.Days != 7 {
		t.Errorf("Days = %d, want 7", tr.Days)
	}
	if len(tr.AITrend) != 2 {
		t.Fatalf("AITrend len = %d, want 2（昨天+今天）", len(tr.AITrend))
	}
	if len(tr.APITrend) != 2 {
		t.Fatalf("APITrend len = %d, want 2", len(tr.APITrend))
	}

	// AI 趋势按 day 升序：第 0 点=昨天，第 1 点=今天
	y := tr.AITrend[0]
	td := tr.AITrend[1]
	if y.Calls != 3 {
		t.Errorf("昨天 AI calls = %d, want 3", y.Calls)
	}
	if y.InputTokens != 350 || y.OutputTokens != 150 {
		t.Errorf("昨天 AI tokens in/out = %d/%d, want 350/150", y.InputTokens, y.OutputTokens)
	}
	// 成功率：2/3 ≈ 0.667
	if got, want := y.SuccessRate, 2.0/3.0; got < want-0.01 || got > want+0.01 {
		t.Errorf("昨天 AI success_rate = %.3f, want ~%.3f", got, want)
	}
	if td.Calls != 2 {
		t.Errorf("今天 AI calls = %d, want 2", td.Calls)
	}
	if td.SuccessRate != 1.0 {
		t.Errorf("今天 AI success_rate = %.3f, want 1.0", td.SuccessRate)
	}

	// API 趋势：昨天 2 calls / 1 错误，今天 1 call / 0 错误
	ay := tr.APITrend[0]
	ad := tr.APITrend[1]
	if ay.Calls != 2 || ay.ErrorCount != 1 {
		t.Errorf("昨天 API calls/err = %d/%d, want 2/1", ay.Calls, ay.ErrorCount)
	}
	if ay.SuccessRate != 0.5 {
		t.Errorf("昨天 API success_rate = %.3f, want 0.5", ay.SuccessRate)
	}
	if ad.Calls != 1 || ad.ErrorCount != 0 {
		t.Errorf("今天 API calls/err = %d/%d, want 1/0", ad.Calls, ad.ErrorCount)
	}
}

// TestService_UsageTrend_DBSize 验证库大小趋势：插 3 条快照（前天/昨天/今天），
// 断言 db_size_trend 按日升序且每天取「当日末值」（同日多条取最大 created_at）。
func TestService_UsageTrend_DBSize(t *testing.T) {
	psID, svc := setupPS(t)
	db := testutil.TestDB(t)
	ctx := context.Background()
	testutil.Truncate(t, db, "db_size_snapshot")

	now := time.Now()
	// 锚定到当日 00:00 再减天：避免 CI 在 UTC 下午/晚间运行时，now 的时分加上
	// +2h/+10h 越过午夜，把「昨天」快照滚到「今天」，日期分组数从 3 塌成 2。
	daysAgo := func(n int) time.Time {
		mid := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		return mid.AddDate(0, 0, -n)
	}

	// 前天 1 条；昨天 2 条（早晚各一，晚的更大）；今天 1 条
	// 当日末值语义：昨天应取「晚」那条
	ins := func(bytes int64, at time.Time) {
		t.Helper()
		if _, err := db.Exec(
			`INSERT INTO db_size_snapshot (id, project_space_id, total_size_bytes, created_at)
			 VALUES ($1,$2,$3,$4)`,
			"dss_"+uuid.NewString()[:18], psID, bytes, at); err != nil {
			t.Fatalf("seed db_size_snapshot: %v", err)
		}
	}
	ins(10*1024*1024, daysAgo(2))                   // 前天 10MB
	ins(20*1024*1024, daysAgo(1).Add(2*time.Hour))  // 昨天早 20MB
	ins(30*1024*1024, daysAgo(1).Add(10*time.Hour)) // 昨天晚 30MB ← 当日末值
	ins(45*1024*1024, daysAgo(0))                   // 今天 45MB

	tr, err := svc.UsageTrend(ctx, psID, 7)
	if err != nil {
		t.Fatalf("UsageTrend: %v", err)
	}
	if len(tr.DBSizeTrend) != 3 {
		t.Fatalf("DBSizeTrend len = %d, want 3（前/昨/今）", len(tr.DBSizeTrend))
	}
	// 升序：前天 10MB，昨天 30MB（末值），今天 45MB
	want := []int{10, 30, 45}
	for i, w := range want {
		if tr.DBSizeTrend[i].SizeMB != w {
			t.Errorf("DBSizeTrend[%d].SizeMB = %d, want %d", i, tr.DBSizeTrend[i].SizeMB, w)
		}
	}
	// 当前总大小：本测试未注入 PGSizeChecker → calcTotalDBSizeMb 返回 0（不阻塞）
	if tr.DBSizeCurrentMB != 0 {
		t.Errorf("DBSizeCurrentMB = %d, want 0（未注入 PGSizeChecker）", tr.DBSizeCurrentMB)
	}
	// usage 字段非空（复用 3a）
	if tr.Usage == nil {
		t.Error("Usage 应非空（复用 3a）")
	}
}

// TestService_UsageTrend_Empty 无数据项目：所有趋势空切片，不报错。
func TestService_UsageTrend_Empty(t *testing.T) {
	psID, svc := setupPS(t)
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "capability_usage", "appgw_access_log", "db_size_snapshot")

	tr, err := svc.UsageTrend(context.Background(), psID, 30)
	if err != nil {
		t.Fatalf("UsageTrend 空数据: %v", err)
	}
	if len(tr.AITrend) != 0 || len(tr.APITrend) != 0 || len(tr.DBSizeTrend) != 0 {
		t.Errorf("空数据应全空，got ai=%d api=%d db=%d",
			len(tr.AITrend), len(tr.APITrend), len(tr.DBSizeTrend))
	}
	if tr.Usage == nil {
		t.Error("Usage 应非空（GetOrCreate 兜底建默认配额）")
	}
}

// TestService_UsageTrend_DaysClamp days 越界夹到默认 30。
func TestService_UsageTrend_DaysClamp(t *testing.T) {
	psID, svc := setupPS(t)
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "capability_usage", "appgw_access_log", "db_size_snapshot")

	cases := []struct{ in, want int }{{0, 30}, {-1, 30}, {91, 30}, {7, 7}, {90, 90}}
	for _, c := range cases {
		tr, err := svc.UsageTrend(context.Background(), psID, c.in)
		if err != nil {
			t.Fatalf("UsageTrend(%d): %v", c.in, err)
		}
		if tr.Days != c.want {
			t.Errorf("days=%d → Days=%d, want %d", c.in, tr.Days, c.want)
		}
	}
}
