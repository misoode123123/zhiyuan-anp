package performance

import (
	"time"

	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/auth"
	"zhiyuan-anp/platform/backend/internal/codews"
	"zhiyuan-anp/platform/backend/internal/httpx"
)

// Handler 绩效记录 HTTP 接口（只读）。
type Handler struct {
	store *Store
	ws    *codews.Manager // opencode live SessionMessages（claude/codex 走 ReaderFor 磁盘）；nil=opencode 详情降级
}

// NewHandler 构造。
func NewHandler(store *Store, ws *codews.Manager) *Handler { return &Handler{store: store, ws: ws} }

// Register 模块级装配：main 调用。
func Register(r gin.IRouter, store *Store, ws *codews.Manager) {
	NewHandler(store, ws).Register(r)
}

// Register 注册路由（me 不登记 routeOps→任意登录自校验；members/sessions 登记 admin 守卫）。
func (h *Handler) Register(r gin.IRouter) {
	r.GET("/project-spaces/:id/performance/me", h.Me)
	r.GET("/project-spaces/:id/performance/members", h.Members)
	r.GET("/project-spaces/:id/performance/members/:userID", h.MemberDetail)
	r.GET("/project-spaces/:id/performance/sessions/:sessionID/messages", h.SessionMessages)
}

// parseWindow 解析 from/to（YYYY-MM-DD，可选）；to 含当天（+24h）；缺省零值=不加窗。
func parseWindow(c *gin.Context) (time.Time, time.Time) {
	from, _ := time.Parse("2006-01-02", c.Query("from"))
	to, _ := time.Parse("2006-01-02", c.Query("to"))
	if !to.IsZero() {
		to = to.Add(24 * time.Hour)
	}
	return from, to
}

// Me 本人绩效（自校验：取登录者 user id）。
func (h *Handler) Me(c *gin.Context) {
	psID := c.Param("id")
	uid := c.GetString(auth.CtxUserDBID)
	if uid == "" {
		httpx.Err(c, 401, 40101, "未识别用户")
		return
	}
	from, to := parseWindow(c)
	p, err := h.store.Summary(c.Request.Context(), psID, uid, from, to)
	if err != nil {
		httpx.Err(c, 500, 50080, err.Error())
		return
	}
	httpx.OK(c, p)
}

// Members 全员绩效排行榜（admin）。
func (h *Handler) Members(c *gin.Context) {
	from, to := parseWindow(c)
	list, err := h.store.Members(c.Request.Context(), c.Param("id"), from, to)
	if err != nil {
		httpx.Err(c, 500, 50080, err.Error())
		return
	}
	httpx.OK(c, list)
}

// MemberDetail 某人明细（admin）。
func (h *Handler) MemberDetail(c *gin.Context) {
	from, to := parseWindow(c)
	p, err := h.store.Summary(c.Request.Context(), c.Param("id"), c.Param("userID"), from, to)
	if err != nil {
		httpx.Err(c, 500, 50080, err.Error())
		return
	}
	httpx.OK(c, p)
}

// SessionMessages 某次互动的完整聊天记录（admin），文件优先 + live 兜底：
// ① ReaderFor 直读工具 transcript 文件（opencode.db SQLite / claude-codex .jsonl）——
//   实时落盘、进程死后文件仍在，解决"点开历史会话是空的"。
// ② opencode 且文件缺失/会话尚未落盘时，降级 live HTTP（须进程存活）。
func (h *Handler) SessionMessages(c *gin.Context) {
	rec, err := h.store.SessionByID(c.Request.Context(), c.Param("sessionID"))
	if err != nil {
		httpx.Err(c, 404, 40401, "会话不存在")
		return
	}
	// ① 文件优先：opencode.db / claude-codex jsonl（实时、进程死后仍在）
	if r := codews.ReaderFor(rec.Tool); r != nil {
		if msgs, err := r.Messages(rec.RepoDir, rec.SessionID); err == nil && len(msgs) > 0 {
			httpx.OK(c, gin.H{"tool": rec.Tool, "messages": msgs})
			return
		}
	}
	// ② opencode live 兜底：db 文件缺失/会话尚未落盘时（进程存活才能拉）
	if rec.Tool == "opencode" && h.ws != nil {
		txt, err := h.ws.SessionMessages(rec.AppID, rec.UserID)
		if err != nil {
			httpx.Err(c, 500, 50081, err.Error())
			return
		}
		httpx.OK(c, gin.H{"tool": "opencode", "transcript": txt})
		return
	}
	httpx.Err(c, 400, 40001, "该工具不支持查看聊天记录")
}
