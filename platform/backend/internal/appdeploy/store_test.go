package appdeploy

import (
	"context"
	"strings"
	"testing"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

// newTestStore 连 anp_test PG（testutil 跑迁移建平台全表）+ 清 appdeploy 三表隔离。
// 替代 sqlite :memory:（sqlite 漏 PG 类型 bug，如 is_secret bool/int；见 memory sqlite-test-pg-type-trap）。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "appdeploy_env", "appdeploy_instance", "appdeploy_application")
	return NewStore(db)
}

// mkApp 构造一条已注册应用（id 由 Create 填充）。
func mkApp(ps, name string) *Application {
	return &Application{ProjectSpaceID: ps, Name: name, RepoDir: "/data/repos/" + name, InternalPort: 8080}
}

// mkExternalApp 构造一条 external 应用（B 类轻接入：无 repo/端口，外部地址直填）。
func mkExternalApp(ps, name, extURL string) *Application {
	return &Application{
		ProjectSpaceID: ps, Name: name,
		DeployMode: AppExternal, ExternalURL: extURL,
		Status: "running",
	}
}

// TestStore_CreateExternal external 应用落库后读回，deploy_mode/external_url/status 正确。
// 覆盖 B 类轻接入：Create 不再只写 managed 列，要支持 external 分支字段。
func TestStore_CreateExternal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mkExternalApp("ps_1", "存量ERP", "http://10.10.0.28:8088")
	if err := s.Create(ctx, a); err != nil {
		t.Fatalf("create external: %v", err)
	}
	if !strings.HasPrefix(a.ID, "app_") {
		t.Fatalf("ID 应以 app_ 开头，得到 %s", a.ID)
	}
	got, err := s.GetByAppID(ctx, a.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.DeployMode != AppExternal {
		t.Fatalf("deploy_mode 应 external，得到 %s", got.DeployMode)
	}
	if got.ExternalURL != "http://10.10.0.28:8088" {
		t.Fatalf("external_url 不匹配: %s", got.ExternalURL)
	}
	if got.Status != "running" {
		t.Fatalf("external 应用 status 应 running，得到 %s", got.Status)
	}
}

// TestStore_CreateManagedDefault 不指定 deploy_mode 时默认 managed（向后兼容）。
func TestStore_CreateManagedDefault(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "snake")
	if err := s.Create(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, _ := s.GetByAppID(ctx, a.ID)
	if got.DeployMode != AppManaged {
		t.Fatalf("未指定 deploy_mode 应默认 managed，得到 %s", got.DeployMode)
	}
	if got.ExternalURL != "" {
		t.Fatalf("managed 应用 external_url 应空，得到 %s", got.ExternalURL)
	}
}

// TestStore_CreateDefaults 新建应用应自动补 ID 和 registered 状态。
func TestStore_CreateDefaults(t *testing.T) {
	s := newTestStore(t)
	a := mkApp("ps_1", "snake")
	if err := s.Create(context.Background(), a); err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasPrefix(a.ID, "app_") {
		t.Fatalf("ID 应以 app_ 开头，得到 %s", a.ID)
	}
	if a.Status != "registered" {
		t.Fatalf("默认 status 应为 registered，得到 %s", a.Status)
	}
}

// TestStore_CreateRespectsExplicitStatus 显式传入 status 时不应被覆盖（如 building）。
func TestStore_CreateRespectsExplicitStatus(t *testing.T) {
	s := newTestStore(t)
	a := mkApp("ps_1", "snake")
	a.Status = "building"
	if err := s.Create(context.Background(), a); err != nil {
		t.Fatalf("create: %v", err)
	}
	if a.Status != "building" {
		t.Fatalf("显式 status 应保留，得到 %s", a.Status)
	}
}

