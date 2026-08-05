package mwsupply

import (
	"context"
	"errors"
	"strings"
	"testing"

	"zhiyuan-anp/platform/backend/internal/appdeploy"
)

// —— P2b Task 1: ConnValue（bind_existing 自管连接值）+ SupplyDedicated（自管 dedicated 供给）分支 ——
//
// 这两个字段均为可选（nil → 走默认路径），redis/milvus 不设（零行为变化）。
// pg 在 T2/T3 才设它们；本任务用 fake kind 覆盖分支，不依赖 pg。

// TestSupplyOne_bindExisting_ConnValue 验证 spec.ConnValue != nil 时 bind_existing 注入 ConnValue 返回值
// （如 pg 的完整 DSN=inst.AuthRef），而非默认 ConnStr(inst)=host:port。
//
// fake kind：ConnValue 返回 inst.AuthRef（模拟 pg 登记的 DSN）。
// 经真 mkBind 路径（supplyOne → store.UpsertBinding + env.UpsertEnv），从 store/env 读回断言。
func TestSupplyOne_bindExisting_ConnValue(t *testing.T) {
	resetRegistry(t)
	k := "fakeweb"
	RegisterKind(KindSpec{
		Kind: k, AddrEnv: "FAKEWEB_URL",
		ConnValue: func(inst *ServiceInstance) string {
			return inst.AuthRef // 模拟 pg 登记的完整 DSN
		},
	})
	r, appStore, db, _, _ := newReconcilerTest(t)
	ctx := context.Background()

	// 登记一个 fake bind_existing 实例（AuthRef 存完整 DSN，模拟 pg 运维登记）。
	if _, err := r.RegisterBindExisting(ctx, "", appdeploy.MWInstance{
		Kind: k, Name: "fake-web", Host: "10.10.0.28", Port: 5432,
		AuthRef: "postgres://user:pass@10.10.0.28:5432/fakedb",
	}); err != nil {
		t.Fatalf("register bind_existing: %v", err)
	}

	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "cvapp", RepoDir: "/x", InternalPort: 8080}
	if err := appStore.Create(ctx, a); err != nil {
		t.Fatalf("create app: %v", err)
	}

	// 默认策略 "" → bind_existing。
	r.supplyOne(ctx, a.ID, "ps_1", DepService{Kind: k})

	wantDSN := "postgres://user:pass@10.10.0.28:5432/fakedb"
	v, _ := appStore.GetEnvValue(ctx, a.ID, "FAKEWEB_URL")
	if v != wantDSN {
		t.Fatalf("FAKEWEB_URL 应为 ConnValue 返回的 DSN %q，得 %q", wantDSN, v)
	}
	// 不应是 ConnStr 默认值 host:port（说明走了 ConnValue 分支而非默认）。
	if v == "10.10.0.28:5432" {
		t.Fatalf("不应走默认 ConnStr(host:port)，说明 ConnValue 未生效")
	}
	// source=platform。
	src, _ := appStore.GetEnvSource(ctx, a.ID, "FAKEWEB_URL")
	if src != "platform" {
		t.Fatalf("source 应 platform，得 %q", src)
	}
	// binding bound + 实例已绑。
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 1 || binds[0].Status != StatusBound {
		t.Fatalf("应 1 binding bound，得 %+v", binds)
	}
}

// TestSupplyDedicated_SupplyDedicatedBranch 验证 spec.SupplyDedicated != nil 时 supplyDedicated 走自管路径：
// 调 SupplyDedicated(ctx, appID, psID, r.host) → mkBind bound(instID, token)；跳过默认 PortRange/LaunchDedicated。
// 用捕获型 mkBind（不写库 → 无须建 app/instance 行；reuse 判定 GetBinding 返 nil 后落入自管供给）。
func TestSupplyDedicated_SupplyDedicatedBranch(t *testing.T) {
	resetRegistry(t)
	k := "fakeweb"
	called := false
	RegisterKind(KindSpec{
		Kind: k, AddrEnv: "FAKEWEB_URL",
		SupplyDedicated: func(ctx context.Context, appID, psID, host string) (string, string, error) {
			called = true
			if host != "testdeploy" {
				t.Errorf("host 应 r.host=testdeploy，得 %q", host)
			}
			return "fake-inst-1", "fake-token", nil
		},
	})
	r, _, _, _, _ := newReconcilerTest(t)

	var gotStatus, gotInst, gotToken, gotErr string
	mkBind := func(status, instID, token, lastErr string) {
		gotStatus, gotInst, gotToken, gotErr = status, instID, token, lastErr
	}
	r.supplyDedicated(context.Background(), "app_fake", "ps_default",
		DepService{Kind: k, Strategy: ModeDedicated}, kindRegistry[k], mkBind)

	if !called {
		t.Fatal("spec.SupplyDedicated 未被调用（应走自管 dedicated 分支）")
	}
	if gotStatus != StatusBound || gotInst != "fake-inst-1" || gotToken != "fake-token" || gotErr != "" {
		t.Fatalf("mkBind 应 (bound, fake-inst-1, fake-token, \"\")，得 (%q,%q,%q,%q)",
			gotStatus, gotInst, gotToken, gotErr)
	}
}

// TestSupplyDedicated_SupplyDedicatedError 验证 SupplyDedicated 返错 → mkBind failed（lastErr 含 kind+原错）。
func TestSupplyDedicated_SupplyDedicatedError(t *testing.T) {
	resetRegistry(t)
	k := "fakeweb"
	RegisterKind(KindSpec{
		Kind: k, AddrEnv: "FAKEWEB_URL",
		SupplyDedicated: func(ctx context.Context, appID, psID, host string) (string, string, error) {
			return "", "", errors.New("起容器失败")
		},
	})
	r, _, _, _, _ := newReconcilerTest(t)

	var gotStatus, gotErr string
	mkBind := func(status, instID, token, lastErr string) {
		gotStatus, gotErr = status, lastErr
	}
	r.supplyDedicated(context.Background(), "app_fake", "ps_default",
		DepService{Kind: k, Strategy: ModeDedicated}, kindRegistry[k], mkBind)

	if gotStatus != StatusFailed {
		t.Fatalf("应 failed，得 %q", gotStatus)
	}
	if !strings.Contains(gotErr, "fakeweb") || !strings.Contains(gotErr, "起容器失败") {
		t.Fatalf("lastError 应含 kind(fakeweb)+原错(起容器失败)，得 %q", gotErr)
	}
}
