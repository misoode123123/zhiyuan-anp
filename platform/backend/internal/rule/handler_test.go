package rule

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// newTestHandler 建内存 store + gin 测试引擎并注册路由，返回 (engine, store)。
// h.v 字段虽被 NewHandler 注入，但 handler.go 全程未使用（dead code），传 nil 即可。
func newTestHandler(t *testing.T) (*gin.Engine, *Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s := newTestStore(t)
	h := NewHandler(s, nil)
	r := gin.New()
	h.Register(r)
	return r, s
}

// doJSON 发起 JSON 请求并返回 recorder。
func doJSON(t *testing.T, r http.Handler, method, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// decodeResp 解包统一响应体。
func decodeResp(t *testing.T, w *httptest.ResponseRecorder) (int, map[string]interface{}) {
	t.Helper()
	var resp struct {
		Code    int                    `json:"code"`
		Message string                 `json:"message"`
		Data    map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body %q: %v", w.Body.String(), err)
	}
	return w.Code, resp.Data
}

// TestDefaults_FillsAllDefaults 入参所有可默认字段为空时，defaults 全量填充。
func TestDefaults_FillsAllDefaults(t *testing.T) {
	in := &createRequest{Name: "N", Condition: "C"}
	defaults(in)
	if in.Category != "general" {
		t.Errorf("Category 默认应为 general，得到 %s", in.Category)
	}
	if in.Type != "mandatory" {
		t.Errorf("Type 默认应为 mandatory，得到 %s", in.Type)
	}
	if in.ConditionField != "prompt" {
		t.Errorf("ConditionField 默认应为 prompt，得到 %s", in.ConditionField)
	}
	if in.Action != "block" {
		t.Errorf("Action 默认应为 block，得到 %s", in.Action)
	}
	if in.Scope != "all" {
		t.Errorf("Scope 默认应为 all，得到 %s", in.Scope)
	}
}

// TestDefaults_PreservesExisting 已提供的字段不被覆盖。
func TestDefaults_PreservesExisting(t *testing.T) {
	in := &createRequest{
		Name: "N", Condition: "C",
		Category: "security", Type: "should",
		ConditionField: "output", Action: "warn", Scope: "dev",
	}
	defaults(in)
	if in.Category != "security" || in.Type != "should" ||
		in.ConditionField != "output" || in.Action != "warn" || in.Scope != "dev" {
		t.Fatalf("已设字段被覆盖: %+v", in)
	}
}

// TestHandler_Create_WithDefaults 缺省字段走默认值；新建默认 Enabled=true；返回 201。
func TestHandler_Create_WithDefaults(t *testing.T) {
	r, _ := newTestHandler(t)
	w := doJSON(t, r, http.MethodPost, "/rules", gin.H{
		"name": "R1", "condition": "foo",
	})
	if w.Code != 201 {
		t.Fatalf("Create 应返回 201，得到 %d body=%s", w.Code, w.Body.String())
	}
	_, data := decodeResp(t, w)
	// 返回 data 是创建后的 Rule（序列化为 map）
	if data["name"] != "R1" {
		t.Errorf("name 不匹配：%v", data["name"])
	}
	if data["action"] != "block" { // 默认值
		t.Errorf("action 默认应为 block，得到 %v", data["action"])
	}
	if data["scope"] != "all" {
		t.Errorf("scope 默认应为 all，得到 %v", data["scope"])
	}
	if data["enabled"] != true { // 新建默认启用
		t.Errorf("enabled 应为 true，得到 %v", data["enabled"])
	}
	if id, ok := data["id"].(string); !ok || len(id) < 5 || id[:5] != "rule_" {
		t.Errorf("id 应以 rule_ 开头，得到 %v", data["id"])
	}
}

// TestHandler_Create_400OnMissingRequired 缺 name 或 condition 时返回 400。
func TestHandler_Create_400OnMissingRequired(t *testing.T) {
	r, _ := newTestHandler(t)

	// 缺 name
	w := doJSON(t, r, http.MethodPost, "/rules", gin.H{"condition": "foo"})
	if w.Code != 400 {
		t.Fatalf("缺 name 应返回 400，得到 %d", w.Code)
	}

	// 缺 condition
	w = doJSON(t, r, http.MethodPost, "/rules", gin.H{"name": "N"})
	if w.Code != 400 {
		t.Fatalf("缺 condition 应返回 400，得到 %d", w.Code)
	}

	// 空 body
	w = doJSON(t, r, http.MethodPost, "/rules", nil)
	if w.Code != 400 {
		t.Fatalf("空 body 应返回 400，得到 %d", w.Code)
	}
}

// TestHandler_List_EmptyAndAfterCreate 空时返回空切片；Create 后能 List 看到。
func TestHandler_List_EmptyAndAfterCreate(t *testing.T) {
	r, _ := newTestHandler(t)

	// 空表 List
	w := doJSON(t, r, http.MethodGet, "/rules", nil)
	if w.Code != 200 {
		t.Fatalf("List 应返回 200，得到 %d", w.Code)
	}
	// data 字段是数组（可能为 null 或 []），通用解码
	var generic map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &generic); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if arr, ok := generic["data"].([]interface{}); ok && len(arr) != 0 {
		t.Fatalf("空表 List data 应为空数组，得到 %v", arr)
	}

	// 创建一条
	_ = doJSON(t, r, http.MethodPost, "/rules", gin.H{"name": "R1", "condition": "foo"})

	// 再次 List —— 应有 1 条
	w = doJSON(t, r, http.MethodGet, "/rules", nil)
	if w.Code != 200 {
		t.Fatalf("List 应返回 200，得到 %d", w.Code)
	}
	generic = map[string]interface{}{}
	if err := json.Unmarshal(w.Body.Bytes(), &generic); err != nil {
		t.Fatalf("decode: %v", err)
	}
	list, ok := generic["data"].([]interface{})
	if !ok || len(list) != 1 {
		t.Fatalf("List 应返回 1 条，得到 %v", generic["data"])
	}
}

