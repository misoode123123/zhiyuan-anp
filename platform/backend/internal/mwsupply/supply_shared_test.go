package mwsupply

import (
	"context"
	"errors"
	"testing"

	"zhiyuan-anp/platform/backend/internal/appdeploy"
)

// —— P2a Task 1: KindSpec.SupplyShared 自管 shared 供给分支 ——

// TestSupplyShared_SelfSupplyBranch 验证 spec.SupplyShared != nil 时走自管路径：
// SupplyShared 返回 (instID, token) → mkBind bound；跳过 LookupShared/AllocSharedToken。
// 用捕获型 mkBind（不写库）只验证 supplyShared 传给 mkBind 的参数。
func TestSupplyShared_SelfSupplyBranch(t *testing.T) {
	resetRegistry(t)
	k := "fakepg"
	called := false
	RegisterKind(KindSpec{
		Kind: k, AddrEnv: "FAKEPG_URL",
		SupplyShared: func(ctx context.Context, appID, psID string) (string, string, error) {
			called = true
			return "", "db-token-1", nil
		},
	})
	r, _, _, _, _ := newReconcilerTest(t)

	// 捕获 mkBind 参数（不写库 → 无须建 app 行；reuse 分支 GetBinding 返 nil 后落入自管供给）。
	var gotStatus, gotInst, gotToken string
	mkBind := func(status, instID, token, lastErr string) {
		gotStatus, gotInst, gotToken = status, instID, token
	}
	r.supplyShared(context.Background(), "app_fake", "ps_default",
		DepService{Kind: k, Strategy: ModeShared}, kindRegistry[k], mkBind)

	if !called {
		t.Fatal("spec.SupplyShared 未被调用")
	}
	if gotStatus != StatusBound || gotInst != "" || gotToken != "db-token-1" {
		t.Fatalf("mkBind 应 (bound, inst=\"\", token=\"db-token-1\")，得 (%q,%q,%q)",
			gotStatus, gotInst, gotToken)
	}
}

// TestSupplyShared_SelfSupplyError 验证 SupplyShared 返错 → mkBind failed（lastErr=err）。
func TestSupplyShared_SelfSupplyError(t *testing.T) {
	resetRegistry(t)
	k := "fakepg"
	RegisterKind(KindSpec{
		Kind: k, AddrEnv: "FAKEPG_URL",
		SupplyShared: func(ctx context.Context, appID, psID string) (string, string, error) {
			return "", "", errors.New("pg 建库失败")
		},
	})
	r, _, _, _, _ := newReconcilerTest(t)

	var gotStatus, gotErr string
	mkBind := func(status, instID, token, lastErr string) {
		gotStatus, gotErr = status, lastErr
	}
	r.supplyShared(context.Background(), "app_fake", "ps_default",
		DepService{Kind: k, Strategy: ModeShared}, kindRegistry[k], mkBind)

	if gotStatus != StatusFailed || gotErr != "pg 建库失败" {
		t.Fatalf("应 failed + lastErr=\"pg 建库失败\"，得 (%q,%q)", gotStatus, gotErr)
	}
}

// TestSupplyShared_SelfSupplyReuse 验证 binding 已 bound（IsolationToken 非空）→ 不再调 SupplyShared（防重复建库）。
// 用真 mkBind（写库，镜像 supplyOne 的 mkBind）：首次供给 binding 入库 → 二次走 reuse 分支。
// IsolationToken != "" 兼容 pg 的空 instID（UpsertBinding NULLIF 空 instID → NULL，FK 不报错）。
func TestSupplyShared_SelfSupplyReuse(t *testing.T) {
	resetRegistry(t)
	k := "fakepg"
	calls := 0
	RegisterKind(KindSpec{
		Kind: k, AddrEnv: "FAKEPG_URL",
		SupplyShared: func(ctx context.Context, appID, psID string) (string, string, error) {
			calls++
			if calls > 1 {
				return "", "", errors.New("不应被二次调用")
			}
			return "", "db-token-1", nil
		},
	})
	r, appStore, _, _, _ := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "sshr", RepoDir: "/x", InternalPort: 8080}
	if err := appStore.Create(ctx, a); err != nil {
		t.Fatalf("create app: %v", err)
	}

	// 真 mkBind：写库（镜像 supplyOne 的 mkBind），使二次调用 GetBinding 能读到 bound 行走 reuse。
	mkBind := func(status, instID, token, lastErr string) {
		_ = r.store.UpsertBinding(ctx, &ServiceBinding{
			AppID: a.ID, ProjectSpaceID: "ps_1", ServiceKind: k,
			Strategy: ModeShared, ServiceInstanceID: instID, IsolationToken: token,
			EnvKey: "FAKEPG_URL", Status: status, LastError: lastErr,
		})
	}
	// 第一次供给 → bound（SupplyShared 调 1 次，binding 入库 token=db-token-1）
	r.supplyShared(ctx, a.ID, "ps_1", DepService{Kind: k, Strategy: ModeShared}, kindRegistry[k], mkBind)
	// 第二次 → reuse 分支命中（IsolationToken != ""），不调 SupplyShared
	r.supplyShared(ctx, a.ID, "ps_1", DepService{Kind: k, Strategy: ModeShared}, kindRegistry[k], mkBind)

	if calls != 1 {
		t.Fatalf("SupplyShared 应只调 1 次（reuse），实际 %d", calls)
	}
}
