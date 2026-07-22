package pgsupply

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestIsUniqueViolation 直接断言 helper：*pgconn.PgError(23505) 识别，其他错误不识别。
func TestIsUniqueViolation(t *testing.T) {
	if isUniqueViolation(nil) {
		t.Fatal("nil 不应是 unique violation")
	}
	if isUniqueViolation(errString("anything")) {
		t.Fatal("普通错误不应识别为 unique violation")
	}
	// 构造真实 *pgconn.PgError
	pgErr := &pgconn.PgError{Code: "23505", Message: "unique constraint"}
	if !isUniqueViolation(pgErr) {
		t.Fatal("23505 应识别为 unique violation")
	}
	other := &pgconn.PgError{Code: "23503", Message: "fk violation"}
	if isUniqueViolation(other) {
		t.Fatal("非 23505 不应识别为 unique violation")
	}
}

// TestProvision_CreateInstanceUniqueConflictFallback 模拟并发兜底核心路径：
// 项目已存在 active 实例（partial unique 冲突源）→ provision 自己起容器后 CreateInstance 冲突 →
// 清自己的容器 + 重查复用项目里已有的实例。
//
// 用真实 PG partial unique index 触发真实 23505（不需要真起 docker：osDocker 调用靠 UsedPorts 不空 + RunPGContainer 失败兜底）。
// 改用 fakeDocker（不起容器）+ 直接预先 CreateInstance 一个 active 实例制造冲突。
func TestProvision_CreateInstanceUniqueConflictFallback(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 预先建一个 active 实例（模拟另一并发 goroutine 已建）
	existing := mkInstance("ps_1")
	existing.ID = "pgi_existing"
	existing.ContainerName = "pg-existing-aaa"
	if err := s.CreateInstance(ctx, existing); err != nil {
		t.Fatalf("seed existing: %v", err)
	}

	// 用 fakeAdmin（Ping 立即成功）+ fakeDocker（记 RmForce 调用）
	dk := &fakeDocker{used: map[int]struct{}{}}
	m := NewInstanceManager(s, dk, fakeAdmin{}, "h")

	// 直接调 provision（绕过 GetOrCreate 的 GetInstanceByProject 缓存，模拟两并发都过查无分支后同时登记）
	// 注意：provision 会真起 fakeDocker 容器 + CreateInstance 因 partial unique 必然冲突
	ins, err := m.provision(ctx, "ps_1")
	if err != nil {
		t.Fatalf("provision 冲突应 fallback 复用，得到 err=%v", err)
	}
	// 应返回已存在的实例（不是新建的）
	if ins.ID != "pgi_existing" {
		t.Fatalf("冲突 fallback 应复用 existing，得到 %+v", ins)
	}
	// 自己刚起的容器应被 RmForce 清理（避免孤儿容器）
	if len(dk.rmCalls) != 1 {
		t.Fatalf("自己起的容器应被 RmForce 一次，得到 %v", dk.rmCalls)
	}
	// docker 应被调起一次（RunPGContainer）—— 自己的容器起完才发现冲突
	if len(dk.runCalls) != 1 {
		t.Fatalf("应起 1 个自己的容器（事后清），得到 %d", len(dk.runCalls))
	}
}

// TestGetOrCreate_ConcurrentRealPG 真实并发：50 个 goroutine 同时 GetOrCreate 同一个新项目，
// partial unique index 兜底，所有 goroutine 都应拿到同一个实例 ID（无重复容器/记录）。
//
// 这是 I5 的端到端验证：去除 partial unique 的话会有多实例；有了它 + fallback 全部归一。
func TestGetOrCreate_ConcurrentRealPG(t *testing.T) {
	s := newTestStore(t)
	dk := &fakeDocker{used: map[int]struct{}{}}
	m := NewInstanceManager(s, dk, fakeAdmin{}, "h")

	const N = 50
	var wg sync.WaitGroup
	results := make([]*PGInstance, N)
	errs := make([]error, N)
	start := make(chan struct{})
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(idx int) {
			defer wg.Done()
			<-start // 同时发车
			ins, err := m.GetOrCreate(context.Background(), "ps_new")
			results[idx] = ins
			errs[idx] = err
		}(i)
	}
	close(start)
	wg.Wait()

	// 全部应成功
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d 失败: %v", i, err)
		}
	}
	// 所有拿到的实例 ID 应一致（首个成功的为准）
	var firstID string
	for i, ins := range results {
		if ins == nil {
			continue
		}
		if firstID == "" {
			firstID = ins.ID
		} else if ins.ID != firstID {
			t.Fatalf("goroutine %d 应返回同一实例 %s，得到 %s", i, firstID, ins.ID)
		}
	}
	if firstID == "" {
		t.Fatal("至少一个 goroutine 应成功建实例")
	}
	// 数据库里该项目下应只有 1 个 active 实例（partial unique 保证）
	list, err := s.ListInstancesByProject(context.Background(), "ps_new")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("项目下应只有 1 条 active 实例记录，得到 %d 条: %+v", len(list), list)
	}
}
