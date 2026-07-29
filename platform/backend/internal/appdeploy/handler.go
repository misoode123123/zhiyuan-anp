package appdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"zhiyuan-anp/platform/backend/internal/appgw"
	"zhiyuan-anp/platform/backend/internal/auth"
	"zhiyuan-anp/platform/backend/internal/change"
	"zhiyuan-anp/platform/backend/internal/codews"
	"zhiyuan-anp/platform/backend/internal/config"
	"zhiyuan-anp/platform/backend/internal/httpx"
	"zhiyuan-anp/platform/backend/internal/pgsupply"
	"zhiyuan-anp/platform/backend/internal/requirement"
	"zhiyuan-anp/platform/backend/internal/standard"
)

// AppQuotaChecker 应用数配额检查（由 quota.Service 实现，避免 appdeploy→quota 循环依赖）。
// 超限返回的错误透传到前端（handler 识别 QuotaExceededError → 409）。
type AppQuotaChecker interface {
	CheckApps(ctx context.Context, psID string) error
}

// Handler 应用部署 HTTP 接口。
type Handler struct {
	store       *Store
	deployer    *Deployer
	codeWS      *codews.Manager         // 交互编码工作台（opencode serve）；nil=未启用
	changes     *change.Store           // 变更闸门（期2）；nil=未启用
	cfg         *config.Store           // 系统配置(取 zhipuai_api_key 做 AI 总结)；nil=不总结
	reqRepo     *requirement.Repository // 需求-代码核对门禁:读 requirement 的验收标准
	provisioner *pgsupply.Provisioner   // 应用库供给（Create 建库 / Delete 删库）
	routeWriter appgw.RouteWriter       // appgw 路由表写入（Deploy 后写 / Delete 时清）；nil=不写路由
	standards   *standard.Store         // 编码规范（启动 opencode 前刷新应用 AGENTS.md）；nil=不刷新
	quota       AppQuotaChecker         // 应用数配额检查；nil=不强制
	nodeStore   *NodeStore              // 部署节点（多机）；nil=仅本地
	checkFn     checkFunc               // 可 mock 的核对函数(默认 checkRequirement);测试可注入
	// 非 web 形态构建产物链路（Task 10）；nil=未装配（BuildArtifacts/ListArtifacts/DownloadArtifact 报"功能未配置"）。
	// Task 13 在 main.go 注入真实值；在此之前 factory 调用处传 nil 保证编译。
	buildCfgStore   *BuildConfigStore  // 构建配置（desktop/mobile/cli 按形态查镜像/命令）
	artifactStore   *ArtifactStore     // 产物记录读写（appdeploy_artifact）
	artifactStorage ArtifactStorage    // 产物实体存储（本地降级 / MinIO）
}

// checkFunc 需求-代码核对的函数签名(便于测试 mock)。
// passed=false&err=nil → 核对未通过(409); err!=nil → AI 失败(503); passed=true → 通过。
type checkFunc func(ctx context.Context, apiKey, code, title, criteria string) (passed bool, err error, details string)

// CodeWS 暴露交互编码工作台 Manager（供 performance 模块读 live opencode 会话消息）。
func (h *Handler) CodeWS() *codews.Manager { return h.codeWS }

// NewHandler 构造。codeWS/changes/cfg/reqRepo/provisioner/routeWriter/standards/quota 可为 nil（不启用对应能力）。
// buildCfgStore/artifactStore/artifactStorage 为非 web 构建产物链路；nil=未装配（Task 13 在 main.go 注入）。
func NewHandler(store *Store, deployer *Deployer, codeWS *codews.Manager, changes *change.Store, cfg *config.Store, reqRepo *requirement.Repository, provisioner *pgsupply.Provisioner, routeWriter appgw.RouteWriter, standards *standard.Store, quota AppQuotaChecker, buildCfgStore *BuildConfigStore, artifactStore *ArtifactStore, artifactStorage ArtifactStorage) *Handler {
	var nodeStore *NodeStore
	if store != nil {
		nodeStore = NewNodeStore(store.db)
	}
	h := &Handler{store: store, deployer: deployer, codeWS: codeWS, changes: changes, cfg: cfg, reqRepo: reqRepo, provisioner: provisioner, routeWriter: routeWriter, standards: standards, quota: quota, nodeStore: nodeStore, buildCfgStore: buildCfgStore, artifactStore: artifactStore, artifactStorage: artifactStorage}
	h.checkFn = checkRequirement
	return h
}

// Register 模块级装配：内部 new Deployer/codews.Manager + NewHandler + Register。
// 返回 *Handler 供 release 模块（发布后自动部署）复用。
// buildCfgStore/artifactStore/artifactStorage 为非 web 构建产物链路；nil=未装配（Task 13 注入）。
func Register(r gin.IRouter, store *Store, appDeployHost string, changeStore *change.Store, configStore *config.Store, reqRepo *requirement.Repository, provisioner *pgsupply.Provisioner, routeWriter appgw.RouteWriter, standards *standard.Store, quota AppQuotaChecker, buildCfgStore *BuildConfigStore, artifactStore *ArtifactStore, artifactStorage ArtifactStorage) *Handler {
	codeWS := codews.NewManager(appDeployHost, configStore)
	if store != nil {
		codeWS.SetSessionLogger(codews.NewPGSessionStore(store.db)) // 会话落库供绩效/互动统计
	}
	h := NewHandler(store, NewDeployer(appDeployHost), codeWS, changeStore, configStore, reqRepo, provisioner, routeWriter, standards, quota, buildCfgStore, artifactStore, artifactStorage)
	h.Register(r)
	return h
}

