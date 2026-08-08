package dev

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/auth"
	"zhiyuan-anp/platform/backend/internal/codetask"
	"zhiyuan-anp/platform/backend/internal/httpx"
	"zhiyuan-anp/platform/backend/internal/testutil"
)

// fakeGrant 测试用 computeGrantChecker：按 ret 返回，并捕获传入的 userID/modelID
// 以断言 Handler 确实调了校验且传对了参数。
type fakeGrant struct {
	called    bool
	gotUserID string
	gotModel  string
	ret       bool
	// ResolveOpencodeModelID 捕获 + 返回（cmd_xxx → "provider/name" 解析）。
	resolveCalled bool
	gotResolveID  string
	resolveRet    string
}

func (f *fakeGrant) IsGranted(_ context.Context, userID, modelID string) (bool, error) {
	f.called = true
	f.gotUserID = userID
	f.gotModel = modelID
	return f.ret, nil
}

func (f *fakeGrant) ResolveOpencodeModelID(_ context.Context, modelID string) (string, error) {
	f.resolveCalled = true
	f.gotResolveID = modelID
	return f.resolveRet, nil
}

// newCodeRouter 装配仅 /code 的 gin 引擎 + 模拟登录中间件（CtxUserDBID=u_test），
// 跳过真实 AuthUser/AutoRequire。
func newCodeRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(auth.CtxUserDBID, "u_test")
		c.Next()
	})
	r.POST("/code", h.Code)
	return r
}

// postCode 发 POST /code，返回 http 状态码 + biz code（统一响应体）。
func postCode(t *testing.T, r *gin.Engine, body string) (int, int) {
	t.Helper()
	req := httptest.NewRequest("POST", "/code", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var resp httpx.Response
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, resp.Code
}

// newDBBackedAgent 构造一个最小 CodingAgent（仅 tasks 落地用真实 DB），
// 让 /code 放行后 Submit 能真建任务、返 200 而非 panic。
// store/engine/standards=nil：goroutine 内 opencodeRun 会快速 panic→recover→MarkFailed，
// 不阻塞、不 crash 测试（notif.Emit 全局 store 为 nil 时静默跳过）。
func newDBBackedAgent(t *testing.T) *CodingAgent {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "code_task")
	return NewCodingAgent(nil, nil, codetask.NewStore(db), nil, nil)
}

// TestCode_DeniedByGrant 安全路径核心：/code 派发指定 Model 且用户未授权 → 403，
// 且 Submit 不应被触达（nil agent 安全：拒绝即 return，不解引用 agent）。
func TestCode_DeniedByGrant(t *testing.T) {
	grant := &fakeGrant{ret: false}
	h := NewHandler(nil, grant) // agent=nil：授权拒绝时 Submit 不会被触达
	r := newCodeRouter(h)
	body := `{"repo_dir":"/tmp/x","prompt":"hi","model":"m_denied"}`

	httpCode, bizCode := postCode(t, r, body)
	if httpCode != 403 {
		t.Fatalf("/code 越权期望 http=403，got %d", httpCode)
	}
	if bizCode != 40302 {
		t.Fatalf("期望 biz code=40302（无权使用该模型），got %d", bizCode)
	}
	if !grant.called {
		t.Fatal("grant.IsGranted 未被调用，授权校验逻辑缺失")
	}
	if grant.gotUserID != "u_test" || grant.gotModel != "m_denied" {
		t.Fatalf("IsGranted 入参期望 (u_test, m_denied)，got (%q, %q)", grant.gotUserID, grant.gotModel)
	}
}

// TestCode_GrantedPassesCheck /code 派发指定 Model 且用户已授权 → 校验放行，
// 不返 403（进入 Submit，真建任务返 200）。
func TestCode_GrantedPassesCheck(t *testing.T) {
	grant := &fakeGrant{ret: true}
	h := NewHandler(newDBBackedAgent(t), grant)
	r := newCodeRouter(h)
	body := `{"repo_dir":"/tmp/x","prompt":"hi","model":"m_ok"}`

	httpCode, _ := postCode(t, r, body)
	if httpCode == 403 {
		t.Fatalf("/code 已授权不应返 403，got %d（grant.called=%v）", httpCode, grant.called)
	}
	if !grant.called {
		t.Fatal("已授权路径应先调 IsGranted（返 true 放行）")
	}
	if httpCode != 200 {
		t.Fatalf("/code 已授权期望 http=200（Submit 成功返 task_id），got %d", httpCode)
	}
}

