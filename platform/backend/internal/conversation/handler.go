package conversation

import (
	"encoding/json"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"

	"zhiyuan-anp/platform/backend/internal/httpx"
	"zhiyuan-anp/platform/backend/internal/requirement"
)

// Handler 对话式需求梳理 HTTP 接口。
type Handler struct {
	svc *Service
}

// NewHandler 构造。
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register 模块级装配：内部 NewStore(db)+NewService+NewHandler+Register。
// reqRepo 由 main 传入（跨模块枢纽，qa/release 也要用）。
func Register(r gin.IRouter, db *sqlx.DB, reqRepo *requirement.Repository, agentRuntimeURL string) {
	NewHandler(NewService(NewStore(db), reqRepo, agentRuntimeURL)).Register(r)
}

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
//
// @Summary      创建对话会话
// @Tags         conversation
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Success      200  {object}  map[string]interface{}  "创建的会话"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/conversations [post]
func (h *Handler) Create(c *gin.Context) {
	conv, err := h.svc.CreateConversation(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50011, err.Error())
		return
	}
	httpx.Created(c, conv)
}

// List 会话列表。
//
// @Summary      列出项目空间下的会话
// @Tags         conversation
// @Produce      json
// @Param        id   path  string  true  "项目空间ID"
// @Success      200  {object}  map[string]interface{}  "会话列表"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /project-spaces/{id}/conversations [get]
func (h *Handler) List(c *gin.Context) {
	list, err := h.svc.ListConversations(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Err(c, 500, 50011, err.Error())
		return
	}
	httpx.OK(c, list)
}

// Get 会话详情 + 全部消息。
//
// @Summary      获取会话详情（含全部消息）
// @Tags         conversation
// @Produce      json
// @Param        cid  path  string  true  "会话ID"
// @Success      200  {object}  map[string]interface{}  "conversation+messages"
// @Failure      404  {object}  map[string]interface{}  "会话不存在"
// @Security     BearerAuth
// @Router       /conversations/{cid} [get]
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
//
// @Summary      发送消息（SSE 流式回复）
// @Tags         conversation
// @Accept       json
// @Produce      text/event-stream
// @Param        cid   path   string          true  "会话ID"
// @Param        body  body   messageRequest  true  "消息内容(text+images)"
// @Success      200  {object}  map[string]interface{}  "SSE 流：data: {delta}|{error}|{done,message}"
// @Failure      400  {object}  map[string]interface{}  "invalid body"
// @Security     BearerAuth
// @Router       /conversations/{cid}/messages [post]
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
//
// @Summary      语音识别（ASR）
// @Tags         conversation
// @Accept       json
// @Produce      json
// @Param        body  body  asrRequest  true  "音频(base64 audio+filename)"
// @Success      200  {object}  map[string]interface{}  "识别文本 {text}"
// @Failure      400  {object}  map[string]interface{}  "invalid body"
// @Failure      500  {object}  map[string]interface{}  "识别失败"
// @Security     BearerAuth
// @Router       /asr [post]
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
//
// @Summary      生成规格草稿（不入库）
// @Tags         conversation
// @Produce      json
// @Param        cid  path  string  true  "会话ID"
// @Success      200  {object}  map[string]interface{}  "spec 草稿"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /conversations/{cid}/generate-spec [post]
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
//
// @Summary      确认入库（生成 requirement）
// @Tags         conversation
// @Accept       json
// @Produce      json
// @Param        cid   path  string         true  "会话ID"
// @Param        body  body  commitRequest  true  "规格(title+user_story+acceptance_criteria)"
// @Success      200  {object}  map[string]interface{}  "conversation_id+requirement"
// @Failure      400  {object}  map[string]interface{}  "invalid body"
// @Failure      500  {object}  map[string]interface{}  "内部错误"
// @Security     BearerAuth
// @Router       /conversations/{cid}/commit [post]
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
