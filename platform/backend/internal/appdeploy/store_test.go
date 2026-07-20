package appdeploy

import (
	"context"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// newTestStore 建内存 SQLite + appdeploy 三表（自包含，仿 change/store_test.go 模式）。
// 类型映射：PG TIMESTAMP→DATETIME、BOOLEAN→INTEGER、INTEGER/TEXT 原样。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sqlx.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.MustExec(`CREATE TABLE appdeploy_application (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL,
  name             TEXT NOT NULL,
  repo_dir         TEXT,
  internal_port    INTEGER NOT NULL DEFAULT 80,
  image            TEXT,
  container_name   TEXT,
  host_port        INTEGER NOT NULL DEFAULT 0,
  url              TEXT,
  version          INTEGER NOT NULL DEFAULT 0,
  status           TEXT NOT NULL DEFAULT 'registered',
  last_error       TEXT,
  build_log        TEXT,
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (project_space_id, name))`)
	db.MustExec(`CREATE TABLE appdeploy_instance (
  id             TEXT PRIMARY KEY,
  app_id         TEXT NOT NULL,
  env            TEXT NOT NULL,
  image          TEXT,
  container_name TEXT,
  host_port      INTEGER NOT NULL DEFAULT 0,
  url            TEXT,
  version        INTEGER NOT NULL DEFAULT 0,
  status         TEXT NOT NULL DEFAULT 'registered',
  last_error     TEXT,
  build_log      TEXT,
  created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (app_id, env))`)
	db.MustExec(`CREATE TABLE appdeploy_env (
  id         TEXT PRIMARY KEY,
  app_id     TEXT NOT NULL,
  key        TEXT NOT NULL,
  value      TEXT,
  is_secret  INTEGER NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (app_id, key))`)
	return NewStore(db)
}

// mkApp 构造一条已注册应用（id 由 Create 填充）。
func mkApp(ps, name string) *Application {
	return &Application{ProjectSpaceID: ps, Name: name, RepoDir: "/data/repos/" + name, InternalPort: 8080}
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

// TestStore_UpsertEnv 新增 → 更新同 key（ON CONFLICT 路径）。
func TestStore_UpsertEnv(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "snake")
	_ = s.Create(ctx, a)

	if err := s.UpsertEnv(ctx, a.ID, "API_KEY", "secret1", true); err != nil {
		t.Fatalf("upsert insert: %v", err)
	}
	// 同 key 二次 upsert → 更新 value 和 is_secret
	if err := s.UpsertEnv(ctx, a.ID, "API_KEY", "secret2", false); err != nil {
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

// TestStore_ListEnvOrderByKey 多变量按 key 字母序返回。
func TestStore_ListEnvOrderByKey(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "snake")
	_ = s.Create(ctx, a)
	_ = s.UpsertEnv(ctx, a.ID, "Z_LAST", "z", false)
	_ = s.UpsertEnv(ctx, a.ID, "A_FIRST", "a", false)
	_ = s.UpsertEnv(ctx, a.ID, "M_MID", "m", false)

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
	_ = s.UpsertEnv(ctx, a.ID, "K1", "v1", false)
	_ = s.UpsertEnv(ctx, a.ID, "K2", "v2", false)

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
	_ = s.UpsertEnv(ctx, a.ID, "PORT", "8080", false)
	_ = s.UpsertEnv(ctx, a.ID, "TOKEN", "secret_xyz", true)

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
