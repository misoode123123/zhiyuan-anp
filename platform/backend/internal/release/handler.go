package release

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/appdeploy"
	"zhiyuan-anp/platform/backend/internal/change"
	"zhiyuan-anp/platform/backend/internal/config"
	"zhiyuan-anp/platform/backend/internal/httpx"
	"zhiyuan-anp/platform/backend/internal/requirement"
)

// TestGate 测试门禁：查某需求 passed 测试用例数（由 qa.Store 实现）。
type TestGate interface {
	PassedCountByRequirement(ctx context.Context, reqID string) (int, error)
}

// Handler 发布中心 HTTP 接口。
type Handler struct {
	store     *Store
	changes   *change.Store
	reqRepo   *requirement.Repository
	appDeploy *appdeploy.Handler // 可选：发布后自动构建部署产出应用（板块06 M2）
	cfg       *config.Store      // 可选：读发布门禁开关
	testGate  TestGate           // 可选：测试门禁查询
}

// NewHandler 构造 Handler。appDeploy/cfg/testGate 均可为 nil（不启用对应能力）。
func NewHandler(store *Store, changes *change.Store, reqRepo *requirement.Repository, appDeploy *appdeploy.Handler, cfg *config.Store, testGate TestGate) *Handler {
	return &Handler{store: store, changes: changes, reqRepo: reqRepo, appDeploy: appDeploy, cfg: cfg, testGate: testGate}
}

// testGateEnabled 发布测试门禁是否启用（开关 release_require_passed_test=true 且依赖已注入）。
func (h *Handler) testGateEnabled() bool {
	return h.cfg != nil && h.testGate != nil && h.cfg.Get("release_require_passed_test", "false") == "true"
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
	// 先取变更（门禁预检 + 后续部署都需要 source_id）。
	var chg *change.ChangeRequest
	if h.changes != nil {
		chg, _ = h.changes.Get(c.Request.Context(), in.ChangeID)
	}
	// 🧪 测试门禁：开关开时，来源需求须至少 1 条 passed 测试用例，否则拒绝发布。
	if h.testGateEnabled() && chg != nil && chg.SourceID != "" {
		if passed, _ := h.testGate.PassedCountByRequirement(c.Request.Context(), chg.SourceID); passed <= 0 {
			httpx.Err(c, 409, 40901, "发布被测试门禁拦截：来源需求无 passed 测试用例。请先到「测试中心」生成用例并运行至至少 1 条 passed，或在「系统配置」关闭 release_require_passed_test")
			return
		}
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
	if h.changes != nil && chg != nil && chg.ID != "" && chg.SourceID != "" {
		if h.reqRepo != nil {
			_ = h.reqRepo.UpdateStatus(c.Request.Context(), chg.SourceID, "delivered")
		}
	}
	// 可选：自动构建部署产出应用。
	// 应用一等公民：优先按来源需求已归属的应用部署（无需手填 deploy_name）；
	// 兼容未归属应用时按名称/repo 部署并回填归属。
	deployed := ""
	if in.Deploy && h.appDeploy != nil && chg != nil {
		appID := ""
		if chg.SourceID != "" {
			if req, e := h.reqRepo.Get(c.Request.Context(), chg.SourceID); e == nil && req != nil {
				appID = req.ApplicationID
			}
		}
		if appID != "" {
			if app, e := h.appDeploy.DeployByAppID(context.Background(), appID); e == nil && app != nil {
				deployed = app.Name
			}
		} else if chg.RepoDir != "" {
			name := in.DeployName
			if name == "" {
				name = filepath.Base(chg.RepoDir)
			}
			port := in.DeployPort
			if port == 0 {
				port = 8080
			}
			if app, e := h.appDeploy.DeployForRelease(context.Background(), psID, name, chg.RepoDir, port); e == nil && app != nil && chg.SourceID != "" {
				_ = h.reqRepo.SetApplication(context.Background(), chg.SourceID, app.ID)
				deployed = app.Name
			}
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