// TestStore_ListOrderByRecent List 按 created_at DESC 排序，且只返回本空间应用。
// 显式设 created_at（同秒默认值会平局，破坏排序判定）。
func TestStore_ListOrderByRecent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a1 := mkApp("ps_1", "a1")
	_ = s.Create(ctx, a1)
	// 手动改 created_at 让 a1 早于 a2，确保 DESC 有序
	_, _ = s.db.ExecContext(ctx, `UPDATE appdeploy_application SET created_at='2024-01-01 00:00:00' WHERE id=$1`, a1.ID)
	a2 := mkApp("ps_1", "a2")
	_ = s.Create(ctx, a2)
	_, _ = s.db.ExecContext(ctx, `UPDATE appdeploy_application SET created_at='2024-02-01 00:00:00' WHERE id=$1`, a2.ID)
	_ = s.Create(ctx, mkApp("ps_2", "other"))

	list, err := s.List(ctx, "ps_1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ps_1 应有 2 个应用，得到 %d", len(list))
	}
	if list[0].ID != a2.ID {
		t.Fatalf("最新创建的 a2 应在前，得到 %s", list[0].ID)
	}
	// 跨空间隔离：ps_2 的应用不应混入
	for _, ap := range list {
		if ap.ProjectSpaceID != "ps_1" {
			t.Fatalf("List 不应跨空间，得到 %s", ap.ProjectSpaceID)
		}
	}
}

// TestStore_Get_miss_unknown_psid 任一条件不匹配返回空+err。
func TestStore_Get_miss(t *testing.T) {
	s := newTestStore(t)
	a := mkApp("ps_1", "snake")
	_ = s.Create(context.Background(), a)

	cases := []struct{ psID, id, desc string }{
		{"ps_1", "app_notexist", "id 不存在"},
		{"ps_other", a.ID, "ps_id 不匹配"},
	}
	for _, c := range cases {
		got, err := s.Get(context.Background(), c.psID, c.id)
		if err == nil {
			t.Fatalf("%s: 应返回 err，得到 nil (got=%+v)", c.desc, got)
		}
	}
}

// TestStore_GetByName 同空间同名查询命中；不同名/不同空间不命中。
func TestStore_GetByName(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "snake")
	_ = s.Create(ctx, a)

	got, err := s.GetByName(ctx, "ps_1", "snake")
	if err != nil {
		t.Fatalf("getbyname: %v", err)
	}
	if got.ID != a.ID {
		t.Fatalf("应返回 a，得到 %s", got.ID)
	}
	if _, err := s.GetByName(ctx, "ps_1", "ghost"); err == nil {
		t.Fatal("不存在名字应返回 err")
	}
}

// TestStore_GetByAppID 跨空间按 id 查询。
func TestStore_GetByAppID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "snake")
	_ = s.Create(ctx, a)

	got, err := s.GetByAppID(ctx, a.ID)
	if err != nil {
		t.Fatalf("getbyappid: %v", err)
	}
	if got.Name != "snake" {
		t.Fatalf("应返回 snake，得到 %s", got.Name)
	}
}

// TestStore_ResolveApp 应用存在返回 repoDir + port；不存在返回错误。
func TestStore_ResolveApp(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "snake")
	_ = s.Create(ctx, a)

	repoDir, port, err := s.ResolveApp(ctx, a.ID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if repoDir != a.RepoDir || port != a.InternalPort {
		t.Fatalf("resolve 不匹配: got repo=%s port=%d", repoDir, port)
	}
	if _, _, err := s.ResolveApp(ctx, "app_ghost"); err == nil {
		t.Fatal("不存在应用应返回 err")
	}
}

// TestStore_AppURLByAppID 优先 test 实例 URL；无实例时回退 application 表 URL。
func TestStore_AppURLByAppID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "snake")
	_ = s.Create(ctx, a)

	t.Run("无实例无URL报错", func(t *testing.T) {
		if _, err := s.AppURLByAppID(ctx, a.ID); err == nil {
			t.Fatal("未部署且无 URL 应报错")
		}
	})
	t.Run("回退 application.URL", func(t *testing.T) {
		a.URL = "http://h:1"
		_ = s.UpdateDeploy(ctx, a)
		got, err := s.AppURLByAppID(ctx, a.ID)
		if err != nil {
			t.Fatalf("应回退成功: %v", err)
		}
		if got != "http://h:1" {
			t.Fatalf("回退 URL 不匹配: %s", got)
		}
	})
	t.Run("test 实例优先", func(t *testing.T) {
		ins, _ := s.GetOrCreateInstance(ctx, a.ID, EnvTest)
		ins.URL = "http://test:9100"
		_ = s.UpdateInstance(ctx, ins)
		got, _ := s.AppURLByAppID(ctx, a.ID)
		if got != "http://test:9100" {
			t.Fatalf("应优先 test 实例 URL，得到 %s", got)
		}
	})
	t.Run("应用不存在", func(t *testing.T) {
		if _, err := s.AppURLByAppID(ctx, "app_ghost"); err == nil {
			t.Fatal("应用不存在应报错")
		}
	})
}

