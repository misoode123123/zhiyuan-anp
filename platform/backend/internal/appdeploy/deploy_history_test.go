package appdeploy

import (
	"context"
	"testing"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

// newHistStore 建干净 deploy_history 环境（truncate 独立，防与其它 fixture 串数据）。
func newHistStore(t *testing.T) *Store {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "deploy_history")
	return NewStore(db)
}

// TestDeployHistory_InsertFinishRoundtrip 一行=一次尝试：INSERT 在途 → Finish 终态。
func TestDeployHistory_InsertFinishRoundtrip(t *testing.T) {
	s := newHistStore(t)
	ctx := context.Background()

	if err := s.InsertDeployHistory(ctx, "app_x", EnvTest, 3, "fixed", "yxt", ""); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// 在途行：result=''，duration/finished 为 NULL
	items, err := s.ListDeployHistoryByApp(ctx, "app_x", 20)
	if err != nil || len(items) != 1 {
		t.Fatalf("在途应 1 行: %v %v", items, err)
	}
	if items[0].Result != "" || items[0].DurationSec != nil || items[0].FinishedAt != nil {
		t.Fatalf("在途行应为空 result/nil duration: %+v", items[0])
	}
	// 终结 success
	if err := s.FinishDeployHistory(ctx, "app_x", EnvTest, 3, "success", "", "", "appdeploy/x-test:v3", 9100); err != nil {
		t.Fatalf("finish: %v", err)
	}
	items, _ = s.ListDeployHistoryByApp(ctx, "app_x", 20)
	if len(items) != 1 || items[0].Result != "success" || items[0].DurationSec == nil || *items[0].DurationSec < 0 {
		t.Fatalf("终态行: %+v", items[0])
	}
	if items[0].Image != "appdeploy/x-test:v3" || items[0].HostPort != 9100 {
		t.Fatalf("成功行应记实态镜像/端口: %+v", items[0])
	}
	// 重复终结 no-op（result='' 守卫）：再次 finish failed 不覆写
	_ = s.FinishDeployHistory(ctx, "app_x", EnvTest, 3, "failed", "later", "", "", 0)
	items, _ = s.ListDeployHistoryByApp(ctx, "app_x", 20)
	if items[0].Result != "success" {
		t.Fatalf("已终态行不得被覆写: %+v", items[0])
	}
}

// TestDeployHistory_OrphanFiltered 孤儿行（result=” 超 30min）不进 List；在途（<30min）可见。
func TestDeployHistory_OrphanFiltered(t *testing.T) {
	s := newHistStore(t)
	ctx := context.Background()
	_ = s.InsertDeployHistory(ctx, "app_o", EnvTest, 1, "ai", "yxt", "")
	// backdate 成孤儿
	if _, err := s.db.Exec(`UPDATE deploy_history SET created_at = NOW() - interval '31 minutes' WHERE app_id='app_o'`); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	_ = s.InsertDeployHistory(ctx, "app_o", EnvTest, 2, "ai", "yxt", "") // 在途（新）
	_ = s.InsertDeployHistory(ctx, "app_o", EnvTest, 3, "ai", "yxt", "")
	_ = s.FinishDeployHistory(ctx, "app_o", EnvTest, 3, "failed", "boom", "", "", 0)

	items, err := s.ListDeployHistoryByApp(ctx, "app_o", 20)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("孤儿应被过滤（剩在途 v2 + 终态 v3 共 2 行），得到 %d: %+v", len(items), items)
	}
}

// TestDeployHistory_OperatorNormalized 空 operator 归一 unknown。
func TestDeployHistory_OperatorNormalized(t *testing.T) {
	s := newHistStore(t)
	ctx := context.Background()
	if err := s.InsertDeployHistory(ctx, "app_n", EnvTest, 1, "fixed", "", ""); err != nil {
		t.Fatalf("insert: %v", err)
	}
	items, _ := s.ListDeployHistoryByApp(ctx, "app_n", 20)
	if len(items) != 1 || items[0].Operator != "unknown" {
		t.Fatalf("空 operator 应归一 unknown: %+v", items)
	}
}

