package appdeploy

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

// newHistStore 建干净 deploy_history 环境（truncate 独立，防与其它 fixture 串数据）。
// 000038 起 deploy_history.app_id 有 FK 指向 appdeploy_application，
// 故连带清 app 三表并要求 Store 层测试先 seedHistApp 建真实应用行。
func newHistStore(t *testing.T) *Store {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "deploy_history", "appdeploy_env", "appdeploy_instance", "appdeploy_application")
	return NewStore(db)
}

// seedHistApp 建一条真实应用行，返回其 ID（deploy_history.app_id FK 要求挂真 app）。
func seedHistApp(t *testing.T, s *Store, name string) *Application {
	t.Helper()
	a := mkApp("ps_1", name)
	if err := s.Create(context.Background(), a); err != nil {
		t.Fatalf("seed app %s: %v", name, err)
	}
	return a
}

// TestDeployHistory_InsertFinishRoundtrip 一行=一次尝试：INSERT 在途 → Finish 终态。
func TestDeployHistory_InsertFinishRoundtrip(t *testing.T) {
	s := newHistStore(t)
	ctx := context.Background()
	app := seedHistApp(t, s, "hist-rt")

	if err := s.InsertDeployHistory(ctx, app.ID, EnvTest, 3, "fixed", "yxt", ""); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// 在途行：result=''，duration/finished 为 NULL
	items, err := s.ListDeployHistoryByApp(ctx, app.ID, 20)
	if err != nil || len(items) != 1 {
		t.Fatalf("在途应 1 行: %v %v", items, err)
	}
	if items[0].Result != "" || items[0].DurationSec != nil || items[0].FinishedAt != nil {
		t.Fatalf("在途行应为空 result/nil duration: %+v", items[0])
	}
	// 终结 success
	if err := s.FinishDeployHistory(ctx, app.ID, EnvTest, 3, "success", "", "", "appdeploy/x-test:v3", 9100); err != nil {
		t.Fatalf("finish: %v", err)
	}
	items, _ = s.ListDeployHistoryByApp(ctx, app.ID, 20)
	if len(items) != 1 || items[0].Result != "success" || items[0].DurationSec == nil || *items[0].DurationSec < 0 {
		t.Fatalf("终态行: %+v", items[0])
	}
	if items[0].Image != "appdeploy/x-test:v3" || items[0].HostPort != 9100 {
		t.Fatalf("成功行应记实态镜像/端口: %+v", items[0])
	}
	// 重复终结 no-op（result='' 守卫）：再次 finish failed 不覆写
	_ = s.FinishDeployHistory(ctx, app.ID, EnvTest, 3, "failed", "later", "", "", 0)
	items, _ = s.ListDeployHistoryByApp(ctx, app.ID, 20)
	if items[0].Result != "success" {
		t.Fatalf("已终态行不得被覆写: %+v", items[0])
	}
}

// TestDeployHistory_OrphanFiltered 孤儿行（result=” 超 30min）不进 List；在途（<30min）可见。
func TestDeployHistory_OrphanFiltered(t *testing.T) {
	s := newHistStore(t)
	ctx := context.Background()
	app := seedHistApp(t, s, "hist-orphan")
	_ = s.InsertDeployHistory(ctx, app.ID, EnvTest, 1, "ai", "yxt", "")
	// backdate 成孤儿
	if _, err := s.db.Exec(`UPDATE deploy_history SET created_at = NOW() - interval '31 minutes' WHERE app_id=$1`, app.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	_ = s.InsertDeployHistory(ctx, app.ID, EnvTest, 2, "ai", "yxt", "") // 在途（新）
	_ = s.InsertDeployHistory(ctx, app.ID, EnvTest, 3, "ai", "yxt", "")
	_ = s.FinishDeployHistory(ctx, app.ID, EnvTest, 3, "failed", "boom", "", "", 0)

	items, err := s.ListDeployHistoryByApp(ctx, app.ID, 20)
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
	app := seedHistApp(t, s, "hist-opnorm")
	if err := s.InsertDeployHistory(ctx, app.ID, EnvTest, 1, "fixed", "", ""); err != nil {
		t.Fatalf("insert: %v", err)
	}
	items, _ := s.ListDeployHistoryByApp(ctx, app.ID, 20)
	if len(items) != 1 || items[0].Operator != "unknown" {
		t.Fatalf("空 operator 应归一 unknown: %+v", items)
	}
}

// TestDeployHistory_FkCascadeDeleteApp 000038 约束回归：删应用级联清 deploy_history
// （与其余 5 张 app 关联表对齐，统计不再残留孤儿行）。
func TestDeployHistory_FkCascadeDeleteApp(t *testing.T) {
	s := newHistStore(t)
	ctx := context.Background()
	app := seedHistApp(t, s, "hist-fk")
	_ = s.InsertDeployHistory(ctx, app.ID, EnvTest, 1, "fixed", "yxt", "")
	_ = s.FinishDeployHistory(ctx, app.ID, EnvTest, 1, "success", "", "", "img:v1", 9100)

	if err := s.Delete(ctx, "ps_1", app.ID); err != nil {
		t.Fatalf("delete app: %v", err)
	}
	items, err := s.ListDeployHistoryByApp(ctx, app.ID, 20)
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("删应用应级联清历史（FK CASCADE），仍剩 %d 行: %+v", len(items), items)
	}
}

