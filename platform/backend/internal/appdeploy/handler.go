package appdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/auth"
	"zhiyuan-anp/platform/backend/internal/change"
	"zhiyuan-anp/platform/backend/internal/codews"
	"zhiyuan-anp/platform/backend/internal/config"
	"zhiyuan-anp/platform/backend/internal/httpx"
	"zhiyuan-anp/platform/backend/internal/requirement"
)

// Handler 应用部署 HTTP 接口。
type Handler struct {
	store    *Store
	deployer *Deployer
	codeWS   *codews.Manager         // 交互编码工作台（opencode serve）；nil=未启用
	changes  *change.Store           // 变更闸门（期2）；nil=未启用
	cfg      *config.Store           // 系统配置(取 zhipuai_api_key 做 AI 总结)；nil=不总结
	reqRepo  *requirement.Repository // 需求-代码核对门禁:读 requirement 的验收标准
	checkFn  checkFunc               // 可 mock 的核对函数(默认 checkRequirement);测试可注入
}

// checkFunc 需求-代码核对的函数签名(便于测试 mock)。
// passed=false&err=nil → 核对未通过(409); err!=nil → AI 失败(503); passed=true → 通过。
type checkFunc func(ctx context.Context, apiKey, code, title, criteria string) (passed bool, err error, details string)

// NewHandler 构造。codeWS/changes/cfg/reqRepo 可为 nil（不启用对应能力）。
func NewHandler(store *Store, deployer *Deployer, codeWS *codews.Manager, changes *change.Store, cfg *config.Store, reqRepo *requirement.Repository) *Handler {
	h := &Handler{store: store, deployer: deployer, codeWS: codeWS, changes: changes, cfg: cfg, reqRepo: reqRepo}
	h.checkFn = checkRequirement // 默认真 AI 核对
	return h
}

// Register 模块级装配：内部 new Deployer/codews.Manager + NewHandler + Register。
// 返回 *Handler 供 release 模块（发布后自动部署）复用。
func Register(r gin.IRouter, store *Store, appDeployHost string, changeStore *change.Store, configStore *config.Store, reqRepo *requirement.Repository) *Handler {
	h := NewHandler(store, NewDeployer(appDeployHost), codews.NewManager(appDeployHost), changeStore, configStore, reqRepo)
	h.Register(r)
	return h
}

// Register 注册路由。
func (h *Handler) Register(r gin.IRouter) {
	r.GET("/project-spaces/:id/apps", h.List)
	r.POST("/project-spaces/:id/apps", h.Create)
	r.GET("/project-spaces/:id/apps/:aid/detail", h.Detail)
	r.POST("/project-spaces/:id/apps/:aid/deploy", h.Deploy)   // 部署到 test（默认）或指定 env
	r.POST("/project-spaces/:id/apps/:aid/promote", h.Promote) // 上线 = 部署到 prod
	r.POST("/project-spaces/:id/apps/:aid/deploy-commit", h.DeployCommit)
	r.POST("/project-spaces/:id/apps/:aid/stop", h.Stop)
	r.POST("/project-spaces/:id/apps/:aid/start", h.Start)
	r.DELETE("/project-spaces/:id/apps/:aid", h.Delete)
	r.POST("/project-spaces/:id/apps/:aid/workspace", h.Workspace)                  // 启动交互编码工作台
	r.POST("/project-spaces/:id/apps/:aid/register-change", h.RegisterChange)       // 登记交互编码变更为待审批（期2 闸门）
	r.POST("/project-spaces/:id/apps/:aid/inject-requirement", h.InjectRequirement) // 把需求注入 opencode 会话(交互式编码)
	r.POST("/project-spaces/:id/apps/:aid/submit", h.Submit)                        // 提交核对门禁(AI 核对代码 vs 需求,不匹配拦)
	r.POST("/project-spaces/:id/apps/:aid/merge", h.Merge)                          // 合并 dev-<user> 到 main(上线前)
	r.GET("/project-spaces/:id/apps/:aid/env", h.ListEnv)                           // 应用运行时环境变量
	r.POST("/project-spaces/:id/apps/:aid/env", h.UpsertEnv)
	r.DELETE("/project-spaces/:id/apps/:aid/env/:key", h.DeleteEnv)
	r.GET("/project-spaces/:id/apps/:aid/stats", h.Stats) // 资源占用 + 健康探测
	r.GET("/project-spaces/:id/apps/:aid/logs", h.Logs)
	r.GET("/project-spaces/:id/apps/:aid/repo-docs", h.RepoDocs) // 应用 repo 文档(README/.md)
	r.GET("/project-spaces/:id/apps/:aid/repo-file", h.RepoFile) // 读 repo 文件内容
}