// TestDeployStats_Aggregation 统计聚合：按 engine 分组计数/均值中位/每日 trend。
func TestDeployStats_Aggregation(t *testing.T) {
	s := newHistStore(t)
	ctx := context.Background()
	// fixed: 2 成功（60s, 100s）1 失败（30s, err="docker build 失败: exit status 1"）
	_ = s.InsertDeployHistory(ctx, "app_s", EnvTest, 1, "fixed", "yxt", "")
	_ = s.FinishDeployHistory(ctx, "app_s", EnvTest, 1, "success", "", "", "img:v1", 9100)
	_ = s.InsertDeployHistory(ctx, "app_s", EnvTest, 2, "fixed", "yxt", "")
	_ = s.FinishDeployHistory(ctx, "app_s", EnvTest, 2, "success", "", "", "img:v2", 9100)
	_ = s.InsertDeployHistory(ctx, "app_s", EnvTest, 3, "fixed", "yxt", "")
	_ = s.FinishDeployHistory(ctx, "app_s", EnvTest, 3, "failed", "docker build 失败: exit status 1", "", "", 0)
	// ai: 1 成功 1 失败（同 err 片段 "docker"）
	_ = s.InsertDeployHistory(ctx, "app_s", EnvTest, 4, "ai", "ai-op", "")
	_ = s.FinishDeployHistory(ctx, "app_s", EnvTest, 4, "success", "", "", "img:v4", 9100)
	_ = s.InsertDeployHistory(ctx, "app_s", EnvTest, 5, "ai", "ai-op", "")
	_ = s.FinishDeployHistory(ctx, "app_s", EnvTest, 5, "failed", "docker run 失败: port 已占用", "已自动回滚", "", 0)
	// 时长用 SQL 回填：直接 UPDATE duration_sec 造确定值（Finish 按真实时钟，测试不可假设）
	if _, err := s.db.Exec(`UPDATE deploy_history SET duration_sec = CASE version
		WHEN 1 THEN 60 WHEN 2 THEN 100 WHEN 3 THEN 30 WHEN 4 THEN 200 WHEN 5 THEN 40 END
		WHERE app_id='app_s'`); err != nil {
		t.Fatalf("fix durations: %v", err)
	}

	res, err := s.DeployStats(ctx, 30)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if len(res.Engines) != 2 {
		t.Fatalf("应 2 引擎组: %+v", res.Engines)
	}
	var fx, ai *EngineStats
	for i := range res.Engines {
		if res.Engines[i].Engine == "fixed" {
			fx = &res.Engines[i]
		}
		if res.Engines[i].Engine == "ai" {
			ai = &res.Engines[i]
		}
	}
	if fx == nil || ai == nil {
		t.Fatalf("缺引擎组: %+v", res.Engines)
	}
	if fx.Success != 2 || fx.Failed != 1 {
		t.Fatalf("fixed 计数: %+v", fx)
	}
	if fx.AvgSec != (60+100+30)/3 || fx.MedSec != 60 {
		t.Fatalf("fixed 均值/中位: avg=%d med=%d", fx.AvgSec, fx.MedSec)
	}
	if ai.Success != 1 || ai.Failed != 1 {
		t.Fatalf("ai 计数: %+v", ai)
	}
	// fixed 组仅 1 条错误：docker/build/exit/status 并列各 1 次，字典序 tie-break 使 build 居首。
	// 此处钉「docker 必在列表」；频次排名语义由 TestTopErrorFragments（docker 3 次居首）钉死。
	dockerHit := false
	for _, f := range fx.TopErrors {
		if f.Fragment == "docker" {
			dockerHit = true
		}
	}
	if !dockerHit {
		t.Fatalf("fixed top error 应含 docker: %+v", fx.TopErrors)
	}
	if len(res.Daily) != 2 {
		t.Fatalf("每日 trend 应 2 行（两引擎同日）: %+v", res.Daily)
	}
}