// TestStore_GetOrCreateInstance 首次创建 + 二次复用，且 env 隔离。
func TestStore_GetOrCreateInstance(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "snake")
	_ = s.Create(ctx, a)

	ins1, err := s.GetOrCreateInstance(ctx, a.ID, EnvTest)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if !strings.HasPrefix(ins1.ID, "ins_") {
		t.Fatalf("实例 ID 应 ins_ 开头，得到 %s", ins1.ID)
	}
	if ins1.Status != "registered" {
		t.Fatalf("新建实例 status 应 registered，得到 %s", ins1.Status)
	}
	// 二次调用应复用（同 ID）
	ins2, _ := s.GetOrCreateInstance(ctx, a.ID, EnvTest)
	if ins2.ID != ins1.ID {
		t.Fatalf("二次调用应复用同实例，得到 %s vs %s", ins1.ID, ins2.ID)
	}
	// 不同 env 各自独立
	insProd, _ := s.GetOrCreateInstance(ctx, a.ID, EnvProd)
	if insProd.ID == ins1.ID {
		t.Fatal("prod 实例不应与 test 复用")
	}
}

// TestStore_GetInstance 不存在返回 nil,nil（不报错）。
func TestStore_GetInstance_miss(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "snake")
	_ = s.Create(ctx, a)

	got, err := s.GetInstance(ctx, a.ID, EnvTest)
	if err != nil {
		t.Fatalf("miss 应返回 nil,nil，得到 err=%v", err)
	}
	if got != nil {
		t.Fatalf("miss 应返回 nil，得到 %+v", got)
	}
}

// TestStore_UpdateInstance 全字段更新 + updated_at 自动刷新。
func TestStore_UpdateInstance(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "snake")
	_ = s.Create(ctx, a)
	ins, _ := s.GetOrCreateInstance(ctx, a.ID, EnvTest)

	ins.Image = "appdeploy/snake-test:v1"
	ins.ContainerName = "appdeploy-snake-test-v1"
	ins.HostPort = 9100
	ins.URL = "http://h:9100"
	ins.Version = 1
	ins.Status = "running"
	ins.LastError = ""
	ins.BuildLog = "build ok"
	if err := s.UpdateInstance(ctx, ins); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := s.GetInstance(ctx, a.ID, EnvTest)
	if got.Image != "appdeploy/snake-test:v1" || got.HostPort != 9100 || got.Status != "running" {
		t.Fatalf("更新未生效: %+v", got)
	}
	if got.URL != "http://h:9100" {
		t.Fatalf("URL 未更新: %s", got.URL)
	}
}

// TestStore_SetInstanceStatus 状态机：building→running→stopped 等。
func TestStore_SetInstanceStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "snake")
	_ = s.Create(ctx, a)
	_, _ = s.GetOrCreateInstance(ctx, a.ID, EnvProd)

	if err := s.SetInstanceStatus(ctx, a.ID, EnvProd, "stopped", "by test", "log tail"); err != nil {
		t.Fatalf("set status: %v", err)
	}
	got, _ := s.GetInstance(ctx, a.ID, EnvProd)
	if got.Status != "stopped" || got.LastError != "by test" || got.BuildLog != "log tail" {
		t.Fatalf("状态字段未更新: %+v", got)
	}
	// 不存在的实例：SetInstanceStatus 不报错（UPDATE 影响 0 行但无 err）
	if err := s.SetInstanceStatus(ctx, "app_ghost", EnvProd, "running", "", ""); err != nil {
		t.Fatalf("ghost instance 不应报错: %v", err)
	}
}