// List 应用列表，附带各环境实例（前端展示 test/prod URL）。
//
// @Summary      应用列表
// @Tags         appdeploy
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Success      200  {object}  map[string]interface{}  "应用列表(含各环境实例)"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps [get]
func (h *Handler) List(c *gin.Context) {
	list, err := h.store.List(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	for i := range list {
		list[i].Instances, _ = h.store.ListInstancesByApp(c.Request.Context(), list[i].ID)
	}
	httpx.OK(c, list)
}

// Detail 应用详情：应用本体 + 归属需求/变更/发布 + 仓库版本 + 各环境实例。
//
// @Summary      应用详情
// @Tags         appdeploy
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Param        aid  path  string  true  "应用ID"
// @Success      200  {object}  map[string]interface{}  "应用详情"
// @Failure      404  {object}  map[string]interface{}  "应用不存在"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/detail [get]
func (h *Handler) Detail(c *gin.Context) {
	d, err := h.store.Detail(c.Request.Context(), c.Param("id"), c.Param("aid"))
	if err != nil || d == nil {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	httpx.OK(c, d)
}

// Workspace 启动/复用应用的 opencode 交互编码工作台，返回 opencode 官方 web UI 的访问 URL。
// 不造轮子：直接集成 opencode serve 自带的 web 界面，开发者用它原生体验编码。
//
// @Summary      启动交互编码工作台
// @Tags         appdeploy
// @Accept       json
// @Produce      json
// @Param        id     path    string  true   "项目空间ID"
// @Param        aid    path    string  true   "应用ID"
// @Param        body   body    object  false  "工作台选项{tool:opencode/claude/codex}"
// @Param        X-User header  string  false  "开发者身份"
// @Success      200    {object}  map[string]interface{}  "工作台信息(url/session_id等)"
// @Failure      404    {object}  map[string]interface{}  "应用不存在"
// @Failure      500    {object}  map[string]interface{}  "工作台未启用/启动失败"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/workspace [post]
func (h *Handler) Workspace(c *gin.Context) {
	psID, aid := c.Param("id"), c.Param("aid")
	if h.codeWS == nil {
		httpx.Err(c, 500, 50021, "交互编码工作台未启用")
		return
	}
	a, err := h.store.Get(c.Request.Context(), psID, aid)
	if err != nil || a == nil || a.ID == "" {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	var in struct {
		Tool string `json:"tool"` // opencode(默认) / claude / codex ...
	}
	_ = c.ShouldBindJSON(&in)
	user := c.GetString(auth.CtxUserID) // 开发者身份（不同开发者可各选各的工具）
	if user == "" {
		user = "anonymous"
	}
	s, err := h.codeWS.Ensure(aid, a.RepoDir, user, in.Tool)
	if err != nil {
		httpx.Err(c, 500, 50021, err.Error())
		return
	}
	httpx.OK(c, gin.H{"app_id": aid, "user": user, "tool": s.Tool, "url": s.URL, "deep_url": s.DeepURL, "port": s.Port, "session_id": s.SessionID, "note": s.Tool + " 工作台已就绪（开发者 " + user + "），浏览器打开 url 即可交互编码"})
}

// RegisterChange 把 opencode 交互编码的产出登记为待审批变更（期2 变更闸门）。
// 自动总结:拉取 opencode 会话的对话内容 + repo 最近提交日志组成变更说明,免手填。
// source_id=应用ID；审批通过后该应用方可 promote prod。
//
// @Summary      登记交互编码变更为待审批
// @Tags         appdeploy
// @Accept       json
// @Produce      json
// @Param        id     path    string  true   "项目空间ID"
// @Param        aid    path    string  true   "应用ID"
// @Param        body   body    object  false  "登记选项{note,req_id}"
// @Param        X-User header  string  false  "开发者身份"
// @Success      200    {object}  map[string]interface{}  "登记的变更"
// @Failure      404    {object}  map[string]interface{}  "应用不存在"
// @Failure      500    {object}  map[string]interface{}  "变更闸门未启用/登记失败"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/register-change [post]
func (h *Handler) RegisterChange(c *gin.Context) {
	psID, aid := c.Param("id"), c.Param("aid")
	a, err := h.store.Get(c.Request.Context(), psID, aid)
	if err != nil || a == nil || a.ID == "" {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	if h.changes == nil {
		httpx.Err(c, 500, 50021, "变更闸门未启用")
		return
	}
	var in struct {
		Note  string `json:"note"`   // 可选:开发者补充说明
		ReqID string `json:"req_id"` // 可选:关联的需求(需求驱动开发时,变更归属该需求)
	}
	_ = c.ShouldBindJSON(&in)

	// 自动获取 opencode 对话内容(免手填)
	conversation := ""
	if h.codeWS != nil {
		user := c.GetString(auth.CtxUserID)
		if user == "" {
			user = "anonymous"
		}
		if conv, err := h.codeWS.SessionMessages(aid, user); err == nil {
			conversation = conv
		}
	}

	commits, _ := Log(c.Request.Context(), a.RepoDir, 10)
	diff := Diff(c.Request.Context(), a.RepoDir, 3)
	var summary string
	if in.ReqID != "" {
		summary = "【需求】" + in.ReqID + "\n" // 关联的需求(需求驱动开发时标注)
	}
	// AI 总结:把 diff/对话总结成人话(改了什么、为什么),放最前让审批人一眼看懂
	if h.cfg != nil {
		if s := summarizeChange(c.Request.Context(), h.cfg.Get("zhipuai_api_key", ""), diff, conversation); s != "" {
			summary = "【总结】" + s + "\n\n"
		}
	}
	if in.Note != "" {
		summary += "【说明】" + in.Note + "\n"
	}
	if conversation != "" {
		summary += "【对话】\n" + truncateStr(conversation, 2000) + "\n"
	}
	if len(commits) > 0 {
		summary += "【commits】\n"
		for _, cm := range commits {
			summary += cm.SHA + " " + cm.Message + "\n"
		}
	}
	if diff != "" {
		summary += "【diff】\n" + truncateStr(diff, 3000) + "\n"
	}
	chg := &change.ChangeRequest{
		ProjectSpaceID: psID, Kind: "code", SourceID: aid, RepoDir: a.RepoDir,
		Prompt: in.Note, Output: strings.TrimSpace(summary),
	}
	if err := h.changes.Create(c.Request.Context(), chg); err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	// 把变更说明追加到 repo docs/开发日志.md(文档随代码版本管理,可追溯)
	appendFile(a.RepoDir, "docs/开发日志.md",
		"\n## 变更 "+chg.ID+" ("+time.Now().Format("2006-01-02 15:04")+")\n"+summary+"\n")
	httpx.Created(c, chg)
}

// truncateStr 截断字符串到最多 n 字符(避免变更摘要过长)。
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(截断)"
}

// summarizeChange 调 GLM 把 diff/对话总结成自然语言(改了什么、为什么、影响),让人看明白。
// apiKey 空 或 无内容时返回空串(非致命,变更仍记录 diff/commits)。
func summarizeChange(ctx context.Context, apiKey, diff, conversation string) string {
	if apiKey == "" || (diff == "" && conversation == "") {
		return ""
	}
	prompt := "你是变更总结助手。根据下面的代码变更,用 2-4 句中文总结:这次改了什么、为什么、影响。不要罗列代码或 diff。\n\n"
	if conversation != "" {
		prompt += "【对话】\n" + truncateStr(conversation, 1000) + "\n\n"
	}
	if diff != "" {
		prompt += "【diff】\n" + truncateStr(diff, 2000)
	}
	body, _ := json.Marshal(map[string]interface{}{
		"model":    "glm-5.1",
		"messages": []map[string]string{{"role": "user", "content": prompt}},
	})
	req, err := http.NewRequestWithContext(ctx, "POST", "https://open.bigmodel.cn/api/coding/paas/v4/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var r struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.NewDecoder(resp.Body).Decode(&r) != nil {
		return ""
	}
	if len(r.Choices) == 0 {
		return ""
	}
	return strings.TrimSpace(r.Choices[0].Message.Content)
}

// InjectRequirement 把需求规格作为 prompt 注入 opencode 会话,AI 在工作台实时编码(开发者看过程/介入)。
// 替代 dispatch 黑盒:交互式需求驱动开发。prompt 由前端从需求规格拼装。
//
// @Summary      向工作台注入需求 prompt
// @Tags         appdeploy
// @Accept       json
// @Produce      json
// @Param        id     path    string  true   "项目空间ID"
// @Param        aid    path    string  true   "应用ID"
// @Param        body   body    object  true   "注入内容{prompt}"
// @Param        X-User header  string  false  "开发者身份"
// @Success      200    {object}  map[string]interface{}  "注入结果"
// @Failure      400    {object}  map[string]interface{}  "invalid body"
// @Failure      404    {object}  map[string]interface{}  "应用不存在"
// @Failure      500    {object}  map[string]interface{}  "工作台未启用/注入失败"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/inject-requirement [post]
func (h *Handler) InjectRequirement(c *gin.Context) {
	psID, aid := c.Param("id"), c.Param("aid")
	a, err := h.store.Get(c.Request.Context(), psID, aid)
	if err != nil || a == nil || a.ID == "" {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	if h.codeWS == nil {
		httpx.Err(c, 500, 50021, "交互编码工作台未启用")
		return
	}
	var in struct {
		Prompt string `json:"prompt" binding:"required"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	user := c.GetString(auth.CtxUserID)
	if user == "" {
		user = "anonymous"
	}
	if err := h.codeWS.SendPrompt(aid, user, in.Prompt); err != nil {
		httpx.Err(c, 500, 50021, err.Error())
		return
	}
	httpx.OK(c, gin.H{"injected": true, "note": "需求已发给 opencode,在工作台看 AI 实时编码"})
}

// Submit 需求-代码核对门禁:从 requirement 读验收标准 + 读开发者 worktree 代码,AI 逐条核对。
// 有 ❌ → 拦截(409);AI 失败 → 拒绝(503,不静默放行);全 ✅ → 自动登记变更(关联需求)。
//
// @Summary      提交需求-代码核对门禁
// @Tags         appdeploy
// @Accept       json
// @Produce      json
// @Param        id     path    string  true   "项目空间ID"
// @Param        aid    path    string  true   "应用ID"
// @Param        body   body    object  true   "提交内容{req_id}"
// @Param        X-User header  string  false  "开发者身份"
// @Success      200    {object}  map[string]interface{}  "核对通过,已登记变更"
// @Failure      400    {object}  map[string]interface{}  "缺少 req_id/无验收标准/工作分支不存在"
// @Failure      404    {object}  map[string]interface{}  "应用/需求不存在"
// @Failure      409    {object}  map[string]interface{}  "核对未通过"
// @Failure      503    {object}  map[string]interface{}  "AI 核对失败"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/submit [post]
func (h *Handler) Submit(c *gin.Context) {
	psID, aid := c.Param("id"), c.Param("aid")
	a, err := h.store.Get(c.Request.Context(), psID, aid)
	if err != nil || a == nil || a.ID == "" {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	var in struct {
		ReqID string `json:"req_id"` // 必填:关联需求(核对其验收标准)
	}
	_ = c.ShouldBindJSON(&in)
	if in.ReqID == "" {
		httpx.Err(c, 400, 40031, "缺少 req_id(需关联需求以核对其验收标准)")
		return
	}
	if h.reqRepo == nil {
		httpx.Err(c, 500, 50021, "需求仓库未启用,无法核对")
		return
	}
	req, err := h.reqRepo.Get(c.Request.Context(), in.ReqID)
	if err != nil || req == nil || req.ID == "" {
		httpx.Err(c, 404, 40420, "需求不存在")
		return
	}
	// P0-2:验收标准从 requirement 读(不信前端 body),空则拒绝(不跳过核对)
	ac := strings.TrimSpace(req.AcceptanceCriteria)
	if ac == "" || ac == "[]" {
		httpx.Err(c, 400, 40031, "需求无验收标准,请先补全后再提交")
		return
	}
	// P0-1:读开发者 worktree 代码(.worktrees/<user>/),不是主 repo
	user := c.GetString(auth.CtxUserID)
	if user == "" {
		user = "anonymous"
	}
	worktreeDir := filepath.Join(a.RepoDir, ".worktrees", sanitizeID(user))
	if _, err := os.Stat(worktreeDir); err != nil {
		httpx.Err(c, 400, 40032, "工作分支不存在,请先认领需求/打开工作台生成 dev-"+sanitizeID(user)+" 分支")
		return
	}
	code := readRepoCode(worktreeDir)
	apiKey := ""
	if h.cfg != nil {
		apiKey = h.cfg.Get("zhipuai_api_key", "")
	}
	check := h.checkFn
	if check == nil {
		check = checkRequirement
	}
	passed, checkErr, details := check(c.Request.Context(), apiKey, code, req.Title, ac)
	if checkErr != nil {
		httpx.Err(c, 503, 50301, "AI 核对失败(请重试): "+checkErr.Error())
		return
	}
	if !passed {
		httpx.Err(c, 409, 40930, "❌ 需求-代码核对未通过(请按差异修正后再提交):\n"+details)
		return
	}
	// P1:全 ✅ 自动登记变更(关联需求),不再两步手工
	if h.changes == nil {
		httpx.OK(c, gin.H{"passed": true, "details": details, "note": "✅ 核对通过(变更闸门未启用,未自动登记)"})
		return
	}
	chg := &change.ChangeRequest{
		ProjectSpaceID: psID, Kind: "code", SourceID: aid, RepoDir: a.RepoDir,
		Output: "【需求】" + in.ReqID + "\n【核对】通过\n" + details,
	}
	if err := h.changes.Create(c.Request.Context(), chg); err != nil {
		httpx.Err(c, 500, 50022, "核对通过但登记变更失败: "+err.Error())
		return
	}
	httpx.OK(c, gin.H{"passed": true, "details": details, "change_id": chg.ID, "note": "✅ 核对通过,已登记变更,待审批"})
}

// Merge 把开发者分支(dev-<user>)合并到主线 main,供上线。
// G3 前置:需有 approved 变更;合并成功后收敛(释放认领+需求delivered+清worktree)。冲突则放弃合并并报错。
//
// @Summary      合并开发者分支到 main
// @Tags         appdeploy
// @Accept       json
// @Produce      json
// @Param        id     path    string  true   "项目空间ID"
// @Param        aid    path    string  true   "应用ID"
// @Param        body   body    object  false  "合并选项{req_id}"
// @Param        X-User header  string  false  "开发者身份"
// @Success      200    {object}  map[string]interface{}  "合并结果(merged/released/delivered)"
// @Failure      404    {object}  map[string]interface{}  "应用不存在"
// @Failure      409    {object}  map[string]interface{}  "需先审批/合并冲突"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/merge [post]
func (h *Handler) Merge(c *gin.Context) {
	psID, aid := c.Param("id"), c.Param("aid")
	a, err := h.store.Get(c.Request.Context(), psID, aid)
	if err != nil || a == nil || a.ID == "" {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	var in struct {
		ReqID string `json:"req_id"` // 合并哪条需求的变更(收敛:释放认领+delivered)
	}
	_ = c.ShouldBindJSON(&in)
	user := c.GetString(auth.CtxUserID)
	if user == "" {
		user = "anonymous"
	}
	// G3 前置:需有 approved 变更才能合并(对齐 Promote grandfather 闸门)
	if h.changes != nil {
		if ok, _ := h.changes.HasApproved(c.Request.Context(), aid); !ok {
			httpx.Err(c, 409, 40940, "需先审批通过变更才能合并(变更闸门)")
			return
		}
	}
	branch := "dev-" + sanitizeID(user)
	ctx := c.Request.Context()
	_, _ = runGit(ctx, a.RepoDir, "checkout", "-q", "main")
	out, err := runGit(ctx, a.RepoDir, "merge", "--no-ff", "-m", "merge "+branch, branch)
	if err != nil {
		_, _ = runGit(ctx, a.RepoDir, "merge", "--abort")
		httpx.Err(c, 409, 40940, "合并冲突(需人工解决后重试):\n"+out)
		return
	}
	// 收敛:释放认领 + 需求 delivered + 清 worktree
	released, delivered, cleaned := "", "", ""
	if h.reqRepo != nil && in.ReqID != "" {
		if err := h.reqRepo.Release(ctx, in.ReqID); err == nil {
			released = in.ReqID
		}
		if err := h.reqRepo.UpdateStatus(ctx, in.ReqID, "delivered"); err == nil {
			delivered = "delivered"
		}
	}
	wt := filepath.Join(a.RepoDir, ".worktrees", sanitizeID(user))
	if _, err := os.Stat(wt); err == nil {
		if _, err := runGit(ctx, a.RepoDir, "worktree", "remove", "--force", wt); err == nil {
			cleaned = wt
		}
	}
	httpx.OK(c, gin.H{"merged": branch, "released": released, "delivered": delivered, "worktree_cleaned": cleaned, "note": "已合并到 main,需求交付,工作区已清理"})
}

// readRepoCode 读 repo 内全部文件内容(代码+文档,截断),供 AI 核对。
func readRepoCode(repoDir string) string {
	docs, _ := ScanDocs(repoDir)
	var sb strings.Builder
	for i, d := range docs {
		if i >= 15 {
			break
		}
		content, err := ReadRepoFile(repoDir, d.Path)
		if err != nil {
			continue
		}
		sb.WriteString("=== " + d.Path + " ===\n")
		sb.WriteString(truncateStr(content, 1200))
		sb.WriteString("\n\n")
		if sb.Len() > 8000 {
			break
		}
	}
	return sb.String()
}

// checkRequirement 调 GLM 核对代码是否实现需求验收标准。
// 返回 (passed, err, details):err!=nil → AI 失败(调用方应 503,不静默放行);
// passed=false&err=nil → 核对未通过(409);passed=true → 通过。
func checkRequirement(ctx context.Context, apiKey, code, title, criteria string) (bool, error, string) {
	if apiKey == "" {
		return false, fmt.Errorf("AI 未配置(zhipuai_api_key 为空)"), ""
	}
	prompt := fmt.Sprintf("你是严格的代码核对员。判断以下代码是否实现了需求的每条验收标准。\n需求标题:%s\n验收标准:\n%s\n\n代码:\n%s\n\n对每条验收标准判断:✅已实现/❌未实现/⚠️偏离,note 指出实现位置或差异。\n严格只返回 JSON: {\"passed\": true/false, \"details\":[{\"criteria\":\"原标准\",\"status\":\"✅/❌/⚠️\",\"note\":\"\"}]}\npassed=true 当且仅当没有任何 ❌。", title, criteria, truncateStr(code, 6000))
	body, _ := json.Marshal(map[string]interface{}{
		"model":    "glm-5.1",
		"messages": []map[string]string{{"role": "user", "content": prompt}},
	})
	req, err := http.NewRequestWithContext(ctx, "POST", "https://open.bigmodel.cn/api/coding/paas/v4/chat/completions", bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("核对请求构造失败: %w", err), ""
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("AI 调用失败: %w", err), ""
	}
	defer resp.Body.Close()
	var r struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.NewDecoder(resp.Body).Decode(&r) != nil || len(r.Choices) == 0 {
		return false, fmt.Errorf("AI 无响应"), ""
	}
	var result struct {
		Passed  bool `json:"passed"`
		Details []struct {
			Criteria string `json:"criteria"`
			Status   string `json:"status"`
			Note     string `json:"note"`
		} `json:"details"`
	}
	if json.Unmarshal([]byte(extractJSONObject(r.Choices[0].Message.Content)), &result) != nil {
		return false, fmt.Errorf("AI 返回解析失败: %s", r.Choices[0].Message.Content), ""
	}
	var sb strings.Builder
	for _, d := range result.Details {
		sb.WriteString(d.Status + " " + d.Criteria + " — " + d.Note + "\n")
	}
	return result.Passed, nil, sb.String()
}

// extractJSONObject 从可能含 markdown 的文本提取首个 JSON 对象。
func extractJSONObject(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}

// RepoDocs 扫描当前应用 repo 的文档(README/.md),供编码时查阅项目文档结构。
//
// @Summary      应用 repo 文档列表
// @Tags         appdeploy
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Param        aid  path  string  true  "应用ID"
// @Success      200  {object}  map[string]interface{}  "文档列表"
// @Failure      404  {object}  map[string]interface{}  "应用不存在"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/repo-docs [get]
func (h *Handler) RepoDocs(c *gin.Context) {
	a, _ := h.store.Get(c.Request.Context(), c.Param("id"), c.Param("aid"))
	if a == nil || a.ID == "" {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	docs, _ := ScanDocs(a.RepoDir)
	httpx.OK(c, docs)
}

// RepoFile 读当前应用 repo 内某文件内容(供文档展开查看)。
//
// @Summary      读应用 repo 文件内容
// @Tags         appdeploy
// @Produce      json
// @Param        id    path   string  true  "项目空间ID"
// @Param        aid   path   string  true  "应用ID"
// @Param        path  query  string  true  "文件路径"
// @Success      200   {object}  map[string]interface{}  "文件内容"
// @Failure      400   {object}  map[string]interface{}  "读取失败"
// @Failure      404   {object}  map[string]interface{}  "应用不存在"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/repo-file [get]
func (h *Handler) RepoFile(c *gin.Context) {
	a, _ := h.store.Get(c.Request.Context(), c.Param("id"), c.Param("aid"))
	if a == nil || a.ID == "" {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	content, err := ReadRepoFile(a.RepoDir, c.Query("path"))
	if err != nil {
		httpx.Err(c, 400, 40001, err.Error())
		return
	}
	httpx.OK(c, gin.H{"path": c.Query("path"), "content": content})
}

// ListEnv 列出应用运行时环境变量（部署时 docker run -e 注入）。is_secret 的 value 接口层 mask（不泄露）。
//
// @Summary      应用环境变量列表
// @Tags         appdeploy
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Param        aid  path  string  true  "应用ID"
// @Success      200  {object}  map[string]interface{}  "环境变量列表(密钥值已 mask)"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/env [get]
func (h *Handler) ListEnv(c *gin.Context) {
	list, err := h.store.ListEnv(c.Request.Context(), c.Param("aid"))
	if err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	for i := range list {
		if list[i].IsSecret {
			list[i].Value = "" // 隐藏密钥明文（实际值仍用于部署注入）
		}
	}
	httpx.OK(c, list)
}

// UpsertEnv 新增/更新环境变量。
//
// @Summary      新增/更新环境变量
// @Tags         appdeploy
// @Accept       json
// @Produce      json
// @Param        id    path  object  true  "项目空间ID"
// @Param        aid   path  object  true  "应用ID"
// @Param        body  body  object  true  "环境变量{key,value,is_secret}"
// @Success      200   {object}  map[string]interface{}  "保存结果"
// @Failure      400   {object}  map[string]interface{}  "invalid body"
// @Failure      500   {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/env [post]
func (h *Handler) UpsertEnv(c *gin.Context) {
	var in struct {
		Key      string `json:"key" binding:"required"`
		Value    string `json:"value"`
		IsSecret bool   `json:"is_secret"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	if err := h.store.UpsertEnv(c.Request.Context(), c.Param("aid"), in.Key, in.Value, in.IsSecret); err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	httpx.OK(c, gin.H{"app_id": c.Param("aid"), "key": in.Key, "saved": true})
}

// DeleteEnv 删除环境变量。
//
// @Summary      删除环境变量
// @Tags         appdeploy
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Param        aid  path  string  true  "应用ID"
// @Param        key  path  string  true  "环境变量键名"
// @Success      200  {object}  map[string]interface{}  "删除结果"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/env/{key} [delete]
func (h *Handler) DeleteEnv(c *gin.Context) {
	if err := h.store.DeleteEnv(c.Request.Context(), c.Param("aid"), c.Param("key")); err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	httpx.OK(c, gin.H{"app_id": c.Param("aid"), "key": c.Param("key"), "deleted": true})
}

type createBody struct {
	Name         string `json:"name" binding:"required"`
	RepoDir      string `json:"repo_dir"`      // 可选；空=平台托管 git 仓库 /data/repos/<name>
	InternalPort int    `json:"internal_port"` // 可选；buildpack 检测或默认 8080
}

// validateAppName 应用名必须人工起名(非随机数/ID):trim 非空、≥2 字符、不带 ID 前缀(chg_/app_/req_/rel_/ps_)、非纯数字。
// 返回错误消息(空串=合法)。各中心显示应用名的前提是 name 本身可读。
func validateAppName(name string) string {
	n := strings.TrimSpace(name)
	rc := 0
	for range n {
		rc++
	}
	if rc < 2 {
		return "应用名需人工填写(至少 2 个字符)"
	}
	lower := strings.ToLower(n)
	for _, p := range []string{"chg_", "app_", "req_", "rel_", "ps_"} {
		if strings.HasPrefix(lower, p) {
			return "应用名不能使用 ID 前缀 " + p + ",请起一个可读的名字(如 hello-go)"
		}
	}
	allDigit := true
	for _, r := range n {
		if r < '0' || r > '9' {
			allDigit = false
			break
		}
	}
	if allDigit {
		return "应用名不能为纯数字,请起一个可读的名字"
	}
	return ""
}

// Create 注册一个产出应用，并初始化其托管 git 仓库（代码归属确立：/data/repos/<name>）。
//
// @Summary      创建应用
// @Tags         appdeploy
// @Accept       json
// @Produce      json
// @Param        id    path  createBody  true  "项目空间ID"
// @Param        body  body  createBody  true  "应用(name+repo_dir+internal_port)"
// @Success      200   {object}  map[string]interface{}  "创建的应用"
// @Failure      400   {object}  map[string]interface{}  "invalid body"
// @Failure      500   {object}  map[string]interface{}  "仓库初始化/创建失败"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps [post]
func (h *Handler) Create(c *gin.Context) {
	var in createBody
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	if msg := validateAppName(in.Name); msg != "" {
		httpx.Err(c, 400, 40001, msg)
		return
	}
	repoDir := in.RepoDir
	if repoDir == "" {
		repoDir = ManagedRepoDir(in.Name) // 平台托管
	}
	if err := EnsureRepo(c.Request.Context(), repoDir); err != nil {
		httpx.Err(c, 500, 50020, "初始化应用仓库失败: "+err.Error())
		return
	}
	port := in.InternalPort
	if port == 0 {
		port = 8080
	}
	a := &Application{ProjectSpaceID: c.Param("id"), Name: in.Name, RepoDir: repoDir, InternalPort: port}
	if err := h.store.Create(c.Request.Context(), a); err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	httpx.Created(c, a)
}

// deployBody 部署请求体（均可选）。
type deployBody struct {
	Env string `json:"env"` // test / prod；空默认 test
	SHA string `json:"sha"` // 可选：部署指定历史版本（回滚）
}

// Deploy 构建+部署到指定环境（默认 test=测试验证）。立即返回 building，后台完成。
//
// @Summary      构建并部署应用
// @Tags         appdeploy
// @Accept       json
// @Produce      json
// @Param        id    path  string  true   "项目空间ID"
// @Param        aid   path  string  true   "应用ID"
// @Param        body  body  object  false  "部署选项{env,sha}"
// @Success      200   {object}  map[string]interface{}  "异步构建状态(building)"
// @Failure      404   {object}  map[string]interface{}  "应用不存在"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/deploy [post]
func (h *Handler) Deploy(c *gin.Context) {
	psID, aid := c.Param("id"), c.Param("aid")
	if a, _ := h.store.Get(c.Request.Context(), psID, aid); a == nil || a.ID == "" {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	var in deployBody
	_ = c.ShouldBindJSON(&in)
	env := in.Env
	if !IsValidEnv(env) {
		env = EnvTest
	}
	go h.buildAndDeploy(psID, aid, "", env)
	httpx.OK(c, gin.H{"id": aid, "env": env, "status": "building", "note": "异步构建部署到 " + env + " 环境中"})
}

// Promote 上线：部署到 prod 环境（用户可访问）。
//
// @Summary      上线应用到 prod
// @Tags         appdeploy
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Param        aid  path  string  true  "应用ID"
// @Success      200  {object}  map[string]interface{}  "上线状态(building)"
// @Failure      404  {object}  map[string]interface{}  "应用不存在"
// @Failure      409  {object}  map[string]interface{}  "需先登记变更并审批通过(变更闸门)"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/promote [post]
func (h *Handler) Promote(c *gin.Context) {
	psID, aid := c.Param("id"), c.Param("aid")
	if a, _ := h.store.Get(c.Request.Context(), psID, aid); a == nil || a.ID == "" {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	// 🚪 变更闸门（grandfather）：登记过变更的应用，必须有 approved 变更才能上线 prod。
	// 从未登记过的老应用不受约束——一旦开始登记变更，即进入治理流程。
	if h.changes != nil {
		if hasAny, _ := h.changes.HasAny(c.Request.Context(), aid); hasAny {
			if ok, _ := h.changes.HasApproved(c.Request.Context(), aid); !ok {
				httpx.Err(c, 409, 40920, "需先登记变更并审批通过才能上线 prod（变更闸门）")
				return
			}
			// 上线后:把该应用的 approved 变更标记为 released（从待上线列表消失）
			_ = h.changes.MarkReleased(c.Request.Context(), aid) // 上线后标记 released;失败不阻塞(下次上线再标)
		}
	}
	go h.buildAndDeploy(psID, aid, "", EnvProd)
	httpx.OK(c, gin.H{"id": aid, "env": EnvProd, "status": "building", "note": "上线中：部署到 prod 环境"})
}

// DeployCommit 部署/回滚到指定历史版本（默认 test 环境）。
//
// @Summary      部署/回滚到指定版本
// @Tags         appdeploy
// @Accept       json
// @Produce      json
// @Param        id    path  string  true  "项目空间ID"
// @Param        aid   path  string  true  "应用ID"
// @Param        body  body  object  true  "部署选项{sha,env}"
// @Success      200   {object}  map[string]interface{}  "版本化部署状态(building)"
// @Failure      400   {object}  map[string]interface{}  "需提供 sha"
// @Failure      404   {object}  map[string]interface{}  "应用不存在"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/deploy-commit [post]
func (h *Handler) DeployCommit(c *gin.Context) {
	psID, aid := c.Param("id"), c.Param("aid")
	var in deployBody
	if err := c.ShouldBindJSON(&in); err != nil || in.SHA == "" {
		httpx.Err(c, 400, 40001, "需提供 sha")
		return
	}
	env := in.Env
	if !IsValidEnv(env) {
		env = EnvTest
	}
	if _, err := h.store.Get(c.Request.Context(), psID, aid); err != nil {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	go h.buildAndDeploy(psID, aid, in.SHA, env)
	httpx.OK(c, gin.H{"id": aid, "sha": in.SHA, "env": env, "status": "building", "note": "版本化部署/回滚到 " + env})
}

// buildAndDeploy 后台执行（脱离 HTTP context）。sha 非空则部署该历史版本；env 指定环境。
func (h *Handler) buildAndDeploy(psID, aid, sha, env string) {
	ctx := context.Background()
	a, err := h.store.Get(ctx, psID, aid)
	if err != nil || a == nil || a.ID == "" {
		return
	}
	ins, err := h.store.GetOrCreateInstance(ctx, a.ID, env)
	if err != nil || ins == nil {
		return
	}
	// 清理该 app+env 所有历史容器（DB 记录的 + 孤儿残留），彻底释放端口避免漂移/Conflict
	_, _ = h.deployer.RemoveByPrefix(ctx, "appdeploy-"+a.Name+"-"+env+"-")
	// 版本化回滚：checkout 指定 commit，构建后恢复工作区
	prevBranch := ""
	if sha != "" {
		prevBranch, _ = Checkout(ctx, a.RepoDir, sha)
		defer Restore(ctx, a.RepoDir, prevBranch)
	}
	note := ""
	if sha != "" {
		note = "版本化部署：commit " + sha[:min(7, len(sha))] + "\n"
	}
	if gen, port, err := EnsureDockerfile(a.RepoDir, a.InternalPort); err == nil {
		if port != 0 && port != a.InternalPort {
			a.InternalPort = port
		}
		if gen {
			note += "buildpack 已按源码类型自动生成 Dockerfile\n"
		}
	}
	log, err := h.deployer.Build(ctx, a, ins)
	if note != "" {
		log = note + log
	}
	if err != nil {
		ins.Status = "failed"
		ins.LastError = err.Error()
		ins.BuildLog = tail(log, 2000)
		_ = h.store.UpdateInstance(ctx, ins)
		h.syncOverviewIfProd(ctx, a, env)
		return
	}
	ins.Status = "building"
	ins.BuildLog = tail(log, 2000)
	_ = h.store.UpdateInstance(ctx, ins)
	envPairs, _ := h.store.EnvPairs(ctx, a.ID) // 应用运行时环境变量（含密钥）注入容器
	if err := h.deployer.Deploy(ctx, a, ins, envPairs); err != nil {
		ins.Status = "failed"
		ins.LastError = err.Error()
		_ = h.store.UpdateInstance(ctx, ins)
		h.syncOverviewIfProd(ctx, a, env)
		return
	}
	ins.Status = "running"
	ins.LastError = ""
	ins.BuildLog = tail(log, 2000)
	_ = h.store.UpdateInstance(ctx, ins)
	h.syncOverviewIfProd(ctx, a, env)
}

// syncOverviewIfProd 仅 prod 环境把实例态同步到 application 概览（列表显示正式上线态）。
// test 部署不改变应用概览——概览始终代表"正式状态"。
func (h *Handler) syncOverviewIfProd(ctx context.Context, a *Application, env string) {
	if env != EnvProd {
		return
	}
	ins, _ := h.store.GetInstance(ctx, a.ID, EnvProd)
	if ins == nil {
		return
	}
	a.Image = ins.Image
	a.ContainerName = ins.ContainerName
	a.HostPort = ins.HostPort
	a.URL = ins.URL
	a.Version = ins.Version
	a.Status = ins.Status
	a.LastError = ins.LastError
	a.BuildLog = ins.BuildLog
	_ = h.store.UpdateDeploy(ctx, a)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// instanceFromCtx 取 prod 实例（停止/启动/日志针对正式环境）。
//
// @Summary      停止应用(prod)
// @Tags         appdeploy
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Param        aid  path  string  true  "应用ID"
// @Success      200  {object}  map[string]interface{}  "停止结果(stopped)"
// @Failure      400  {object}  map[string]interface{}  "应用未在 prod 部署"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/stop [post]
func (h *Handler) Stop(c *gin.Context) {
	a, _ := h.store.Get(c.Request.Context(), c.Param("id"), c.Param("aid"))
	ins, _ := h.store.GetInstance(c.Request.Context(), c.Param("aid"), EnvProd)
	if a == nil || ins == nil || ins.ContainerName == "" {
		httpx.Err(c, 400, 50020, "应用未在 prod 部署")
		return
	}
	if _, err := h.deployer.Stop(c.Request.Context(), ins.ContainerName); err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	_ = h.store.SetInstanceStatus(c.Request.Context(), a.ID, EnvProd, "stopped", "", "")
	_ = h.store.SetStatus(c.Request.Context(), a.ProjectSpaceID, a.ID, "stopped", "", "")
	httpx.OK(c, gin.H{"id": a.ID, "status": "stopped"})
}

// Start 启动应用(prod 实例)。
//
// @Summary      启动应用(prod)
// @Tags         appdeploy
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Param        aid  path  string  true  "应用ID"
// @Success      200  {object}  map[string]interface{}  "启动结果(running)"
// @Failure      400  {object}  map[string]interface{}  "应用未在 prod 部署"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/start [post]
func (h *Handler) Start(c *gin.Context) {
	a, _ := h.store.Get(c.Request.Context(), c.Param("id"), c.Param("aid"))
	ins, _ := h.store.GetInstance(c.Request.Context(), c.Param("aid"), EnvProd)
	if a == nil || ins == nil || ins.ContainerName == "" {
		httpx.Err(c, 400, 50020, "应用未在 prod 部署")
		return
	}
	if _, err := h.deployer.Start(c.Request.Context(), ins.ContainerName); err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	_ = h.store.SetInstanceStatus(c.Request.Context(), a.ID, EnvProd, "running", "", "")
	_ = h.store.SetStatus(c.Request.Context(), a.ProjectSpaceID, a.ID, "running", "", "")
	httpx.OK(c, gin.H{"id": a.ID, "status": "running"})
}

// Delete 删除应用 + 清理所有环境实例容器。
//
// @Summary      删除应用
// @Tags         appdeploy
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Param        aid  path  string  true  "应用ID"
// @Success      200  {object}  map[string]interface{}  "删除结果"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid} [delete]
func (h *Handler) Delete(c *gin.Context) {
	a, _ := h.store.Get(c.Request.Context(), c.Param("id"), c.Param("aid"))
	if a != nil {
		// 删除所有环境的容器
		inss, _ := h.store.ListInstancesByApp(c.Request.Context(), a.ID)
		for _, ins := range inss {
			if ins.ContainerName != "" {
				_, _ = h.deployer.Remove(c.Request.Context(), ins.ContainerName)
			}
		}
		if a.ContainerName != "" { // 兜底：旧概览容器名
			_, _ = h.deployer.Remove(c.Request.Context(), a.ContainerName)
		}
		// 删除该应用的所有镜像(避免堆积)
		_, _ = h.deployer.RemoveImages(c.Request.Context(), a.Name)
	}
	if err := h.store.Delete(c.Request.Context(), c.Param("id"), c.Param("aid")); err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": c.Param("aid"), "deleted": true})
}

// Logs 应用 prod 实例日志。
//
// @Summary      应用日志
// @Tags         appdeploy
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Param        aid  path  string  true  "应用ID"
// @Success      200  {object}  map[string]interface{}  "日志内容"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/logs [get]
func (h *Handler) Logs(c *gin.Context) {
	a, _ := h.store.Get(c.Request.Context(), c.Param("id"), c.Param("aid"))
	ins, _ := h.store.GetInstance(c.Request.Context(), c.Param("aid"), EnvProd)
	if a == nil || ins == nil || ins.ContainerName == "" {
		httpx.OK(c, gin.H{"logs": "(应用未在 prod 部署)"})
		return
	}
	log, err := h.deployer.Logs(c.Request.Context(), ins.ContainerName, 200)
	if err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	httpx.OK(c, gin.H{"logs": log})
}

// Stats 应用某环境的资源占用(docker stats) + URL 健康探测（运维可观测性）。
//
// @Summary      应用资源占用与健康探测
// @Tags         appdeploy
// @Produce      json
// @Param        id   path   string  true  "项目空间ID"
// @Param        aid  path   string  true  "应用ID"
// @Param        env  query  string  false "环境(test/prod,默认 prod)"
// @Success      200  {object}  map[string]interface{}  "资源占用+健康状态"
// @Failure      404  {object}  map[string]interface{}  "应用不存在"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/stats [get]
func (h *Handler) Stats(c *gin.Context) {
	psID, aid := c.Param("id"), c.Param("aid")
	a, _ := h.store.Get(c.Request.Context(), psID, aid)
	if a == nil || a.ID == "" {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	env := c.Query("env")
	if !IsValidEnv(env) {
		env = EnvProd
	}
	ins, _ := h.store.GetInstance(c.Request.Context(), aid, env)
	if ins == nil || ins.ContainerName == "" {
		httpx.OK(c, gin.H{"env": env, "deployed": false})
		return
	}
	stats, _ := h.deployer.Stats(c.Request.Context(), ins.ContainerName)
	httpx.OK(c, gin.H{
		"env": env, "url": ins.URL, "deployed": true,
		"stats": stats, "health": probeHealth(ins.URL),
	})
}

// probeHealth 探测 URL 健康状态：up / down / error(code) / unknown。
func probeHealth(url string) string {
	if url == "" {
		return "unknown"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "down"
	}
	defer resp.Body.Close()
	if resp.StatusCode < 400 {
		return "up"
	}
	return "error(" + strconv.Itoa(resp.StatusCode) + ")"
}

// DeployByAppID 供发布中心调用：部署应用到 test 环境（发布=测试验证，由 promote 上线 prod）。
func (h *Handler) DeployByAppID(ctx context.Context, appID string) (*Application, error) {
	a, err := h.store.GetByAppID(ctx, appID)
	if err != nil || a == nil || a.ID == "" {
		return nil, errAppNotFound
	}
	go h.buildAndDeploy(a.ProjectSpaceID, appID, "", EnvTest)
	return a, nil
}

var errAppNotFound = errString("应用不存在")

type errString string

func (e errString) Error() string { return string(e) }

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}
