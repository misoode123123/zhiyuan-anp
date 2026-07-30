package appdeploy

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

// fakeAppQuotaChecker 可控的 quota checker（CheckApps 返回预设 err）。
type fakeAppQuotaChecker struct {
	err error
}

func (f fakeAppQuotaChecker) CheckApps(ctx context.Context, psID string) error {
	return f.err
}

// newHTTPHandlerWithQuota 像 newHTTPHandler，但注入 fake quota checker。
func newHTTPHandlerWithQuota(t *testing.T, quota fakeAppQuotaChecker) (*Handler, *sqlx.DB) {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db,
		"release_record", "change_request", "requirement",
		"appdeploy_env", "appdeploy_instance", "appdeploy_application",
	)
	store := NewStore(db)
	h := NewHandler(store, NewDeployer("test"), nil, nil, nil, nil, nil, nil, nil, quota, nil, nil, nil, "", nil, nil)
	return h, db
}

// TestHandler_Create_QuotaExceeded 配额超限时 Create 返回 409（不触发 EnsureRepo）。
func TestHandler_Create_QuotaExceeded(t *testing.T) {
	psID := "ps_" + uuid.NewString()[:20]
	// 建项目空间（FK 约束）
	db := testutil.TestDB(t)
	if _, err := db.Exec(
		`INSERT INTO project_space (id, name, slug, status) VALUES ($1, $2, $3, 'active')`,
		psID, "qt-"+psID, "s-"+psID); err != nil {
		t.Fatalf("建 project_space: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM project_space WHERE id=$1`, psID) })

	// 注入恒报错的 quota checker
	h, _ := newHTTPHandlerWithQuota(t, fakeAppQuotaChecker{err: errFakeQuota})
	r := newRouterWith(h)

	code, resp := doReq(t, r, http.MethodPost,
		"/api/v1/project-spaces/"+psID+"/apps",
		map[string]interface{}{"name": "quota-app"},
	)
	if code != 409 {
		t.Fatalf("状态码 = %d, want 409; resp=%v", code, resp)
	}
	if resp["message"] == "" {
		t.Error("超限消息为空")
	}
	// 错误码 40950（配额超限-应用数）
	if c, ok := resp["code"].(float64); !ok || int(c) != 40950 {
		t.Errorf("业务码 = %v, want 40950", resp["code"])
	}
	// 确保没建应用
	count := 0
	_ = db.Get(&count, `SELECT COUNT(*) FROM appdeploy_application WHERE project_space_id=$1`, psID)
	if count != 0 {
		t.Errorf("超限不应建应用，DB 有 %d 条", count)
	}
}

// errFakeQuota 模拟 quota.Service 的配额超限错误（任意非空 error 即可）。
var errFakeQuota = errString("应用数已达上限：0 / 0")

// TestHandler_Create_NoQuotaChecker quota=nil 时 Create 走原流程（配额未启用）。
// 不实际调 Create（EnsureRepo 依赖 git），仅验证 quota=nil 不 panic。
func TestHandler_Create_NoQuotaChecker(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "appdeploy_application")
	store := NewStore(db)
	h := NewHandler(store, NewDeployer("test"), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "", nil, nil)
	if h.quota != nil {
		t.Errorf("quota 应为 nil")
	}
}
