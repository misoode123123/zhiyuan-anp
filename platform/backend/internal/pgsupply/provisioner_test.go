package pgsupply

import (
	"context"
	"testing"
)

// fakeEnvWriter 记录 UpsertEnv 调用。
type fakeEnvWriter struct {
	writes []struct {
		appID, key, value string
		secret            bool
		source            string
	}
	err error
}

func (f *fakeEnvWriter) UpsertEnv(_ context.Context, appID, key, value string, secret bool, source string) error {
	f.writes = append(f.writes, struct {
		appID, key, value string
		secret            bool
		source            string
	}{appID, key, value, secret, source})
	return f.err
}

// recordingInstance 固定返回一个实例（不起容器）。
type recordingInstance struct{ ins *PGInstance }

func (r recordingInstance) GetOrCreate(context.Context, string) (*PGInstance, error) {
	return r.ins, nil
}

func TestProvisioner_Provision(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ins := mkInstance("ps_1")
	_ = s.CreateInstance(ctx, ins)
	env := &fakeEnvWriter{}
	p := NewProvisioner(recordingInstance{ins: ins}, s, fakeAdmin{}, env, nil)
	ad, err := p.Provision(ctx, "ps_1", "app_1")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if ad.Status != StatusReady || ad.DBHost != "h" || ad.PGInstanceID != ins.ID {
		t.Fatalf("库记录字段不符: %+v", ad)
	}
	// 写了 DATABASE_URL（secret）
	if len(env.writes) != 1 || env.writes[0].key != "DATABASE_URL" || !env.writes[0].secret {
		t.Fatalf("应写 DATABASE_URL(secret)，得到 %+v", env.writes)
	}
	// DSN 含库名
	if !contains(env.writes[0].value, ad.DBName) {
		t.Fatalf("DSN 应含库名 %s，得到 %s", ad.DBName, env.writes[0].value)
	}
	// 库记录入库
	got, _ := s.GetAppDBByApp(ctx, "app_1")
	if got == nil || got.DBName != ad.DBName {
		t.Fatalf("库记录应入库，得到 %+v", got)
	}
}

func TestProvisioner_Cleanup(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ins := mkInstance("ps_1")
	_ = s.CreateInstance(ctx, ins)
	env := &fakeEnvWriter{}
	p := NewProvisioner(recordingInstance{ins: ins}, s, fakeAdmin{}, env, nil)
	ad, _ := p.Provision(ctx, "ps_1", "app_1")
	_ = ad
	// cleanup 删库记录（status=deleted）
	if err := p.Cleanup(ctx, "app_1"); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if got, _ := s.GetAppDBByApp(ctx, "app_1"); got != nil {
		t.Fatalf("cleanup 后应查不到库记录，得到 %+v", got)
	}
	// cleanup 不存在的应用不报错
	if err := p.Cleanup(ctx, "app_ghost"); err != nil {
		t.Fatalf("cleanup 不存在应用应不报错: %v", err)
	}
}

// countingAdmin 计数 CreateDatabase/CreateRole 调用，验证幂等复用时未重建库/role。
// 指针接收者：计数需可变；其余方法空实现（与 fakeAdmin 行为一致，仅多了计数）。
type countingAdmin struct {
	createDB   int
	createRole int
}

func (f *countingAdmin) CreateDatabase(context.Context, string, string) error     { f.createDB++; return nil }
func (f *countingAdmin) CreateRole(context.Context, string, string, string) error { f.createRole++; return nil }
func (f *countingAdmin) GrantAll(context.Context, string, string, string) error   { return nil }
func (f *countingAdmin) DropDatabase(context.Context, string, string) error       { return nil }
func (f *countingAdmin) DropRole(context.Context, string, string) error           { return nil }
func (f *countingAdmin) Ping(context.Context, string) error                       { return nil }
func (f *countingAdmin) DatabaseSizes(context.Context, string, []string) (map[string]int64, error) {
	return nil, nil
}

// TestProvision_IdempotentReuse 验证：同 app 已 ready 时，再次 Provision 复用既有库记录，
// 不再 CreateDatabase/CreateRole，返回同 DBName。
// 使 P2a 新增 declared pg=shared 与既有无条件 auto-provision 共存期不双建。
func TestProvision_IdempotentReuse(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ins := mkInstance("ps_1")
	_ = s.CreateInstance(ctx, ins)
	adm := &countingAdmin{}
	p := NewProvisioner(recordingInstance{ins: ins}, s, adm, &fakeEnvWriter{}, nil)

	first, err := p.Provision(ctx, "ps_1", "app_1")
	if err != nil {
		t.Fatalf("首次 Provision: %v", err)
	}
	dbBefore, roleBefore := adm.createDB, adm.createRole

	// 二次 Provision 同 app：应复用 ready 库，不再 CreateDatabase/CreateRole。
	second, err := p.Provision(ctx, "ps_1", "app_1")
	if err != nil {
		t.Fatalf("二次 Provision 应复用而非报错: %v", err)
	}
	if adm.createDB != dbBefore || adm.createRole != roleBefore {
		t.Fatalf("二次 Provision 应复用未重建，createDB %d→%d createRole %d→%d",
			dbBefore, adm.createDB, roleBefore, adm.createRole)
	}
	if second.DBName != first.DBName {
		t.Fatalf("复用库名不一致 %q vs %q", second.DBName, first.DBName)
	}
	// store 中仍只有一条 ready 记录，且为首次的库名
	got, _ := s.GetAppDBByApp(ctx, "app_1")
	if got == nil || got.DBName != first.DBName || got.Status != StatusReady {
		t.Fatalf("store 库记录应保持首次 ready，got=%+v", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
