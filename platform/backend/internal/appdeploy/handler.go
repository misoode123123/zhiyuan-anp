package appdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
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
	monitor     *ServerMonitor          // 服务器指标采集（Task 8 加；nil=未启用，Task 9 注入）
	metricStore *MetricStore            // 服务器指标持久化（Task 8 加；nil=未启用，Task 9 注入）
	checkFn     checkFunc               // 可 mock 的核对函数(默认 checkRequirement);测试可注入
	// 非 web 形态构建产物链路（Task 10）；nil=未装配（BuildArtifacts/ListArtifacts/DownloadArtifact 报"功能未配置"）。
	// Task 13 在 main.go 注入真实值；在此之前 factory 调用处传 nil 保证编译。
	buildCfgStore   *BuildConfigStore // 构建配置（desktop/mobile/cli 按形态查镜像/命令）
	artifactStore   *ArtifactStore    // 产物记录读写（appdeploy_artifact）
	artifactStorage ArtifactStorage   // 产物实体存储（本地降级 / MinIO）
	scaffoldsBase   string            // 脚手架种子根目录（建非 web 应用时克隆到 RepoDir；空=不克隆）
	adaptSubmitter  AdaptSubmitter    // 导入后 AI 编码适配触发器（main.go 经 SetAdaptSubmitter 注入）；nil=不自动适配
	mwReconciler    MWReconciler      // 中间件依赖供给（部署前注入 REDIS_ADDR 等）；nil=不注入
	grant           grantChecker      // /workspace 模型授权校验；nil=跳过校验（兼容未注入 computeStore）
}

// grantChecker 模型授权校验（局部鸭子类型，解耦对 compute.Store 的直接依赖）。
// *compute.Store 实现了 IsGranted；main.go 经 SetGrantChecker 注入。nil=跳过校验（兜底）。
type grantChecker interface {
	IsGranted(ctx context.Context, userID, modelID string) (bool, error)
}

// SetGrantChecker 注入模型授权校验器（main.go 在 Register 后调，避免改 NewHandler/Register 签名）。
// nil=跳过 /workspace 的模型授权校验（兼容未接 computeStore 的部署）。
func (h *Handler) SetGrantChecker(g grantChecker) { h.grant = g }

// AdaptSubmitter 触发 AI 编码适配（导入后让 opencode 把应用适配成可部署）。
// dev.CodingAgent 经 main.go 的 adapter 实现之；nil=未启用（导入不自动适配，仍可手动用编码工作台）。
type AdaptSubmitter interface {
	SubmitAdapt(ctx context.Context, psID, appID, repoDir, prompt string) error
}

// SetAdaptSubmitter 注入适配触发器（main.go 在 Register 后调，避免改 NewHandler 签名）。
func (h *Handler) SetAdaptSubmitter(a AdaptSubmitter) { h.adaptSubmitter = a }

// MWReconciler 中间件依赖供给（部署前读 DB binding 声明 → 注入 REDIS_ADDR 等连接 env；
// 删 app 时回收 dedicated 中间件容器）。由 mwsupply.Reconciler 实现（经 main.go SetMwReconciler 注入，避免 appdeploy→mwsupply 依赖）。
type MWReconciler interface {
	Reconcile(ctx context.Context, appID, psID string) error
	Cleanup(ctx context.Context, appID string) error                                          // P3：docker rm dedicated 容器（best-effort）
	SeedFromManifest(ctx context.Context, appID, psID, repoDir string) error                  // P6：导入时 .anp/deps.yaml → DB declared binding
	ListDeps(ctx context.Context, appID string) ([]DepDeclaration, error)                     // P6：读 app 的依赖声明（binding → DTO）
	DepsCatalog(ctx context.Context, psID string) (DepsCatalog, error)                        // P6：读勾选器选项（kinds/strategies/instances）
	SetDeps(ctx context.Context, appID, psID string, decls []DepDeclaration) error            // P6：整体替换声明（diff 释放/声明）
	RegisterBindExisting(ctx context.Context, psID string, m MWInstance) (*MWInstance, error) // 注册已有中间件实例(spec ②)
	ListBindExisting(ctx context.Context, psID string) ([]MWInstance, error)                  // 列已注册实例
	DeleteInstance(ctx context.Context, id string) error                                      // 删实例
}

// SetMwReconciler 注入中间件供给器（main.go 在 Register 后调）。
func (h *Handler) SetMwReconciler(r MWReconciler) { h.mwReconciler = r }

// checkFunc 需求-代码核对的函数签名(便于测试 mock)。
// passed=false&err=nil → 核对未通过(409); err!=nil → AI 失败(503); passed=true → 通过。
type checkFunc func(ctx context.Context, apiKey, code, title, criteria string) (passed bool, err error, details string)

// CodeWS 暴露交互编码工作台 Manager（供 performance 模块读 live opencode 会话消息）。
func (h *Handler) CodeWS() *codews.Manager { return h.codeWS }

// NewHandler 构造。codeWS/changes/cfg/reqRepo/provisioner/routeWriter/standards/quota 可为 nil（不启用对应能力）。
// buildCfgStore/artifactStore/artifactStorage 为非 web 构建产物链路；nil=未装配（Task 13 在 main.go 注入）。
// scaffoldsBase 为脚手架种子根目录（建非 web 应用时克隆到 RepoDir）；空=不克隆脚手架。
// monitor/metricStore 为服务器指标采集链路（Task 8 加）；nil=未启用（Task 9 在 main.go 注入）。
func NewHandler(store *Store, deployer *Deployer, codeWS *codews.Manager, changes *change.Store, cfg *config.Store, reqRepo *requirement.Repository, provisioner *pgsupply.Provisioner, routeWriter appgw.RouteWriter, standards *standard.Store, quota AppQuotaChecker, buildCfgStore *BuildConfigStore, artifactStore *ArtifactStore, artifactStorage ArtifactStorage, scaffoldsBase string, monitor *ServerMonitor, metricStore *MetricStore) *Handler {
	var nodeStore *NodeStore
	if store != nil {
		nodeStore = NewNodeStore(store.db)
	}
	h := &Handler{store: store, deployer: deployer, codeWS: codeWS, changes: changes, cfg: cfg, reqRepo: reqRepo, provisioner: provisioner, routeWriter: routeWriter, standards: standards, quota: quota, nodeStore: nodeStore, monitor: monitor, metricStore: metricStore, buildCfgStore: buildCfgStore, artifactStore: artifactStore, artifactStorage: artifactStorage, scaffoldsBase: scaffoldsBase}
	h.checkFn = checkRequirement
	return h
}

// Register 模块级装配：内部 new Deployer/codews.Manager + NewHandler + Register。
// 返回 *Handler 供 release 模块（发布后自动部署）复用。
// buildCfgStore/artifactStore/artifactStorage 为非 web 构建产物链路；nil=未装配（Task 13 注入）。
// scaffoldsBase 为脚手架种子根目录（建非 web 应用时克隆到 RepoDir）；空=不克隆。
// monitor/metricStore 为服务器指标采集链路（Task 8 加）；nil=未启用（Task 9 注入）。
func Register(r gin.IRouter, store *Store, appDeployHost string, changeStore *change.Store, configStore *config.Store, reqRepo *requirement.Repository, provisioner *pgsupply.Provisioner, routeWriter appgw.RouteWriter, standards *standard.Store, quota AppQuotaChecker, buildCfgStore *BuildConfigStore, artifactStore *ArtifactStore, artifactStorage ArtifactStorage, scaffoldsBase string, monitor *ServerMonitor, metricStore *MetricStore) *Handler {
	codeWS := codews.NewManager(appDeployHost, configStore)
	if store != nil {
		codeWS.SetSessionLogger(codews.NewPGSessionStore(store.db)) // 会话落库供绩效/互动统计
	}
	codeWS.Start() // 启动后台空闲会话驱逐（reaper），回收 9400-9499 端口池容量
	h := NewHandler(store, NewDeployer(appDeployHost), codeWS, changeStore, configStore, reqRepo, provisioner, routeWriter, standards, quota, buildCfgStore, artifactStore, artifactStorage, scaffoldsBase, monitor, metricStore)
	h.Register(r)
	return h
}

