package mwsupply

import (
	"context"
	"strings"
	"testing"

	"zhiyuan-anp/platform/backend/internal/appdeploy"
)

// —— P2a Task 4: supplyOne 的 pg 非 shared guard 覆盖（reviewer flagged: guard had no unit test）——

// TestSupplyOne_pgNonSharedRejected 验证 pg（SupplyShared 自管 kind）在非 shared 策略下
// 命中 supplyOne 的 guard 分支 → mkBind failed（不调 SupplyShared，故 nil pgProv 不 panic）。
//
// 覆盖三种 strategy：
//   - dedicated     → 显式非 shared
//   - bind_existing → 显式非 shared
//   - ""（空）       → 默认解析为 bind_existing → 非 shared
//
// 走真 mkBind 路径（supplyOne 内部闭包 → store.UpsertBinding），从 store 读回 binding 断言
// StatusFailed + lastError 含「仅支持 shared」与「P2b」+ 无实例/token。
// newReconcilerTest 经 BuildSpecs 注册 pg（pgProv 传 nil：guard 在 SupplyShared 前触发，nil 安全）。
func TestSupplyOne_pgNonSharedRejected(t *testing.T) {
	r, appStore, db, _, _ := newReconcilerTest(t)
	ctx := context.Background()

	// 前置：确认 pg 已注册且 SupplyShared 非 nil（否则 guard 不构成、测试无意义）。
	spec, ok := LookupKind("pg")
	if !ok {
		t.Fatal("pg 应已注册（BuildSpecs），ok=false")
	}
	if spec.SupplyShared == nil {
		t.Fatal("pg SupplyShared 应非 nil（guard 的前置条件）")
	}

	cases := []struct {
		name     string // 子测名
		strategy string // 传给 DepService.Strategy 的原始值
		wantStr  string // 期望写入 binding.Strategy（dedicated 原样；空→默认 bind_existing）
	}{
		{"dedicated", ModeDedicated, ModeDedicated},
		{"bind_existing", ModeBindExisting, ModeBindExisting},
		{"empty_defaults_bind_existing", "", ModeBindExisting},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &appdeploy.Application{
				ProjectSpaceID: "ps_1", Name: "pgguard-" + c.name,
				RepoDir: "/x", InternalPort: 8080,
			}
			if err := appStore.Create(ctx, a); err != nil {
				t.Fatalf("create app: %v", err)
			}
			// 直饲 supplyOne（单依赖，不经 supplyAll 循环），触发 guard。
			r.supplyOne(ctx, a.ID, "ps_1", DepService{Kind: "pg", Strategy: c.strategy})

			binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
			if len(binds) != 1 {
				t.Fatalf("应 1 binding，得 %d (%+v)", len(binds), binds)
			}
			b := binds[0]
			if b.Status != StatusFailed {
				t.Errorf("Status 应 failed，得 %q", b.Status)
			}
			if b.ServiceKind != "pg" {
				t.Errorf("ServiceKind 应 pg，得 %q", b.ServiceKind)
			}
			if b.Strategy != c.wantStr {
				t.Errorf("Strategy 应 %q，得 %q", c.wantStr, b.Strategy)
			}
			// guard 不分配实例/token（mkBind 的 instID/token 均空）。
			if b.ServiceInstanceID != "" {
				t.Errorf("不应有实例，得 %q", b.ServiceInstanceID)
			}
			if b.IsolationToken != "" {
				t.Errorf("不应有 token，得 %q", b.IsolationToken)
			}
			// pg 注册 kind → envKey 取 spec.AddrEnv=DATABASE_URL（binding 行可读性）。
			if b.EnvKey != "DATABASE_URL" {
				t.Errorf("EnvKey 应 DATABASE_URL，得 %q", b.EnvKey)
			}
			// lastError 须明确指出 pg 仅支持 shared + 指向 P2b。
			if !strings.Contains(b.LastError, "仅支持 shared") {
				t.Errorf("LastError 应含「仅支持 shared」，得 %q", b.LastError)
			}
			if !strings.Contains(b.LastError, "P2b") {
				t.Errorf("LastError 应提及 P2b，得 %q", b.LastError)
			}
			// env 不应被写（guard 早退，未到 UpsertEnv）。
			if v, _ := appStore.GetEnvValue(ctx, a.ID, "DATABASE_URL"); v != "" {
				t.Errorf("guard 失败不应写 DATABASE_URL，得 %q", v)
			}
		})
	}
}
