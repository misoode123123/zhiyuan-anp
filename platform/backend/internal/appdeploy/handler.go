package appdeploy

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/httpx"
)

// Handler 应用部署 HTTP 接口。
type Handler struct {
	store    *Store
	deployer *Deployer
}

// NewHandler 构造。
func NewHandler(store *Store, deployer *Deployer) *Handler {
	return &Handler{store: store, deployer: deployer}
}

// Register 注册路由。
func (h *Handler) Register(r gin.IRouter) {
	r.GET("/project-spaces/:id/apps", h.List)
	r.POST("/project-spaces/:id/apps", h.Create)
	r.POST("/project-spaces/:id/apps/:aid/deploy", h.Deploy)
	r.POST("/project-spaces/:id/apps/:aid/stop", h.Stop)
	r.POST("/project-spaces/:id/apps/:aid/start", h.Start)
	r.DELETE("/project-spaces/:id/apps/:aid", h.Delete)
	r.GET("/project-spaces/:id/apps/:aid/logs", h.Logs)
}

func (h *Handler) List(c *gin.Context) {
	list, err := h.store.List(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	httpx.OK(c, list)
}

type createBody struct {
	Name         string `json:"name" binding:"required"`
	RepoDir      string `json:"repo_dir" binding:"required"` // docker 守护进程可见的源码路径（含 Dockerfile）
	InternalPort int    `json:"internal_port" binding:"required"`
}

// Create 注册一个产出应用。
func (h *Handler) Create(c *gin.Context) {
	var in createBody
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	a := &Application{ProjectSpaceID: c.Param("id"), Name: in.Name, RepoDir: in.RepoDir, InternalPort: in.InternalPort}
	if err := h.store.Create(c.Request.Context(), a); err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	httpx.Created(c, a)
}

// Deploy 异步构建并部署：docker build → docker run → 分配端口 → 回写 URL。
// 立即返回（status=building），goroutine 完成后置 running/failed。
func (h *Handler) Deploy(c *gin.Context) {
	psID, aid := c.Param("id"), c.Param("aid")
	a, err := h.store.Get(c.Request.Context(), psID, aid)
	if err != nil || a == nil || a.ID == "" {
		httpx.Err(c, 404, 40420, "应用不存在")
		return
	}
	_ = h.store.SetStatus(c.Request.Context(), psID, aid, "building", "", "")
	go h.buildAndDeploy(psID, aid)
	httpx.OK(c, gin.H{"id": aid, "status": "building", "note": "异步构建部署中，轮询列表查状态"})
}

// buildAndDeploy 后台执行（脱离 HTTP context）。
func (h *Handler) buildAndDeploy(psID, aid string) {
	ctx := context.Background()
	a, err := h.store.Get(ctx, psID, aid)
	if err != nil || a == nil || a.ID == "" {
		return
	}
	// 若已有旧容器，先清理（重新部署）
	if a.ContainerName != "" {
		_, _ = h.deployer.Remove(ctx, a.ContainerName)
	}
	// 0. 确保 Dockerfile：无则按 buildpack 检测类型自动生成；采纳检测到的内部端口
	note := ""
	if gen, port, err := EnsureDockerfile(a.RepoDir, a.InternalPort); err == nil {
		if port != 0 && port != a.InternalPort {
			a.InternalPort = port
		}
		if gen {
			note = "buildpack 已按源码类型自动生成 Dockerfile\n"
		}
	}
	// 1. 构建
	log, err := h.deployer.Build(ctx, a)
	if note != "" {
		log = note + log
	}
	if err != nil {
		_ = h.store.SetStatus(ctx, psID, aid, "failed", err.Error(), tail(log, 2000))
		return
	}
	_ = h.store.SetStatus(ctx, psID, aid, "building", "", tail(log, 2000))
	// 2. 部署
	if err := h.deployer.Deploy(ctx, a); err != nil {
		_ = h.store.SetStatus(ctx, psID, aid, "failed", err.Error(), tail(log, 2000))
		return
	}
	a.Status = "running"
	a.LastError = ""
	a.BuildLog = tail(log, 2000)
	_ = h.store.UpdateDeploy(ctx, a)
}

func (h *Handler) Stop(c *gin.Context) {
	a, _ := h.store.Get(c.Request.Context(), c.Param("id"), c.Param("aid"))
	if a == nil || a.ContainerName == "" {
		httpx.Err(c, 400, 50020, "应用未部署")
		return
	}
	if _, err := h.deployer.Stop(c.Request.Context(), a.ContainerName); err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	_ = h.store.SetStatus(c.Request.Context(), c.Param("id"), c.Param("aid"), "stopped", "", "")
	httpx.OK(c, gin.H{"id": a.ID, "status": "stopped"})
}

func (h *Handler) Start(c *gin.Context) {
	a, _ := h.store.Get(c.Request.Context(), c.Param("id"), c.Param("aid"))
	if a == nil || a.ContainerName == "" {
		httpx.Err(c, 400, 50020, "应用未部署")
		return
	}
	if _, err := h.deployer.Start(c.Request.Context(), a.ContainerName); err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	_ = h.store.SetStatus(c.Request.Context(), c.Param("id"), c.Param("aid"), "running", "", "")
	httpx.OK(c, gin.H{"id": a.ID, "status": "running"})
}

func (h *Handler) Delete(c *gin.Context) {
	a, _ := h.store.Get(c.Request.Context(), c.Param("id"), c.Param("aid"))
	if a != nil && a.ContainerName != "" {
		_, _ = h.deployer.Remove(c.Request.Context(), a.ContainerName)
	}
	if err := h.store.Delete(c.Request.Context(), c.Param("id"), c.Param("aid")); err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": c.Param("aid"), "deleted": true})
}

func (h *Handler) Logs(c *gin.Context) {
	a, _ := h.store.Get(c.Request.Context(), c.Param("id"), c.Param("aid"))
	if a == nil || a.ContainerName == "" {
		httpx.OK(c, gin.H{"logs": "(应用未部署)"})
		return
	}
	log, err := h.deployer.Logs(c.Request.Context(), a.ContainerName, 200)
	if err != nil {
		httpx.Err(c, 500, 50020, err.Error())
		return
	}
	httpx.OK(c, gin.H{"logs": log})
}

// DeployForRelease 供发布中心调用：按 repo_dir 找/建应用并触发部署。
// 返回应用的 URL（部署完成由后台置位，调用方即时拿到 building 状态）。
func (h *Handler) DeployForRelease(ctx context.Context, psID, name, repoDir string, internalPort int) (*Application, error) {
	a, _ := h.store.GetByName(ctx, psID, name)
	if a == nil || a.ID == "" {
		a = &Application{ProjectSpaceID: psID, Name: name, RepoDir: repoDir, InternalPort: internalPort}
		if err := h.store.Create(ctx, a); err != nil {
			return nil, err
		}
	} else if a.RepoDir == "" {
		a.RepoDir = repoDir
	}
	_ = h.store.SetStatus(ctx, psID, a.ID, "building", "", "")
	go h.buildAndDeploy(psID, a.ID)
	return a, nil
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}