// TestDeployStats_Empty 空表零值不 500。
func TestDeployStats_Empty(t *testing.T) {
	s := newHistStore(t)
	res, err := s.DeployStats(context.Background(), 30)
	if err != nil {
		t.Fatalf("空表应不报错: %v", err)
	}
	if res == nil || res.Engines == nil || res.Daily == nil {
		t.Fatalf("空表应返回零值结构（非 nil 切片）: %+v", res)
	}
	if len(res.Engines) != 0 || len(res.Daily) != 0 {
		t.Fatalf("空表应 0 组: %+v", res)
	}
}

// TestDeployStats_DaysClamped days 钳制（0→1, 999→90），超窗行不进统计。
func TestDeployStats_DaysClamped(t *testing.T) {
	s := newHistStore(t)
	ctx := context.Background()
	_ = s.InsertDeployHistory(ctx, "app_c", EnvTest, 1, "fixed", "yxt", "")
	_ = s.FinishDeployHistory(ctx, "app_c", EnvTest, 1, "success", "", "", "img", 1)
	// 91 天前的行：days=90 窗外
	_ = s.InsertDeployHistory(ctx, "app_old", EnvTest, 1, "fixed", "yxt", "")
	_ = s.FinishDeployHistory(ctx, "app_old", EnvTest, 1, "success", "", "", "img", 1)
	if _, err := s.db.Exec(`UPDATE deploy_history SET created_at = NOW() - interval '91 days' WHERE app_id='app_old'`); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	res, err := s.DeployStats(ctx, 0) // 钳到 1
	if err != nil {
		t.Fatalf("days=0 应钳 1 不报错: %v", err)
	}
	total := 0
	for _, e := range res.Engines {
		total += e.Success + e.Failed
	}
	if total != 1 {
		t.Fatalf("91 天前行不应计入（期望 1），得 %d", total)
	}
	if _, err := s.DeployStats(ctx, 999); err != nil { // 钳到 90，不报错
		t.Fatalf("days=999 应钳 90 不报错: %v", err)
	}
}

// TestDeployHistory_FixedChainFailPath 固定链失败路径：buildAndDeploy（无 docker 环境
// Build 必失败）→ deploy_history 落 failed 行（result/errSummary/duration）。
// 成功路径（result=success）本机无 docker 不可测，由 .28 e2e 验证（计划「实现精化」第 4 条）。
func TestDeployHistory_FixedChainFailPath(t *testing.T) {
	h, _ := newHTTPHandler(t)
	ctx := context.Background()
	a := seedApp(t, h, "ps_1", "hist-fail", t.TempDir())
	h.buildAndDeploy("ps_1", a.ID, "", EnvTest, "", "", "yxt")

	items, err := h.store.ListDeployHistoryByApp(ctx, a.ID, 20)
	if err != nil || len(items) != 1 {
		t.Fatalf("应恰 1 行: %v %v", items, err)
	}
	it := items[0]
	if it.Engine != "fixed" || it.Result != "failed" || it.Operator != "yxt" || it.Version != 1 {
		t.Fatalf("失败行字段: %+v", it)
	}
	if it.ErrorSummary == "" {
		t.Fatalf("失败行应记 error_summary: %+v", it)
	}
	if it.DurationSec == nil || *it.DurationSec < 0 {
		t.Fatalf("失败行应有 duration: %+v", it)
	}
}

// TestTopErrorFragments 分词词频：标点切 + ≥4 rune 片段 + topN + 大小写归一。
func TestTopErrorFragments(t *testing.T) {
	errs := []string{
		"Docker build 失败: exit status 1",
		"docker pull 超时",
		"DOCKER! build again",
	}
	got := topErrorFragments(errs, 5)
	if len(got) == 0 || got[0].Fragment != "docker" || got[0].Count != 3 {
		t.Fatalf("docker 应 3 次居首: %+v", got)
	}
	// 短中文词（失败/超时=2 字）被 ≥4 过滤
	for _, f := range got {
		if f.Fragment == "失败" || f.Fragment == "超时" {
			t.Fatalf("2 字词不应入选: %+v", got)
		}
	}
	if len(topErrorFragments(nil, 5)) != 0 {
		t.Fatal("空输入应空输出")
	}
}