// TestDeployHistory_InflightUnique 000038 约束回归：同键 (app_id,env,version) 至多一行
// 在途——并发双击/复合故障双 INSERT 从源头消灭；终态行不占位，同版本可再插新在途行。
// INSERT 冲突报错由调用点 zap.Warn 吞掉（best-effort 不破），此处断言约束本身。
func TestDeployHistory_InflightUnique(t *testing.T) {
	s := newHistStore(t)
	ctx := context.Background()
	app := seedHistApp(t, s, "hist-uniq")

	if err := s.InsertDeployHistory(ctx, app.ID, EnvTest, 1, "fixed", "yxt", ""); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	// 同键第二行在途 → 唯一索引拦截
	if err := s.InsertDeployHistory(ctx, app.ID, EnvTest, 1, "fixed", "yxt", ""); err == nil {
		t.Fatal("同键双在途应被 uq_dephist_inflight 拒绝")
	}
	// 终结后（result≠''）唯一索引不再占位：同键可再插（版本计数器只升不降时正常不会走到，
	// 但约束语义上不能阻碍补录/重试）
	_ = s.FinishDeployHistory(ctx, app.ID, EnvTest, 1, "failed", "boom", "", "", 0)
	if err := s.InsertDeployHistory(ctx, app.ID, EnvTest, 1, "fixed", "yxt", ""); err != nil {
		t.Fatalf("终态行不应占在途唯一位: %v", err)
	}
	// 孤儿 app_id 被 FK 拦截（杜绝新增孤儿）
	if err := s.InsertDeployHistory(ctx, "app_ghost", EnvTest, 1, "fixed", "yxt", ""); err == nil {
		t.Fatal("孤儿 app_id 应被 deploy_history_app_fk 拒绝")
	}
}

