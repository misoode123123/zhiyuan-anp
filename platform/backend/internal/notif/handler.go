package notif

import (
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/httpx"
)

// Handler 通知 HTTP + SSE 接口。
type Handler struct {
	store *Store
}

// NewHandler 构造。
func NewHandler(store *Store) *Handler { return &Handler{store: store} }

// Register 注册路由。
func (h *Handler) Register(r gin.IRouter) {
	r.GET("/notifications", h.List)
	r.GET("/notifications/stream", h.Stream)
	r.PATCH("/notifications/:id/read", h.MarkRead)
	r.POST("/notifications/read-all", h.MarkAllRead)
}

// List 查询通知列表。
func (h *Handler) List(c *gin.Context) {
	userID := c.GetString("user_id")
	unreadOnly := c.Query("unread") == "true"
	list, err := h.store.List(c.Request.Context(), userID, unreadOnly, 0)
	if err != nil {
		httpx.Err(c, 500, 50013, err.Error())
		return
	}
	count, _ := h.store.UnreadCount(c.Request.Context(), userID)
	httpx.OK(c, gin.H{"items": list, "unread_count": count})
}

// Stream SSE 实时推送（text/event-stream）。
func (h *Handler) Stream(c *gin.Context) {
	userID := c.GetString("user_id")
	// SSE headers
	h2 := c.Writer.Header()
	h2.Set("Content-Type", "text/event-stream")
	h2.Set("Cache-Control", "no-cache")
	h2.Set("Connection", "keep-alive")
	h2.Set("X-Accel-Buffering", "no") // nginx 不缓冲

	ch := h.store.Subscribe(userID)
	defer h.store.Unsubscribe(userID, ch)

	// 发心跳（确认连接）
	c.Writer.WriteString(": connected\n\n")
	c.Writer.Flush()

	// 发当前未读数
	count, _ := h.store.UnreadCount(c.Request.Context(), userID)
	c.Writer.WriteString(fmt.Sprintf("data: {\"type\":\"unread\",\"count\":%d}\n\n", count))
	c.Writer.Flush()

	// 阻塞等待通知
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case n := <-ch:
			c.Writer.WriteString(n.SSEData())
			c.Writer.Flush()
		}
	}
}

// MarkRead 标记单条已读。
func (h *Handler) MarkRead(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Err(c, 400, 40001, "invalid id")
		return
	}
	if err := h.store.MarkRead(c.Request.Context(), id); err != nil {
		httpx.Err(c, 500, 50013, err.Error())
		return
	}
	httpx.OK(c, gin.H{"id": id, "read": true})
}

// MarkAllRead 标记全部已读。
func (h *Handler) MarkAllRead(c *gin.Context) {
	userID := c.GetString("user_id")
	if err := h.store.MarkAllRead(c.Request.Context(), userID); err != nil {
		httpx.Err(c, 500, 50013, err.Error())
		return
	}
	httpx.OK(c, gin.H{"read_all": true})
}