// Register 注册路由。
func (h *Handler) Register(r gin.IRouter) {
	r.GET("/project-spaces/:id/apps", h.List)
	r.POST("/project-spaces/:id/apps", h.Create)
	r.POST("/project-spaces/:id/import/apps", h.Import) // 导入已有项目（git/dir）；放 /import/apps 避开 /apps/:aid 冲突
	r.POST("/project-spaces/:id/import/apps/upload", h.ImportUpload) // 本机 zip 上传导入
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
	r.GET("/project-spaces/:id/apps/:aid/git-status", h.GitStatus)           // 编码工作台 git 变更：工作区改动 + 提交历史
	r.GET("/project-spaces/:id/apps/:aid/file-diff", h.FileDiff)             // 单文件行级 diff（工作区 / 指定提交）
	r.GET("/project-spaces/:id/apps/:aid/commit-files", h.CommitFilesList)   // 某次提交改了哪些文件
	r.POST("/project-spaces/:id/apps/:aid/commit", h.CommitWorktree)         // 仅提交 dev-<user> worktree（不部署）

	// 非 web 应用构建产物链路（Task 10）：触发构建 / 列产物 / 下载产物。
	r.POST("/project-spaces/:id/apps/:aid/build-artifacts", h.BuildArtifacts)                 // 触发非 web 构建
	r.GET("/project-spaces/:id/apps/:aid/artifacts", h.ListArtifacts)                         // 列产物
	r.GET("/project-spaces/:id/apps/:aid/artifacts/:artid/download", h.DownloadArtifact)      // 下载产物

	// 部署节点管理（多机部署）
	r.GET("/deploy-nodes", h.ListNodes)
	r.POST("/deploy-nodes", h.CreateNode)
	r.DELETE("/deploy-nodes/:nid", h.DeleteNode)
	r.POST("/deploy-nodes/:nid/test", h.TestNode) // 测试连通性
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
		Tool          string `json:"tool"`           // opencode(默认) / claude / codex ...
		RequirementID string `json:"requirement_id"` // 绑定的需求（工作直播按此关联；空=application 页老入口）
	}
	_ = c.ShouldBindJSON(&in)
	user := c.GetString(auth.CtxUserID) // 开发者身份（不同开发者可各选各的工具）
	if user == "" {
		user = "anonymous"
	}
	s, err := h.codeWS.Ensure(psID, aid, a.RepoDir, user, in.Tool, in.RequirementID)
	if err != nil {
		httpx.Err(c, 500, 50021, err.Error())
		return
	}
	// 刷新 AGENTS.md：opencode 的 cwd 是 dev-<user> worktree，规范须写进 worktree 才被加载
	// （写主仓工作区，worktree 看不到）。Ensure 已建 worktree；失败不阻塞编码。
	if h.standards != nil && a.RepoDir != "" {
		_ = h.standards.RefreshAgentsMD(c.Request.Context(), filepath.Join(a.RepoDir, ".worktrees", sanitizeID(user)), psID, "")
	}
	httpx.OK(c, gin.H{"app_id": aid, "user": user, "tool": s.Tool, "url": s.URL, "deep_url": s.DeepURL, "port": s.Port, "session_id": s.SessionID, "requirement_id": in.RequirementID, "note": s.Tool + " 工作台已就绪（开发者 " + user + "），浏览器打开 url 即可交互编码"})
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

	// 自动获取 opencode 对话内容(免手填)。仅 opencode 有平台侧 session API 可拉对话；
	// claude/codex 走 ttyd 终端无对等接口，跳过（变更总结降级为基于 diff/commits，登记变更本身仍可用）。
	conversation := ""
	if h.codeWS != nil {
		user := c.GetString(auth.CtxUserID)
		if user == "" {
			user = "anonymous"
		}
		if s := h.codeWS.Get(aid, user); s != nil && s.Tool == "opencode" {
			if conv, err := h.codeWS.SessionMessages(aid, user); err == nil {
				conversation = conv
			}
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
	// 闭环收敛（PRD 2026-07-26）：带 req_id 时 source_id=requirement_id（发布中心据此回写 delivered/过门禁/触发部署），
	// application_id=aid（激活 historically 恒 NULL 的列，应用闸门/部署关联走它）。无 req_id 时维持 source_id=aid。
	sourceID := aid
	if in.ReqID != "" {
		sourceID = in.ReqID
	}
	chg := &change.ChangeRequest{
		ProjectSpaceID: psID, UserID: c.GetString(auth.CtxUserDBID), Kind: "code", SourceID: sourceID, ApplicationID: aid, RepoDir: a.RepoDir,
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

// truncateStr 截断字符串到最多 n 字节(避免变更摘要过长),但退到完整 UTF-8 边界,
// 不从多字节字符中间切断——否则产生无效 UTF-8,PG UTF8 列拒收(SQLSTATE 22021),
// register-change 在含中文 git 内容的应用上必 500。
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	end := n
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + "...(截断)"
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

// commitMessageFor 为 worktree 未提交改动生成 commit message：读 git diff HEAD 让 AI 总结；
// AI 不可用 / diff 为空时回退固定文案。用于「构建部署」自动提交（test 从 dev 分支构建前）。
func commitMessageFor(ctx context.Context, repoDir, apiKey string) string {
	diff, _ := runGit(ctx, repoDir, "diff", "HEAD")
	if s := summarizeChange(ctx, apiKey, strings.TrimSpace(diff), ""); s != "" {
		return s
	}
	return "编码工作台自动提交"
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
	// claude/codex 走 ttyd 终端，无平台侧 session API（不支持 prompt 注入）；仅 opencode 可注入。
	// 若当前活跃工作台是 claude/codex，提示用户在终端手动操作（而非误报"无活跃会话"）。
	if s := h.codeWS.Get(aid, user); s != nil && s.Tool != "opencode" {
		httpx.Err(c, 400, 40001, "该工具（"+s.Tool+"）不支持平台侧需求注入，请在工作台终端手动粘贴需求编码")
		return
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
	// 闭环收敛：Submit 的 req_id 必填（上方已校验），source_id=reqID 让发布中心能闭环；application_id=aid。
	chg := &change.ChangeRequest{
		ProjectSpaceID: psID, UserID: c.GetString(auth.CtxUserDBID), Kind: "code", SourceID: in.ReqID, ApplicationID: aid, RepoDir: a.RepoDir,
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
		if n, err := h.reqRepo.UpdateStatus(ctx, in.ReqID, "delivered"); err == nil && n > 0 {
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
	RepoDir      string `json:"repo_dir"`      // managed 可选；空=平台托管 git 仓库 /data/repos/<name>
	InternalPort int    `json:"internal_port"` // managed 可选；buildpack 检测或默认 8080
	DeployMode   string `json:"deploy_mode"`   // managed(默认,A类) / external(B类纳管外部应用)
	AppKind      string `json:"app_kind"`      // web/desktop/mobile/cli/service，空默认 web
	ExternalURL  string `json:"external_url"`  // external 必填：外部应用访问地址 http(s)://host[:port][/path]
}

// validAppKind 应用形态合法性校验（web/desktop/mobile/cli/service）。
// 纯函数，供 Create 校验 + 测试断言。
func validAppKind(k string) bool {
	switch k {
	case AppKindWeb, AppKindDesktop, AppKindMobile, AppKindCLI, AppKindService:
		return true
	}
	return false
}

// importBody 导入已有项目请求体（zip 走 ImportUpload multipart 端点）。
type importBody struct {
	Source       string `json:"source" binding:"required,oneof=git dir"` // git=远程仓库 dir=服务器目录
	Name         string `json:"name" binding:"required"`
	InternalPort int    `json:"internal_port"` // 可选，默认 8080
	GitURL       string `json:"git_url"`       // source=git 必填
	AuthToken    string `json:"auth_token"`    // 私有 HTTPS 仓 token（不落库）；SSH 仓留空
	ServerPath   string `json:"server_path"`   // source=dir 必填，须在白名单下
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

// isUniqueViolation 判断是否 PG 唯一约束冲突（错误码 23505）。
// Import/ImportUpload 在 store.Create 撞 UNIQUE(project_space_id, name) 时识别为名字冲突返 409，
// 而非 500——GetByName 预检与 Create 之间存在竞态窗口，唯一约束是兜底。
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}

// validateExternalURL 校验 external 模式的外部应用访问地址。
// 要求：非空 + http/https scheme + host 非空。返回规范化的 URL（去掉末尾斜杠）+ 错误消息（空=合法）。
func validateExternalURL(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "external 模式必须填 external_url（外部应用访问地址）"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", "external_url 非法（需 http(s)://host[:port][/path]）"
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "external_url scheme 必须是 http 或 https"
	}
	// 去掉末尾斜杠避免反代路径双斜杠（/prefix//rest）
	return strings.TrimRight(raw, "/"), ""
}

// Create 注册一个产出应用，并初始化其托管 git 仓库（代码归属确立：/data/repos/<name>）。
// 配额强制：建应用前 CheckApps，超限 409 拦截；建库 Provision 配额失败写到 last_error 不阻塞应用创建。
//
// @Summary      创建应用
// @Tags         appdeploy
// @Accept       json
// @Produce      json
// @Param        id    path  createBody  true  "项目空间ID"
// @Param        body  body  createBody  true  "应用(name+repo_dir+internal_port)"
// @Success      200   {object}  map[string]interface{}  "创建的应用"
// @Failure      400   {object}  map[string]interface{}  "invalid body"
// @Failure      409   {object}  map[string]interface{}  "应用数配额超限"
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
	// app_kind 校验：空默认 web；非法拒绝。
	in.AppKind = strings.TrimSpace(in.AppKind)
	if in.AppKind == "" {
		in.AppKind = AppKindWeb
	}
	if !validAppKind(in.AppKind) {
		httpx.Err(c, 400, 40001, "app_kind 非法（需 web/desktop/mobile/cli/service）")
		return
	}
	// 配额强制：建应用前查应用数（超限直接 409，不建仓库/记录）
	if h.quota != nil {
		if err := h.quota.CheckApps(c.Request.Context(), c.Param("id")); err != nil {
			httpx.Err(c, 409, 40950, err.Error())
			return
		}
	}
	// external 模式（B 类轻接入）：纳管外部已在运行的应用。
	// 不 EnsureRepo / 不 AI 编码 / 不部署 / 不建库；仅注册 + appgw 统一入口 + ops 按 external_url 探活。
	if in.DeployMode == AppExternal {
		extURL, msg := validateExternalURL(in.ExternalURL)
		if msg != "" {
			httpx.Err(c, 400, 40001, msg)
			return
		}
		a := &Application{
			ProjectSpaceID: c.Param("id"), Name: in.Name,
			DeployMode: AppExternal, ExternalURL: extURL, AppKind: in.AppKind,
			Status: "running", // external 应用外部已活，注册即"运行中"
		}
		if err := h.store.Create(c.Request.Context(), a); err != nil {
			httpx.Err(c, 500, 50020, err.Error())
			return
		}
		// 写 appgw 路由：external_url 非空 → /apps/<app_id>/ 反代到 external_url。
		// 失败不阻塞注册（应用已创建；路由失败记到 last_error，前端可见）。
		if h.routeWriter != nil {
			if err := h.routeWriter.UpsertExternalRoute(c.Request.Context(), a.ID, a.ProjectSpaceID, EnvProd, extURL); err != nil {
				_ = h.store.SetStatus(c.Request.Context(), a.ProjectSpaceID, a.ID, a.Status, "appgw 路由写入失败: "+err.Error(), "")
			}
		}
		httpx.Created(c, a)
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
	a := &Application{ProjectSpaceID: c.Param("id"), Name: in.Name, RepoDir: repoDir, InternalPort: port, AppKind: in.AppKind}
	if err := h.store.Create(c.Request.Context(), a); err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	// 供给独立库 + 注入 DATABASE_URL（失败不阻塞应用创建，仅记录；DATABASE_URL 缺失时应用自处理）
	// 配额超限（库数/库大小）→ Provision 在最前拦（不建任何库记录）；
	// 此处把配额错误写到 application.last_error，前端应用列表能显示「库供给失败：配额超限」。
	if h.provisioner != nil {
		if _, perr := h.provisioner.Provision(c.Request.Context(), a.ProjectSpaceID, a.ID); perr != nil {
			_ = h.store.SetStatus(c.Request.Context(), a.ProjectSpaceID, a.ID, a.Status, perr.Error(), "")
		}
	}
	httpx.Created(c, a)
}

// Import 导入已有项目（git 仓库 / 服务器目录）。占位落库后异步执行，前端轮询 status。
// 路由用 /import/apps 避开 /apps/:aid 的 Gin 同层冲突。
//
// @Summary      导入已有项目
// @Tags         appdeploy
// @Accept       json
// @Produce      json
// @Param        id    path  string      true  "项目空间ID"
// @Param        body  body  importBody  true  "导入参数(source/name/git_url|server_path)"
// @Success      200   {object}  map[string]interface{}  "占位应用(importing态)"
// @Failure      400   {object}  map[string]interface{}  "invalid body/来源参数非法"
// @Failure      409   {object}  map[string]interface{}  "同名应用已存在/配额超限"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/import/apps [post]
func (h *Handler) Import(c *gin.Context) {
	var in importBody
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	if msg := validateAppName(in.Name); msg != "" {
		httpx.Err(c, 400, 40001, msg)
		return
	}
	psID := c.Param("id")
	if h.quota != nil {
		if err := h.quota.CheckApps(c.Request.Context(), psID); err != nil {
			httpx.Err(c, 409, 40950, err.Error())
			return
		}
	}
	// 预检名字唯一（避免先 clone 再冲突浪费）
	if ex, _ := h.store.GetByName(c.Request.Context(), psID, in.Name); ex != nil && ex.ID != "" {
		httpx.Err(c, 409, 40950, "应用名已存在: "+in.Name)
		return
	}
	// 校验来源参数 + 定位 import_source/ref
	src, ref, msg := validateImportSource(&in)
	if msg != "" {
		httpx.Err(c, 400, 40001, msg)
		return
	}
	port := in.InternalPort
	if port == 0 {
		port = 8080
	}
	a := &Application{
		ProjectSpaceID: psID, Name: in.Name, RepoDir: ManagedRepoDir(in.Name),
		InternalPort: port, DeployMode: AppManaged,
		ImportSource: src, ImportRef: ref, Status: StatusImporting,
	}
	if err := h.store.Create(c.Request.Context(), a); err != nil {
		// GetByName 预检与 Create 间存在竞态窗口；唯一约束兜底识别为名字冲突 → 409（非 500）
		if isUniqueViolation(err) {
			httpx.Err(c, 409, 40950, "应用名已存在: "+in.Name)
			return
		}
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	// 异步：context.Background 派生 + 超时；token 仅闭包内（不落库）
	go h.runImport(a.ID, psID, in.Name, in.Source, in.GitURL, in.AuthToken, in.ServerPath)
	httpx.Created(c, a)
}

// validateImportSource 校验 git/dir 来源参数，返回 (import_source, import_ref, errMsg)。
func validateImportSource(in *importBody) (src, ref, msg string) {
	switch in.Source {
	case ImportSourceGit:
		// 允许 http(s)://host、git@host:path（生产远程仓）。本地路径须在白名单下——
		// 否则 git_url 接受任意存在目录会绕过 dir 分支的 isUnderAllowedRoot 白名单（../ 穿越）。
		u, err := url.Parse(in.GitURL)
		isRemote := err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https")
		isSSH := strings.HasPrefix(in.GitURL, "git@")
		if isRemote || isSSH {
			return ImportSourceGit, in.GitURL, "" // 远程：放行
		}
		// 本地路径：须在白名单下（防绕过 dir 白名单）
		clean := filepath.Clean(in.GitURL)
		if !isUnderAllowedRoot(clean) {
			return "", "", "本地 git_url 须在允许根目录下（/data/、/opt/legacy/）"
		}
		if _, err := os.Stat(clean); err != nil {
			return "", "", "git_url 不存在: " + in.GitURL
		}
		return ImportSourceGit, in.GitURL, ""
	case ImportSourceDir:
		clean := filepath.Clean(in.ServerPath)
		if !isUnderAllowedRoot(clean) {
			return "", "", "server_path 不在允许根目录下（/data/、/opt/legacy/）"
		}
		if _, err := os.Stat(clean); err != nil {
			return "", "", "server_path 不存在: " + in.ServerPath
		}
		return ImportSourceDir, in.ServerPath, ""
	}
	return "", "", "未知来源: " + in.Source
}

// runImport 异步执行导入。用 context.Background（禁 request context，HTTP 返回即取消）。
// token 在写入 last_error 前脱敏（PRD §8 安全要求）。
func (h *Handler) runImport(appID, psID, name, source, gitURL, authToken, serverPath string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	label := "克隆仓库"
	if source == ImportSourceDir {
		label = "复制目录"
	}
	_ = h.store.SetStatus(ctx, psID, appID, StatusImporting, "正在"+label+"...", "")

	var repoDir string
	var err error
	switch source {
	case ImportSourceGit:
		repoDir, err = ImportFromGit(ctx, name, gitURL, authToken)
	case ImportSourceDir:
		repoDir, err = ImportFromDir(ctx, name, serverPath)
	}
	if err != nil {
		// token 脱敏：git clone 失败信息可能回显 extraHeader 中的 token，写入 DB 前替换掉。
		// 注意：authToken==""（SSH/公开仓）时 ReplaceAll 不是 no-op——Go 语义空 old 在每 rune 间匹配，
		// 结果 "克***隆***失***败..." 乱码，故先判空。
		msg := err.Error()
		if authToken != "" {
			msg = strings.ReplaceAll(msg, authToken, "***")
		}
		_ = os.RemoveAll(ManagedRepoDir(name))
		_ = h.store.SetStatus(ctx, psID, appID, "failed", msg, "")
		return
	}
	if e := h.store.UpdateImportDone(ctx, psID, appID, repoDir); e != nil {
		msg := e.Error()
		if authToken != "" {
			msg = strings.ReplaceAll(msg, authToken, "***")
		}
		_ = os.RemoveAll(ManagedRepoDir(name))
		_ = h.store.SetStatus(ctx, psID, appID, "failed", msg, "")
		return
	}
	// 供给独立库（失败不阻塞导入，仅记 last_error）
	if h.provisioner != nil {
		if _, pe := h.provisioner.Provision(ctx, psID, appID); pe != nil {
			_ = h.store.SetStatus(ctx, psID, appID, "registered", pe.Error(), "")
		}
	}
}

// ImportUpload 本机 zip 上传导入。multipart: file=zip + 表单 name/internal_port。
// 路由 /import/apps/upload。
//
// @Summary      上传 zip 导入应用
// @Tags         appdeploy
// @Accept       multipart/form-data
// @Produce      json
// @Param        id    path   string  true  "项目空间ID"
// @Param        name  formData string  true  "应用名"
// @Param        internal_port formData int  false "内部端口(默认 8080)"
// @Param        file  formData file  true  "zip 压缩包"
// @Success      200   {object}  map[string]interface{}  "占位应用(importing态)"
// @Failure      400   {object}  map[string]interface{}  "缺 file / 超大小 / 名非法"
// @Failure      409   {object}  map[string]interface{}  "同名应用已存在/配额超限"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/import/apps/upload [post]
func (h *Handler) ImportUpload(c *gin.Context) {
	name := strings.TrimSpace(c.PostForm("name"))
	if msg := validateAppName(name); msg != "" {
		httpx.Err(c, 400, 40001, msg)
		return
	}
	psID := c.Param("id")
	if h.quota != nil {
		if err := h.quota.CheckApps(c.Request.Context(), psID); err != nil {
			httpx.Err(c, 409, 40950, err.Error())
			return
		}
	}
	if ex, _ := h.store.GetByName(c.Request.Context(), psID, name); ex != nil && ex.ID != "" {
		httpx.Err(c, 409, 40950, "应用名已存在: "+name)
		return
	}
	fh, err := c.FormFile("file")
	if err != nil {
		httpx.Err(c, 400, 40001, "未收到 zip 文件（字段名 file）")
		return
	}
	if fh.Size > MaxZipSize {
		httpx.Err(c, 400, 40001, fmt.Sprintf("zip 超过 %d 限制", MaxZipSize))
		return
	}
	port := 8080
	if v := strings.TrimSpace(c.PostForm("internal_port")); v != "" {
		if n, e := strconv.Atoi(v); e == nil && n > 0 {
			port = n
		}
	}
	a := &Application{
		ProjectSpaceID: psID, Name: name, RepoDir: ManagedRepoDir(name),
		InternalPort: port, DeployMode: AppManaged,
		ImportSource: ImportSourceDir, ImportRef: fh.Filename, Status: StatusImporting,
	}
	if err := h.store.Create(c.Request.Context(), a); err != nil {
		// GetByName 预检与 Create 间存在竞态窗口；唯一约束兜底识别为名字冲突 → 409（非 500）
		if isUniqueViolation(err) {
			httpx.Err(c, 409, 40950, "应用名已存在: "+name)
			return
		}
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	// 读取上传内容到内存（已过 MaxZipSize 校验）供异步解压
	// defer src.Close 紧跟 Open，防 panic 泄漏（应修1）
	src, err := fh.Open()
	if err != nil {
		// C2：占位已落 importing，读上传失败须回滚到 failed，否则永久卡 importing（违 PRD §9 不留僵尸）
		_ = h.store.SetStatus(c.Request.Context(), psID, a.ID, "failed", "读取上传内容失败: "+err.Error(), "")
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	defer src.Close()
	data, err := io.ReadAll(src)
	if err != nil {
		// C2：占位已落 importing，读上传失败须回滚到 failed（同步分支，用 request ctx）
		_ = h.store.SetStatus(c.Request.Context(), psID, a.ID, "failed", "读取上传内容失败: "+err.Error(), "")
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	appID := a.ID
	go h.runImportZip(appID, psID, name, data, fh.Size)
	httpx.Created(c, a)
}

// runImportZip 异步解压 zip 导入（zip 归 import_source=dir 语义）。context.Background 派生。
func (h *Handler) runImportZip(appID, psID, name string, data []byte, size int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	_ = h.store.SetStatus(ctx, psID, appID, StatusImporting, "正在解压 zip...", "")
	repoDir, err := ImportFromZip(ctx, name, bytes.NewReader(data), size)
	if err != nil {
		_ = os.RemoveAll(ManagedRepoDir(name))
		_ = h.store.SetStatus(ctx, psID, appID, "failed", err.Error(), "")
		return
	}
	if e := h.store.UpdateImportDone(ctx, psID, appID, repoDir); e != nil {
		_ = os.RemoveAll(ManagedRepoDir(name))
		_ = h.store.SetStatus(ctx, psID, appID, "failed", e.Error(), "")
		return
	}
	if h.provisioner != nil {
		if _, pe := h.provisioner.Provision(ctx, psID, appID); pe != nil {
			_ = h.store.SetStatus(ctx, psID, appID, "registered", pe.Error(), "")
		}
	}
}

// deployBody 部署请求体（均可选）。
type deployBody struct {
	Env           string `json:"env"`            // test / prod；空默认 test
	SHA           string `json:"sha"`            // 可选：部署指定历史版本（回滚）
	NodeID        string `json:"node_id"`        // 可选：部署到指定节点（空=本地 .28，如 node_30）
	FromWorkspace bool   `json:"from_workspace"` // 编码工作台发起：test 从 dev-<user> worktree 构建 + 未提交检测
	AutoCommit    bool   `json:"auto_commit"`    // FromWorkspace 检测到未提交时，前端确认后传 true：先 AI 总结 + commit 再构建
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
	a, _ := h.store.Get(c.Request.Context(), psID, aid)
	if a == nil || a.ID == "" {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	var in deployBody
	_ = c.ShouldBindJSON(&in)
	env := in.Env
	if !IsValidEnv(env) {
		env = EnvTest
	}
	// 部署权限分离（spec 2026-07-26）：按 env 鉴权
	if !auth.Allowed("app.deploy."+env, rolesFromCtx(c)) {
		httpx.Err(c, 403, 40301, "无权限部署到 "+env+" 环境")
		return
	}
	// prod 额外要求变更已审批（变更闸门，与 Promote 一致，防 /deploy env=prod 绕过 /promote）
	if env == EnvProd && h.changes != nil {
		if hasAny, _ := h.changes.HasAny(c.Request.Context(), aid); hasAny {
			if ok, _ := h.changes.HasApproved(c.Request.Context(), aid); !ok {
				httpx.Err(c, 409, 40920, "需先登记变更并审批通过才能上线 prod（变更闸门）")
				return
			}
			// 🚪 AC7 delivered 前置（对称 Promote，防 /deploy env=prod 绕过 /promote）：
			// approved 变更关联的需求须已 delivered（即已走 release/merge 发布）。
			// 查不到需求时放行（grandfather，对称 release 回写）。
			if h.reqRepo != nil {
				if undelivered, _ := h.reqRepo.HasUnDeliveredApprovedByApp(c.Request.Context(), aid); undelivered {
					httpx.Err(c, 409, 40921, "来源需求未交付，请先在发布中心发布上线后再部署 prod")
					return
				}
			}
			_ = h.changes.MarkReleased(c.Request.Context(), aid)
		}
	}
	// 编码工作台 test 部署：从开发者 dev-<user> worktree 构建（自测用最新代码，不走 main 合并）；
	// 有未提交改动时先回 need_commit 让前端确认，确认后 AI 总结 diff + commit 再构建。
	buildDir := ""
	if env == EnvTest && in.FromWorkspace {
		user := c.GetString(auth.CtxUserID)
		if user == "" {
			user = "anonymous"
		}
		wt := filepath.Join(a.RepoDir, ".worktrees", sanitizeID(user))
		if _, err := os.Stat(wt); err == nil {
			if n, _ := CountUncommitted(c.Request.Context(), wt); n > 0 {
				if !in.AutoCommit {
					httpx.OK(c, gin.H{"id": aid, "env": env, "status": "need_commit", "uncommitted": n, "note": fmt.Sprintf("dev-%s 分支有 %d 个文件未提交，是否提交并部署？", sanitizeID(user), n)})
					return
				}
				apiKey := ""
				if h.cfg != nil {
					apiKey = h.cfg.Get("zhipuai_api_key", "")
				}
				if _, err := Commit(c.Request.Context(), wt, commitMessageFor(c.Request.Context(), wt, apiKey)); err != nil {
					httpx.Err(c, 500, 50022, "自动提交失败: "+err.Error())
					return
				}
			}
			buildDir = wt
		}
	}
	h.markBuilding(c.Request.Context(), psID, aid, env) // 同步标 building，前端立即看到进度条
	go h.buildAndDeploy(psID, aid, "", env, in.NodeID, buildDir)
	noteSuffix := ""
	if in.NodeID != "" && in.NodeID != "node_local" {
		noteSuffix = "（节点 " + in.NodeID + "）"
	}
	httpx.OK(c, gin.H{"id": aid, "env": env, "status": "building", "note": "异步构建部署到 " + env + " 环境" + noteSuffix})
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
	// 部署权限分离：上线 prod 需 gatekeeper/admin
	if !auth.Allowed("app.deploy.prod", rolesFromCtx(c)) {
		httpx.Err(c, 403, 40301, "无权限上线 prod（仅 gatekeeper/admin）")
		return
	}
	var in struct {
		NodeID string `json:"node_id"`
	}
	_ = c.ShouldBindJSON(&in)
	// 🚪 变更闸门（grandfather）：登记过变更的应用，必须有 approved 变更才能上线 prod。
	// 从未登记过的老应用不受约束——一旦开始登记变更，即进入治理流程。
	if h.changes != nil {
		if hasAny, _ := h.changes.HasAny(c.Request.Context(), aid); hasAny {
			if ok, _ := h.changes.HasApproved(c.Request.Context(), aid); !ok {
				httpx.Err(c, 409, 40920, "需先登记变更并审批通过才能上线 prod（变更闸门）")
				return
			}
			// 🚪 AC7 delivered 前置（PRD 2026-07-26 主线闭环收敛 AC7）：
			// approved 变更关联的需求须已 delivered（即已走 release/merge 发布），
			// 堵「变更一审批就 promote、跳过发布」的绕过。查不到需求时放行（grandfather，对称 release 回写）。
			if h.reqRepo != nil {
				if undelivered, _ := h.reqRepo.HasUnDeliveredApprovedByApp(c.Request.Context(), aid); undelivered {
					httpx.Err(c, 409, 40921, "来源需求未交付，请先在发布中心发布上线后再 promote")
					return
				}
			}
			// 上线后:把该应用的 approved 变更标记为 released（从待上线列表消失）
			_ = h.changes.MarkReleased(c.Request.Context(), aid) // 上线后标记 released;失败不阻塞(下次上线再标)
		}
	}
	h.markBuilding(c.Request.Context(), psID, aid, EnvProd) // 同步标 building，前端立即看到进度条
	go h.buildAndDeploy(psID, aid, "", EnvProd, in.NodeID, "")
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
	// 部署权限分离：按 env 鉴权
	if !auth.Allowed("app.deploy-commit."+env, rolesFromCtx(c)) {
		httpx.Err(c, 403, 40301, "无权限部署到 "+env+" 环境")
		return
	}
	if _, err := h.store.Get(c.Request.Context(), psID, aid); err != nil {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	h.markBuilding(c.Request.Context(), psID, aid, env) // 同步标 building，前端立即看到进度条
	go h.buildAndDeploy(psID, aid, in.SHA, env, in.NodeID, "")
	httpx.OK(c, gin.H{"id": aid, "sha": in.SHA, "env": env, "status": "building", "note": "版本化部署/回滚到 " + env})
}

// buildAndDeploy 后台执行（脱离 HTTP context）。sha 非空则部署该历史版本；env 指定环境。
// buildDir 非空则从该目录构建（test 环境编码工作台传 dev-<user> worktree），空则用主仓 a.RepoDir。
func (h *Handler) buildAndDeploy(psID, aid, sha, env, nodeID, buildDir string) {
	ctx := context.Background()
	// panic 兜底：goroutine 内任何 panic 都回写 failed，避免状态永卡 building、build_log 为空
	// （参照 dev/coding.go:92-99）。历史根因之一：docker build 卡死/无超时 + 无 recover → 一次异常永久挂起。
	defer func() {
		if r := recover(); r != nil {
			zap.L().Error("[appdeploy] buildAndDeploy panic", zap.String("app", aid), zap.Any("panic", r))
			msg := fmt.Sprintf("[panic] 构建部署异常崩溃: %v", r)
			h.markFailed(ctx, psID, aid, msg, "")
			if ins, e := h.store.GetOrCreateInstance(ctx, aid, env); e == nil && ins != nil {
				ins.Status = "failed"
				ins.LastError = msg
				_ = h.store.UpdateInstance(ctx, ins)
			}
		}
	}()
	a, err := h.store.Get(ctx, psID, aid)
	if err != nil || a == nil || a.ID == "" {
		// 读取失败也回写 failed，否则状态无声卡 building（前端永远转圈）。
		reason := "[系统]应用记录不存在"
		if err != nil {
			reason = "[系统]应用记录读取失败: " + err.Error()
		}
		h.markFailed(ctx, psID, aid, reason, "")
		return
	}
	// 解析部署节点的 docker host（空=本地）
	dockerHost := ""
	if nodeID != "" && nodeID != "node_local" && h.nodeStore != nil {
		if node, e := h.nodeStore.Get(ctx, nodeID); e == nil && node != nil && node.DockerURL != "" {
			dockerHost = node.DockerURL
		}
	}
	ins, err := h.store.GetOrCreateInstance(ctx, a.ID, env)
	if err != nil || ins == nil {
		reason := "[系统]部署实例创建失败"
		if err != nil {
			reason = "[系统]部署实例创建失败: " + err.Error()
		}
		h.markFailed(ctx, psID, a.ID, reason, "")
		return
	}
	// 清理该 app+env 所有历史容器（DB 记录的 + 孤儿残留），彻底释放端口避免漂移/Conflict
	if _, err := h.deployer.RemoveByPrefix(ctx, "appdeploy-"+a.Name+"-"+env+"-"); err != nil {
		zap.L().Warn("清理历史容器失败（不阻塞部署）",
			zap.String("app", a.Name), zap.String("env", env), zap.Error(err))
	}
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
	if buildDir == "" {
		buildDir = a.RepoDir // 兜底：非工作台入口（application 页 / 版本回滚）走主仓
	}
	if gen, port, err := EnsureDockerfile(buildDir, a.InternalPort); err == nil {
		if port != 0 && port != a.InternalPort {
			a.InternalPort = port
		}
		if gen {
			note += "buildpack 已按源码类型自动生成 Dockerfile\n"
		}
	}
	// Build（含 pull 基础镜像）限 15 分钟：.28 半内网 mirror 慢/失效时 docker build 可能长时间阻塞；
	// 无超时则 cmd.Run 永不返回、goroutine 挂起、状态永卡 building。超时 ctx cancel → 杀子进程 → 走 failed。
	buildCtx, buildCancel := context.WithTimeout(ctx, 15*time.Minute)
	log, err := h.deployer.Build(buildCtx, a, ins, dockerHost, buildDir)
	buildCancel()
	if note != "" {
		log = note + log
	}
	if err != nil {
		ins.Status = "failed"
		ins.LastError = err.Error()
		ins.BuildLog = tail(log, 2000)
		_ = h.store.UpdateInstance(ctx, ins)
		h.markFailed(ctx, psID, a.ID, ins.LastError, ins.BuildLog) // 不限环境，前端立即看到失败 + 原因
		return
	}
	ins.Status = "building" // build 完成，进入 run 阶段
	ins.BuildLog = tail(log, 2000)
	_ = h.store.UpdateInstance(ctx, ins)
	// a.Status=building 已由 Deploy handler 同步标记（markBuilding），此处无需重写
	envPairs, _ := h.store.EnvPairs(ctx, a.ID) // 应用运行时环境变量（含密钥）注入容器
	// docker run 限 3 分钟：镜像已构建，run 卡住通常是端口/挂载问题，无需长等；超时同走 failed。
	deployCtx, deployCancel := context.WithTimeout(ctx, 3*time.Minute)
	dErr := h.deployer.Deploy(deployCtx, a, ins, envPairs, dockerHost)
	deployCancel()
	if dErr != nil {
		ins.Status = "failed"
		ins.LastError = dErr.Error()
		_ = h.store.UpdateInstance(ctx, ins)
		h.markFailed(ctx, psID, a.ID, ins.LastError, ins.BuildLog) // 不限环境，前端立即看到失败 + 原因
		return
	}
	ins.Status = "running"
	ins.LastError = ""
	ins.BuildLog = tail(log, 2000)
	_ = h.store.UpdateInstance(ctx, ins)
	// 写 appgw 路由表：部署成功即时把 /apps/<app_id>/ 映射到本环境容器。
	// 失败不阻塞部署（应用仍可裸端口访问）；routeWriter nil = 未启用 appgw。
	if h.routeWriter != nil {
		if err := h.routeWriter.UpsertRoute(ctx, a.ID, a.ProjectSpaceID, env, h.deployer.host, ins.HostPort); err != nil {
			// 路由表写入失败仅记录到 instance.LastError，不影响部署成功态；另记 WARN 便于排查
			ins.LastError = "部署成功但 appgw 路由表写入失败: " + err.Error()
			_ = h.store.UpdateInstance(ctx, ins)
			zap.L().Warn("appgw 路由表写入失败（部署仍成功）",
				zap.String("app_id", a.ID), zap.String("env", env), zap.Error(err))
		}
	}
	h.syncOverviewIfProd(ctx, a, env) // prod 同步部署态字段 + status=running
	if env != EnvProd {
		_ = h.store.UpdateAppStatus(ctx, a.ID, "running") // test 成功也回 running，避免卡 building
	}
}

// syncOverviewAll 所有环境都同步实例态到 application 概览。
func (h *Handler) syncOverviewAll(ctx context.Context, a *Application, env string) {
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

// markBuilding 部署开始前同步标记 building：application + 当前环境实例都置 building，
// 并清空上次的 last_error。在 Deploy/Promote/DeployCommit 的同步段调用，确保 HTTP 返回前
// DB 已是 building——前端下一次 3s 轮询立即看到进度条。原实现 a.Status="building" 在
// docker build 之后才写（handler.go:1046），构建期间（可能数分钟）状态滞后，进度条不出现。
// 清空 last_error 避免上次失败的红条残留误导。
func (h *Handler) markBuilding(ctx context.Context, psID, aid, env string) {
	_ = h.store.SetStatus(ctx, psID, aid, "building", "", "")
	if _, err := h.store.GetOrCreateInstance(ctx, aid, env); err == nil {
		_ = h.store.SetInstanceStatus(ctx, aid, env, "building", "", "")
	}
}

// markFailed 部署失败时把 application.status 写成 failed 并记录原因 + 构建日志，
// 对 test/prod 所有环境都写。修复前仅 prod 经 syncOverviewIfProd 同步失败态，而它对 test
// early return → test 失败时 a.status 不变，前端 a.status==="failed" 红条永不触发
// （用户"只看到 failed、无原因"）。instance 的失败态由调用方 buildAndDeploy 单独 UpdateInstance 写入。
func (h *Handler) markFailed(ctx context.Context, psID, aid, lastErr, buildLog string) {
	_ = h.store.SetStatus(ctx, psID, aid, "failed", lastErr, buildLog)
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
	var in struct {
		Env string `json:"env"`
	}
	_ = c.ShouldBindJSON(&in)
	env := in.Env
	if !IsValidEnv(env) {
		env = EnvProd // 默认 prod（向后兼容现有调用）
	}
	// 部署权限分离：按 env 鉴权
	if !auth.Allowed("app.stop."+env, rolesFromCtx(c)) {
		httpx.Err(c, 403, 40301, "无权限停止 "+env+" 实例")
		return
	}
	a, _ := h.store.Get(c.Request.Context(), c.Param("id"), c.Param("aid"))
	ins, _ := h.store.GetInstance(c.Request.Context(), c.Param("aid"), env)
	if a == nil || ins == nil || ins.ContainerName == "" {
		httpx.Err(c, 400, 50020, "应用未在 "+env+" 部署")
		return
	}
	if _, err := h.deployer.Stop(c.Request.Context(), ins.ContainerName); err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	_ = h.store.SetInstanceStatus(c.Request.Context(), a.ID, env, "stopped", "", "")
	if env == EnvProd {
		_ = h.store.SetStatus(c.Request.Context(), a.ProjectSpaceID, a.ID, "stopped", "", "")
	}
	httpx.OK(c, gin.H{"id": a.ID, "env": env, "status": "stopped"})
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
	var in struct {
		Env string `json:"env"`
	}
	_ = c.ShouldBindJSON(&in)
	env := in.Env
	if !IsValidEnv(env) {
		env = EnvProd // 默认 prod（向后兼容现有调用）
	}
	// 部署权限分离：按 env 鉴权
	if !auth.Allowed("app.start."+env, rolesFromCtx(c)) {
		httpx.Err(c, 403, 40301, "无权限启动 "+env+" 实例")
		return
	}
	a, _ := h.store.Get(c.Request.Context(), c.Param("id"), c.Param("aid"))
	ins, _ := h.store.GetInstance(c.Request.Context(), c.Param("aid"), env)
	if a == nil || ins == nil || ins.ContainerName == "" {
		httpx.Err(c, 400, 50020, "应用未在 "+env+" 部署")
		return
	}
	if _, err := h.deployer.Start(c.Request.Context(), ins.ContainerName); err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	_ = h.store.SetInstanceStatus(c.Request.Context(), a.ID, env, "running", "", "")
	if env == EnvProd {
		_ = h.store.SetStatus(c.Request.Context(), a.ProjectSpaceID, a.ID, "running", "", "")
	}
	httpx.OK(c, gin.H{"id": a.ID, "env": env, "status": "running"})
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
	// 部署权限分离：删除应用仅管理员
	if !auth.Allowed("app.delete", rolesFromCtx(c)) {
		httpx.Err(c, 403, 40301, "无权限删除应用（仅管理员）")
		return
	}
	a, _ := h.store.Get(c.Request.Context(), c.Param("id"), c.Param("aid"))
	if a != nil {
		// 先删应用库（DropDatabase/Role；库记录在 Store.Delete 级联删 appdeploy_database 前先清）
		if h.provisioner != nil {
			_ = h.provisioner.Cleanup(c.Request.Context(), a.ID)
		}
		// 显式清 appgw 路由（不依赖 FK CASCADE；routeWriter nil = 未启用 appgw）
		if h.routeWriter != nil {
			_ = h.routeWriter.DeleteRouteByApp(c.Request.Context(), a.ID)
		}
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
	// external 应用（B 类轻接入）：无容器，按 external_url 直接 HTTP GET 探活。
	// 不查实例、不调 docker；返回 external 标识供前端区分展示。
	if a.DeployMode == AppExternal {
		httpx.OK(c, gin.H{
			"env": env, "deployed": true, "external": true,
			"url": a.ExternalURL, "health": probeHealth(a.ExternalURL),
		})
		return
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
	h.markBuilding(ctx, a.ProjectSpaceID, appID, EnvTest) // 同步标 building，前端立即看到进度条
	go h.buildAndDeploy(a.ProjectSpaceID, appID, "", EnvTest, "", "")
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

// --- 部署节点管理 ---

func (h *Handler) ListNodes(c *gin.Context) {
	if h.nodeStore == nil {
		c.JSON(200, gin.H{"code": 0, "data": []interface{}{}})
		return
	}
	list, err := h.nodeStore.List(c.Request.Context())
	if err != nil {
		httpx.Err(c, 500, 50014, err.Error())
		return
	}
	// 附带每节点应用数
	type nodeWithCount struct {
		DeployNode
		AppCount int `json:"app_count"`
	}
	out := []nodeWithCount{}
	for _, n := range list {
		cnt, _ := h.nodeStore.AppCount(c.Request.Context(), n.ID)
		out = append(out, nodeWithCount{DeployNode: n, AppCount: cnt})
	}
	httpx.OK(c, out)
}

func (h *Handler) CreateNode(c *gin.Context) {
	if h.nodeStore == nil {
		httpx.Err(c, 500, 50014, "nodeStore 未初始化")
		return
	}
	var n DeployNode
	if err := c.ShouldBindJSON(&n); err != nil {
		httpx.Err(c, 400, 40001, err.Error())
		return
	}
	if err := h.nodeStore.Create(c.Request.Context(), &n); err != nil {
		httpx.Err(c, 500, 50014, err.Error())
		return
	}
	httpx.Created(c, n)
}

func (h *Handler) DeleteNode(c *gin.Context) {
	if h.nodeStore == nil {
		return
	}
	if err := h.nodeStore.Delete(c.Request.Context(), c.Param("nid")); err != nil {
		httpx.Err(c, 500, 50014, err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": c.Param("nid"), "deleted": true})
}

func (h *Handler) TestNode(c *gin.Context) {
	if h.nodeStore == nil {
		return
	}
	n, err := h.nodeStore.Get(c.Request.Context(), c.Param("nid"))
	if err != nil {
		httpx.Err(c, 404, 40401, "节点不存在")
		return
	}
	dockerURL := n.DockerURL
	if dockerURL == "" {
		dockerURL = "unix:///var/run/docker.sock" // 本地
	}
	if err := h.nodeStore.TestDocker(c.Request.Context(), dockerURL); err != nil {
		httpx.OK(c, gin.H{"status": "fail", "detail": err.Error()})
		return
	}
	httpx.OK(c, gin.H{"status": "ok", "detail": "Docker 连通"})
}

// worktreeDir 解析当前开发者 dev-<user> worktree 路径（与 Deploy 一致，handler.go:1277）。
func (h *Handler) worktreeDir(c *gin.Context, a *Application) string {
	user := c.GetString(auth.CtxUserID)
	if user == "" {
		user = "anonymous"
	}
	return filepath.Join(a.RepoDir, ".worktrees", sanitizeID(user))
}

// GitStatus 编码工作台 git 变更：工作区改动文件 + 提交历史（均查 dev-<user> worktree）。
// worktree 不存在（未认领需求）→ worktree_exists=false 空列表不报错。
//
// @Summary      工作台 git 变更
// @Tags         appdeploy
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Param        aid  path  string  true  "应用ID"
// @Success      200  {object}  map[string]interface{}  "{worktree_exists,branch,changes,commits}"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/git-status [get]
func (h *Handler) GitStatus(c *gin.Context) {
	a, err := h.store.Get(c.Request.Context(), c.Param("id"), c.Param("aid"))
	if err != nil || a == nil || a.ID == "" {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	wt := h.worktreeDir(c, a)
	if _, err := os.Stat(wt); err != nil {
		httpx.OK(c, gin.H{"worktree_exists": false, "branch": "", "changes": []FileChange{}, "commits": []CommitInfo{}})
		return
	}
	changes, _ := StatusFiles(c.Request.Context(), wt)
	commits, _ := Log(c.Request.Context(), wt, 20)
	httpx.OK(c, gin.H{
		"worktree_exists": true,
		"branch":          "dev-" + sanitizeID(c.GetString(auth.CtxUserID)),
		"changes":         changes,
		"commits":         commits,
	})
}

// FileDiff 单文件行级 diff（unified）。path 必填；sha 可选（空=工作区 vs HEAD，给=该提交对该文件 diff）。
// 输出截断前 2000 行 + truncated 标注。
//
// @Summary      工作台单文件 diff
// @Tags         appdeploy
// @Produce      json
// @Param        id    path   string  true  "项目空间ID"
// @Param        aid   path   string  true  "应用ID"
// @Param        path  query  string  true  "文件路径"
// @Param        sha   query  string  false  "提交 sha（空=工作区 diff）"
// @Success      200   {object}  map[string]interface{}  "{path,sha,diff,truncated}"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/file-diff [get]
func (h *Handler) FileDiff(c *gin.Context) {
	a, err := h.store.Get(c.Request.Context(), c.Param("id"), c.Param("aid"))
	if err != nil || a == nil || a.ID == "" {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	path := c.Query("path")
	if path == "" {
		httpx.Err(c, 400, 40001, "path 必填")
		return
	}
	wt := h.worktreeDir(c, a)
	sha := c.Query("sha")
	diff, err := FileDiff(c.Request.Context(), wt, path, sha)
	if err != nil {
		httpx.Err(c, 400, 40001, err.Error())
		return
	}
	truncated := false
	lines := strings.Split(diff, "\n")
	if len(lines) > 2000 {
		diff = strings.Join(lines[:2000], "\n")
		truncated = true
	}
	httpx.OK(c, gin.H{"path": path, "sha": sha, "diff": diff, "truncated": truncated})
}

// CommitFilesList 某次提交改动的文件列表（供提交历史点 SHA 展开）。
//
// @Summary      某次提交改动文件列表
// @Tags         appdeploy
// @Produce      json
// @Param        id   path   string  true  "项目空间ID"
// @Param        aid  path   string  true  "应用ID"
// @Param        sha  query  string  true  "提交 sha"
// @Success      200  {object}  map[string]interface{}  "{sha,files}"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/commit-files [get]
func (h *Handler) CommitFilesList(c *gin.Context) {
	a, err := h.store.Get(c.Request.Context(), c.Param("id"), c.Param("aid"))
	if err != nil || a == nil || a.ID == "" {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	sha := c.Query("sha")
	if sha == "" {
		httpx.Err(c, 400, 40001, "sha 必填")
		return
	}
	wt := h.worktreeDir(c, a)
	files, err := CommitFiles(c.Request.Context(), wt, sha)
	if err != nil {
		httpx.Err(c, 400, 40001, err.Error())
		return
	}
	httpx.OK(c, gin.H{"sha": sha, "files": files})
}

// CommitWorktree 仅提交 dev-<user> worktree（不部署）。message 空走 AI 总结兜底。
// 与 Deploy 的 auto_commit 不同：此处不触发构建，部署仍走顶部工具栏。
//
// @Summary      提交工作台改动
// @Tags         appdeploy
// @Accept       json
// @Produce      json
// @Param        id     path    string  true  "项目空间ID"
// @Param        aid    path    string  true  "应用ID"
// @Param        body   body    object  false  "{message?}"
// @Success      200    {object}  map[string]interface{}  "{sha,message}"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/commit [post]
func (h *Handler) CommitWorktree(c *gin.Context) {
	a, err := h.store.Get(c.Request.Context(), c.Param("id"), c.Param("aid"))
	if err != nil || a == nil || a.ID == "" {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	wt := h.worktreeDir(c, a)
	if _, err := os.Stat(wt); err != nil {
		httpx.Err(c, 400, 40032, "工作分支不存在,请先认领需求/打开工作台生成 dev-"+sanitizeID(c.GetString(auth.CtxUserID))+" 分支")
		return
	}
	var in struct {
		Message string `json:"message"`
	}
	_ = c.ShouldBindJSON(&in)
	msg := in.Message
	if strings.TrimSpace(msg) == "" {
		apiKey := ""
		if h.cfg != nil {
			apiKey = h.cfg.Get("zhipuai_api_key", "")
		}
		msg = commitMessageFor(c.Request.Context(), wt, apiKey)
	}
	if _, err := Commit(c.Request.Context(), wt, msg); err != nil {
		httpx.Err(c, 500, 50022, "提交失败: "+err.Error())
		return
	}
	latest, _ := Log(c.Request.Context(), wt, 1)
	sha := ""
	if len(latest) > 0 {
		sha = latest[0].SHA
	}
	httpx.OK(c, gin.H{"sha": sha, "message": msg})
}

// --- 非 web 应用构建产物链路（Task 10）---

// BuildArtifacts 触发非 web 应用构建产物（desktop/mobile/cli）。
// web/service 应用走容器部署链路，不构建产物文件。同步执行：标记 building → DispatchBuilder →
// Build → UploadArtifacts → 标记 built（部分失败也标 built，错误入 last_error 不阻塞已成功产物）。
//
// @Summary      构建应用产物
// @Tags         appdeploy
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Param        aid  path  string  true  "应用ID"
// @Success      200  {object}  map[string]interface{}  "构建结果(built 数量 + 版本号)"
// @Failure      400  {object}  map[string]interface{}  "web/service 应用不构建产物"
// @Failure      404  {object}  map[string]interface{}  "应用不存在"
// @Failure      500  {object}  map[string]interface{}  "功能未配置/构建失败"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/build-artifacts [post]
func (h *Handler) BuildArtifacts(c *gin.Context) {
	psID, aid := c.Param("id"), c.Param("aid")
	a, err := h.store.Get(c.Request.Context(), psID, aid)
	if err != nil || a == nil || a.ID == "" {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	// web/service 走容器部署链路（Deploy handler），不构建产物文件
	if a.AppKind == AppKindWeb || a.AppKind == AppKindService {
		httpx.Err(c, 400, 40001, "web/service 应用走部署流程，不构建产物")
		return
	}
	// nil-safe：Task 13 前未装配构建产物链路时拒绝（而非 nil 解引用 panic）
	if h.buildCfgStore == nil || h.artifactStorage == nil || h.artifactStore == nil {
		httpx.Err(c, 500, 50021, "构建产物功能未配置（buildCfgStore/artifactStorage/artifactStore 未注入）")
		return
	}
	ctx := c.Request.Context()
	// 标记 building（清空上次 last_error）
	_ = h.store.SetStatus(ctx, psID, aid, "building", "", "")
	b, err := DispatchBuilder(a.AppKind, h.deployer, h.buildCfgStore)
	if err != nil {
		h.markFailed(ctx, psID, aid, err.Error(), "")
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	outs, err := b.Build(ctx, a)
	if err != nil {
		h.markFailed(ctx, psID, aid, "构建失败: "+err.Error(), "")
		httpx.Err(c, 500, 50020, "构建失败: "+err.Error())
		return
	}
	a.Version++
	// 上传产物：单产物失败不阻塞其他；返回首个错误（已成功的仍落库）
	upErr := UploadArtifacts(ctx, a, outs, h.artifactStorage, h.artifactStore)
	if upErr != nil {
		// 部分成功也标 built，错误入 last_error（前端可见，不阻塞已落库产物）
		_ = h.store.SetStatus(ctx, psID, aid, StatusBuilt, upErr.Error(), "")
	} else {
		_ = h.store.SetStatus(ctx, psID, aid, StatusBuilt, "", "")
	}
	httpx.OK(c, gin.H{"id": aid, "built": len(outs), "version": a.Version, "status": StatusBuilt})
}

// ListArtifacts 列出应用全部产物（按 build_version 倒序）。
//
// @Summary      应用产物列表
// @Tags         appdeploy
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Param        aid  path  string  true  "应用ID"
// @Success      200  {object}  map[string]interface{}  "产物列表"
// @Failure      500  {object}  map[string]interface{}  "功能未配置/加载失败"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/artifacts [get]
func (h *Handler) ListArtifacts(c *gin.Context) {
	aid := c.Param("aid")
	// nil-safe：未装配产物链路时返回"功能未配置"而非 panic
	if h.artifactStore == nil {
		httpx.Err(c, 500, 50021, "产物功能未配置（artifactStore 未注入）")
		return
	}
	list, err := h.artifactStore.ListByApp(c.Request.Context(), aid)
	if err != nil {
		httpx.Err(c, 500, 50020, "加载产物失败: "+err.Error())
		return
	}
	httpx.OK(c, gin.H{"artifacts": list})
}

// DownloadArtifact 下载产物：MinIO 预签名 URL 存在时 302 跳转；
// 本地降级（PresignedGet 返回空）直接流式返回文件内容。
//
// @Summary      下载应用产物
// @Tags         appdeploy
// @Produce      octet-stream
// @Param        id     path  string  true  "项目空间ID"
// @Param        aid    path  string  true  "应用ID"
// @Param        artid  path  string  true  "产物ID"
// @Success      200    {file}  binary  "产物文件"
// @Success      302    {string} string  "跳转到预签名下载 URL"
// @Failure      404    {object}  map[string]interface{}  "产物不存在"
// @Failure      500    {object}  map[string]interface{}  "功能未配置/产物文件缺失"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/artifacts/{artid}/download [get]
func (h *Handler) DownloadArtifact(c *gin.Context) {
	artid := c.Param("artid")
	// nil-safe：未装配产物链路时拒绝
	if h.artifactStore == nil || h.artifactStorage == nil {
		httpx.Err(c, 500, 50021, "产物功能未配置（artifactStore/artifactStorage 未注入）")
		return
	}
	a, err := h.artifactStore.Get(c.Request.Context(), artid)
	if err != nil || a == nil {
		httpx.Err(c, 404, 40420, "产物不存在")
		return
	}
	ctx := c.Request.Context()
	// MinIO 路径：预签名 URL 非空 → 302 跳转
	if u, _ := h.artifactStorage.PresignedGet(ctx, a.StorageKey); u != "" {
		c.Redirect(302, u)
		return
	}
	// 本地降级：流式返回
	rc, err := h.artifactStorage.Open(ctx, a.StorageKey)
	if err != nil {
		httpx.Err(c, 500, 50020, "产物文件缺失: "+err.Error())
		return
	}
	defer rc.Close()
	c.Header("Content-Type", a.ContentType)
	c.Header("Content-Disposition", "attachment; filename="+a.Filename)
	if _, err := io.Copy(c.Writer, rc); err != nil {
		// 已开始写 body，无法改状态码；仅日志
		zap.L().Warn("流式下载产物失败", zap.String("artifact", artid), zap.Error(err))
	}
}
