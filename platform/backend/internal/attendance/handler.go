package attendance

import (
	"time"

	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/auth"
	"zhiyuan-anp/platform/backend/internal/httpx"
)

// Handler 考勤管理 HTTP 接口。
type Handler struct {
	svc *Service
}

// NewHandler 构造 Handler。
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register 模块级装配:main 调用,内部 new handler + 注册路由(减少 main.go 集中 new)。
func Register(r gin.IRouter, svc *Service) {
	NewHandler(svc).Register(r)
}

// Register 注册路由。
func (h *Handler) Register(r gin.IRouter) {
	r.POST("/project-spaces/:id/attendance", h.Submit)
	r.GET("/project-spaces/:id/attendance", h.List)
	r.GET("/project-spaces/:id/attendance/mine", h.Mine)
	r.GET("/attendance/inbox", h.Inbox)
	r.POST("/attendance/:id/approve", h.Approve)
	r.POST("/attendance/:id/reject", h.Reject)
}

type submitRequest struct {
	Status       string `json:"status" binding:"required"`     // rest/overtime/leave
	StartTime    string `json:"start_time" binding:"required"` // RFC3339
	EndTime      string `json:"end_time" binding:"required"`   // RFC3339
	Reason       string `json:"reason,omitempty"`
	SupervisorID string `json:"supervisor_id" binding:"required"` // 直接上级，提交后转其审批
}

// Submit 员工提交考勤（选择状态、起止时间）→ 入库并转直接上级审批。
//
// @Summary      提交考勤记录
// @Tags         attendance
// @Accept       json
// @Produce      json
// @Param        id    path  string         true  "项目空间ID"
// @Param        body  body  submitRequest  true  "考勤提交内容(status+start_time+end_time+supervisor_id)"
// @Success      200   {object}  map[string]interface{}  "创建的考勤记录"
// @Failure      400   {object}  map[string]interface{}  "invalid body / 时间格式错误"
// @Failure      500   {object}  map[string]interface{}  "服务端错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/attendance [post]
func (h *Handler) Submit(c *gin.Context) {
	var in submitRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, "invalid body: "+err.Error())
		return
	}
	start, err := time.Parse(time.RFC3339, in.StartTime)
	if err != nil {
		httpx.Err(c, 400, 40001, "start_time 需为 RFC3339 格式")
		return
	}
	end, err := time.Parse(time.RFC3339, in.EndTime)
	if err != nil {
		httpx.Err(c, 400, 40001, "end_time 需为 RFC3339 格式")
		return
	}
	rec, err := h.svc.Submit(c.Request.Context(), SubmitInput{
		ProjectSpaceID: c.Param("id"),
		UserID:         c.GetString(auth.CtxUserID),
		Status:         in.Status,
		StartTime:      start,
		EndTime:        end,
		Reason:         in.Reason,
		SupervisorID:   in.SupervisorID,
	})
	if err != nil {
		httpx.Err(c, 500, 50013, err.Error())
		return
	}
	httpx.Created(c, rec)
}

// List 列出项目空间下的考勤记录。
//
// @Summary      列出项目空间下的考勤记录
// @Tags         attendance
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Success      200  {object}  map[string]interface{}  "考勤记录列表"
// @Failure      500  {object}  map[string]interface{}  "服务端错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/attendance [get]
func (h *Handler) List(c *gin.Context) {
	list, err := h.svc.List(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50013, err.Error())
		return
	}
	httpx.OK(c, list)
}

// Mine 列出当前员工的考勤记录。
//
// @Summary      当前员工的考勤记录
// @Tags         attendance
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Success      200  {object}  map[string]interface{}  "当前用户的考勤记录列表"
// @Failure      500  {object}  map[string]interface{}  "服务端错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/attendance/mine [get]
func (h *Handler) Mine(c *gin.Context) {
	list, err := h.svc.ListMine(c.Request.Context(), c.Param("id"), c.GetString(auth.CtxUserID))
	if err != nil {
		httpx.Err(c, 500, 50013, err.Error())
		return
	}
	httpx.OK(c, list)
}

// Inbox 列出待当前用户（作为直接上级）审批的考勤记录。
//
// @Summary      待我审批的考勤收件箱
// @Tags         attendance
// @Produce      json
// @Param        status  query  string  false  "状态过滤(pending/approved/rejected)"
// @Success      200  {object}  map[string]interface{}  "待审批考勤列表"
// @Failure      500  {object}  map[string]interface{}  "服务端错误"
// @Security     BearerAuth
// @Router       /attendance/inbox [get]
func (h *Handler) Inbox(c *gin.Context) {
	list, err := h.svc.Inbox(c.Request.Context(), c.GetString(auth.CtxUserID), c.Query("status"))
	if err != nil {
		httpx.Err(c, 500, 50013, err.Error())
		return
	}
	httpx.OK(c, list)
}

// Approve 直接上级审批通过。
//
// @Summary      审批通过考勤
// @Tags         attendance
// @Produce      json
// @Param        id   path  string  true  "考勤记录ID"
// @Success      200  {object}  map[string]interface{}  "审批后的考勤记录"
// @Failure      500  {object}  map[string]interface{}  "服务端错误"
// @Security     BearerAuth
// @Router       /attendance/{id}/approve [post]
func (h *Handler) Approve(c *gin.Context) {
	rec, err := h.svc.Approve(c.Request.Context(), c.Param("id"), c.GetString(auth.CtxUserID))
	if err != nil {
		httpx.Err(c, 500, 50013, err.Error())
		return
	}
	httpx.OK(c, rec)
}

// Reject 直接上级审批驳回。
//
// @Summary      驳回考勤
// @Tags         attendance
// @Produce      json
// @Param        id   path  string  true  "考勤记录ID"
// @Success      200  {object}  map[string]interface{}  "驳回后的考勤记录"
// @Failure      500  {object}  map[string]interface{}  "服务端错误"
// @Security     BearerAuth
// @Router       /attendance/{id}/reject [post]
func (h *Handler) Reject(c *gin.Context) {
	rec, err := h.svc.Reject(c.Request.Context(), c.Param("id"), c.GetString(auth.CtxUserID))
	if err != nil {
		httpx.Err(c, 500, 50013, err.Error())
		return
	}
	httpx.OK(c, rec)
}