// TestHandler_Update_SuccessAndUpdate_UpdateNotFound 更新存在/不存在的规则。
func TestHandler_Update_SuccessAndUpdate_NotFound(t *testing.T) {
	r, s := newTestHandler(t)
	// 先经 store 直接造一条
	created := mustCreate(t, s, newRule("Orig", "foo", "prompt", "block", "all", true))

	// 更新存在
	w := doJSON(t, r, http.MethodPut, "/rules/"+created.ID, gin.H{
		"name": "Updated", "condition": "bar", "action": "warn",
	})
	if w.Code != 200 {
		t.Fatalf("Update 应返回 200，得到 %d body=%s", w.Code, w.Body.String())
	}
	got, _ := s.List(context.Background())
	if len(got) != 1 || got[0].Name != "Updated" || got[0].Action != "warn" {
		t.Fatalf("Update 未落库: %+v", got)
	}

	// 更新不存在 → 500（store 报错 "规则 X 不存在"）
	w = doJSON(t, r, http.MethodPut, "/rules/rule_nope", gin.H{"name": "x", "condition": "y"})
	if w.Code != 500 {
		t.Fatalf("Update 不存在应返回 500，得到 %d", w.Code)
	}

	// 缺 name → 400
	w = doJSON(t, r, http.MethodPut, "/rules/"+created.ID, gin.H{"condition": "y"})
	if w.Code != 400 {
		t.Fatalf("缺 name 应返回 400，得到 %d", w.Code)
	}
}

// TestHandler_SetEnabled 启停路由：返回 id+enabled，且落库可见。
func TestHandler_SetEnabled(t *testing.T) {
	r, s := newTestHandler(t)
	created := mustCreate(t, s, newRule("R", "foo", "prompt", "block", "all", true))

	w := doJSON(t, r, http.MethodPatch, "/rules/"+created.ID+"/enabled", gin.H{"enabled": false})
	if w.Code != 200 {
		t.Fatalf("SetEnabled 应返回 200，得到 %d body=%s", w.Code, w.Body.String())
	}
	_, data := decodeResp(t, w)
	if data["enabled"] != false || data["id"] != created.ID {
		t.Fatalf("响应 id/enabled 不匹配: %+v", data)
	}
	got, _ := s.List(context.Background())
	if got[0].Enabled {
		t.Fatal("SetEnabled(false) 应落库")
	}

	// 非法 body（缺 enabled 字段）—— 注意 setEnabledRequest 的字段非 required，缺失会被解析为零值 false
	// 故此处期望 200（gin 不报 binding 错），这是当前实现的容错行为。
	w = doJSON(t, r, http.MethodPatch, "/rules/"+created.ID+"/enabled", gin.H{})
	if w.Code != 200 {
		t.Fatalf("缺 enabled 字段当前实现容错为 false 并返回 200，得到 %d", w.Code)
	}
}