// Register 注册路由。
func (h *Handler) Register(r gin.IRouter) {
	r.GET("/project-spaces/:id/apps", h.List)
	r.POST("/project-spaces/:id/apps", h.Create)
	r.POST("/project-spaces/:id/import/apps", h.Import)              // 导入已有项目（git/dir）；放 /import/apps 避开 /apps/:aid 冲突
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
	r.GET("/project-spaces/:id/apps/:aid/deps", h.GetDeps)                // P6：依赖声明列表
	r.PUT("/project-spaces/:id/apps/:aid/deps", h.PutDeps)                // P6：整体替换依赖声明
	r.PUT("/project-spaces/:id/apps/:aid/network-mode", h.PutNetworkMode) // host 网络门禁：设网络模式（需 gatekeeper/admin）
	r.GET("/project-spaces/:id/deps/catalog", h.GetDepsCatalog)           // P6：依赖目录（勾选器选项）
	r.POST("/project-spaces/:id/mw-instances", h.RegisterMwInstance)      // 注册已有中间件实例(spec ②)
	r.GET("/project-spaces/:id/mw-instances", h.ListMwInstances)          // 列已注册实例
	r.DELETE("/project-spaces/:id/mw-instances/:iid", h.DeleteMwInstance)
	r.GET("/project-spaces/:id/apps/:aid/stats", h.Stats) // 资源占用 + 健康探测
	r.GET("/project-spaces/:id/apps/:aid/logs", h.Logs)
	r.GET("/project-spaces/:id/apps/:aid/repo-docs", h.RepoDocs)           // 应用 repo 文档(README/.md)
	r.GET("/project-spaces/:id/apps/:aid/repo-file", h.RepoFile)           // 读 repo 文件内容
	r.GET("/project-spaces/:id/apps/:aid/git-status", h.GitStatus)         // 编码工作台 git 变更：工作区改动 + 提交历史
	r.GET("/project-spaces/:id/apps/:aid/file-diff", h.FileDiff)           // 单文件行级 diff（工作区 / 指定提交）
	r.GET("/project-spaces/:id/apps/:aid/commit-files", h.CommitFilesList) // 某次提交改了哪些文件
	r.POST("/project-spaces/:id/apps/:aid/commit", h.CommitWorktree)       // 仅提交 dev-<user> worktree（不部署）

	// 非 web 应用构建产物链路（Task 10）：触发构建 / 列产物 / 下载产物。
	r.POST("/project-spaces/:id/apps/:aid/build-artifacts", h.BuildArtifacts)            // 触发非 web 构建
	r.GET("/project-spaces/:id/apps/:aid/artifacts", h.ListArtifacts)                    // 列产物
	r.GET("/project-spaces/:id/apps/:aid/artifacts/:artid/download", h.DownloadArtifact) // 下载产物

	// 部署节点管理（多机部署）
	r.GET("/deploy-nodes", h.ListNodes)
	r.POST("/deploy-nodes", h.CreateNode)
	r.PUT("/deploy-nodes/:nid", h.UpdateNode)
	r.DELETE("/deploy-nodes/:nid", h.DeleteNode)
	r.POST("/deploy-nodes/:nid/test", h.TestNode)           // 测试连通性
	r.POST("/deploy-nodes/:nid/provision", h.ProvisionNode) // 异步搭建环境（SSH/WinRM 节点）
	r.POST("/deploy-nodes/:nid/collect", h.CollectNode)     // 手动触发一次指标采集
	r.GET("/deploy-nodes/:nid/metrics", h.NodeMetrics)      // 历史指标趋势
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
	ctx := c.Request.Context()
	d, err := h.store.Detail(ctx, c.Param("id"), c.Param("aid"))
	if err != nil || d == nil {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	// P1-c：Deps 经 MWReconciler 接口填（appdeploy 不直依 mwsupply）。best-effort，失败仅留空。
	if h.mwReconciler != nil {
		if deps, derr := h.mwReconciler.ListDeps(ctx, c.Param("aid")); derr == nil {
			d.Deps = deps
			if d.Deps == nil {
				d.Deps = []DepDeclaration{}
			}
		}
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
// @Param        body   body    object  false  "工作台选项{tool,model,requirement_id}"
// @Param        X-User header  string  false  "开发者身份"
// @Success      200    {object}  map[string]interface{}  "工作台信息(url/session_id等)"
// @Failure      404    {object}  map[string]interface{}  "应用不存在"
// @Failure      500    {object}  map[string]interface{}  "工作台未启用/启动失败"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/workspace [post]
// validateRepoDir 校验应用代码仓库可编码：非空、存在、是有效 git 仓库。
// 缺失/损坏时返明确错误——防 opencode 以无效 cwd 启动 → /api/reference 500 → 进程崩 →
// ERR_CONNECTION_REFUSED → 空白 iframe。典型触发：repo 被外部清理删除、或导入失败后目录被
// RemoveAll 但 DB 记录仍在（repo_dir 列非空、磁盘无目录）。在此最早拦截，不分配端口/不留
// .worktrees 残留/不起子进程。Ensure 的生产唯一调用方即 Workspace，覆盖足够（#31）。
func validateRepoDir(repoDir string) error {
	if repoDir == "" {
		return fmt.Errorf("应用未托管代码仓库（外部应用不支持交互编码）")
	}
	if _, err := os.Stat(repoDir); err != nil {
		return fmt.Errorf("应用代码仓库不存在（%s），可能已被清理或导入失败，请重新创建或导入应用", repoDir)
	}
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); err != nil {
		return fmt.Errorf("应用代码仓库未初始化为 git 仓库（%s），请重新创建应用", repoDir)
	}
	return nil
}

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
		Tool          string `json:"tool"`                // opencode(默认) / claude / codex ...
		RequirementID string `json:"requirement_id"`      // 绑定的需求（工作直播按此关联；空=application 页老入口）
		Model         string `json:"model,omitempty"`     // 授权模型 id（cmd_xxx）；空=未选模型，走全局 config 兜底
		ForceNew      bool   `json:"force_new,omitempty"` // 前端「🆕 新会话」按钮：强制开空会话
		Prompt        string `json:"prompt,omitempty"`    // 新会话(force_new)时前端拼好的需求规格文本：Ensure 建会话后立即注入，使该会话成为活动会话且已含需求，deep_url 返回前就绪 → iframe 加载即可见（消除"先空会话后异步注入"导致的会话错位/空窗）
	}
	_ = c.ShouldBindJSON(&in)
	// 模型授权校验：选了模型且 grant 已注入时，校验当前用户是否被授权该模型；越权即拒 403，不 fallback。
	// 用 CtxUserDBID(usr_xxx，grant 表 user_id) 校验，不要用 CtxUserID(用户名)。
	// Model 空=走默认路由（兼容旧调用）；grant nil=未注入 computeStore（跳过，兼容）。
	if in.Model != "" && h.grant != nil {
		uid := c.GetString(auth.CtxUserDBID)
		ok, err := h.grant.IsGranted(c.Request.Context(), uid, in.Model)
		if err != nil {
			// fail-closed：DB 校验出错时保守按未授权拒绝（err 时 ok=false → 落入 !ok 分支返 403）。
			log.Printf("warn: IsGranted 校验出错 user=%s model=%s: %v", uid, in.Model, err)
		}
		if !ok {
			httpx.Err(c, 403, 40302, "无权使用该模型")
			return
		}
	}
	user := c.GetString(auth.CtxUserID) // 开发者身份（用户名，不同开发者可各选各的工具；worktree/session 用，保持不变）
	if user == "" {
		user = "anonymous"
	}
	// 闸门（#31）：Ensure 前校验 repo 存在且有效。repo 缺失/损坏时 opencode 以无效 cwd 启动 →
	// 空白 iframe；在此返清晰错误（409，前端 r.message 红字），最早拦截、不分配端口/不留残留。
	if err := validateRepoDir(a.RepoDir); err != nil {
		httpx.Err(c, 409, 40961, err.Error())
		return
	}
	s, err := h.codeWS.Ensure(psID, aid, a.RepoDir, user, in.Tool, in.RequirementID, in.Model, in.ForceNew)
	if err != nil {
		httpx.Err(c, 500, 50021, err.Error())
		return
	}
	// 新会话即时注入上下文：Ensure 已创建会话，立即 SendPrompt → 会话成为活动会话且已含上下文，
	// 且在 deep_url 返回前完成 → iframe 加载时"活动会话==deep_url会话==已含上下文"，消除 SPA
	// 读到旧空/临时会话的竞态（旧流程先 setUrl 后异步注入，iframe 加载瞬间活动会话可能仍是旧的）。
	// 两类注入（force_new 新会话才注入，复用会话不注入、不重复）：
	//   ① 需求驱动：前端拼好需求规格随 Prompt 发送（buildReqPrompt）。
	//   ② 自主发起（无 RequirementID）：注入应用上下文 AppContextPrompt——这是什么应用/仓库结构/
	//      依赖中间件/部署态。公司开发规范不在此处注入（已由 RefreshAgentsMD 写进 worktree AGENTS.md）。
	// best-effort：失败不阻断 boot。
	prompt := in.Prompt
	if prompt == "" && in.RequirementID != "" && in.ForceNew {
		// 需求驱动新会话（「绑需求开发」）：按 requirement_id 拼需求规格注入，单一真源
		// requirement.BuildCodePrompt，避免前端 TS 重复拼装造成漂移。gate 在 ForceNew：
		// 复用（继续编码）不重注入，精确匹配双按钮语义。best-effort：加载失败仅 log。
		if h.reqRepo != nil {
			if req, err := h.reqRepo.Get(c.Request.Context(), in.RequirementID); err == nil && req != nil {
				prompt = requirement.BuildCodePrompt(req)
			} else if err != nil {
				log.Printf("[appdeploy] 加载需求规格失败(会话已就绪): req=%s err=%v", in.RequirementID, err)
			}
		}
	} else if prompt == "" && in.RequirementID == "" && in.ForceNew {
		prompt = AppContextPrompt(a)
	}
	if prompt != "" && s.Tool == "opencode" {
		if err := h.codeWS.SendPrompt(aid, user, prompt); err != nil {
			log.Printf("[appdeploy] boot 即时注入上下文失败(会话已就绪,可手动点AI编码重试): app=%s user=%s err=%v", aid, user, err)
		}
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
	// 平台托管 env（source=platform，如 DATABASE_URL/REDIS_ADDR）禁止用户面板改：部署 reconcile 保障其值。
	if src, _ := h.store.GetEnvSource(c.Request.Context(), c.Param("aid"), in.Key); src == "platform" {
		httpx.Err(c, 409, 40960, "平台托管的环境变量不可修改（由部署供给）")
		return
	}
	if err := h.store.UpsertEnv(c.Request.Context(), c.Param("aid"), in.Key, in.Value, in.IsSecret, "user"); err != nil {
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
	// 平台托管 env 禁止用户面板删（同 UpsertEnv 保护）。
	if src, _ := h.store.GetEnvSource(c.Request.Context(), c.Param("aid"), c.Param("key")); src == "platform" {
		httpx.Err(c, 409, 40960, "平台托管的环境变量不可删除（由部署供给）")
		return
	}
	if err := h.store.DeleteEnv(c.Request.Context(), c.Param("aid"), c.Param("key")); err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	httpx.OK(c, gin.H{"app_id": c.Param("aid"), "key": c.Param("key"), "deleted": true})
}

// GetDeps 列应用依赖声明。
//
// @Summary      应用依赖声明列表
// @Tags         appdeploy
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Param        aid  path  string  true  "应用ID"
// @Success      200  {object}  map[string]interface{}  "依赖声明列表"
// @Failure      404  {object}  map[string]interface{}  "应用不存在"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/deps [get]
func (h *Handler) GetDeps(c *gin.Context) {
	psID, aid := c.Param("id"), c.Param("aid")
	if a, _ := h.store.Get(c.Request.Context(), psID, aid); a == nil || a.ID == "" {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	decls, err := h.mwReconciler.ListDeps(c.Request.Context(), aid)
	if err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	httpx.OK(c, decls)
}

// PutDeps 整体替换应用依赖声明（校验 kind/strategy → SetDeps）。
// 只调 SetDeps（声明落库），不调 Reconcile：声明与部署解耦，下次 deploy 由 buildAndDeploy 供给。
//
// @Summary      替换应用依赖声明
// @Tags         appdeploy
// @Accept       json
// @Produce      json
// @Param        id    path  string  true  "项目空间ID"
// @Param        aid   path  string  true  "应用ID"
// @Param        body  body  []DepDeclaration  true  "依赖声明数组"
// @Success      200   {object}  map[string]interface{}  "替换结果"
// @Failure      400   {object}  map[string]interface{}  "kind/strategy 非法或重复"
// @Failure      404   {object}  map[string]interface{}  "应用不存在"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/apps/{aid}/deps [put]
func (h *Handler) PutDeps(c *gin.Context) {
	psID, aid := c.Param("id"), c.Param("aid")
	a, _ := h.store.Get(c.Request.Context(), psID, aid)
	if a == nil || a.ID == "" {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	var body []DepDeclaration
	if err := c.ShouldBindJSON(&body); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	seen := map[string]bool{}
	for _, d := range body {
		if d.Kind != "redis" && d.Kind != "milvus" {
			httpx.Err(c, 400, 40001, "kind 非法: "+d.Kind)
			return
		}
		if d.Strategy != "bind_existing" && d.Strategy != "shared" && d.Strategy != "dedicated" {
			httpx.Err(c, 400, 40001, "strategy 非法: "+d.Strategy)
			return
		}
		if seen[d.Kind] {
			httpx.Err(c, 400, 40001, "重复 kind: "+d.Kind)
			return
		}
		seen[d.Kind] = true
	}
	if err := h.mwReconciler.SetDeps(c.Request.Context(), aid, a.ProjectSpaceID, body); err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	httpx.OK(c, gin.H{"ok": true})
}

// PutNetworkMode 设置应用网络模式（bridge/host）。host 削弱隔离 → 需 gatekeeper/admin（op app.net.host）。
// network_mode 为 app 级配置（test/prod 共用）；下次部署生效（deploy 时 deployer 按 mode 拼 --network）。
func (h *Handler) PutNetworkMode(c *gin.Context) {
	psID, aid := c.Param("id"), c.Param("aid")
	a, _ := h.store.Get(c.Request.Context(), psID, aid)
	if a == nil || a.ID == "" {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	if !auth.Allowed("app.net.host", rolesFromCtx(c)) {
		httpx.Err(c, 403, 40301, "无权限修改网络模式（仅 gatekeeper/admin）")
		return
	}
	var in struct {
		Mode string `json:"mode"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	if in.Mode != "bridge" && in.Mode != "host" {
		httpx.Err(c, 400, 40001, "mode 非法: "+in.Mode)
		return
	}
	if err := h.store.UpdateNetworkMode(c.Request.Context(), aid, in.Mode); err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	httpx.OK(c, gin.H{"ok": true, "network_mode": in.Mode})
}

// GetDepsCatalog 依赖勾选器选项（kinds/strategies/可见实例）。
//
// @Summary      依赖目录（勾选器选项）
// @Tags         appdeploy
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Success      200  {object}  map[string]interface{}  "依赖目录"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/deps/catalog [get]
func (h *Handler) GetDepsCatalog(c *gin.Context) {
	psID := c.Param("id")
	cat, err := h.mwReconciler.DepsCatalog(c.Request.Context(), psID)
	if err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	httpx.OK(c, cat)
}

// RegisterMwInstance 注册一个已有中间件实例(运维把部署机服务登记给 ANP,部署时自动注入连接 env)。
// 鉴权:复用 host 网络门禁的 admin op(app.net.host,仅 admin)——mw 实例是平台/项目级管理操作。
func (h *Handler) RegisterMwInstance(c *gin.Context) {
	if h.mwReconciler == nil {
		httpx.Err(c, 503, 50301, "中间件供给未启用")
		return
	}
	if !auth.Allowed("app.net.host", rolesFromCtx(c)) {
		httpx.Err(c, 403, 40301, "无权限注册中间件实例(仅管理员)")
		return
	}
	var in MWInstance
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	if in.Kind == "" || in.Host == "" || in.Port == 0 {
		httpx.Err(c, 400, 40001, "kind/host/port 必填")
		return
	}
	out, err := h.mwReconciler.RegisterBindExisting(c.Request.Context(), c.Param("id"), in)
	if err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	httpx.OK(c, out)
}

// ListMwInstances 列出项目空间可见的已注册中间件实例(auth_ref 掩码)。
func (h *Handler) ListMwInstances(c *gin.Context) {
	if h.mwReconciler == nil {
		httpx.OK(c, gin.H{"data": []MWInstance{}})
		return
	}
	list, err := h.mwReconciler.ListBindExisting(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	httpx.OK(c, gin.H{"data": list})
}

// DeleteMwInstance 删除已注册中间件实例(仅管理员)。
func (h *Handler) DeleteMwInstance(c *gin.Context) {
	if h.mwReconciler == nil {
		httpx.Err(c, 503, 50301, "中间件供给未启用")
		return
	}
	if !auth.Allowed("app.net.host", rolesFromCtx(c)) {
		httpx.Err(c, 403, 40301, "无权限删除中间件实例(仅管理员)")
		return
	}
	if err := h.mwReconciler.DeleteInstance(c.Request.Context(), c.Param("iid")); err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	httpx.OK(c, gin.H{"deleted": c.Param("iid")})
}

type createBody struct {
	Name         string `json:"name" binding:"required"`
	RepoDir      string `json:"repo_dir"`      // managed 可选；空=平台托管 git 仓库 /data/repos/<name>
	InternalPort int    `json:"internal_port"` // managed 可选；buildpack 检测或默认 8080
	DeployMode   string `json:"deploy_mode"`   // managed(默认,A类) / external(B类纳管外部应用)
	// 应用形态：web/desktop/mobile/cli/service/headless，空默认 web。与 deploy_mode 正交：
	// web/service/headless 走容器部署链路(headless 容器部署无 HTTP 路由,进程存活健康)；desktop/mobile/cli 走预置构建容器出可下载产物。
	AppKind     string `json:"app_kind"`
	ExternalURL string `json:"external_url"` // external 必填：外部应用访问地址 http(s)://host[:port][/path]
}

// validAppKind 应用形态合法性校验(web/desktop/mobile/cli/service/headless)。
// 纯函数，供 Create 校验 + 测试断言。空=默认 web，合法。
func validAppKind(k string) bool {
	switch k {
	case "", AppKindWeb, AppKindDesktop, AppKindMobile, AppKindCLI, AppKindService, AppKindHeadless:
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
// @Param        body  body  createBody  true  "应用(name+repo_dir+internal_port+deploy_mode+app_kind+external_url)"
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
	// managed 非 web（且非 service）：克隆脚手架种子到 RepoDir 并首次提交。
	// 克隆失败不阻断建应用（log 警告继续）——脚手架缺失可后续补，应用记录已建。
	// web/service 走自带 Dockerfile / 容器链路，不克隆脚手架。
	if h.buildCfgStore != nil && h.scaffoldsBase != "" &&
		in.AppKind != AppKindWeb && in.AppKind != AppKindService {
		h.cloneScaffold(c.Request.Context(), a, in.AppKind)
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

// cloneScaffold 按应用形态的构建配置查 scaffold 标识，把脚手架种子克隆到 a.RepoDir 并首次提交。
// 脚手架目录缺失/克隆失败仅记日志不阻断建应用（务实：脚手架可后续补，应用记录已建）。
func (h *Handler) cloneScaffold(ctx context.Context, a *Application, appKind string) {
	cfg, err := h.buildCfgStore.Get(ctx, appKind)
	if err != nil || cfg.Scaffold == "" {
		return
	}
	scaffoldDir := filepath.Join(h.scaffoldsBase, cfg.Scaffold)
	if _, err := os.Stat(scaffoldDir); err != nil {
		zap.L().Warn("脚手架目录不存在，跳过克隆（建应用继续）",
			zap.String("app_kind", appKind), zap.String("scaffold", cfg.Scaffold), zap.String("dir", scaffoldDir))
		return
	}
	if err := CloneScaffold(scaffoldDir, a.RepoDir); err != nil {
		zap.L().Warn("克隆脚手架失败（建应用继续）",
			zap.String("app", a.ID), zap.String("scaffold", cfg.Scaffold), zap.Error(err))
		return
	}
	// 把脚手架内容作为首次提交（覆盖 EnsureRepo 的模板 README/docs，脚手架自带更具体内容）
	_, _ = runGit(ctx, a.RepoDir, "add", "-A")
	_, _ = runGit(ctx, a.RepoDir, "commit", "-q", "-m", "init: scaffold "+cfg.Scaffold)
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
	// 导入种子：仓库 .anp/deps.yaml → declared binding（best-effort，不覆盖 UI 声明，失败不阻塞导入）。
	if h.mwReconciler != nil {
		_ = h.mwReconciler.SeedFromManifest(ctx, appID, psID, repoDir)
	}
	// 供给独立库（失败不阻塞导入，仅记 last_error）
	if h.provisioner != nil {
		if _, pe := h.provisioner.Provision(ctx, psID, appID); pe != nil {
			_ = h.store.SetStatus(ctx, psID, appID, "registered", pe.Error(), "")
		}
	}
	// 导入后触发 opencode 适配（改应用代码 to ANP；best-effort，失败不阻塞导入）。
	if h.adaptSubmitter != nil {
		if h.standards != nil {
			_ = h.standards.RefreshAgentsMD(ctx, repoDir, psID, "")
		}
		_ = h.adaptSubmitter.SubmitAdapt(ctx, psID, appID, repoDir, AdaptPrompt(name))
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
	// 导入种子：仓库 .anp/deps.yaml → declared binding（best-effort，不覆盖 UI 声明，失败不阻塞导入）。
	if h.mwReconciler != nil {
		_ = h.mwReconciler.SeedFromManifest(ctx, appID, psID, repoDir)
	}
	if h.provisioner != nil {
		if _, pe := h.provisioner.Provision(ctx, psID, appID); pe != nil {
			_ = h.store.SetStatus(ctx, psID, appID, "registered", pe.Error(), "")
		}
	}
	// 导入后触发 opencode 适配（改应用代码 to ANP；best-effort，失败不阻塞导入）。
	if h.adaptSubmitter != nil {
		if h.standards != nil {
			_ = h.standards.RefreshAgentsMD(ctx, repoDir, psID, "")
		}
		_ = h.adaptSubmitter.SubmitAdapt(ctx, psID, appID, repoDir, AdaptPrompt(name))
	}
}

// deployBody 部署请求体（均可选）。
type deployBody struct {
	Env           string `json:"env"`            // test / prod；空默认 test
	SHA           string `json:"sha"`            // 可选：部署指定历史版本（回滚）
	NodeID        string `json:"node_id"`        // 可选：部署到指定节点（空=本地 .28，如 node_30）
	FromWorkspace bool   `json:"from_workspace"` // 编码工作台发起：test 从 dev-<user> worktree 构建 + 未提交检测
	AutoCommit    bool   `json:"auto_commit"`    // FromWorkspace 检测到未提交时，前端确认后传 true：先 AI 总结 + commit 再构建
	Engine        string `json:"engine"`         // "ai"/"fixed" 显式指定（failed 后「固定引擎重试」传 fixed）；空=按 system_config.deploy_engine
}

// checkNodeDeployable 部署节点分流前置守卫（同步段显式报错，替代异步段静默回退）。
// 与 buildAndDeploy 的 I6 原生部署分流派驻一致：
//   - ssh/winrm 节点（如 Windows 服务器）：走 NativeDeployer，要求应用仓库有 deploy.yaml；
//     没有则 409 显式报错——异步段现状是静默落回本地 docker 链路，用户以为部署到了
//     远程 Windows，实际容器跑在 .28，目标错位极难排查。
//   - 本地/docker_tcp 节点：走 docker 链路，os_type=windows 的节点没有 docker 守护进程，
//     必 409（此前靠前端过滤兜底，API 直调无防护）。
func (h *Handler) checkNodeDeployable(a *Application, nodeID string) *nodeDeployError {
	if nodeID == "" || nodeID == "node_local" || h.nodeStore == nil {
		return nil
	}
	node, err := h.nodeStore.Get(context.Background(), nodeID)
	if err != nil || node == nil || node.ID == "" {
		return &nodeDeployError{Status: 404, Code: 40421, Message: "部署节点不存在：" + nodeID}
	}
	if node.ConnectType == "ssh" || node.ConnectType == "winrm" {
		desc, _ := loadDeployDesc(a.RepoDir)
		if desc == nil {
			return &nodeDeployError{Status: 409, Code: 40971, Message: fmt.Sprintf(
				"节点 %s(%s) 为 %s 原生部署节点，需应用仓库含 deploy.yaml 才可部署（当前 %s 无此文件）。请在编码工作台让 AI 生成 deploy.yaml（原生部署描述）后再部署，或改选 docker 节点",
				node.Name, node.Host, node.ConnectType, a.RepoDir)}
		}
		return nil
	}
	if node.OSType == "windows" {
		return &nodeDeployError{Status: 409, Code: 40972, Message: fmt.Sprintf(
			"节点 %s(%s) 为 Windows 且非 ssh/winrm 连接，无 docker 守护进程，不可容器部署。请改选 Linux docker 节点，或把节点连接方式改为 ssh/winrm 走原生部署",
			node.Name, node.Host)}
	}
	return nil
}

// nodeDeployError checkNodeDeployable 的错误载体（status+code+message 直接喂 httpx.Err）。
type nodeDeployError struct {
	Status  int
	Code    int
	Message string
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
	// host 网络门禁：host 模式应用须 gatekeeper/admin 部署（防 dev 部署他人开启的 host 应用）
	if a.NetworkMode == "host" && !auth.Allowed("app.net.host", rolesFromCtx(c)) {
		httpx.Err(c, 403, 40301, "host 网络应用需 gatekeeper/admin 部署")
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
	// 节点分流守卫：ssh/winrm 节点须有 deploy.yaml（否则异步段静默落回本地 docker，目标错位）；
	// windows 节点不可走 docker 链路。同步段显式报错（别静默）。
	if ne := h.checkNodeDeployable(a, in.NodeID); ne != nil {
		httpx.Err(c, ne.Status, ne.Code, ne.Message)
		return
	}
	buildDir := ""
	if env == EnvTest && in.FromWorkspace {
		user := c.GetString(auth.CtxUserID)
		if user == "" {
			user = "anonymous"
		}
		wt := filepath.Join(a.RepoDir, ".worktrees", sanitizeID(user))
		if _, err := os.Stat(wt); err != nil {
			// 显式报错，不静默回退主仓：工作台入口语义=部署开发者 worktree 最新代码，
			// worktree 缺失（空闲驱逐/仓库重建）时回退主仓会部署 master 旧代码，
			// 开发者误以为新改动已生效——版本不一致极难排查。
			httpx.Err(c, 409, 40970, fmt.Sprintf(
				"开发者 worktree 不存在：%s（可能被空闲会话回收或仓库重建）。请回到编码工作台重新打开（自动重建 worktree）后再构建部署；或从应用全景页部署主仓 master 代码",
				wt))
			return
		}
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
	// 部署前刷新 AGENTS.md：让 opencode（P2 部署前备料）/ 导入适配读到与引擎实现一致的最新 ANP 规则。
	// best-effort：失败不阻断部署（与 workspace/导入 三处调用点一致）。
	// 工作台部署优先刷 worktree(buildDir)，普通部署刷主仓 a.RepoDir。
	repoDir := buildDir
	if repoDir == "" {
		repoDir = a.RepoDir
	}
	if h.standards != nil && repoDir != "" {
		_ = h.standards.RefreshAgentsMD(c.Request.Context(), repoDir, psID, "")
	}
	// P2 AI 引擎分流：满足 test 环境 + 本地节点 + 非 host 网络 + deploy_engine 判定为 ai
	// 时走 AI 引擎；否则走固定引擎（含显式/降级）。判定见 engineFor（显式请求 > system_config > fixed）。
	// 固定引擎链 buildAndDeploy 一字不动；AI 引擎走 aiDeploy（简报→受限执行→五步验证）。
	// host 网络排除：host 模式无 -p 映射，hostPortOf 恒读不到端口 → 验证 3 恒失败，AI 链对 host
	// 恒假失败——不满足任一条件即走固定引擎（不报错，静默降级为既有链路）。
	localNode := in.NodeID == "" || in.NodeID == "node_local"
	if localNode && a.NetworkMode != "host" && h.engineFor(in.Engine, env) == "ai" {
		h.markPreparing(c.Request.Context(), psID, aid, env) // 同步标 preparing，前端立即看到进度条
		go h.aiDeploy(psID, aid, env, buildDir)
		httpx.OK(c, gin.H{"id": aid, "env": env, "status": "preparing", "engine": "ai",
			"note": "异步 AI 部署到 " + env + " 环境（受限执行 + 平台验证）"})
		return
	}
	h.markBuilding(c.Request.Context(), psID, aid, env) // 同步标 building，前端立即看到进度条
	go h.buildAndDeploy(psID, aid, "", env, in.NodeID, buildDir)
	noteSuffix := ""
	if in.NodeID != "" && in.NodeID != "node_local" {
		noteSuffix = "（节点 " + in.NodeID + "）"
	}
	httpx.OK(c, gin.H{"id": aid, "env": env, "status": "building", "engine": "fixed", "note": "异步构建部署到 " + env + " 环境" + noteSuffix})
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
	a, _ := h.store.Get(c.Request.Context(), psID, aid)
	if a == nil || a.ID == "" {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	// 部署权限分离：上线 prod 需 gatekeeper/admin
	if !auth.Allowed("app.deploy.prod", rolesFromCtx(c)) {
		httpx.Err(c, 403, 40301, "无权限上线 prod（仅 gatekeeper/admin）")
		return
	}
	// host 网络门禁（defense-in-depth：当前角色矩阵下 gatekeeper 同持 app.deploy.prod+app.net.host，此 403 不可达；
	// 保留以备 app.net.host 收窄或新增只持 prod 权限的角色）
	if a.NetworkMode == "host" && !auth.Allowed("app.net.host", rolesFromCtx(c)) {
		httpx.Err(c, 403, 40301, "host 网络应用需 gatekeeper/admin 部署")
		return
	}
	var in struct {
		NodeID string `json:"node_id"`
	}
	_ = c.ShouldBindJSON(&in)
	// 节点分流守卫（同 Deploy：同步段显式报错，别静默）。
	if ne := h.checkNodeDeployable(a, in.NodeID); ne != nil {
		httpx.Err(c, ne.Status, ne.Code, ne.Message)
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
	a, err := h.store.Get(c.Request.Context(), psID, aid)
	if err != nil {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	// host 网络门禁：host 模式应用须 gatekeeper/admin 部署
	if a.NetworkMode == "host" && !auth.Allowed("app.net.host", rolesFromCtx(c)) {
		httpx.Err(c, 403, 40301, "host 网络应用需 gatekeeper/admin 部署")
		return
	}
	h.markBuilding(c.Request.Context(), psID, aid, env) // 同步标 building，前端立即看到进度条
	go h.buildAndDeploy(psID, aid, in.SHA, env, in.NodeID, "")
	httpx.OK(c, gin.H{"id": aid, "sha": in.SHA, "env": env, "status": "building", "note": "版本化部署/回滚到 " + env})
}

// validateEnvKeys 软校验 needs.env_keys：声明但无值来源的 key 记 WARN，不阻断部署。
// best-effort（避免 opencode 误填 / 中间件未绑定卡死部署）；缺失提示排查中间件绑定或密钥配置。
func validateEnvKeys(a *Application, mf *DeployManifest, envPairs []string, hasConfig bool) {
	for _, k := range missingEnvKeys(mf, envPairs, hasConfig) {
		zap.L().Warn("[appdeploy] needs.env_keys 声明但无值来源（不阻断，检查中间件绑定/密钥配置）",
			zap.String("app", a.Name), zap.String("env_key", k))
	}
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
	// I6 修复：原生部署分流 —— 若应用 RepoDir 含 deploy.yaml 且目标节点为 ssh/winrm，
	// 走 NativeDeployer（非容器原生部署：传文件 + 渲染脚本 + 远程执行 + 健康检查），
	// 不走 docker build/run。web 应用（无 deploy.yaml 或节点为 docker_tcp）仍走下方 docker 链路。
	if nodeID != "" && nodeID != "node_local" && h.nodeStore != nil {
		if node, e := h.nodeStore.Get(ctx, nodeID); e == nil && node != nil &&
			(node.ConnectType == "ssh" || node.ConnectType == "winrm") {
			if desc, derr := loadDeployDesc(a.RepoDir); derr == nil && desc != nil {
				if nerr := h.deployNative(ctx, a, ins, env, node, desc); nerr != nil {
					ins.Status = "failed"
					ins.LastError = nerr.Error()
					_ = h.store.UpdateInstance(ctx, ins)
					h.markFailed(ctx, psID, a.ID, ins.LastError, ins.BuildLog)
				}
				return
			}
		}
	}
	// 清理该 app+env 所有历史容器（DB 记录的 + 孤儿残留），彻底释放端口避免漂移/Conflict
	if _, err := h.deployer.RemoveByPrefix(ctx, "appdeploy-"+dockerSlug(a.Name)+"-"+env+"-"); err != nil {
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
	// 中间件依赖供给（P6：读 DB binding 声明 → 注入 REDIS_ADDR 等 env）。
	// best-effort（失败不阻塞部署）。声明源已由导入/UI 切到 DB（SeedFromManifest）。
	if h.mwReconciler != nil {
		_ = h.mwReconciler.Reconcile(ctx, a.ID, a.ProjectSpaceID)
	}
	envPairs, _ := h.store.EnvPairs(ctx, a.ID) // 应用运行时环境变量（含密钥）注入容器
	// docker run 限 3 分钟：镜像已构建，run 卡住通常是端口/挂载问题，无需长等；超时同走 failed。
	deployCtx, deployCancel := context.WithTimeout(ctx, 3*time.Minute)
	// #44 部署清单：读 .anp/deploy.yaml（needs=声明 actual=已记录实际值）。读失败不阻塞（回退自动探测）。
	mf, mfErr := LoadDeployManifest(a.RepoDir)
	if mfErr != nil {
		zap.L().Warn("读取 .anp/deploy.yaml 失败（不阻塞，回退自动探测）",
			zap.String("app", a.Name), zap.Error(mfErr))
	}
	// config.yaml 挂载(#44 resolved-priority)：manifest 声明优先（actual 记录的宿主路径且可读→用记录=确定性
	// 抗引擎回归；否则 toHostRepoDir(src) 重算）；无 manifest/无声明→detectConfigPath+toHostRepoDir。
	// 修回归：原 detectConfigPath 返容器路径 /data/repos/... 直接作 -v 源，宿主不存在致 docker 建空目录，
	// 应用读 config.yaml 得「is a directory」exit 1（yxt-eino-v2 崩溃根因）。
	configHostPath, configSrc, hasConfig := ResolveConfigMount(a.RepoDir, mf)
	if hasConfig {
		envPairs = append(envPairs, "CONFIG_PATH=/app/config.yaml")
	}
	// P1-b：needs 全字段消费。ports→容器监听端口(优先 needs.ports[0])；command→覆盖镜像 CMD；
	// mounts→额外挂载(非 config，config 由 ConfigPath 单独处理)；env_keys→校验有值来源(缺失仅 WARN)。
	deployOpts := DeployOpts{
		ConfigPath: configHostPath,
		Mounts:     ResolveExtraMounts(a.RepoDir, mf),
	}
	if mf != nil {
		if len(mf.Needs.Ports) > 0 {
			deployOpts.Port = mf.Needs.Ports[0]
		}
		deployOpts.Command = mf.Needs.Command
	}
	validateEnvKeys(a, mf, envPairs, hasConfig)
	dErr := h.deployer.Deploy(deployCtx, a, ins, envPairs, dockerHost, deployOpts)
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
	ins.RestartCount = 0 // 新容器 docker RestartCount=0,重置 DB 基线避免上轮 reconcile 残留
	_ = h.store.UpdateInstance(ctx, ins)
	_ = h.store.UpdateRestartCount(ctx, ins.AppID, ins.Env, 0) // 持久化新基线(UpdateInstance 不写 restart_count)
	// #44 部署成功回填 .anp/deploy.yaml.actual（镜像/已解析宿主源/宿主端口/引擎版本）。
	// 下次部署优先用 actual.mounts_src 重放（确定性）；needs 段不动（opencode 维护）。失败仅 warn，不阻塞已成功的部署。
	if rErr := RecordActuals(a.RepoDir, mf, ins.Image, ins.HostPort, configSrc, time.Now().Format(time.RFC3339)); rErr != nil {
		zap.L().Warn("回填 .anp/deploy.yaml actual 失败（不阻塞部署）",
			zap.String("app", a.Name), zap.Error(rErr))
	}
	// 守卫：部署后回读容器实际镜像，校验与意图 ins.Image 一致（部署时真相源=意图）。
	// 不一致=部署异常（RemoveByPrefix 未清干净 / 端口冲突留旧容器），只 WARN 不阻断、不自愈
	// （部署时不向陈旧容器对齐——与定时守卫「容器为真相」相反）。交 DriftReconciler / 人工。
	if actualImg, dErr := h.deployer.InspectImage(ctx, ins.ContainerName); dErr == nil && actualImg != "" && actualImg != ins.Image {
		zap.L().Warn("部署后镜像回读不一致（部署异常）",
			zap.String("app", a.Name), zap.String("env", ins.Env),
			zap.String("intended", ins.Image), zap.String("actual", actualImg))
	}
	// 写 appgw 路由表:部署成功即时把 /apps/<app_id>/ 映射到本环境容器。
	// headless 无端口/无 URL,不写 HTTP 路由。失败不阻塞部署;routeWriter nil = 未启用 appgw。
	if h.routeWriter != nil && a.AppKind != AppKindHeadless {
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

// deployNative 原生部署链路（I6）：应用 RepoDir 含 deploy.yaml 且目标节点为 ssh/winrm 时，
// 经 NativeDeployer 远程传文件 + 执行渲染脚本 + 健康检查，不走 docker build/run。
// 成功写 instance(running) + 概览；失败返回 error，由调用方回写 failed。
func (h *Handler) deployNative(ctx context.Context, a *Application, ins *AppInstance, env string, node *DeployNode, desc *DeployDesc) error {
	exec, err := NewRemoteExecutor(node)
	if err != nil {
		return fmt.Errorf("new remote executor: %w", err)
	}
	// 原生部署限 10 分钟：远程执行/健康检查卡住时避免 goroutine 永挂。
	deployCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	res, err := (&NativeDeployer{}).Deploy(deployCtx, a, node, exec, desc)
	if err != nil {
		ins.Status = "failed"
		ins.LastError = err.Error()
		ins.BuildLog = tail(res.Log, 2000)
		_ = h.store.UpdateInstance(ctx, ins)
		return err
	}
	ins.Status = "running"
	ins.LastError = ""
	ins.BuildLog = tail(res.Log, 2000)
	// 原生部署无容器端口映射，URL 指向节点主机（具体端口由 deploy.yaml 内服务决定）。
	if ins.URL == "" {
		ins.URL = "http://" + node.Host
	}
	_ = h.store.UpdateInstance(ctx, ins)
	// 写 appgw 路由表（与 docker 链路一致，失败不阻塞）。
	if h.routeWriter != nil {
		_ = h.routeWriter.UpsertRoute(ctx, a.ID, a.ProjectSpaceID, env, node.Host, 0)
	}
	h.syncOverviewIfProd(ctx, a, env)
	if env != EnvProd {
		_ = h.store.UpdateAppStatus(ctx, a.ID, "running")
	}
	return nil
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

// engineFor 引擎判定（spec §1）：显式请求 > system_config.deploy_engine > fixed。
// ai 仅 test 生效（prod 恒 fixed）；远程节点 / host 网络排除在 Deploy handler 分流处判
// （engineFor 只看 env——纯函数可单测，节点与网络约束依赖请求/应用上下文）。
func (h *Handler) engineFor(reqEngine, env string) string {
	e := reqEngine
	if e == "" && h.cfg != nil {
		e = h.cfg.Get("deploy_engine", "fixed")
	}
	if e != "ai" {
		return "fixed"
	}
	if env != EnvTest {
		return "fixed"
	}
	return "ai"
}

// markPreparing AI 引擎专用前置状态（组装简报+AI 执行中）；仿 markBuilding。
// preparing 语义上属于 building 的一种（前端两者同展示），单列以便审计 AI 部署在途。
func (h *Handler) markPreparing(ctx context.Context, psID, aid, env string) {
	_ = h.store.SetStatus(ctx, psID, aid, "preparing", "", "")
	if _, err := h.store.GetOrCreateInstance(ctx, aid, env); err == nil {
		_ = h.store.SetInstanceStatus(ctx, aid, env, "preparing", "", "")
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
		// 回收 dedicated 中间件容器（best-effort，不阻塞删 app；shared/bind_existing 靠 CASCADE）
		if h.mwReconciler != nil {
			_ = h.mwReconciler.Cleanup(c.Request.Context(), a.ID)
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
		_, _ = h.deployer.RemoveImages(c.Request.Context(), dockerSlug(a.Name))
		// 清理非 web 构建产物（I-6）：FK CASCADE 只删 DB 记录，data/artifacts/ 实体成孤儿。
		// 先按 app 列出产物 → 逐个删存储实体 → 再删 DB 记录（DB 记录也可由 CASCADE 兜底，
		// 但显式删避免依赖外键且便于 storage 与 store 一致）。放 Store.Delete 之前避免 race。
		if h.artifactStore != nil && h.artifactStorage != nil {
			ctx := c.Request.Context()
			if arts, lerr := h.artifactStore.ListByApp(ctx, a.ID); lerr == nil {
				for _, art := range arts {
					_ = h.artifactStorage.Delete(ctx, art.StorageKey)
				}
			}
			_ = h.artifactStore.DeleteByApp(ctx, a.ID)
		}
		// 删除平台托管仓库目录(含 .worktrees/ 编码工作台);external 模式(外部仓库,平台不管)不删。
		// 仅认 ManagedRepoBase 下的 RepoDir(前缀校验),防误删任意路径;失败仅 Warn 不阻塞删 app。
		if a.DeployMode != AppExternal && a.RepoDir != "" && strings.HasPrefix(a.RepoDir, ManagedRepoBase) {
			if err := os.RemoveAll(a.RepoDir); err != nil {
				zap.L().Warn("删应用仓库目录失败(不阻塞)", zap.String("app", a.ID), zap.String("repo", a.RepoDir), zap.Error(err))
			}
		}
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
	// 守卫：按需三方比对 DB image ↔ 容器 ↔ deploy.yaml actual（per-instance，无多 env 歧义）。
	// drift.ok=true 一致；InspectImage 失败（容器查不到）时 drift=nil，前端不展示。
	var drift *DriftResult
	mf, _ := LoadDeployManifest(a.RepoDir)
	manifestImg := ""
	if mf != nil {
		manifestImg = mf.Actual.ImageDigest
	}
	if containerImg, dErr := h.deployer.InspectImage(c.Request.Context(), ins.ContainerName); dErr == nil {
		r := checkDrift(ins.Image, containerImg, manifestImg)
		drift = &r
	}
	httpx.OK(c, gin.H{
		"env": env, "url": ins.URL, "deployed": true,
		"stats": stats, "health": appDeployHealth(a, ins), "drift": drift,
	})
}

// appDeployHealth 部署响应的健康字段:headless 用实例 status(无 URL 可探);其余按 URL 探活。
// headless 无端口，ins.URL 为空，probeHealth 只会回 unknown；改用 status(running/degraded/failed) 更准。
func appDeployHealth(a *Application, ins *AppInstance) string {
	if a.AppKind == AppKindHeadless {
		return ins.Status // running/degraded/failed(由 HealthReconciler 维护;刚部署=running)
	}
	return probeHealth(ins.URL)
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
	// 附带每节点应用数 + 最新指标（metricStore 为 nil 时跳过指标填充）
	type nodeWithCount struct {
		DeployNode
		AppCount     int           `json:"app_count"`
		LatestMetric *ServerMetric `json:"latest_metric,omitempty"`
		HasOSCreds   bool          `json:"has_os_creds"`
	}
	out := []nodeWithCount{}
	for _, n := range list {
		cnt, _ := h.nodeStore.AppCount(c.Request.Context(), n.ID)
		item := nodeWithCount{DeployNode: n, AppCount: cnt}
		if h.metricStore != nil {
			if m, err := h.metricStore.Latest(c.Request.Context(), n.ID); err == nil {
				item.LatestMetric = m
			}
		}
		// 掩码凭证：列表不回传敏感字段
		item.WinRMPassword = ""
		item.SSHKey = ""
		item.SSHPassword = ""
		// has_os_creds:有 ssh/winrm 凭证 或 ssh/winrm 类型(前端据此启用采集按钮;用掩码前的原始 n)
		item.HasOSCreds = hasOSCreds(&n)
		out = append(out, item)
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
	// 与 ListNodes/UpdateNode 一致:创建响应不回传敏感凭证(ssh_password/ssh_key/winrm_password)
	n.WinRMPassword = ""
	n.SSHKey = ""
	n.SSHPassword = ""
	httpx.Created(c, n)
}

// UpdateNode 编辑已存在的部署节点（PUT /deploy-nodes/:nid）。
// body 与 CreateNode 一致；ID 取路径参数（不允许改 ID）。
func (h *Handler) UpdateNode(c *gin.Context) {
	if h.nodeStore == nil {
		httpx.Err(c, 500, 50014, "nodeStore 未初始化")
		return
	}
	nid := c.Param("nid")
	if nid == "" {
		httpx.Err(c, 400, 40001, "缺少节点 id")
		return
	}
	var n DeployNode
	if err := c.ShouldBindJSON(&n); err != nil {
		httpx.Err(c, 400, 40001, err.Error())
		return
	}
	n.ID = nid
	// 凭证保留：ListNodes/UpdateNode 返回时把 winrm_password/ssh_key 掩码成空串，
	// 前端编辑保存会回传空串。若直接 Update 会把真实凭证覆盖成空，导致后续采集/部署
	// 鉴权失败（WinRM 空密码网络登录被 Windows 拒、SSH 无 key 无密码）。收到空串时保留 DB 旧值。
	if n.WinRMPassword == "" || n.SSHKey == "" || n.SSHPassword == "" {
		if existing, err := h.nodeStore.Get(c.Request.Context(), nid); err == nil && existing != nil {
			if n.WinRMPassword == "" {
				n.WinRMPassword = existing.WinRMPassword
			}
			if n.SSHKey == "" {
				n.SSHKey = existing.SSHKey
			}
			if n.SSHPassword == "" {
				n.SSHPassword = existing.SSHPassword
			}
		}
	}
	if err := h.nodeStore.Update(c.Request.Context(), &n); err != nil {
		httpx.Err(c, 500, 50014, err.Error())
		return
	}
	got, err := h.nodeStore.Get(c.Request.Context(), nid)
	if err != nil || got == nil {
		httpx.OK(c, gin.H{"id": nid, "updated": true})
		return
	}
	// 与 ListNodes 一致：不回传敏感凭证
	got.WinRMPassword = ""
	got.SSHKey = ""
	got.SSHPassword = ""
	httpx.OK(c, got)
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

// ProvisionNode 异步搭建节点环境（docker_tcp 节点不支持，走 docker -H）。
// 立即返回 provisioning，后台 goroutine 跑 Provisioner 并更新节点 status + last_seen。
func (h *Handler) ProvisionNode(c *gin.Context) {
	if h.nodeStore == nil {
		httpx.Err(c, 500, 50014, "nodeStore 未初始化")
		return
	}
	nid := c.Param("nid")
	n, err := h.nodeStore.Get(c.Request.Context(), nid)
	if err != nil || n == nil {
		httpx.Err(c, 404, 40401, "节点不存在")
		return
	}
	// docker_tcp 节点不走 RemoteExecutor（docker -H 直连），无法 provision
	if n.ConnectType == "docker_tcp" || n.ConnectType == "" {
		httpx.Err(c, 400, 40010, "docker_tcp 节点无需 provision（直接 docker -H 连通）")
		return
	}
	_ = h.nodeStore.SetNodeStatus(c.Request.Context(), nid, "provisioning", "")
	go func() {
		exec, err := NewRemoteExecutor(n)
		if err != nil {
			_ = h.nodeStore.SetNodeStatus(context.Background(), nid, "provision_failed", err.Error())
			return
		}
		log, err := (&Provisioner{}).Provision(context.Background(), n, exec)
		if err != nil {
			_ = h.nodeStore.SetNodeStatus(context.Background(), nid, "provision_failed", log+":"+err.Error())
			return
		}
		_ = h.nodeStore.SetNodeStatus(context.Background(), nid, "ready", log)
	}()
	httpx.OK(c, gin.H{"id": nid, "status": "provisioning"})
}

// CollectNode 手动触发一次节点指标采集（monitor 为 nil 时报未启用）。
func (h *Handler) CollectNode(c *gin.Context) {
	if h.monitor == nil {
		httpx.Err(c, 500, 50020, "监控未启用（monitor 未注入）")
		return
	}
	nid := c.Param("nid")
	n, err := h.nodeStore.Get(c.Request.Context(), nid)
	if err != nil || n == nil {
		httpx.Err(c, 404, 40401, "节点不存在")
		return
	}
	// node_local 走本地采集;其他节点按 OS 凭证决定(与 NewOSExecutor 一致),无凭证返回 skipped。
	if n.ID != "node_local" && !hasOSCreds(n) {
		httpx.OK(c, gin.H{"id": nid, "status": "skipped", "message": "未配置 OS 凭证（SSH/WinRM），无法采集"})
		return
	}
	if err := h.monitor.CollectOnce(c.Request.Context(), nid); err != nil {
		httpx.Err(c, 500, 50020, "采集失败: "+err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": nid, "status": "collected"})
}

// NodeMetrics 返回节点历史指标趋势（默认最近 60 条）。
func (h *Handler) NodeMetrics(c *gin.Context) {
	if h.metricStore == nil {
		httpx.Err(c, 500, 50020, "指标库未启用（metricStore 未注入）")
		return
	}
	nid := c.Param("nid")
	limit := 60
	list, err := h.metricStore.History(c.Request.Context(), nid, limit)
	if err != nil {
		httpx.Err(c, 500, 50020, "加载失败")
		return
	}
	httpx.OK(c, gin.H{"metrics": list})
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
	// external 纳管应用（B 类）无 RepoDir/源码，构建会失败翻转状态；直接拒绝。
	// app_kind 与 deploy_mode 正交：external + desktop 仍走此拒绝分支。
	if a.DeployMode == AppExternal {
		httpx.Err(c, 400, 40001, "external 纳管应用不构建产物（无托管源码）")
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
	// 持久化版本号（I-7）：a.Version++ 只改内存，需写回 appdeploy_application.version，
	// 否则下次构建/前端展示的 version 不递增。即便部分产物上传失败，已落库产物也绑定了此版本号，须持久化。
	if vErr := h.store.UpdateVersion(ctx, a.ID, a.Version); vErr != nil {
		zap.L().Warn("持久化构建版本号失败", zap.String("app", a.ID), zap.Int("version", a.Version), zap.Error(vErr))
	}
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
