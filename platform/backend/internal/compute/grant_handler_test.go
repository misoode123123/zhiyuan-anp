package compute_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/auth"
	"zhiyuan-anp/platform/backend/internal/compute"
	"zhiyuan-anp/platform/backend/internal/httpx"
	"zhiyuan-anp/platform/backend/internal/testutil"
)

// newGrantRouter 装配仅 GrantHandler 的 gin 引擎 + 模拟登录中间件
// （跳过真实 AuthUser/AutoRequire，用 CtxUserDBID=u_test 模拟当前管理员）。
func newGrantRouter(s *compute.Store) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(auth.CtxUserDBID, "u_test")
		c.Next()
	})
	compute.NewGrantHandler(s).Register(r)
	return r
}

// doJSON 发请求并解包统一响应体到 out（data 字段）。
func doJSON(t *testing.T, r *gin.Engine, method, path string, body interface{}) (int, httpx.Response) {
	t.Helper()
	var reqBody *bytes.Buffer
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reqBody = bytes.NewBuffer(b)
	} else {
		reqBody = &bytes.Buffer{}
	}
	req := httptest.NewRequest(method, path, reqBody)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var resp httpx.Response
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, resp
}

// TestGrantHandler_LifecycleAndMineModels 覆盖 4 端点往返：
// POST 授权 → GET list 命中 → DELETE 收回 → GET list 为空；
// GET me/models 返回 CtxUserDBID 对应用户的授权（授权给 u_test 后命中）。
func TestGrantHandler_LifecycleAndMineModels(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "user_model_grant", "compute_model", "compute_provider")
	s := compute.NewStore(db)
	ctx := context.Background()

	// 前置：一个 provider + 一个 model
	prov := &compute.Provider{
		Name: "t-prov-h", Type: "api", BaseURL: "http://x",
		APIKey: "k", Enabled: true,
	}
	if err := s.CreateProvider(ctx, prov); err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	mdl := &compute.Model{
		ProviderID: prov.ID, Name: "t-h", Modality: "text",
		Enabled: true,
	}
	if err := s.CreateModel(ctx, mdl); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	targetUser := "u_grantee"

	r := newGrantRouter(s)

	// 1) 授权前 list 为空
	if code, resp := doJSON(t, r, "GET", "/users/"+targetUser+"/models", nil); code != 200 || resp.Code != 0 {
		t.Fatalf("授权前 list 期望 code=0，got http=%d resp=%+v", code, resp)
	} else if resp.Data != nil {
		t.Fatalf("授权前 list 期望空，got data=%v", resp.Data)
	}

	// 2) POST 授权
	code, resp := doJSON(t, r, "POST", "/users/"+targetUser+"/models",
		map[string]interface{}{"model_ids": []string{mdl.ID}})
	if code != 200 || resp.Code != 0 {
		t.Fatalf("POST 授权失败: http=%d resp=%+v", code, resp)
	}
	m := resp.Data.(map[string]interface{})
	if int(m["granted"].(float64)) != 1 {
		t.Fatalf("granted 期望 1，got %v", m["granted"])
	}

	// 3) GET list 命中该模型
	_, resp = doJSON(t, r, "GET", "/users/"+targetUser+"/models", nil)
	arr, ok := resp.Data.([]interface{})
	if !ok || len(arr) != 1 {
		t.Fatalf("list 期望 1 个模型，got %+v", resp.Data)
	}
	first := arr[0].(map[string]interface{})
	if first["id"] != mdl.ID {
		t.Fatalf("list[0].id 期望 %s，got %v", mdl.ID, first["id"])
	}

	// 4) DELETE 收回
	code, resp = doJSON(t, r, "DELETE", "/users/"+targetUser+"/models/"+mdl.ID, nil)
	if code != 200 || resp.Code != 0 {
		t.Fatalf("DELETE 收回失败: http=%d resp=%+v", code, resp)
	}

	// 5) GET list 再次为空
	_, resp = doJSON(t, r, "GET", "/users/"+targetUser+"/models", nil)
	if resp.Data != nil {
		t.Fatalf("收回后 list 期望空，got %+v", resp.Data)
	}

	// 6) me/models：当前用户=u_test（中间件注入），授权给 u_test 后应命中
	if err := s.GrantModels(ctx, "u_test", []string{mdl.ID}, "admin"); err != nil {
		t.Fatalf("GrantModels(u_test): %v", err)
	}
	_, resp = doJSON(t, r, "GET", "/users/me/models", nil)
	arr, ok = resp.Data.([]interface{})
	if !ok || len(arr) != 1 {
		t.Fatalf("me/models 期望 1 个（u_test），got %+v", resp.Data)
	}
	if arr[0].(map[string]interface{})["id"] != mdl.ID {
		t.Fatalf("me/models[0].id 期望 %s", mdl.ID)
	}
}

// TestGrantHandler_InvalidBody 验证 bind 失败 → 400。
func TestGrantHandler_InvalidBody(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "user_model_grant", "compute_model", "compute_provider")
	s := compute.NewStore(db)
	r := newGrantRouter(s)

	req := httptest.NewRequest("POST", "/users/u1/models", strings.NewReader("{not-json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("无效 body 期望 400，got %d body=%s", w.Code, w.Body.String())
	}
	var resp httpx.Response
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != 40001 {
		t.Fatalf("期望 biz code=40001，got %d", resp.Code)
	}
}

// TestGrantHandler_GuardRouteOps 断言 guard.routeOps 已登记 3 个管理员授权端点
// → config.manage，且 me/models 未登记（任意登录用户放行）。防止 routeOps 误删/误登记。
func TestGrantHandler_GuardRouteOps(t *testing.T) {
	ops := auth.RegisteredOps()
	must := map[string]string{
		"GET /api/v1/users/:id/models":              "config.manage",
		"POST /api/v1/users/:id/models":             "config.manage",
		"DELETE /api/v1/users/:id/models/:model_id": "config.manage",
	}
	for k, want := range must {
		got, ok := ops[k]
		if !ok {
			t.Errorf("routeOps 缺少登记: %q", k)
		} else if got != want {
			t.Errorf("routeOps[%q]=%q，期望 %q", k, got, want)
		}
	}
	if _, ok := ops["GET /api/v1/users/me/models"]; ok {
		t.Error("routeOps 不应登记 GET /users/me/models（任意登录用户放行）")
	}
}
