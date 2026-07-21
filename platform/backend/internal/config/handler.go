package config

import (
	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/httpx"
)

// Handler 系统配置 HTTP 接口（配置中心化：业务配置入库，从此页管理）。
type Handler struct {
	store *Store
}

// NewHandler 构造 Handler。
func NewHandler(store *Store) *Handler { return &Handler{store: store} }

// Register 模块级装配:main 调用,内部 new handler + 注册路由(减少 main.go 集中 new)。
func Register(r gin.IRouter, store *Store) {
	NewHandler(store).Register(r)
}

// Register 注册路由。
func (h *Handler) Register(r gin.IRouter) {
	r.GET("/config", h.List)
	r.PUT("/config/:key", h.Set)
}

// List 列出全部系统配置。
//
// @Summary      列出全部系统配置
// @Tags         config
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "系统配置列表"
// @Security     BearerAuth
// @Router       /config [get]
func (h *Handler) List(c *gin.Context) {
	httpx.OK(c, h.store.All())
}

type setRequest struct {
	Value       string `json:"value" binding:"required"`
	Category    string `json:"category,omitempty"`
	Description string `json:"description,omitempty"`
}

// Set 新增/更新一条配置（热生效：写入即刷新缓存）。
//
// @Summary      新增/更新一条系统配置
// @Tags         config
// @Accept       json
// @Produce      json
// @Param        key   path  string       true  "配置键"
// @Param        body  body  setRequest   true  "配置值(value)"
// @Success      200  {object}  map[string]interface{}  "{key,value}"
// @Failure      400  {object}  map[string]interface{}  "invalid body"
// @Failure      500  {object}  map[string]interface{}  "服务端错误"
// @Security     BearerAuth
// @Router       /config/{key} [put]
func (h *Handler) Set(c *gin.Context) {
	var in setRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	key := c.Param("key")
	if err := h.store.Set(c.Request.Context(), key, in.Value, in.Category, in.Description); err != nil {
		httpx.Err(c, 500, 50005, err.Error())
		return
	}
	httpx.OK(c, gin.H{"key": key, "value": in.Value})
}
