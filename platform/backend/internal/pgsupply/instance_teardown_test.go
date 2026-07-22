package pgsupply

import (
	"context"
	"testing"
)

// TestTeardownForProject_Mixed 验证三种清理路径：
//   - managed + 有 container_name → docker rm 成功 → Removed++ 记录删
//   - external + 无 container_name → NoContainer++ 记录删
//   - docker rm 失败 → Failed++ 记录保留（便于人工排查）
//
// partial unique index（迁移 000005）限制每项目至多 1 个 active，故本测试把状态改 draining 腾位。
func TestTeardownForProject_Mixed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 实例1：managed + 容器名（docker rm 成功）
	mngd := mkInstance("ps_1")
	mngd.ID = "pgi_managed"
	mngd.ContainerName = "pg-managed-xxx"
	if err := s.CreateInstance(ctx, mngd); err != nil {
		t.Fatalf("create managed: %v", err)
	}
	// 让出 active 位（partial unique）
	if _, err := s.db.ExecContext(ctx, `UPDATE pg_instance SET status='draining' WHERE id=$1`, mngd.ID); err != nil {
		t.Fatalf("drain mngd: %v", err)
	}

	// 实例2：external + 无容器名
	ext := mkInstance("ps_1")
	ext.ID = "pgi_external"
	ext.ContainerName = ""
	ext.DeployMode = DeployExternal
	if err := s.CreateInstance(ctx, ext); err != nil {
		t.Fatalf("create external: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE pg_instance SET status='draining' WHERE id=$1`, ext.ID); err != nil {
		t.Fatalf("drain ext: %v", err)
	}

	// 实例3：active + 容器名，docker rm 故意失败
	rmfail := mkInstance("ps_1")
	rmfail.ID = "pgi_rmfail"
	rmfail.ContainerName = "pg-rmfail-yyy"
	if err := s.CreateInstance(ctx, rmfail); err != nil {
		t.Fatalf("create rmfail: %v", err)
	}

	// fakeDocker.rmErr=boom：所有 rm 都失败
	dk := &fakeDocker{used: map[int]struct{}{}, rmErr: errString("docker rm boom")}
	m := NewInstanceManager(s, dk, fakeAdmin{}, "h")

	r := m.TeardownForProject(ctx, "ps_1")
	if r.Total != 3 {
		t.Fatalf("Total 应=3，得到 %+v", r)
	}
	// mngd+rmfail 都有容器名且 rm 失败 → Failed=2
	if r.Failed != 2 || len(r.FailedIDs) != 2 {
		t.Fatalf("Failed 应=2 (mngd+rmfail)，得到 %+v", r)
	}
	// ext 无容器名 → NoContainer=1（仅删记录）
	if r.NoContainer != 1 {
		t.Fatalf("NoContainer 应=1 (ext)，得到 %+v", r)
	}
	// rm 全失败 → Removed=0
	if r.Removed != 0 {
		t.Fatalf("Removed 应=0（rm 全失败），得到 %+v", r)
	}
	// docker rm 应被调 2 次（mngd+rmfail）
	if len(dk.rmCalls) != 2 {
		t.Fatalf("docker rm 应调 2 次，得到 %v", dk.rmCalls)
	}
	// ext 记录被删（NoContainer 分支）；mngd/rmfail 记录保留（rm 失败）
	_, errMngd := s.GetInstance(ctx, mngd.ID)
	_, errRmfail := s.GetInstance(ctx, rmfail.ID)
	_, errExt := s.GetInstance(ctx, ext.ID)
	// GetInstance 记录存在 → err=nil；记录被删 → sql.ErrNoRows（非 nil）
	if errMngd != nil || errRmfail != nil {
		t.Fatalf("rm 失败的实例记录应保留（err=nil），得到 errMngd=%v errRmfail=%v", errMngd, errRmfail)
	}
	if errExt == nil {
		t.Fatal("ext（无容器名）记录应被删（GetInstance 应返 not found），但记录还在")
	}
}

// TestTeardownForProject_Success managed rm 成功 → Removed=1，记录被删。
func TestTeardownForProject_Success(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mngd := mkInstance("ps_1")
	mngd.ID = "pgi_ok"
	mngd.ContainerName = "pg-ok-zzz"
	if err := s.CreateInstance(ctx, mngd); err != nil {
		t.Fatalf("create: %v", err)
	}

	dk := &fakeDocker{used: map[int]struct{}{}}
	m := NewInstanceManager(s, dk, fakeAdmin{}, "h")

	r := m.TeardownForProject(ctx, "ps_1")
	if r.Total != 1 || r.Removed != 1 || r.Failed != 0 {
		t.Fatalf("应 Removed=1，得到 %+v", r)
	}
	if len(dk.rmCalls) != 1 || dk.rmCalls[0] != "pg-ok-zzz" {
		t.Fatalf("docker rm 应调 1 次且名字正确，得到 %v", dk.rmCalls)
	}
	// 记录被删
	if _, err := s.GetInstance(ctx, mngd.ID); err == nil {
		t.Fatal("实例记录应被删")
	}
}

// TestTeardownForProject_NoInstance 项目下无实例 → 空结果，不报错。
func TestTeardownForProject_NoInstance(t *testing.T) {
	s := newTestStore(t)
	dk := &fakeDocker{used: map[int]struct{}{}}
	m := NewInstanceManager(s, dk, fakeAdmin{}, "h")
	r := m.TeardownForProject(context.Background(), "ps_none_exists")
	if r.Total != 0 || r.Removed != 0 {
		t.Fatalf("无实例应空结果，得到 %+v", r)
	}
}
