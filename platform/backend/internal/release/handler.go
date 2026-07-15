package release

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/appdeploy"
	"zhiyuan-anp/platform/backend/internal/change"
	"zhiyuan-anp/platform/backend/internal/httpx"
	"zhiyuan-anp/platform/backend/internal/requirement"
)

// Handler 发布中心 HTTP 接口。
type Handler struct {
	store     *Store
	changes   *change.Store
	reqRepo   *requirement.Repository
	appDeploy *appdeploy.Handler // 可选：发布后自动构建部署产出应用（板块06 M2）
}

// NewHandler 构造 Handler。appDeploy 可为 nil（不启用自动部署）。
func NewHandler(store *Store, changes *change.Store, reqRepo *requirement.Repository, appDeploy *appdeploy.Handler) *Handler {
	return &Handler{store: store, changes: changes, reqRepo: reqRepo, appDeploy: appDeploy}
}

// Register 注册路由。
func (h *Handler) Register(r gin.IRouter) {
	r.POST("/project-spaces/:id/releases", h.Create)
	r.GET("/project-spaces/:id/releases", h.List)
}

type createRequest struct {
	ChangeID   string `json:"change_id" binding:"required"`
	Deploy     bool   `json:"deploy"`      // 发布后自动构建部署产出应用
	DeployName string `json:"deploy_name"` // 应用名，空则取 repo_dir 末段
	DeployPort int    `json:"deploy_port"` // 应用容器内端口，默认 8080
}

// Create 把已审批变更发布上线（🚪G5 后），版本号自增；
// 并追溯 change.source_id → 标记来源需求为"已交付"（需求生命周期闭环）。
// 若 deploy=true 且变更含 repo_dir，自动触发应用部署引擎构建部署。
func (h *Handler) Create(c *gin.Context) {
	psID := c.Param("id")
	var in createRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	n, err := h.store.Count(c.Request.Context(), psID)
	if err != nil {
		httpx.Err(c, 500, 50009, err.Error())
		return
	}
	r := &Release{
		ProjectSpaceID: psID,
		ChangeID:       in.ChangeID,
		Version:        fmt.Sprintf("v%d", n+1),
		Status:         "released",
	}
	if err := h.store.Create(c.Request.Context(), r); err != nil {
		httpx.Err(c, 500, 50009, err.Error())
		return
	}
	// 追溯 change → 标记来源需求"已交付"
	var chg *change.ChangeRequest
	if h.changes != nil {
		var errc error
		chg, errc = h.changes.Get(c.Request.Context(), in.ChangeID)
		if errc == nil && chg != nil && chg.ID != "" && chg.SourceID != "" {
			if h.reqRepo != nil {
				_ = h.reqRepo.UpdateStatus(c.Request.Context(), chg.SourceID, "delivered")
			}
		}
	}
	// 可选：自动构建部署产出应用，并把来源需求归属到该应用（应用一等公民）
	deployed := ""
	if in.Deploy && h.appDeploy != nil && chg != nil && chg.RepoDir != "" {
		name := in.DeployName
		if name == "" {
			name = filepath.Base(chg.RepoDir)
		}
		port := in.DeployPort
		if port == 0 {
			port = 8080
		}
		app, derr := h.appDeploy.DeployForRelease(context.Background(), psID, name, chg.RepoDir, port)
		deployed = name
		// DeployForRelease 已返回应用 id（构建在其内部异步）；回填需求→应用归属
		if derr == nil && app != nil && app.ID != "" && chg.SourceID != "" {
			_ = h.reqRepo.SetApplication(context.Background(), chg.SourceID, app.ID)
		}
	}
	httpx.Created(c, gin.H{
		"id": r.ID, "version": r.Version, "status": r.Status,
		"deploy_triggered": deployed,
		"note":             ternary(deployed == "", "需求已交付", "应用 "+deployed+" 异步构建部署中，见「应用部署」页"),
	})
}

// List 发布历史。
func (h *Handler) List(c *gin.Context) {
	list, err := h.store.List(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50009, err.Error())
		return
	}
	httpx.OK(c, list)
}

func ternary(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
