package mwsupply

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// —— P4 Task 2: supplyDedicated 起容器前 dedicated 实例数配额 hard-block ——
//
// DedicatedQuotaChecker 注入到 Reconciler.dedQuota；nil=不强制（开发/测试/未注入 quota）。
// 非 nil 且 CheckDedicatedInstances 返错 → mkBind failed（lastErr 含超限）+ 不起容器（hard-block）。
// 强制点在 supplyDedicated reuse 判定之后、SupplyDedicated/PortRange 起容器之前（reuse 不耗新配额）。

// fakeDedQuota 可控 CheckDedicatedInstances（返 err 模拟超限）。
type fakeDedQuota struct {
	err   error
	calls int
}

func (f *fakeDedQuota) CheckDedicatedInstances(context.Context, string) error {
	f.calls++
	return f.err
}

// TestSupplyDedicated_QuotaExceeded fake dedQuota 返超限 → supplyDedicated mkBind failed、
// 不调 LaunchDedicated（fake docker.runCalls 不增）/ spec.SupplyDedicated（pg ded fake provCalls 不增）。
func TestSupplyDedicated_QuotaExceeded(t *testing.T) {
	resetRegistry(t)
	k := "fakeweb"
	RegisterKind(KindSpec{Kind: k, AddrEnv: "F_URL",
		PortRange:     func() (int, int) { return 9100, 9199 },
		ContainerName: func(short string) string { return "fake-" + short },
		LaunchDedicated: func(ctx context.Context, name string, port int) (string, error) {
			return "pw", nil
		},
	})
	r, _, _, _, dk := newReconcilerTest(t)
	r.dedQuota = &fakeDedQuota{err: errors.New("专属实例数已达上限: 5个 / 5个")} // 注入（同包字段）

	var gotStatus, gotErr string
	mkBind := func(status, _, _, lastErr string) { gotStatus, gotErr = status, lastErr }
	r.supplyDedicated(context.Background(), "app_q", "ps_q", DepService{Kind: k, Strategy: ModeDedicated}, kindRegistry[k], mkBind)

	if gotStatus != StatusFailed || !strings.Contains(gotErr, "专属实例数已达上限") {
		t.Fatalf("应 failed+lastErr 含超限，得 (%q,%q)", gotStatus, gotErr)
	}
	if len(dk.runCalls) != 0 {
		t.Fatalf("超限不应起容器，LaunchDedicated 被调 %d 次", len(dk.runCalls))
	}
}

// TestSupplyDedicated_QuotaNil 不注入(nil) → 不拦（回归既有 dedicated 行为）。
func TestSupplyDedicated_QuotaNil(t *testing.T) {
	// newReconcilerTest 默认 dedQuota=nil；既有 dedicated 用例（如 TestSupplyDedicated_*）已覆盖起容器路径，
	// 此处仅断言 nil 时不 panic、能进到 launch。用 fake kind 触发默认 PortRange 路径。
	resetRegistry(t)
	k := "fakeweb"
	called := false
	RegisterKind(KindSpec{Kind: k, AddrEnv: "F_URL",
		PortRange:     func() (int, int) { return 9100, 9199 },
		ContainerName: func(short string) string { return "fake-" + short },
		LaunchDedicated: func(ctx context.Context, name string, port int) (string, error) {
			called = true
			return "pw", nil
		},
		ReadyDedicated: func(context.Context, string, string, int, string) error { return nil },
		DedicatedEnv:   func(string) []EnvKV { return nil },
	})
	r, _, _, _, _ := newReconcilerTest(t)
	r.supplyDedicated(context.Background(), "app_q2", "ps_q", DepService{Kind: k, Strategy: ModeDedicated}, kindRegistry[k],
		func(status, _, _, _ string) {})
	if !called {
		t.Fatal("nil dedQuota 不应拦，应进入 LaunchDedicated")
	}
}
