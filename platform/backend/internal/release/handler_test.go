package release

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"

	"zhiyuan-anp/platform/backend/internal/change"
	"zhiyuan-anp/platform/backend/internal/config"
	"zhiyuan-anp/platform/backend/internal/requirement"
	"zhiyuan-anp/platform/backend/internal/testutil"
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

// newCfgStore 连 anp_test PG + 清 system_config 表，返回装填好缓存的 config.Store。
// 用于驱动 Handler.testGateEnabled() 读 release_require_passed_test 开关。
// 替代 sqlite :memory:（sqlite 漏 PG 类型 bug，见 memory sqlite-test-pg-type-trap）。
func newCfgStore(t *testing.T, kv map[string]string) *config.Store {
	t.Helper()
	var db *sqlx.DB = testutil.TestDB(t)
	testutil.Truncate(t, db, "system_config")
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

// ===== 发布中心 Create：审批前置 + 闭环回写（PRD 2026-07-26 主线闭环收敛）=====

// newReleaseHandler 建 release Handler（真实 PG store；appDeploy=nil 不触发 docker；cfg=nil 门禁关）。
func newReleaseHandler(t *testing.T) (*Handler, *sqlx.DB) {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "release_record", "change_request", "requirement")
	return &Handler{
		store:   NewStore(db),
		changes: change.NewStore(db),
		reqRepo: requirement.NewRepository(db),
		// appDeploy/cfg/testGate 均 nil：不部署、门禁关，聚焦审批前置与回写
	}, db
}

// seedReqAndChange 建一条需求（reqID 固定）+ 一条 source_id=reqID 的变更，返回 chgID。
// chgStatus="approved" 时再审批通过。
func seedReqAndChange(t *testing.T, h *Handler, psID, chgStatus string) (reqID, chgID string) {
	t.Helper()
	ctx := context.Background()
	if err := h.reqRepo.Create(ctx, &requirement.Requirement{
		ID: "req_rt", ProjectSpaceID: psID, Title: "回写测试", Status: "developing",
	}); err != nil {
		t.Fatalf("seed req: %v", err)
	}
	chg := &change.ChangeRequest{ProjectSpaceID: psID, Kind: "code", SourceID: "req_rt", Output: "diff"}
	if err := h.changes.Create(ctx, chg); err != nil {
		t.Fatalf("seed chg: %v", err)
	}
	if chgStatus == "approved" {
		if err := h.changes.Decide(ctx, chg.ID, "approved", "admin"); err != nil {
			t.Fatalf("decide: %v", err)
		}
	}
	return "req_rt", chg.ID
}

func newReleaseRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h.Register(r.Group("/api/v1"))
	return r
}

func doReleaseReq(t *testing.T, r http.Handler, psID, changeID string) (int, map[string]interface{}) {
	t.Helper()
	b, _ := json.Marshal(map[string]interface{}{"change_id": changeID})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/project-spaces/"+psID+"/releases", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, resp
}

// noteOf 从响应信封取 data.note（缺失返回空串）。
func noteOf(resp map[string]interface{}) string {
	d, _ := resp["data"].(map[string]interface{})
	n, _ := d["note"].(string)
	return n
}

// TestCreate_RejectsNonApproved 变更未审批（pending）→ 发布中心必须拒绝（409），不得绕过 G3。
func TestCreate_RejectsNonApproved(t *testing.T) {
	h, _ := newReleaseHandler(t)
	_, chgID := seedReqAndChange(t, h, "ps_t", "pending")
	r := newReleaseRouter(h)

	code, resp := doReleaseReq(t, r, "ps_t", chgID)
	if code != 409 {
		t.Fatalf("pending 变更发布应 409，得到 %d body=%v", code, resp)
	}
	// 被拒不应建发布记录
	if resp["code"].(float64) == 0 {
		t.Fatal("被拒响应 code 不应为 0")
	}
}

// TestCreate_DeliversRequirement approved 变更（source_id=reqID）→ 发布成功，需求 status=delivered。
func TestCreate_DeliversRequirement(t *testing.T) {
	h, _ := newReleaseHandler(t)
	reqID, chgID := seedReqAndChange(t, h, "ps_t", "approved")
	r := newReleaseRouter(h)

	code, resp := doReleaseReq(t, r, "ps_t", chgID)
	if code != 200 && code != 201 {
		t.Fatalf("approved 变更发布应成功，得到 %d body=%v", code, resp)
	}
	got, _ := h.reqRepo.Get(context.Background(), reqID)
	if got.Status != "delivered" {
		t.Fatalf("来源需求应被标记 delivered，得到 %s", got.Status)
	}
	if !strings.Contains(noteOf(resp), "已交付") {
		t.Fatalf("回写成功时 note 应含「已交付」，得到 %q", noteOf(resp))
	}
}

// TestCreate_HonestWhenSourceNotRequirement source_id 匹配不到需求 → 回写 0 行，
// note 不得谎报「已交付」（修复前：接口报成功 + note「需求已交付」但状态没变）。
func TestCreate_HonestWhenSourceNotRequirement(t *testing.T) {
	h, _ := newReleaseHandler(t)
	ctx := context.Background()
	// 变更 source_id 是 appID（非 requirement），模拟修复前 appdeploy 路径产物
	chg := &change.ChangeRequest{ProjectSpaceID: "ps_t", Kind: "code", SourceID: "app_ghost", Output: "x"}
	if err := h.changes.Create(ctx, chg); err != nil {
		t.Fatalf("create chg: %v", err)
	}
	if err := h.changes.Decide(ctx, chg.ID, "approved", "admin"); err != nil {
		t.Fatalf("decide: %v", err)
	}
	r := newReleaseRouter(h)

	code, resp := doReleaseReq(t, r, "ps_t", chg.ID)
	if code >= 500 {
		t.Fatalf("不应 5xx，得到 %d body=%v", code, resp)
	}
	if strings.Contains(noteOf(resp), "已交付") {
		t.Fatalf("source_id 未解析到需求时 note 不得含「已交付」（谎报），得到 %q", noteOf(resp))
	}
}
