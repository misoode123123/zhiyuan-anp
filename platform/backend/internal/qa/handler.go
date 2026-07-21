package qa

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"

	"zhiyuan-anp/platform/backend/internal/httpx"
	"zhiyuan-anp/platform/backend/internal/requirement"
)

// AppURLResolver 按应用 id 解析其运行 URL（由 appdeploy.Store 实现）。
// 自动验收时，测试中心用它拿到被测应用的访问地址。
type AppURLResolver interface {
	AppURLByAppID(ctx context.Context, appID string) (string, error)
}

// Handler 测试中心 HTTP 接口。
type Handler struct {
	svc     *Service
	reqRepo *requirement.Repository
	apps    AppURLResolver // 可为 nil（则用例运行恒为 manual）
}

// NewHandler 构造 Handler（reqRepo 读需求验收标准 + 解析归属应用；apps 解析应用运行 URL）。
func NewHandler(svc *Service, reqRepo *requirement.Repository, apps AppURLResolver) *Handler {
	return &Handler{svc: svc, reqRepo: reqRepo, apps: apps}
}

// Register 模块级装配：内部 NewStore(db)+NewService+NewHandler+Register。
// apps 用本包 AppURLResolver interface（由 *appdeploy.Store 实现），避免 import appdeploy。
func Register(r gin.IRouter, db *sqlx.DB, agentRuntimeURL string, reqRepo *requirement.Repository, apps AppURLResolver) {
	NewHandler(NewService(NewStore(db), agentRuntimeURL), reqRepo, apps).Register(r)
}

// Register 注册路由。
func (h *Handler) Register(r gin.IRouter) {
	r.POST("/project-spaces/:id/requirements/:rid/generate-tests", h.Generate)
	r.GET("/project-spaces/:id/test-cases", h.List)
	r.POST("/project-spaces/:id/test-cases/:tcid/run", h.Run)              // 单条自动验收
	r.POST("/project-spaces/:id/requirements/:rid/run-tests", h.RunForReq) // 批量验收某需求的用例
}

// Generate 把需求验收标准转为测试用例并入库。
//
// @Summary      根据需求生成测试用例
// @Tags         qa
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Param        rid  path  string  true  "需求ID"
// @Success      200  {object}  map[string]interface{}  "生成的测试用例"
// @Failure      404  {object}  map[string]interface{}  "需求不存在"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/requirements/{rid}/generate-tests [post]
func (h *Handler) Generate(c *gin.Context) {
	psID := c.Param("id")
	rid := c.Param("rid")
	req, err := h.reqRepo.Get(c.Request.Context(), rid)
	if err != nil || req == nil || req.ID == "" {
		httpx.Err(c, 404, 40401, "需求不存在")
		return
	}
	cases, err := h.svc.GenerateTests(c.Request.Context(), psID, rid, req.Title, req.AcceptanceCriteria)
	if err != nil {
		httpx.Err(c, 500, 50008, err.Error())
		return
	}
	httpx.Created(c, cases)
}

// List 列出项目空间下的测试用例。
//
// @Summary      测试用例列表
// @Tags         qa
// @Produce      json
// @Param        id  path  string  true  "项目空间ID"
// @Success      200  {object}  map[string]interface{}  "测试用例列表"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/test-cases [get]
func (h *Handler) List(c *gin.Context) {
	list, err := h.svc.ListByProjectSpace(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50008, err.Error())
		return
	}
	httpx.OK(c, list)
}

// Run 单条自动验收：对着该用例归属应用的运行 URL 真发请求、比对期望、写回结果。
//
// @Summary      运行单条测试用例(自动验收)
// @Tags         qa
// @Produce      json
// @Param        id    path  string  true  "项目空间ID"
// @Param        tcid  path  string  true  "测试用例ID"
// @Success      200  {object}  map[string]interface{}  "运行后的用例(含结果)"
// @Failure      404  {object}  map[string]interface{}  "用例不存在"
// @Failure      500  {object}  map[string]interface{}  "运行失败"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/test-cases/{tcid}/run [post]
func (h *Handler) Run(c *gin.Context) {
	tc, err := h.svc.GetCase(c.Request.Context(), c.Param("tcid"))
	if err != nil || tc == nil || tc.ID == "" {
		httpx.Err(c, 404, 40402, "用例不存在")
		return
	}
	base, _ := h.baseURLForReq(c.Request.Context(), tc.RequirementID)
	if err := h.svc.RunHTTPRequest(c.Request.Context(), tc, base); err != nil {
		httpx.Err(c, 500, 50008, err.Error())
		return
	}
	httpx.OK(c, tc)
}

// RunForReq 批量验收某需求下所有用例（统一对着该需求归属应用的 URL）。
//
// @Summary      批量运行需求的测试用例
// @Tags         qa
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Param        rid  path  string  true  "需求ID"
// @Success      200  {object}  map[string]interface{}  "批量验收结果(total/passed/failed/manual)"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/requirements/{rid}/run-tests [post]
func (h *Handler) RunForReq(c *gin.Context) {
	rid := c.Param("rid")
	list, err := h.svc.ListByRequirement(c.Request.Context(), rid)
	if err != nil {
		httpx.Err(c, 500, 50008, err.Error())
		return
	}
	base, _ := h.baseURLForReq(c.Request.Context(), rid)
	passed, failed, manual := 0, 0, 0
	for i := range list {
		tc := &list[i]
		_ = h.svc.RunHTTPRequest(c.Request.Context(), tc, base)
		switch tc.Status {
		case "passed":
			passed++
		case "failed":
			failed++
		case "manual":
			manual++
		}
	}
	httpx.OK(c, gin.H{"total": len(list), "passed": passed, "failed": failed, "manual": manual, "base_url": base})
}

// baseURLForReq 按需求归属应用解析其运行 URL；未归属/未部署返回空（由 RunHTTPRequest 判为 manual）。
func (h *Handler) baseURLForReq(ctx context.Context, reqID string) (string, error) {
	if h.apps == nil {
		return "", nil
	}
	req, err := h.reqRepo.Get(ctx, reqID)
	if err != nil || req == nil || req.ID == "" || req.ApplicationID == "" {
		return "", nil
	}
	url, err := h.apps.AppURLByAppID(ctx, req.ApplicationID)
	if err != nil {
		return "", nil
	}
	return url, nil
}
