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

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
