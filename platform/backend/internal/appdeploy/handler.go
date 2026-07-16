package appdeploy

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/codews"
	"zhiyuan-anp/platform/backend/internal/httpx"
)

// Handler 应用部署 HTTP 接口。
type Handler struct {
	store    *Store
	deployer *Deployer
	codeWS   *codews.Manager // 交互编码工作台（opencode serve）；nil=未启用
}

// NewHandler 构造。codeWS 可为 nil（不启用交互编码）。
func NewHandler(store *Store, deployer *Deployer, codeWS *codews.Manager) *Handler {
	return &Handler{store: store, deployer: deployer, codeWS: codeWS}
}

// Register 注册路由。
func (h *Handler) Register(r gin.IRouter) {
	r.GET("/project-spaces/:id/apps", h.List)
	r.POST("/project-spaces/:id/apps", h.Create)
	r.GET("/project-spaces/:id/apps/:aid/detail", h.Detail)
	r.POST("/project-spaces/:id/apps/:aid/deploy", h.Deploy)     // 部署到 test（默认）或指定 env
	r.POST("/project-spaces/:id/apps/:aid/promote", h.Promote)   // 上线 = 部署到 prod
	r.POST("/project-spaces/:id/apps/:aid/deploy-commit", h.DeployCommit)
	r.POST("/project-spaces/:id/apps/:aid/stop", h.Stop)
	r.POST("/project-spaces/:id/apps/:aid/start", h.Start)
	r.DELETE("/project-spaces/:id/apps/:aid", h.Delete)
	r.POST("/project-spaces/:id/apps/:aid/workspace", h.Workspace) // 启动交互编码工作台
	r.GET("/project-spaces/:id/apps/:aid/env", h.ListEnv)          // 应用运行时环境变量
	r.POST("/project-spaces/:id/apps/:aid/env", h.UpsertEnv)
	r.DELETE("/project-spaces/:id/apps/:aid/env/:key", h.DeleteEnv)
	r.GET("/project-spaces/:id/apps/:aid/stats", h.Stats) // 资源占用 + 健康探测
	r.GET("/project-spaces/:id/apps/:aid/logs", h.Logs)
}

// List 应用列表，附带各环境实例（前端展示 test/prod URL）。
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
	user := c.GetHeader("X-User") // 开发者身份（不同开发者可各选各的工具）
	if user == "" {
		user = "anonymous"
	}
	s, err := h.codeWS.Ensure(aid, a.RepoDir, user, in.Tool)
	if err != nil {
		httpx.Err(c, 500, 50021, err.Error())
		return
	}
	httpx.OK(c, gin.H{"app_id": aid, "user": user, "tool": s.Tool, "url": s.URL, "port": s.Port, "session_id": s.SessionID, "note": s.Tool + " 工作台已就绪（开发者 " + user + "），浏览器打开 url 即可交互编码"})
}

// ListEnv 列出应用运行时环境变量（部署时 docker run -e 注入）。is_secret 的 value 接口层 mask（不泄露）。
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

// Create 注册一个产出应用，并初始化其托管 git 仓库（代码归属确立：/data/repos/<name>）。
func (h *Handler) Create(c *gin.Context) {
	var in createBody
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
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
func (h *Handler) Promote(c *gin.Context) {
	psID, aid := c.Param("id"), c.Param("aid")
	if a, _ := h.store.Get(c.Request.Context(), psID, aid); a == nil || a.ID == "" {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	go h.buildAndDeploy(psID, aid, "", EnvProd)
	httpx.OK(c, gin.H{"id": aid, "env": EnvProd, "status": "building", "note": "上线中：部署到 prod 环境"})
}

// DeployCommit 部署/回滚到指定历史版本（默认 test 环境）。
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
	// 清理该环境旧容器
	if ins.ContainerName != "" {
		_, _ = h.deployer.Remove(ctx, ins.ContainerName)
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
func (h *Handler) Delete(c *gin.Context) {
	a, _ := h.store.Get(c.Request.Context(), c.Param("id"), c.Param("aid"))
	if a != nil {
		inss, _ := h.store.ListInstancesByApp(c.Request.Context(), a.ID)
		for _, ins := range inss {
			if ins.ContainerName != "" {
				_, _ = h.deployer.Remove(c.Request.Context(), ins.ContainerName)
			}
		}
		if a.ContainerName != "" { // 兜底：旧概览容器名
			_, _ = h.deployer.Remove(c.Request.Context(), a.ContainerName)
		}
	}
	if err := h.store.Delete(c.Request.Context(), c.Param("id"), c.Param("aid")); err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": c.Param("aid"), "deleted": true})
}

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
