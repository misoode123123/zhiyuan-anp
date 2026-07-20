package workspace

import (
	"context"
	"errors"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// newTestRepo 建内存 SQLite + project_space/project/membership 三张核心表，
// 并附带 Overview 聚合查询所需的辅助表（appdeploy_application/requirement/change_request/release_record），
// 使 Overview 的 COUNT 查询能真正命中表而非被静默吞错返回 0。
// DDL 由 internal/db/migrations/pg/000001_init.up.sql 映射而来：
// TIMESTAMP→DATETIME、UNIQUE/INDEX 保持一致、FK 在 SQLite 默认不强制（与 change/standard 测试一致）。
func newTestRepo(t *testing.T) *Repository {
	t.Helper()
	db, err := sqlx.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	db.MustExec(`CREATE TABLE project_space (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  slug       TEXT NOT NULL UNIQUE,
  status     TEXT NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)

	db.MustExec(`CREATE TABLE project (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL REFERENCES project_space(id),
  name             TEXT NOT NULL,
  slug             TEXT NOT NULL,
  status           TEXT NOT NULL DEFAULT 'active',
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (project_space_id, slug))`)

	db.MustExec(`CREATE TABLE membership (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL REFERENCES project_space(id),
  user_id          TEXT NOT NULL,
  role             TEXT NOT NULL,
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (project_space_id, user_id))`)

	// Overview 聚合所引用的跨上下文表，列对齐迁移文件（仅保留 COUNT 所需列 + status 过滤列）。
	db.MustExec(`CREATE TABLE appdeploy_application (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL,
  status           TEXT NOT NULL DEFAULT 'registered')`)
	db.MustExec(`CREATE TABLE requirement (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL)`)
	db.MustExec(`CREATE TABLE change_request (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT)`)
	db.MustExec(`CREATE TABLE release_record (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL)`)

	return NewRepository(db)
}

// seedSpace 建一条 project_space 并返回其指针。
func seedSpace(t *testing.T, r *Repository, id, name, slug string) *ProjectSpace {
	t.Helper()
	ps := &ProjectSpace{ID: id, Name: name, Slug: slug, Status: "active"}
	if err := r.CreateProjectSpace(context.Background(), ps); err != nil {
		t.Fatalf("seed space %q: %v", id, err)
	}
	return ps
}

// seedProject 在指定空间下建一条 project。
func seedProject(t *testing.T, r *Repository, id, psID, name, slug string) {
	t.Helper()
	p := &Project{ID: id, ProjectSpaceID: psID, Name: name, Slug: slug, Status: "active"}
	if err := r.CreateProject(context.Background(), p); err != nil {
		t.Fatalf("seed project %q: %v", id, err)
	}
}

// TestRepository_CreateAndGetProjectSpace 创建→读回，字段一一核对。
func TestRepository_CreateAndGetProjectSpace(t *testing.T) {
	r := newTestRepo(t)
	ps := &ProjectSpace{ID: "ps_1", Name: "空间A", Slug: "space-a", Status: "active"}
	if err := r.CreateProjectSpace(context.Background(), ps); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := r.GetProjectSpace(context.Background(), "ps_1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != "ps_1" || got.Name != "空间A" || got.Slug != "space-a" || got.Status != "active" {
		t.Fatalf("字段不匹配： %+v", got)
	}
	// created_at/updated_at 由 DB 默认值生成，应非零。
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("时间戳应为 DB 默认值，got created=%v updated=%v", got.CreatedAt, got.UpdatedAt)
	}
}

// TestRepository_GetProjectSpace_NotFound 查询不存在的 ID → ErrNotFound。
func TestRepository_GetProjectSpace_NotFound(t *testing.T) {
	r := newTestRepo(t)
	_, err := r.GetProjectSpace(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("期望 ErrNotFound，得到 %v", err)
	}
}

// TestRepository_CreateProjectSpace_DuplicateSlug slug 唯一约束：同 slug 二次插入报错。
func TestRepository_CreateProjectSpace_DuplicateSlug(t *testing.T) {
	r := newTestRepo(t)
	seedSpace(t, r, "ps_1", "空间A", "dup-slug")
	ps2 := &ProjectSpace{ID: "ps_2", Name: "空间B", Slug: "dup-slug", Status: "active"}
	err := r.CreateProjectSpace(context.Background(), ps2)
	if err == nil {
		t.Fatal("slug 重复时应返回错误")
	}
}

// TestRepository_ListProjectSpaces 按 created_at DESC 排序，且空库时返回空切片。
func TestRepository_ListProjectSpaces(t *testing.T) {
	r := newTestRepo(t)

	// 空库。
	list, err := r.ListProjectSpaces(context.Background())
	if err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("空库期望 0 条，得到 %d", len(list))
	}

	// 依次插入三条，验证 DESC ordering（后插入的在前）。
	seedSpace(t, r, "ps_1", "A", "a")
	seedSpace(t, r, "ps_2", "B", "b")
	seedSpace(t, r, "ps_3", "C", "c")
	list, err = r.ListProjectSpaces(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("期望 3 条，得到 %d", len(list))
	}
	// SQLite CURRENT_TIMESTAMP 精度到秒，三条若同一秒插入则顺序不稳定；
	// 这里只校验集合，不强制严格顺序（避免时序 flake）。
	gotIDs := map[string]bool{}
	for _, ps := range list {
		gotIDs[ps.ID] = true
	}
	for _, want := range []string{"ps_1", "ps_2", "ps_3"} {
		if !gotIDs[want] {
			t.Fatalf("列表缺失 %s，得到 %v", want, gotIDs)
		}
	}
}

// TestRepository_CreateAndListProjects 项目 CRUD：建后能按空间查出。
func TestRepository_CreateAndListProjects(t *testing.T) {
	r := newTestRepo(t)
	seedSpace(t, r, "ps_1", "空间A", "space-a")

	// 空间下无项目。
	list, err := r.ListProjects(context.Background(), "ps_1")
	if err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("空空间期望 0 个项目，得到 %d", len(list))
	}

	seedProject(t, r, "prj_1", "ps_1", "项目1", "p1")
	seedProject(t, r, "prj_2", "ps_1", "项目2", "p2")
	list, err = r.ListProjects(context.Background(), "ps_1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("期望 2 个项目，得到 %d", len(list))
	}
	// 字段断言。
	var ids []string
	for _, p := range list {
		ids = append(ids, p.ID)
		if p.ProjectSpaceID != "ps_1" {
			t.Fatalf("project_space_id 应为 ps_1，得到 %s", p.ProjectSpaceID)
		}
	}
	if !contains(ids, "prj_1") || !contains(ids, "prj_2") {
		t.Fatalf("列表应包含 prj_1/prj_2，得到 %v", ids)
	}
}

// TestRepository_ListProjects_OtherSpace 隔离：空间 B 查不到空间 A 的项目。
func TestRepository_ListProjects_OtherSpace(t *testing.T) {
	r := newTestRepo(t)
	seedSpace(t, r, "ps_a", "A", "a")
	seedSpace(t, r, "ps_b", "B", "b")
	seedProject(t, r, "prj_1", "ps_a", "项目1", "p1")

	list, err := r.ListProjects(context.Background(), "ps_b")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("空间 B 不应看到空间 A 的项目，得到 %d 条", len(list))
	}
}

// TestRepository_CreateProject_DuplicateSlugInSameSpace
// 同空间下 slug 唯一约束（UNIQUE(project_space_id, slug)）；不同空间同 slug 允许。
func TestRepository_CreateProject_DuplicateSlugInSameSpace(t *testing.T) {
	r := newTestRepo(t)
	seedSpace(t, r, "ps_a", "A", "a")
	seedSpace(t, r, "ps_b", "B", "b")

	// 同空间同 slug：冲突。
	seedProject(t, r, "prj_1", "ps_a", "项目1", "shared")
	{
		dup := &Project{ID: "prj_2", ProjectSpaceID: "ps_a", Name: "项目2", Slug: "shared", Status: "active"}
		if err := r.CreateProject(context.Background(), dup); err == nil {
			t.Fatal("同空间 slug 重复时应返回错误")
		}
	}

	// 不同空间同 slug：允许。
	seedProject(t, r, "prj_3", "ps_b", "项目3", "shared")
}

// TestRepository_Overview_NotFound 不存在的空间 → 透传 ErrNotFound（不返回零值 Overview）。
func TestRepository_Overview_NotFound(t *testing.T) {
	r := newTestRepo(t)
	o, err := r.Overview(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("期望 ErrNotFound，得到 err=%v o=%+v", err, o)
	}
	if o != nil {
		t.Fatalf("空间不存在时 Overview 应为 nil，得到 %+v", o)
	}
}

// TestRepository_Overview_Aggregation
// 多项目/多成员/多应用/多需求/多变更/多发布场景下，Overview 各计数与实际一致。
// 此用例覆盖 overview 聚合的纯查询逻辑。
func TestRepository_Overview_Aggregation(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	ps := seedSpace(t, r, "ps_1", "空间A", "space-a")

	// 项目 2 个。
	seedProject(t, r, "prj_1", "ps_1", "P1", "p1")
	seedProject(t, r, "prj_2", "ps_1", "P2", "p2")

	// 成员 3 个（不同角色：owner/maintainer/guest）。
	for _, m := range []struct{ id, role string }{
		{"m_1", "owner"},
		{"m_2", "maintainer"},
		{"m_3", "guest"},
	} {
		if _, err := r.db.ExecContext(ctx,
			`INSERT INTO membership (id, project_space_id, user_id, role) VALUES ($1, $2, $3, $4)`,
			m.id, ps.ID, m.id+"_user", m.role); err != nil {
			t.Fatalf("seed membership %s: %v", m.id, err)
		}
	}

	// 应用 4 个，其中 2 个 running（DeployedApps 计数）。
	for i, app := range []struct {
		id     string
		status string
	}{
		{"app_1", "running"},
		{"app_2", "running"},
		{"app_3", "registered"},
		{"app_4", "failed"},
	} {
		if _, err := r.db.ExecContext(ctx,
			`INSERT INTO appdeploy_application (id, project_space_id, status) VALUES ($1, $2, $3)`,
			app.id, ps.ID, app.status); err != nil {
			t.Fatalf("seed app #%d: %v", i, err)
		}
	}

	// 需求 2 条。
	for _, id := range []string{"req_1", "req_2"} {
		if _, err := r.db.ExecContext(ctx,
			`INSERT INTO requirement (id, project_space_id) VALUES ($1, $2)`, id, ps.ID); err != nil {
			t.Fatalf("seed requirement %s: %v", id, err)
		}
	}
	// 变更 5 条。
	for i := 0; i < 5; i++ {
		if _, err := r.db.ExecContext(ctx,
			`INSERT INTO change_request (id, project_space_id) VALUES ($1, $2)`,
			"chg_"+string(rune('A'+i)), ps.ID); err != nil {
			t.Fatalf("seed change #%d: %v", i, err)
		}
	}
	// 发布 1 条。
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO release_record (id, project_space_id) VALUES ($1, $2)`, "rel_1", ps.ID); err != nil {
		t.Fatalf("seed release: %v", err)
	}

	o, err := r.Overview(ctx, ps.ID)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if o.Space.ID != ps.ID {
		t.Fatalf("Overview.Space.ID 不匹配：%s", o.Space.ID)
	}
	// 注意：项目数不在 Overview 字段中（设计如此），仅校验实际存在的计数字段。
	if o.Members != 3 {
		t.Fatalf("Members 计数期望 3，得到 %d", o.Members)
	}
	if o.Apps != 4 {
		t.Fatalf("Apps 计数期望 4，得到 %d", o.Apps)
	}
	if o.DeployedApps != 2 {
		t.Fatalf("DeployedApps（status=running）期望 2，得到 %d", o.DeployedApps)
	}
	if o.Requirements != 2 {
		t.Fatalf("Requirements 计数期望 2，得到 %d", o.Requirements)
	}
	if o.Changes != 5 {
		t.Fatalf("Changes 计数期望 5，得到 %d", o.Changes)
	}
	if o.Releases != 1 {
		t.Fatalf("Releases 计数期望 1，得到 %d", o.Releases)
	}
}

// TestRepository_Overview_DeployedApps_StatusFilter
// DeployedApps 只计 status='running'；其它状态（registered/failed/stopped）不计入。
func TestRepository_Overview_DeployedApps_StatusFilter(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	ps := seedSpace(t, r, "ps_1", "空间A", "space-a")

	statuses := []string{"running", "registered", "failed", "stopped", "running"}
	for i, st := range statuses {
		if _, err := r.db.ExecContext(ctx,
			`INSERT INTO appdeploy_application (id, project_space_id, status) VALUES ($1, $2, $3)`,
			"app_"+string(rune('A'+i)), ps.ID, st); err != nil {
			t.Fatalf("seed #%d: %v", i, err)
		}
	}

	o, err := r.Overview(ctx, ps.ID)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if o.Apps != len(statuses) {
		t.Fatalf("Apps 总数期望 %d，得到 %d", len(statuses), o.Apps)
	}
	if o.DeployedApps != 2 {
		t.Fatalf("DeployedApps 应仅统计 running，期望 2，得到 %d", o.DeployedApps)
	}
}

// TestRepository_Overview_MembershipRoles 成员角色不影响计数（COUNT(*)），
// 但不同角色的成员都应被计入 —— 验证 membership 表对多角色的支持。
func TestRepository_Overview_MembershipRoles(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	ps := seedSpace(t, r, "ps_1", "空间A", "space-a")

	roles := []string{"owner", "maintainer", "maintainer", "guest", "guest"}
	for i, role := range roles {
		if _, err := r.db.ExecContext(ctx,
			`INSERT INTO membership (id, project_space_id, user_id, role) VALUES ($1, $2, $3, $4)`,
			"m_"+string(rune('A'+i)), ps.ID, "u_"+string(rune('A'+i)), role); err != nil {
			t.Fatalf("seed #%d: %v", i, err)
		}
	}

	o, err := r.Overview(ctx, ps.ID)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if o.Members != len(roles) {
		t.Fatalf("Members 计数期望 %d（角色不影响计数），得到 %d", len(roles), o.Members)
	}
}

// TestRepository_Overview_MembershipUnique 同空间同 user_id 二次插入报错。
// 验证 membership 表 UNIQUE(project_space_id, user_id) 约束生效。
func TestRepository_Overview_MembershipUnique(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	ps := seedSpace(t, r, "ps_1", "空间A", "space-a")

	insert := func(id, userID string) error {
		_, err := r.db.ExecContext(ctx,
			`INSERT INTO membership (id, project_space_id, user_id, role) VALUES ($1, $2, $3, $4)`,
			id, ps.ID, userID, "guest")
		return err
	}
	if err := insert("m_1", "u_1"); err != nil {
		t.Fatalf("首次插入应成功：%v", err)
	}
	if err := insert("m_2", "u_1"); err == nil {
		t.Fatal("同空间同 user_id 应触发 UNIQUE 约束")
	}
}

// contains 简易切片包含判断。
func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
