package workspace

import (
	"context"
	"errors"
	"testing"
)

// TestService_DeleteProjectSpace_WithHook 验证：
//   - 删除前 teardown 钩子被调用（拿到正确的 psID）
//   - 钩子失败不阻塞删除（数据级联由 FK CASCADE）
//   - 删除后 GetProjectSpace 返回 ErrNotFound
func TestService_DeleteProjectSpace_WithHook(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	ps, err := svc.CreateProjectSpace(ctx, CreateProjectSpaceInput{Name: "待删", Slug: "del-me"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// 注册两个钩子：一个成功、一个失败；都应被调用但不阻塞删除
	called := []string{}
	svc.AddTeardownHook(func(c context.Context, psID string) error {
		called = append(called, "ok:"+psID)
		return nil
	})
	svc.AddTeardownHook(func(c context.Context, psID string) error {
		called = append(called, "fail:"+psID)
		return errors.New("simulated teardown failure")
	})

	if err := svc.DeleteProjectSpace(ctx, ps.ID); err != nil {
		t.Fatalf("delete 应成功（钩子失败不阻塞）: %v", err)
	}
	if len(called) != 2 {
		t.Fatalf("两个钩子都应被调用，得到 %v", called)
	}
	for _, c := range called {
		if !containsStr(c, ps.ID) {
			t.Fatalf("钩子应收到 psID=%s，得到 %s", ps.ID, c)
		}
	}
	// 删除后查不到
	if _, err := svc.GetProjectSpace(ctx, ps.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后应 ErrNotFound，得到 %v", err)
	}
}

// TestService_DeleteProjectSpace_NotFound 删不存在空间 → ErrNotFound（不调钩子）。
func TestService_DeleteProjectSpace_NotFound(t *testing.T) {
	svc := newTestService(t)
	called := false
	svc.AddTeardownHook(func(context.Context, string) error { called = true; return nil })

	err := svc.DeleteProjectSpace(context.Background(), "ps_nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("应 ErrNotFound，得到 %v", err)
	}
	if called {
		t.Fatal("不存在的空间不应调钩子")
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
