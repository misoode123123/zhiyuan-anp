package qa

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/auth"
)

// newVerdictRouter 搭真 store+service+handler，注入测试 CtxUserDBID。
func newVerdictRouter(t *testing.T) (*Store, *gin.Engine) {
	t.Helper()
	svc := newSvcWithStore(t)
	h := NewHandler(svc, nil, nil) // ManualVerdict 不用 reqRepo/apps
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(auth.CtxUserDBID, "usr_test"); c.Next() })
	r.POST("/project-spaces/:id/test-cases/:tcid/manual-verdict", h.ManualVerdict)
	return svc.store, r
}

func doVerdict(t *testing.T, r *gin.Engine, tcid, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/project-spaces/ps_1/test-cases/"+tcid+"/manual-verdict", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestHandler_ManualVerdict_OK manual 用例录通过 → 200，响应 status=passed/verifier_id=usr_test。
func TestHandler_ManualVerdict_OK(t *testing.T) {
	st, r := newVerdictRouter(t)
	tc := mkTC("ps_1", "req_1", "ok")
	tc.ID = "tc_ok"
	tc.Status = "manual"
	_ = st.Create(context.Background(), tc)

	w := doVerdict(t, r, "tc_ok", `{"verdict":"passed","note":"手验通过"}`)
	if w.Code != 200 {
		t.Fatalf("应 200，得到 %d body=%s", w.Code, w.Body.String())
	}
	var env struct {
		Code int `json:"code"`
		Data struct {
			Status     string `json:"status"`
			VerifierID string `json:"verifier_id"`
			ManualNote string `json:"manual_note"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if env.Code != 0 || env.Data.Status != "passed" || env.Data.VerifierID != "usr_test" || env.Data.ManualNote != "手验通过" {
		t.Fatalf("响应不符：code=%d status=%s verifier=%q note=%q", env.Code, env.Data.Status, env.Data.VerifierID, env.Data.ManualNote)
	}
}

// TestHandler_ManualVerdict_NotFound 用例不存在 → 404。
func TestHandler_ManualVerdict_NotFound(t *testing.T) {
	_, r := newVerdictRouter(t)
	w := doVerdict(t, r, "nope", `{"verdict":"passed"}`)
	if w.Code != 404 {
		t.Fatalf("应 404，得到 %d", w.Code)
	}
}

// TestHandler_ManualVerdict_BadVerdict verdict 非法 → 400。
func TestHandler_ManualVerdict_BadVerdict(t *testing.T) {
	st, r := newVerdictRouter(t)
	tc := mkTC("ps_1", "req_1", "bad")
	tc.ID = "tc_bad"
	tc.Status = "manual"
	_ = st.Create(context.Background(), tc)
	w := doVerdict(t, r, "tc_bad", `{"verdict":"bogus"}`)
	if w.Code != 400 {
		t.Fatalf("应 400，得到 %d", w.Code)
	}
}

// TestHandler_ManualVerdict_AutoOnlyRejected auto-only → 409。
func TestHandler_ManualVerdict_AutoOnlyRejected(t *testing.T) {
	st, r := newVerdictRouter(t)
	tc := mkTC("ps_1", "req_1", "auto")
	tc.ID = "tc_auto_h"
	tc.Status = "passed" // 自动验过，无 verifier_id
	_ = st.Create(context.Background(), tc)
	w := doVerdict(t, r, "tc_auto_h", `{"verdict":"failed"}`)
	if w.Code != 409 {
		t.Fatalf("应 409，得到 %d", w.Code)
	}
}
