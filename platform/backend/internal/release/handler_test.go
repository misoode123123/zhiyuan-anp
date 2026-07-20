package release

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"

	"zhiyuan-anp/platform/backend/internal/config"
)

// fakeTestGate TestGate 接口的桩实现，返回预设的 count/err。
type fakeTestGate struct {
	count int
	err   error
	calls int
	last  string
}

func (f *fakeTestGate) PassedCountByRequirement(_ context.Context, reqID string) (int, error) {
	f.calls++
	f.last = reqID
	return f.count, f.err
}

// newCfgStore 建内存 SQLite + system_config 表，返回装填好缓存的 config.Store。
// 用于驱动 Handler.testGateEnabled() 读 release_require_passed_test 开关。
func newCfgStore(t *testing.T, kv map[string]string) *config.Store {
	t.Helper()
	db, err := sqlx.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.MustExec(`CREATE TABLE system_config (
  key         TEXT PRIMARY KEY,
  value       TEXT,
  category    TEXT NOT NULL DEFAULT 'general',
  description TEXT,
  updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	cs := config.NewStore(db)
	for k, v := range kv {
		if err := cs.Set(context.Background(), k, v, "general", ""); err != nil {
			t.Fatalf("cfg set %s: %v", k, err)
		}
	}
	return cs
}

// TestTernary 纯逻辑：cond=true 返回 a，cond=false 返回 b。
// 该函数被 Create handler 用于选择 note 文案，简单但关键。
func TestTernary(t *testing.T) {
	if got := ternary(true, "yes", "no"); got != "yes" {
		t.Fatalf("cond=true 应返回 a，得到 %q", got)
	}
	if got := ternary(false, "yes", "no"); got != "no" {
		t.Fatalf("cond=false 应返回 b，得到 %q", got)
	}
	// 边界：空串
	if got := ternary(true, "", "fallback"); got != "" {
		t.Fatalf("cond=true 且 a 为空时应返回空串，得到 %q", got)
	}
}

// TestHandler_TestGateEnabled 表驱动：测试门禁开关在各依赖组合下的开/关判定。
// 覆盖：① cfg 缺失 ② testGate 缺失 ③ 开关=false ④ 开关=true 且依赖齐 ⑤ 开关未配置（fallback false）。
func TestHandler_TestGateEnabled(t *testing.T) {
	t.Run("cfg为nil返回false", func(t *testing.T) {
		h := &Handler{testGate: &fakeTestGate{}}
		if h.testGateEnabled() {
			t.Fatal("cfg 为 nil 时应返回 false")
		}
	})
	t.Run("testGate为nil返回false", func(t *testing.T) {
		cs := newCfgStore(t, map[string]string{"release_require_passed_test": "true"})
		h := &Handler{cfg: cs} // testGate 仍为 nil
		if h.testGateEnabled() {
			t.Fatal("testGate 为 nil 时应返回 false")
		}
	})
	t.Run("开关=true且依赖齐返回true", func(t *testing.T) {
		cs := newCfgStore(t, map[string]string{"release_require_passed_test": "true"})
		h := &Handler{cfg: cs, testGate: &fakeTestGate{}}
		if !h.testGateEnabled() {
			t.Fatal("开关 true 且依赖齐时应返回 true")
		}
	})
	t.Run("开关=false返回false", func(t *testing.T) {
		cs := newCfgStore(t, map[string]string{"release_require_passed_test": "false"})
		h := &Handler{cfg: cs, testGate: &fakeTestGate{}}
		if h.testGateEnabled() {
			t.Fatal("开关 false 时应返回 false")
		}
	})
	t.Run("开关未配置fallback为false", func(t *testing.T) {
		// 缓存里没有 release_require_passed_test，Get 返回 fallback "false"
		cs := newCfgStore(t, map[string]string{"other_key": "true"})
		h := &Handler{cfg: cs, testGate: &fakeTestGate{}}
		if h.testGateEnabled() {
			t.Fatal("开关未配置时应 fallback 到 false")
		}
	})
}