// TestDeployStats_Aggregation 统计聚合：按 engine 分组计数/均值中位/每日 trend。
func TestDeployStats_Aggregation(t *testing.T) {
	s := newHistStore(t)
	ctx := context.Background()
	app := seedHistApp(t, s, "hist-agg")
	// fixed: 2 成功（60s, 100s）1 失败（30s, err="docker build 失败: exit status 1"）
	_ = s.InsertDeployHistory(ctx, app.ID, EnvTest, 1, "fixed", "yxt", "")
	_ = s.FinishDeployHistory(ctx, app.ID, EnvTest, 1, "success", "", "", "img:v1", 9100)
	_ = s.InsertDeployHistory(ctx, app.ID, EnvTest, 2, "fixed", "yxt", "")
	_ = s.FinishDeployHistory(ctx, app.ID, EnvTest, 2, "success", "", "", "img:v2", 9100)
	_ = s.InsertDeployHistory(ctx, app.ID, EnvTest, 3, "fixed", "yxt", "")
	_ = s.FinishDeployHistory(ctx, app.ID, EnvTest, 3, "failed", "docker build 失败: exit status 1", "", "", 0)
	// ai: 1 成功 1 失败（同 err 片段 "docker"）
	_ = s.InsertDeployHistory(ctx, app.ID, EnvTest, 4, "ai", "ai-op", "")
	_ = s.FinishDeployHistory(ctx, app.ID, EnvTest, 4, "success", "", "", "img:v4", 9100)
	_ = s.InsertDeployHistory(ctx, app.ID, EnvTest, 5, "ai", "ai-op", "")
	_ = s.FinishDeployHistory(ctx, app.ID, EnvTest, 5, "failed", "docker run 失败: port 已占用", "已自动回滚", "", 0)
	// 时长用 SQL 回填：直接 UPDATE duration_sec 造确定值（Finish 按真实时钟，测试不可假设）
	if _, err := s.db.Exec(`UPDATE deploy_history SET duration_sec = CASE version
		WHEN 1 THEN 60 WHEN 2 THEN 100 WHEN 3 THEN 30 WHEN 4 THEN 200 WHEN 5 THEN 40 END
		WHERE app_id=$1`, app.ID); err != nil {
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
	appC := seedHistApp(t, s, "hist-clamp")
	appOld := seedHistApp(t, s, "hist-clamp-old")
	_ = s.InsertDeployHistory(ctx, appC.ID, EnvTest, 1, "fixed", "yxt", "")
	_ = s.FinishDeployHistory(ctx, appC.ID, EnvTest, 1, "success", "", "", "img", 1)
	// 91 天前的行：days=90 窗外
	_ = s.InsertDeployHistory(ctx, appOld.ID, EnvTest, 1, "fixed", "yxt", "")
	_ = s.FinishDeployHistory(ctx, appOld.ID, EnvTest, 1, "success", "", "", "img", 1)
	if _, err := s.db.Exec(`UPDATE deploy_history SET created_at = NOW() - interval '91 days' WHERE app_id=$1`, appOld.ID); err != nil {
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

// TestDeployHistory_AiChainSuccess AI 链成功：fake opencode + fake inspect → history 行
// engine=ai result=success（版本=1，镜像/端口为实态回填值）。
func TestDeployHistory_AiChainSuccess(t *testing.T) {
	aiInspect = func(ctx context.Context, h *Handler, container string) (bool, int, string) {
		return true, 9100, "appdeploy/ai-demo-test:v1"
	}
	defer func() { aiInspect = nil }()
	origRun := aiOpencodeRun
	aiOpencodeRun = fakeOpencode(t, true)
	defer func() { aiOpencodeRun = origRun }()

	h, _ := newHTTPHandler(t)
	ctx := context.Background()
	a := seedApp(t, h, "ps_1", "ai-demo", t.TempDir())
	h.aiDeploy("ps_1", a.ID, EnvTest, "", "ai-op")

	items, err := h.store.ListDeployHistoryByApp(ctx, a.ID, 20)
	if err != nil || len(items) != 1 {
		t.Fatalf("应恰 1 行: %v %v", items, err)
	}
	it := items[0]
	if it.Engine != "ai" || it.Result != "success" || it.Operator != "ai-op" || it.Version != 1 {
		t.Fatalf("AI 成功行: %+v", it)
	}
	if it.Image != "appdeploy/ai-demo-test:v1" || it.HostPort != 9100 {
		t.Fatalf("成功行应记实态: %+v", it)
	}
}

// TestDeployHistory_AiChainFailRollbackNotes AI 链失败：fake 失败 → history 行 failed，
// notes 含回滚记录（spec：回滚成功也记 failed，详情进 notes）。
func TestDeployHistory_AiChainFailRollbackNotes(t *testing.T) {
	aiInspect = func(ctx context.Context, h *Handler, container string) (bool, int, string) {
		return false, 0, ""
	}
	defer func() { aiInspect = nil }()
	origRun := aiOpencodeRun
	aiOpencodeRun = fakeOpencode(t, false)
	defer func() { aiOpencodeRun = origRun }()

	h, _ := newHTTPHandler(t)
	ctx := context.Background()
	a := seedApp(t, h, "ps_1", "hist-ai2", t.TempDir())
	h.aiDeploy("ps_1", a.ID, EnvTest, "", "ai-op")

	items, _ := h.store.ListDeployHistoryByApp(ctx, a.ID, 20)
	if len(items) != 1 || items[0].Result != "failed" || items[0].Engine != "ai" {
		t.Fatalf("AI 失败行: %+v", items)
	}
	if items[0].ErrorSummary == "" {
		t.Fatalf("失败行应记 error_summary: %+v", items[0])
	}
	// AI 链失败经 aiFailWithRollback：notes 固定记自动回滚标记（spec：回滚不稀释失败率）
	if !strings.Contains(items[0].Notes, "自动回滚") {
		t.Fatalf("失败行 notes 应含自动回滚记录: %+v", items[0])
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

// TestDetail_DeployHistorySection Detail 聚合含 deploy_history 节（最近 20 条 + 空归一 []）。
func TestDetail_DeployHistorySection(t *testing.T) {
	h, _ := newHTTPHandler(t)
	ctx := context.Background()
	a := seedApp(t, h, "ps_1", "hist-detail", t.TempDir())
	_ = h.store.InsertDeployHistory(ctx, a.ID, EnvTest, 1, "fixed", "yxt", "")
	_ = h.store.FinishDeployHistory(ctx, a.ID, EnvTest, 1, "success", "", "", "img:v1", 9100)

	d, err := h.store.Detail(ctx, "ps_1", a.ID)
	if err != nil || d == nil {
		t.Fatalf("detail: %v %v", d, err)
	}
	if len(d.DeployHistory) != 1 || d.DeployHistory[0].Version != 1 {
		t.Fatalf("detail 应含 1 条历史: %+v", d.DeployHistory)
	}

	// 空应用（无历史）→ 空切片非 nil
	b := seedApp(t, h, "ps_1", "no-hist", t.TempDir())
	d2, _ := h.store.Detail(ctx, "ps_1", b.ID)
	if d2.DeployHistory == nil || len(d2.DeployHistory) != 0 {
		t.Fatalf("无历史应空切片: %+v", d2.DeployHistory)
	}
}

// TestHandler_DeployStatsAPI 统计 API：空表 200 零值（days 钳制由 Store 层
// TestDeployStats_DaysClamped 覆盖，此处只测 HTTP 层空表不 500）。
func TestHandler_DeployStatsAPI(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	// 空表
	code, resp := doReq(t, r, http.MethodGet, "/api/v1/appdeploy/deploy-stats?days=30", nil)
	if code != 200 {
		t.Fatalf("空表应 200，得 %d %v", code, resp)
	}
	eng, _ := resp["data"].(map[string]interface{})["engines"].([]interface{})
	if len(eng) != 0 {
		t.Fatalf("空表应 0 引擎组: %v", resp)
	}
}
