package change

import (
	"errors"

	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/auth"
	"zhiyuan-anp/platform/backend/internal/httpx"
	"zhiyuan-anp/platform/backend/internal/notif"
)

// Handler 变更闸门 HTTP 接口。
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
	r.GET("/changes", h.List)
	r.POST("/changes/:id/approve", h.Approve)
	r.POST("/changes/:id/reject", h.Reject)
}

// List 列出变更（?status=pending|approved|rejected，默认全部）。
//
// @Summary      列出变更
// @Tags         change
// @Produce      json
// @Param        status  query  string  false  "状态过滤(pending|approved|rejected)"
// @Success      200  {object}  map[string]interface{}  "变更列表"
// @Security     BearerAuth
// @Router       /changes [get]
func (h *Handler) List(c *gin.Context) {
	list, err := h.store.List(c.Request.Context(), c.Query("status"))
	if err != nil {
		httpx.Err(c, 500, 50007, err.Error())
		return
	}
	httpx.OK(c, list)
}

// Approve 批准（🚪G3 通过 → 合入）。
//
// @Summary      批准变更
// @Tags         change
// @Produce      json
// @Param        id      path    string  true   "变更ID"
// @Param        X-User  header  string  false  "审批人(默认 user)"
// @Success      200  {object}  map[string]interface{}  "approved"
// @Failure      409  {object}  map[string]interface{}  "非 pending 状态"
// @Security     BearerAuth
// @Router       /changes/{id}/approve [post]
func (h *Handler) Approve(c *gin.Context) {
	id := c.Param("id")
	chg, err := h.store.Get(c.Request.Context(), id)
	if err != nil || chg == nil || chg.ID == "" {
		httpx.Err(c, 404, 40401, "变更不存在")
		return
	}
	// 禁自批：作者（change.user_id 存 usr_id）不得审批自己的变更，需他人复核。
	if reviewerDBID := c.GetString(auth.CtxUserDBID); chg.UserID != "" && chg.UserID == reviewerDBID {
		httpx.Err(c, 403, 40302, "不可审批自己的变更（需他人复核）")
		return
	}
	if err := h.store.Decide(c.Request.Context(), id, "approved", reviewer(c)); err != nil {
		if errors.Is(err, errNotPending) {
			httpx.Err(c, 409, 40901, err.Error())
			return
		}
		httpx.Err(c, 500, 50007, err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": id, "status": "approved", "message": "🚪G3 通过，可合入"})
	notif.EmitBroadcast("change_decided", "变更已批准", "变更 "+id+" 已通过 G3 审批，可发布", "/release")
}

// Reject 拒绝（需回滚/重做）。
//
// @Summary      拒绝变更
// @Tags         change
// @Produce      json
// @Param        id      path    string  true   "变更ID"
// @Param        X-User  header  string  false  "审批人(默认 user)"
// @Success      200  {object}  map[string]interface{}  "rejected"
// @Failure      409  {object}  map[string]interface{}  "非 pending 状态"
// @Security     BearerAuth
// @Router       /changes/{id}/reject [post]
func (h *Handler) Reject(c *gin.Context) {
	if err := h.store.Decide(c.Request.Context(), c.Param("id"), "rejected", reviewer(c)); err != nil {
		if errors.Is(err, errNotPending) {
			httpx.Err(c, 409, 40901, err.Error())
			return
		}
		httpx.Err(c, 500, 50007, err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": c.Param("id"), "status": "rejected", "message": "已拒绝，需回滚或重做"})
	notif.EmitBroadcast("change_decided", "变更已拒绝", "变更 "+c.Param("id")+" 被拒绝，需回滚", "/approvals")
}

// reviewer M1 取自请求头 X-User，默认 user。
func reviewer(c *gin.Context) string {
	if u := c.GetString(auth.CtxUserID); u != "" {
		return u
	}
	return "user"
}
