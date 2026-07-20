package workspace

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// newTestService 复用 newTestRepo 的内存 DB，构造 Service（含底层 Repository）。
func newTestService(t *testing.T) *Service {
	t.Helper()
	return NewService(newTestRepo(t))
}

// TestService_CreateProjectSpace 业务层建空间：ID 前缀 ps_/status=active，字段透传。
func TestService_CreateProjectSpace(t *testing.T) {
	svc := newTestService(t)
	ps, err := svc.CreateProjectSpace(context.Background(), CreateProjectSpaceInput{
		Name: "空间A",
		Slug: "space-a",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasPrefix(ps.ID, "ps_") {
		t.Fatalf("ID 前缀应为 ps_，得到 %s", ps.ID)
	}
	// newID = prefix + 20 hex chars。
	if len(ps.ID) != len("ps_")+20 {
		t.Fatalf("ID 长度应为 %d，得到 %d (%q)", len("ps_")+20, len(ps.ID), ps.ID)
	}
	if ps.Name != "空间A" || ps.Slug != "space-a" {
		t.Fatalf("Name/Slug 不匹配：%+v", ps)
	}
	if ps.Status != "active" {
		t.Fatalf("新空间 status 应为 active，得到 %s", ps.Status)
	}

	// 落库后可读回。
	got, err := svc.GetProjectSpace(context.Background(), ps.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Slug != "space-a" {
		t.Fatalf("读回 slug 不匹配：%s", got.Slug)
	}
}

// TestService_CreateProjectSpace_DuplicateSlug 业务层透传底层唯一约束错误。
func TestService_CreateProjectSpace_DuplicateSlug(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.CreateProjectSpace(context.Background(), CreateProjectSpaceInput{Name: "A", Slug: "dup"}); err != nil {
		t.Fatalf("首次创建：%v", err)
	}
	if _, err := svc.CreateProjectSpace(context.Background(), CreateProjectSpaceInput{Name: "B", Slug: "dup"}); err == nil {
		t.Fatal("slug 重复时业务层应返回错误")
	}
}

// TestService_GetProjectSpace_NotFound 业务层透传 ErrNotFound。
func TestService_GetProjectSpace_NotFound(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.GetProjectSpace(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("期望 ErrNotFound，得到 %v", err)
	}
}

// TestService_ListProjectSpaces 业务层列表透传。
func TestService_ListProjectSpaces(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.CreateProjectSpace(context.Background(), CreateProjectSpaceInput{Name: "A", Slug: "a"}); err != nil {
		t.Fatalf("create A: %v", err)
	}
	if _, err := svc.CreateProjectSpace(context.Background(), CreateProjectSpaceInput{Name: "B", Slug: "b"}); err != nil {
		t.Fatalf("create B: %v", err)
	}
	list, err := svc.ListProjectSpaces(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("期望 2 条，得到 %d", len(list))
	}
}

// TestService_CreateProject_SpaceIDBinding
// 多租户隔离关键点：project_space_id 只取自路径参数，不来自 CreateProjectInput（后者无此字段）。
// 即便业务调用方手滑传错，落库的 project_space_id 也始终是路径上的那个。
func TestService_CreateProject_SpaceIDBinding(t *testing.T) {
	svc := newTestService(t)
	psA, _ := svc.CreateProjectSpace(context.Background(), CreateProjectSpaceInput{Name: "A", Slug: "a"})
	psB, _ := svc.CreateProjectSpace(context.Background(), CreateProjectSpaceInput{Name: "B", Slug: "b"})

	// 在空间 A 下创建项目，psA 作为路径参数。
	p, err := svc.CreateProject(context.Background(), psA.ID, CreateProjectInput{Name: "项目1", Slug: "p1"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if !strings.HasPrefix(p.ID, "prj_") {
		t.Fatalf("ID 前缀应为 prj_，得到 %s", p.ID)
	}
	if p.ProjectSpaceID != psA.ID {
		t.Fatalf("project_space_id 应绑定到路径参数 %s，得到 %s", psA.ID, p.ProjectSpaceID)
	}
	if p.Status != "active" {
		t.Fatalf("新项目 status 应为 active，得到 %s", p.Status)
	}

	// 空间 B 下看不到该项目。
	bList, _ := svc.ListProjects(context.Background(), psB.ID)
	if len(bList) != 0 {
		t.Fatalf("空间 B 不应看到空间 A 的项目，得到 %d 条", len(bList))
	}
	aList, _ := svc.ListProjects(context.Background(), psA.ID)
	if len(aList) != 1 || aList[0].ID != p.ID {
		t.Fatalf("空间 A 应看到刚建的项目 %s，得到 %+v", p.ID, aList)
	}
}

// TestService_CreateProject_DuplicateSlugInSameSpace
// 同空间下项目 slug 冲突报错；不同空间同 slug 允许（业务层透传底层 UNIQUE 约束）。
func TestService_CreateProject_DuplicateSlugInSameSpace(t *testing.T) {
	svc := newTestService(t)
	psA, _ := svc.CreateProjectSpace(context.Background(), CreateProjectSpaceInput{Name: "A", Slug: "a"})
	psB, _ := svc.CreateProjectSpace(context.Background(), CreateProjectSpaceInput{Name: "B", Slug: "b"})

	if _, err := svc.CreateProject(context.Background(), psA.ID, CreateProjectInput{Name: "P1", Slug: "shared"}); err != nil {
		t.Fatalf("首次创建：%v", err)
	}
	// 同空间同 slug：报错。
	if _, err := svc.CreateProject(context.Background(), psA.ID, CreateProjectInput{Name: "P2", Slug: "shared"}); err == nil {
		t.Fatal("同空间项目 slug 重复时应返回错误")
	}
	// 不同空间同 slug：允许。
	if _, err := svc.CreateProject(context.Background(), psB.ID, CreateProjectInput{Name: "P3", Slug: "shared"}); err != nil {
		t.Fatalf("跨空间同 slug 应允许：%v", err)
	}
}

// TestService_Overview_Delegates 业务层 Overview 透传到 Repository，计数正确。
func TestService_Overview_Delegates(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	ps, _ := svc.CreateProjectSpace(ctx, CreateProjectSpaceInput{Name: "A", Slug: "a"})
	// 建一个 owner 成员。
	if _, err := svc.repo.db.ExecContext(ctx,
		`INSERT INTO membership (id, project_space_id, user_id, role) VALUES ($1, $2, $3, $4)`,
		"m_1", ps.ID, "u_1", "owner"); err != nil {
		t.Fatalf("seed membership: %v", err)
	}

	o, err := svc.Overview(ctx, ps.ID)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if o.Space.ID != ps.ID {
		t.Fatalf("Overview.Space.ID 不匹配：%s", o.Space.ID)
	}
	if o.Members != 1 {
		t.Fatalf("Members 计数期望 1，得到 %d", o.Members)
	}
}

// TestService_Overview_NotFound 业务层 Overview 透传 ErrNotFound。
func TestService_Overview_NotFound(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.Overview(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("期望 ErrNotFound，得到 %v", err)
	}
}