// TestStore_ListInstancesByApp 一个应用多环境实例按 env 字母序返回。
func TestStore_ListInstancesByApp(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "snake")
	_ = s.Create(ctx, a)
	_, _ = s.GetOrCreateInstance(ctx, a.ID, EnvTest)
	_, _ = s.GetOrCreateInstance(ctx, a.ID, EnvProd)

	list, err := s.ListInstancesByApp(ctx, a.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("应有 2 个实例，得到 %d", len(list))
	}
	// ORDER BY env → prod 在前（字典序 p < t）
	if list[0].Env != EnvProd {
		t.Fatalf("prod 应在前，得到 %s", list[0].Env)
	}
}

// TestStore_ListHeadlessActiveInstances 返回 headless 且 running/degraded/failed 的实例,带 restart_count + 项目空间。
// failed 纳入是为了让 HealthReconciler 能发现"崩溃后 docker 又拉起"的实例并翻回 running+resolve 告警。
// stopped(用户主动停)/web 形态 仍排除。
func TestStore_ListHeadlessActiveInstances(t *testing.T) {
	s := newTestStore(t)
	ps := "ps_test_headless"
	// headless running 实例(应命中)
	ah := &Application{ProjectSpaceID: ps, Name: "bot1", AppKind: AppKindHeadless, InternalPort: 0}
	if err := s.Create(context.Background(), ah); err != nil {
		t.Fatal(err)
	}
	ih := &AppInstance{ID: "ins_h1", AppID: ah.ID, Env: EnvTest, Status: "running"}
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO appdeploy_instance (id, app_id, env, status, restart_count) VALUES ($1,$2,$3,$4,$5)`,
		ih.ID, ih.AppID, ih.Env, ih.Status, 2); err != nil {
		t.Fatal(err)
	}
	// headless failed 实例(应命中 — 崩溃后可能被 docker restart 拉起,reconcile 需复查)
	// 单独建一个 headless app 以避开 (app_id,env) UNIQUE,与 running/stopped 同 app 区分。
	ahf := &Application{ProjectSpaceID: ps, Name: "bot_failed", AppKind: AppKindHeadless, InternalPort: 0}
	if err := s.Create(context.Background(), ahf); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO appdeploy_instance (id, app_id, env, status) VALUES ($1,$2,$3,'failed')`,
		"ins_h_failed", ahf.ID, EnvTest); err != nil {
		t.Fatal(err)
	}
	// web running 实例(不应命中)
	aw := &Application{ProjectSpaceID: ps, Name: "web1", AppKind: AppKindWeb, InternalPort: 3000}
	s.Create(context.Background(), aw)
	s.db.ExecContext(context.Background(),
		`INSERT INTO appdeploy_instance (id, app_id, env, status) VALUES ($1,$2,$3,'running')`,
		"ins_w1", aw.ID, EnvTest)
	// headless stopped 实例(不应命中 — 用户主动停)
	ih2 := &AppInstance{ID: "ins_h2", AppID: ah.ID, Env: EnvProd, Status: "stopped"}
	s.db.ExecContext(context.Background(),
		`INSERT INTO appdeploy_instance (id, app_id, env, status) VALUES ($1,$2,$3,'stopped')`,
		ih2.ID, ih2.AppID, ih2.Env)

	got, err := s.ListHeadlessActiveInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("期望 2 条 headless 活跃实例(running+failed),得到 %d: %+v", len(got), got)
	}
	// 找到 running 那条校验字段(restart_count/name/ps 透传)
	var runHit *headlessInstance
	hitFailed := false
	for i := range got {
		if got[i].Status == "running" {
			runHit = &got[i]
		}
		if got[i].Status == "failed" {
			hitFailed = true
		}
	}
	if runHit == nil || runHit.AppID != ah.ID || runHit.RestartCount != 2 || runHit.Name != "bot1" || runHit.ProjectSpaceID != ps {
		t.Fatalf("running 返回字段不对: %+v", runHit)
	}
	if !hitFailed {
		t.Fatalf("failed 实例应被纳入巡检,结果集: %+v", got)
	}
}

