package qa

import (
	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/httpx"
	"zhiyuan-anp/platform/backend/internal/requirement"
)

// Handler 测试中心 HTTP 接口。
type Handler struct {
	svc     *Service
	reqRepo *requirement.Repository
}

// NewHandler 构造 Handler（reqRepo 用于读取需求验收标准）。
func NewHandler(svc *Service, reqRepo *requirement.Repository) *Handler {
	return &Handler{svc: svc, reqRepo: reqRepo}
}

// Register 注册路由。
func (h *Handler) Register(r gin.IRouter) {
	r.POST("/project-spaces/:id/requirements/:rid/generate-tests", h.Generate)
	r.GET("/project-spaces/:id/test-cases", h.List)
}

// Generate 把需求验收标准转为测试用例并入库。
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
func (h *Handler) List(c *gin.Context) {
	list, err := h.svc.store.ListByProjectSpace(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50008, err.Error())
		return
	}
	httpx.OK(c, list)
}