// TestHandler_Delete 删除后 List 看不到。
func TestHandler_Delete(t *testing.T) {
	r, s := newTestHandler(t)
	created := mustCreate(t, s, newRule("R", "foo", "prompt", "block", "all", true))

	w := doJSON(t, r, http.MethodDelete, "/rules/"+created.ID, nil)
	if w.Code != 200 {
		t.Fatalf("Delete 应返回 200，得到 %d", w.Code)
	}
	_, data := decodeResp(t, w)
	if data["deleted"] != true || data["id"] != created.ID {
		t.Fatalf("响应 id/deleted 不匹配: %+v", data)
	}
	got, _ := s.List(context.Background())
	if len(got) != 0 {
		t.Fatalf("删除后应空，得到 %d 条", len(got))
	}
}

// TestHandler_Check_BlockAndWarn HTTP 层端到端：命中 block 时 blocked=true，仅 warn 时 blocked=false。
func TestHandler_Check_BlockAndWarn(t *testing.T) {
	r, _ := newTestHandler(t)
	// 经 HTTP Create 造规则（顺便覆盖 Create 路径）
	_ = doJSON(t, r, http.MethodPost, "/rules", gin.H{
		"name": "block-r", "condition": "delete.*migrate",
		"condition_field": "prompt", "action": "block", "scope": "all",
	})
	_ = doJSON(t, r, http.MethodPost, "/rules", gin.H{
		"name": "warn-r", "condition": "api[_-]?key",
		"condition_field": "output", "action": "warn", "scope": "all",
	})

	// 命中 block
	w := doJSON(t, r, http.MethodPost, "/rules/check", gin.H{
		"scope": "dev", "field": "prompt", "content": "please delete migrate file",
	})
	if w.Code != 200 {
		t.Fatalf("Check 应返回 200，得到 %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Violations []map[string]interface{} `json:"violations"`
			Blocked    bool                     `json:"blocked"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data.Violations) != 1 || !resp.Data.Blocked {
		t.Fatalf("应命中 1 条 block 并 blocked=true，得到 %+v", resp.Data)
	}

	// 仅 warn
	w = doJSON(t, r, http.MethodPost, "/rules/check", gin.H{
		"scope": "dev", "field": "output", "content": "API_KEY=sk-xxx",
	})
	if w.Code != 200 {
		t.Fatalf("Check 应返回 200，得到 %d", w.Code)
	}
	resp = struct {
		Code int `json:"code"`
		Data struct {
			Violations []map[string]interface{} `json:"violations"`
			Blocked    bool                     `json:"blocked"`
		} `json:"data"`
	}{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data.Violations) != 1 || resp.Data.Blocked {
		t.Fatalf("应命中 1 条 warn 且 blocked=false，得到 %+v", resp.Data)
	}

	// 不命中
	w = doJSON(t, r, http.MethodPost, "/rules/check", gin.H{
		"scope": "dev", "field": "prompt", "content": "hello world",
	})
	resp = struct {
		Code int `json:"code"`
		Data struct {
			Violations []map[string]interface{} `json:"violations"`
			Blocked    bool                     `json:"blocked"`
		} `json:"data"`
	}{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Data.Violations) != 0 || resp.Data.Blocked {
		t.Fatalf("无命中应 0 violation 且 blocked=false，得到 %+v", resp.Data)
	}
}

// TestHandler_Register_Routes 注册的 6 条路由都存在（避免遗漏路由注册）。
// 这里仅校验路由表，不发请求。
func TestHandler_Register_Routes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestStore(t)
	h := NewHandler(s, nil)
	r := gin.New()
	h.Register(r)

	want := map[string]bool{
		"GET /rules":               true,
		"POST /rules":              true,
		"PUT /rules/:id":           true,
		"PATCH /rules/:id/enabled": true,
		"DELETE /rules/:id":        true,
		"POST /rules/check":        true,
	}
	got := map[string]bool{}
	for _, rs := range r.Routes() {
		got[rs.Method+" "+rs.Path] = true
	}
	for k := range want {
		if !got[k] {
			t.Errorf("缺少路由 %s", k)
		}
	}
}
