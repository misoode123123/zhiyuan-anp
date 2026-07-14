package conversation

import (
	"encoding/json"
	"fmt"

	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/httpx"
)

// Handler 对话式需求梳理 HTTP 接口。
type Handler struct {
	svc *Service
}

// NewHandler 构造。
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register 注册路由。
func (h *Handler) Register(r gin.IRouter) {
	r.POST("/project-spaces/:id/conversations", h.Create)
	r.GET("/project-spaces/:id/conversations", h.List)
	r.GET("/conversations/:cid", h.Get)
	r.POST("/conversations/:cid/messages", h.Message)
	r.POST("/conversations/:cid/generate-spec", h.GenSpec)
	r.POST("/conversations/:cid/commit", h.Commit)
	r.POST("/asr", h.ASR)
}

// Create 建会话。
func (h *Handler) Create(c *gin.Context) {
	conv, err := h.svc.CreateConversation(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50011, err.Error())
		return
	}
	httpx.Created(c, conv)
}

// List 会话列表。
func (h *Handler) List(c *gin.Context) {
	list, err := h.svc.ListConversations(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50011, err.Error())
		return
	}
	httpx.OK(c, list)
}

// Get 会话详情 + 全部消息。
func (h *Handler) Get(c *gin.Context) {
	conv, msgs, err := h.svc.GetConversation(c.Request.Context(), c.Param("cid"))
	if err != nil {
		httpx.Err(c, 404, 40403, err.Error())
		return
	}
	httpx.OK(c, gin.H{"conversation": conv, "messages": msgs})
}

type messageRequest struct {
	Text   string   `json:"text" binding:"required"`
	Images []string `json:"images,omitempty"`
}

// Message 用户发消息 → SSE 流式返回 assistant 回复（逐 chunk），流结束发完整 message。
func (h *Handler) Message(c *gin.Context) {
	var in messageRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, err.Error())
		return
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Flush()
	msg, err := h.svc.SendMessageStream(c.Request.Context(), c.Param("cid"), in.Text, in.Images, func(delta string) {
		fmt.Fprintf(c.Writer, "data: %s\n\n", mustSSE(map[string]string{"delta": delta}))
		c.Writer.Flush()
	})
	if err != nil {
		fmt.Fprintf(c.Writer, "data: %s\n\n", mustSSE(map[string]string{"error": err.Error()}))
		c.Writer.Flush()
		return
	}
	fmt.Fprintf(c.Writer, "data: %s\n\n", mustSSE(map[string]interface{}{"done": true, "message": msg}))
	c.Writer.Flush()
}

// mustSSE 序列化为 SSE data 负载。
func mustSSE(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

type asrRequest struct {
	Audio    string `json:"audio" binding:"required"`
	Filename string `json:"filename"`
}

// ASR 语音识别 → 文字。
func (h *Handler) ASR(c *gin.Context) {
	var in asrRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, err.Error())
		return
	}
	fn := in.Filename
	if fn == "" {
		fn = "audio.webm"
	}
	text, err := h.svc.ASR(c.Request.Context(), in.Audio, fn)
	if err != nil {
		httpx.Err(c, 500, 50012, err.Error())
		return
	}
	httpx.OK(c, gin.H{"text": text})
}

// GenSpec 生成规格草稿（不入库）。
func (h *Handler) GenSpec(c *gin.Context) {
	spec, err := h.svc.GenerateSpec(c.Request.Context(), c.Param("cid"))
	if err != nil {
		httpx.Err(c, 500, 50011, err.Error())
		return
	}
	httpx.OK(c, spec)
}

type commitRequest struct {
	Title              string   `json:"title" binding:"required"`
	UserStory          string   `json:"user_story" binding:"required"`
	AcceptanceCriteria []string `json:"acceptance_criteria" binding:"required"`
}

// Commit 确认入库 → 生成 requirement + 会话 submitted。
func (h *Handler) Commit(c *gin.Context) {
	var in commitRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Err(c, 400, 40001, err.Error())
		return
	}
	req, err := h.svc.Commit(c.Request.Context(), c.Param("cid"), &SpecResult{
		Title: in.Title, UserStory: in.UserStory, AcceptanceCriteria: in.AcceptanceCriteria,
	})
	if err != nil {
		httpx.Err(c, 500, 50011, err.Error())
		return
	}
	httpx.Created(c, gin.H{"conversation_id": c.Param("cid"), "requirement": req})
}
