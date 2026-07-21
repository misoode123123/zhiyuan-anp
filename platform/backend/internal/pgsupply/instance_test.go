package pgsupply

import (
	"context"
	"testing"
	"time"
)

// fakeDocker 记录调用，可控端口占用。
type fakeDocker struct {
	used     map[int]struct{}
	runCalls []struct {
		name, pwd string
		port      int
	}
	rmCalls []string
	runErr  error
}

func (f *fakeDocker) UsedPorts(context.Context) map[int]struct{} { return f.used }
func (f *fakeDocker) RunPGContainer(_ context.Context, name, pwd string, port int) error {
	f.runCalls = append(f.runCalls, struct {
		name, pwd string
		port      int
	}{name, pwd, port})
	return f.runErr
}
func (f *fakeDocker) RmForce(_ context.Context, name string) error {
	f.rmCalls = append(f.rmCalls, name)
	return nil
}

// fakeAdmin Ping 可控，其余空实现。
type fakeAdmin struct{ pingErr error }

func (fakeAdmin) CreateDatabase(context.Context, string, string) error     { return nil }
func (fakeAdmin) CreateRole(context.Context, string, string, string) error { return nil }
func (fakeAdmin) GrantAll(context.Context, string, string, string) error   { return nil }
func (fakeAdmin) DropDatabase(context.Context, string, string) error       { return nil }
func (fakeAdmin) DropRole(context.Context, string, string) error           { return nil }
func (f fakeAdmin) Ping(context.Context, string) error                     { return f.pingErr }

func TestInstanceManager_GetOrCreate_Reuse(t *testing.T) {
	s := newTestStore(t)
	_ = s.CreateInstance(context.Background(), mkInstance("ps_1")) // 项目已有实例
	dk := &fakeDocker{used: map[int]struct{}{}}
	m := NewInstanceManager(s, dk, fakeAdmin{}, "host")
	ins, err := m.GetOrCreate(context.Background(), "ps_1")
	if err != nil {
		t.Fatalf("getorcreate: %v", err)
	}
	if ins.ID != "pgi_abc" || len(dk.runCalls) != 0 {
		t.Fatalf("已有实例应复用、不起容器，得到 %+v runCalls=%v", ins, dk.runCalls)
	}
}

func TestInstanceManager_GetOrCreate_Provision(t *testing.T) {
	s := newTestStore(t)
	dk := &fakeDocker{used: map[int]struct{}{9500: {}}}
	m := NewInstanceManager(s, dk, fakeAdmin{pingErr: nil}, "10.10.0.28")
	ins, err := m.GetOrCreate(context.Background(), "ps_new")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if len(dk.runCalls) != 1 {
		t.Fatalf("应起 1 个容器，得到 %d", len(dk.runCalls))
	}
	if dk.runCalls[0].port != 9501 { // 9500 已占 → 9501
		t.Fatalf("应分配 9501，得到 %d", dk.runCalls[0].port)
	}
	if ins.Port != 9501 || ins.Host != "10.10.0.28" || ins.Status != StatusActive {
		t.Fatalf("实例字段不符: %+v", ins)
	}
	// 二次调用复用（不再起容器）
	_, _ = m.GetOrCreate(context.Background(), "ps_new")
	if len(dk.runCalls) != 1 {
		t.Fatalf("二次应复用，runCalls 应仍为 1，得到 %d", len(dk.runCalls))
	}
}

func TestInstanceManager_GetOrCreate_NoPort(t *testing.T) {
	s := newTestStore(t)
	full := map[int]struct{}{}
	for p := pgPortMin; p <= pgPortMax; p++ {
		full[p] = struct{}{}
	}
	m := NewInstanceManager(s, &fakeDocker{used: full}, fakeAdmin{}, "h")
	_, err := m.GetOrCreate(context.Background(), "ps_x")
	if err == nil {
		t.Fatal("端口全满应报错")
	}
}

// errString 是一个实现 error 的字符串类型，用于 fake Ping 恒失败。
type errString string

func (e errString) Error() string { return string(e) }

var errNotReady = errString("not ready")

func TestInstanceManager_GetOrCreate_NotReady(t *testing.T) {
	s := newTestStore(t)
	dk := &fakeDocker{used: map[int]struct{}{}}
	m := NewInstanceManager(s, dk, fakeAdmin{pingErr: errNotReady}, "h")
	// 用短 ctx：fakeAdmin 恒返回 err → waitForReady 重试到 ctx 超时 → provision 触发 RmForce
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := m.GetOrCreate(ctx, "ps_new")
	if err == nil {
		t.Fatal("PG 未 ready 应报错并清理容器")
	}
	if len(dk.rmCalls) != 1 {
		t.Fatalf("未 ready 应 rm 清理容器，得到 %d", len(dk.rmCalls))
	}
}

func TestInstanceManager_waitForReady(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Ping 立即成功路径
	if err := waitForReady(ctx, fakeAdmin{pingErr: nil}, "u"); err != nil {
		t.Fatalf("ping 成功应 ready: %v", err)
	}
}