// TestStore_UpdateInstanceHealth 写 status+last_error+restart_count。
func TestStore_UpdateInstanceHealth(t *testing.T) {
	s := newTestStore(t)
	ps := "ps_test_uh"
	a := &Application{ProjectSpaceID: ps, Name: "bot2", AppKind: AppKindHeadless, InternalPort: 0}
	s.Create(context.Background(), a)
	s.db.ExecContext(context.Background(),
		`INSERT INTO appdeploy_instance (id, app_id, env, status) VALUES ('ins_uh',$1,$2,'running')`, a.ID, EnvTest)
	if err := s.UpdateInstanceHealth(context.Background(), a.ID, EnvTest, "degraded", "crash-loop", 7); err != nil {
		t.Fatal(err)
	}
	ins, _ := s.GetInstance(context.Background(), a.ID, EnvTest)
	if ins.Status != "degraded" || ins.LastError != "crash-loop" || ins.RestartCount != 7 {
		t.Fatalf("UpdateInstanceHealth 未生效: %+v", ins)
	}
}

// TestStore_UpsertEnv 新增 → 更新同 key（ON CONFLICT 路径）。
func TestStore_UpsertEnv(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "snake")
	_ = s.Create(ctx, a)

	if err := s.UpsertEnv(ctx, a.ID, "API_KEY", "secret1", true, "user"); err != nil {
		t.Fatalf("upsert insert: %v", err)
	}
	// 同 key 二次 upsert → 更新 value 和 is_secret
	if err := s.UpsertEnv(ctx, a.ID, "API_KEY", "secret2", false, "user"); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	list, _ := s.ListEnv(ctx, a.ID)
	if len(list) != 1 {
		t.Fatalf("upsert 后应仍只 1 条，得到 %d", len(list))
	}
	if list[0].Value != "secret2" {
		t.Fatalf("value 应更新为 secret2，得到 %s", list[0].Value)
	}
	if list[0].IsSecret {
		t.Fatal("is_secret 应已被覆盖为 false")
	}
}

// TestStore_UpsertEnv_source 验证 source 列持久化（platform/user 区分）。
func TestStore_UpsertEnv_source(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "envsrc")
	if err := s.Create(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.UpsertEnv(ctx, a.ID, "REDIS_ADDR", "10.10.0.28:6381", false, "platform"); err != nil {
		t.Fatalf("upsert platform: %v", err)
	}
	if err := s.UpsertEnv(ctx, a.ID, "MY_KEY", "v", false, "user"); err != nil {
		t.Fatalf("upsert user: %v", err)
	}
	vars, err := s.ListEnv(ctx, a.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := map[string]string{}
	for _, v := range vars {
		got[v.Key] = v.Source
	}
	if got["REDIS_ADDR"] != "platform" {
		t.Fatalf("REDIS_ADDR source 应 platform，得 %q", got["REDIS_ADDR"])
	}
	if got["MY_KEY"] != "user" {
		t.Fatalf("MY_KEY source 应 user，得 %q", got["MY_KEY"])
	}
}

// TestStore_ListEnvOrderByKey 多变量按 key 字母序返回。
func TestStore_ListEnvOrderByKey(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "snake")
	_ = s.Create(ctx, a)
	_ = s.UpsertEnv(ctx, a.ID, "Z_LAST", "z", false, "user")
	_ = s.UpsertEnv(ctx, a.ID, "A_FIRST", "a", false, "user")
	_ = s.UpsertEnv(ctx, a.ID, "M_MID", "m", false, "user")

	list, _ := s.ListEnv(ctx, a.ID)
	if len(list) != 3 {
		t.Fatalf("应有 3 条，得到 %d", len(list))
	}
	if list[0].Key != "A_FIRST" {
		t.Fatalf("首条应 A_FIRST，得到 %s", list[0].Key)
	}
	if list[2].Key != "Z_LAST" {
		t.Fatalf("末条应 Z_LAST，得到 %s", list[2].Key)
	}
}

// TestStore_DeleteEnv 删除指定 key；不影响其他。
func TestStore_DeleteEnv(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "snake")
	_ = s.Create(ctx, a)
	_ = s.UpsertEnv(ctx, a.ID, "K1", "v1", false, "user")
	_ = s.UpsertEnv(ctx, a.ID, "K2", "v2", false, "user")

	if err := s.DeleteEnv(ctx, a.ID, "K1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	list, _ := s.ListEnv(ctx, a.ID)
	if len(list) != 1 || list[0].Key != "K2" {
		t.Fatalf("删除 K1 后应剩 K2，得到 %v", list)
	}
	// 删除不存在的 key 不报错
	if err := s.DeleteEnv(ctx, a.ID, "GHOST"); err != nil {
		t.Fatalf("删除不存在 key 应不报错: %v", err)
	}
}

