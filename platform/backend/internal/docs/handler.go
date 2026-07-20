package docs

import (
	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/httpx"
)

// Handler 方案文档中心 HTTP 接口。
type Handler struct {
	svc *Service
}

// NewHandler 构造。
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register 注册路由。
func (h *Handler) Register(r gin.IRouter) {
	r.GET("/docs", h.List)
	r.GET("/docs/content", h.Content)
	r.GET("/docs/search", h.Search)
}

// List 全部方案文档。
//
// @Summary      列出全部方案文档
// @Tags         docs
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "文档列表"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /docs [get]
func (h *Handler) List(c *gin.Context) {
	list, err := h.svc.List()
	if err != nil {
		httpx.Err(c, 500, 50010, err.Error())
		return
	}
	httpx.OK(c, list)
}

// Content 取某文档原文。
//
// @Summary      获取文档原文
// @Tags         docs
// @Produce      json
// @Param        path  query  string  true  "文档路径"
// @Success      200  {object}  map[string]interface{}  "{path,content}"
// @Failure      400  {object}  map[string]interface{}  "path 必填"
// @Failure      500  {object}  map[string]interface{}  "读取失败"
// @Security     BearerAuth
// @Router       /docs/content [get]
func (h *Handler) Content(c *gin.Context) {
	p := c.Query("path")
	if p == "" {
		httpx.Err(c, 400, 40001, "path 必填")
		return
	}
	content, err := h.svc.Content(p)
	if err != nil {
		httpx.Err(c, 500, 50010, err.Error())
		return
	}
	httpx.OK(c, gin.H{"path": p, "content": content})
}

// Search 关键字搜索。
//
// @Summary      关键字搜索文档
// @Tags         docs
// @Produce      json
// @Param        q  query  string  true  "关键字"
// @Success      200  {object}  map[string]interface{}  "匹配结果列表"
// @Failure      500  {object}  map[string]interface{}  "搜索失败"
// @Security     BearerAuth
// @Router       /docs/search [get]
func (h *Handler) Search(c *gin.Context) {
	list, err := h.svc.Search(c.Query("q"))
	if err != nil {
		httpx.Err(c, 500, 50010, err.Error())
		return
	}
	httpx.OK(c, list)
}