// TestCode_EmptyModelSkipsGrant /code 不指定 Model → 跳过授权校验（兼容旧调用）。
// 即便 grant 返 false 也不拦截：Model 空时根本不进入 IsGranted 分支。
func TestCode_EmptyModelSkipsGrant(t *testing.T) {
	grant := &fakeGrant{ret: false}
	h := NewHandler(newDBBackedAgent(t), grant)
	r := newCodeRouter(h)
	body := `{"repo_dir":"/tmp/x","prompt":"hi"}` // 无 model 字段

	httpCode, _ := postCode(t, r, body)
	if httpCode == 403 {
		t.Fatalf("/code 空 Model 应跳过授权校验，不应返 403，got %d", httpCode)
	}
	if grant.called {
		t.Fatal("空 Model 不应触发 IsGranted（应短路跳过）")
	}
	if httpCode != 200 {
		t.Fatalf("/code 空 Model 期望 http=200（Submit 成功），got %d", httpCode)
	}
}

// TestCode_NilGrantSkipsCheck /code 指定 Model 但 grant 未注入（nil）→ 跳过校验。
// 兼容 computeStore 缺失的部署场景（不阻断主路径）。
func TestCode_NilGrantSkipsCheck(t *testing.T) {
	h := NewHandler(newDBBackedAgent(t), nil) // grant=nil
	r := newCodeRouter(h)
	body := `{"repo_dir":"/tmp/x","prompt":"hi","model":"m_x"}`

	httpCode, _ := postCode(t, r, body)
	if httpCode == 403 {
		t.Fatalf("/code grant=nil 应跳过授权校验（兼容），不应返 403，got %d", httpCode)
	}
}

// TestCode_ResolvesModelBeforeSubmit /code 已授权 + model=cmd_xxx → Submit 收到解析后的
// "provider/name"（而非原始 cmd_xxx）。断言：落库的 code_task.Model == 解析后的 provider/name，
// 且 ResolveOpencodeModelID 被调用并收到原始 cmd_xxx。
// 证明 handler 在 IsGranted 通过后、Submit 前完成模型 id 解析。
func TestCode_ResolvesModelBeforeSubmit(t *testing.T) {
	agent := newDBBackedAgent(t)
	grant := &fakeGrant{ret: true, resolveRet: "zai-coding/glm-5.1"}
	h := NewHandler(agent, grant)
	r := newCodeRouter(h)
	body := `{"repo_dir":"/tmp/no-such-repo","prompt":"hi","model":"cmd_abc"}`

	req := httptest.NewRequest("POST", "/code", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("/code 期望 http=200（已授权+解析后 Submit 成功），got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			TaskID string `json:"task_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v body=%s", err, w.Body.String())
	}
	if resp.Data.TaskID == "" {
		t.Fatalf("未返回 task_id: %s", w.Body.String())
	}
	// 落库任务的 Model 应为解析后的 provider/name（证明 Submit 收到解析值，非原始 cmd_abc）。
	// goroutine run 异步执行（panic-safe recover），但不修改 Model 字段，读取安全。
	got, err := agent.tasks.Get(context.Background(), resp.Data.TaskID)
	if err != nil {
		t.Fatalf("读回任务 %s: %v", resp.Data.TaskID, err)
	}
	if got.Model != "zai-coding/glm-5.1" {
		t.Fatalf("任务 Model 期望解析后的 zai-coding/glm-5.1，got %q（cmd_abc 未被解析）", got.Model)
	}
	if !grant.resolveCalled || grant.gotResolveID != "cmd_abc" {
		t.Fatalf("ResolveOpencodeModelID 未被正确调用: called=%v gotID=%q",
			grant.resolveCalled, grant.gotResolveID)
	}
}