// TestStore_EnvPairs 返回 ["KEY=VALUE", ...]，含 secret 实际值（部署注入用）。
func TestStore_EnvPairs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "snake")
	_ = s.Create(ctx, a)
	_ = s.UpsertEnv(ctx, a.ID, "PORT", "8080", false, "user")
	_ = s.UpsertEnv(ctx, a.ID, "TOKEN", "secret_xyz", true, "user")

	pairs, err := s.EnvPairs(ctx, a.ID)
	if err != nil {
		t.Fatalf("envpairs: %v", err)
	}
	if len(pairs) != 2 {
		t.Fatalf("应有 2 对，得到 %d", len(pairs))
	}
	// 必须含 secret 明文（部署要注入）
	joined := strings.Join(pairs, ",")
	if !strings.Contains(joined, "TOKEN=secret_xyz") {
		t.Fatalf("EnvPairs 应含 secret 明文，得到 %v", pairs)
	}
	if !strings.Contains(joined, "PORT=8080") {
		t.Fatalf("EnvPairs 应含 PORT，得到 %v", pairs)
	}
}

// TestStore_EnvPairs_empty 无环境变量时返回空切片（非 nil），不报错。
func TestStore_EnvPairs_empty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "snake")
	_ = s.Create(ctx, a)

	pairs, err := s.EnvPairs(ctx, a.ID)
	if err != nil {
		t.Fatalf("envpairs empty: %v", err)
	}
	if len(pairs) != 0 {
		t.Fatalf("空应用应 0 对，得到 %v", pairs)
	}
}

// TestStore_UpdateDeploy 全字段更新 application 概览。
func TestStore_UpdateDeploy(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "snake")
	_ = s.Create(ctx, a)

	a.Image = "appdeploy/snake-prod:v3"
	a.ContainerName = "appdeploy-snake-prod-v3"
	a.HostPort = 9201
	a.URL = "http://h:9201"
	a.Version = 3
	a.Status = "running"
	if err := s.UpdateDeploy(ctx, a); err != nil {
		t.Fatalf("update deploy: %v", err)
	}
	got, _ := s.GetByAppID(ctx, a.ID)
	if got.HostPort != 9201 || got.Version != 3 || got.Status != "running" {
		t.Fatalf("UpdateDeploy 未生效: %+v", got)
	}
	if got.URL != "http://h:9201" {
		t.Fatalf("URL 未更新: %s", got.URL)
	}
}

// TestStore_SetStatus 状态字段更新 + 不存在应用报错。
func TestStore_SetStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "snake")
	_ = s.Create(ctx, a)

	if err := s.SetStatus(ctx, "ps_1", a.ID, "failed", "oom", "log"); err != nil {
		t.Fatalf("set status: %v", err)
	}
	got, _ := s.Get(ctx, "ps_1", a.ID)
	if got.Status != "failed" || got.LastError != "oom" || got.BuildLog != "log" {
		t.Fatalf("SetStatus 未生效: %+v", got)
	}
	// 不存在的应用 → RowsAffected=0 → 报错
	if err := s.SetStatus(ctx, "ps_1", "app_ghost", "running", "", ""); err == nil {
		t.Fatal("不存在应用 SetStatus 应报错")
	}
	// psID 不匹配也算不存在
	if err := s.SetStatus(ctx, "ps_other", a.ID, "running", "", ""); err == nil {
		t.Fatal("psID 不匹配 SetStatus 应报错")
	}
}

// TestStore_Delete 删除应用（实例/env 由 FK 或应用层负责，这里只测主表）。
func TestStore_Delete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "snake")
	_ = s.Create(ctx, a)

	if err := s.Delete(ctx, "ps_1", a.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Get(ctx, "ps_1", a.ID); err == nil {
		t.Fatal("删除后 Get 应报错")
	}
	// psID 不匹配不会删（条件删除安全）
	b := mkApp("ps_1", "other")
	_ = s.Create(ctx, b)
	if err := s.Delete(ctx, "ps_other", b.ID); err != nil {
		t.Fatalf("delete return: %v", err)
	}
	if _, err := s.Get(ctx, "ps_1", b.ID); err != nil {
		t.Fatal("psID 不匹配时不应实际删除")
	}
}

// TestStore_EnsureAppForRequirement_HitAppExists 同名应用已存在 → 直接复用，不调 EnsureRepo。
// 仅测此分支；新建分支会调真实 git init（属外部进程，跳过）。
func TestStore_EnsureAppForRequirement_HitAppExists(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "snake")
	a.InternalPort = 3000
	_ = s.Create(ctx, a)

	appID, repoDir, port, err := s.EnsureAppForRequirement(ctx, "ps_1", "snake")
	if err != nil {
		t.Fatalf("ensure hit: %v", err)
	}
	if appID != a.ID {
		t.Fatalf("应返回已存在 app ID，得到 %s", appID)
	}
	if repoDir != a.RepoDir || port != 3000 {
		t.Fatalf("应返回已存 app 的 repo/port，得到 repo=%s port=%d", repoDir, port)
	}
}

// TestStore_CreateImport Create 写入 import_source/import_ref，读回正确（managed 默认空串兼容）。
func TestStore_CreateImport(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := &Application{
		ProjectSpaceID: "ps_1", Name: "legacy-api", RepoDir: "/data/repos/legacy-api",
		DeployMode: AppManaged, ImportSource: ImportSourceGit, ImportRef: "https://gitlab/x/y.git",
		Status: StatusImporting,
	}
	if err := s.Create(ctx, a); err != nil {
		t.Fatalf("create import: %v", err)
	}
	got, err := s.GetByAppID(ctx, a.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ImportSource != ImportSourceGit {
		t.Fatalf("import_source 应 git，得到 %q", got.ImportSource)
	}
	if got.ImportRef != "https://gitlab/x/y.git" {
		t.Fatalf("import_ref 不匹配: %q", got.ImportRef)
	}
	if got.Status != StatusImporting {
		t.Fatalf("status 应 importing，得到 %q", got.Status)
	}
	if got.ImportedAt != nil {
		t.Fatalf("进行中 imported_at 应 nil，得到 %v", got.ImportedAt)
	}
}

// TestStore_CreateImportDefault 未设 import_source 的老流程默认空串（向后兼容）。
func TestStore_CreateImportDefault(t *testing.T) {
	s := newTestStore(t)
	a := mkApp("ps_1", "snake") // 未设 ImportSource
	if err := s.Create(context.Background(), a); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, _ := s.GetByAppID(context.Background(), a.ID)
	if got.ImportSource != "" || got.ImportRef != "" {
		t.Fatalf("未设导入字段应为空，得到 source=%q ref=%q", got.ImportSource, got.ImportRef)
	}
}

// TestStore_Create_AppKind 显式 app_kind=desktop 落库后读回。
func TestStore_Create_AppKind(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := &Application{ProjectSpaceID: "ps_1", Name: "deskapp", AppKind: AppKindDesktop}
	if err := s.Create(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.Get(ctx, "ps_1", a.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.AppKind != AppKindDesktop {
		t.Fatalf("app_kind = %q, want %q", got.AppKind, AppKindDesktop)
	}
}

// TestStore_Create_AppKindDefaultWeb 不设 AppKind 时默认 web（向后兼容）。
func TestStore_Create_AppKindDefaultWeb(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := &Application{ProjectSpaceID: "ps_1", Name: "webapp"}
	if err := s.Create(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, _ := s.Get(ctx, "ps_1", a.ID)
	if got.AppKind != AppKindWeb {
		t.Fatalf("default app_kind = %q, want %q", got.AppKind, AppKindWeb)
	}
}

// TestStore_UpdateVersion 持久化构建版本号（I-7）。
// BuildArtifacts 里 a.Version++ 只改内存，需 UpdateVersion 写回 DB，
// 否则下次构建/前端展示的 version 不递增。
func TestStore_UpdateVersion(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := &Application{ProjectSpaceID: "ps_1", Name: "verapp", AppKind: AppKindDesktop}
	if err := s.Create(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	// 初始 version=0（Create 不设 version）
	got, _ := s.Get(ctx, "ps_1", a.ID)
	if got.Version != 0 {
		t.Fatalf("初始 version = %d, want 0", got.Version)
	}
	// 模拟 BuildArtifacts：递增并持久化
	a.Version = 3
	if err := s.UpdateVersion(ctx, a.ID, a.Version); err != nil {
		t.Fatalf("UpdateVersion: %v", err)
	}
	got, _ = s.Get(ctx, "ps_1", a.ID)
	if got.Version != 3 {
		t.Fatalf("更新后 version = %d, want 3", got.Version)
	}
	// 再次递增验证累加
	a.Version = 4
	if err := s.UpdateVersion(ctx, a.ID, a.Version); err != nil {
		t.Fatalf("UpdateVersion 2: %v", err)
	}
	got, _ = s.Get(ctx, "ps_1", a.ID)
	if got.Version != 4 {
		t.Fatalf("二次更新后 version = %d, want 4", got.Version)
	}
}

// TestStore_UpdateImportDone 导入完成写 registered + imported_at + repo_dir，清 last_error。
func TestStore_UpdateImportDone(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := &Application{ProjectSpaceID: "ps_1", Name: "imp", RepoDir: "/data/repos/imp",
		DeployMode: AppManaged, ImportSource: ImportSourceGit, Status: StatusImporting}
	_ = s.Create(ctx, a)
	// 先写个 last_error 模拟进度
	_ = s.SetStatus(ctx, "ps_1", a.ID, StatusImporting, "正在克隆...", "")

	if err := s.UpdateImportDone(ctx, "ps_1", a.ID, "/data/repos/imp"); err != nil {
		t.Fatalf("UpdateImportDone: %v", err)
	}
	got, _ := s.GetByAppID(ctx, a.ID)
	if got.Status != "registered" {
		t.Fatalf("完成应 registered，得到 %q", got.Status)
	}
	if got.ImportedAt == nil {
		t.Fatalf("imported_at 应已填，得到 nil")
	}
	if got.LastError != "" {
		t.Fatalf("last_error 应清空，得到 %q", got.LastError)
	}
}

// TestStore_Delete_cascadesEnvAndInstance 删 app 后 appdeploy_env / appdeploy_instance 行
// 应被 FK ON DELETE CASCADE 清掉（修 000001 init schema 漏加 FK 的缺口）。
// 验证路径：testutil.TestDB 跑全部迁移 → 000031 加的 FK 生效 → Delete 触发级联清空。
func TestStore_Delete_cascadesEnvAndInstance(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "snake")
	if err := s.Create(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	// 注入 env 行（模拟 pgsupply 的 DATABASE_URL + mwsupply 的 REDIS_ADDR）
	if err := s.UpsertEnv(ctx, a.ID, "DATABASE_URL", "postgres://u:p@h/db", true, "platform"); err != nil {
		t.Fatalf("upsert env DATABASE_URL: %v", err)
	}
	if err := s.UpsertEnv(ctx, a.ID, "REDIS_ADDR", "redis://h:6379", false, "platform"); err != nil {
		t.Fatalf("upsert env REDIS_ADDR: %v", err)
	}
	// 建 instance 行（模拟部署生成 prod 实例记录）
	if _, err := s.GetOrCreateInstance(ctx, a.ID, "prod"); err != nil {
		t.Fatalf("getorcreate instance: %v", err)
	}

	// 删 app → 期待 FK CASCADE 清 env + instance
	if err := s.Delete(ctx, "ps_1", a.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	envs, err := s.ListEnv(ctx, a.ID)
	if err != nil {
		t.Fatalf("list env after delete: %v", err)
	}
	if len(envs) != 0 {
		t.Fatalf("env 应被 CASCADE 清空，仍剩 %d 行: %+v", len(envs), envs)
	}
	inss, err := s.ListInstancesByApp(ctx, a.ID)
	if err != nil {
		t.Fatalf("list instance after delete: %v", err)
	}
	if len(inss) != 0 {
		t.Fatalf("instance 应被 CASCADE 清空，仍剩 %d 行: %+v", len(inss), inss)
	}
}
