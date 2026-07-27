package change

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"zhiyuan-anp/platform/backend/internal/auth"
	"zhiyuan-anp/platform/backend/internal/testutil"
)

func newChangeHandler(t *testing.T) (*Handler, *Store) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "change_request")
	s := NewStore(db)
	return NewHandler(s), s
}

// TestApprove_ForbidSelfApprove 作者（user_id=usr_alice）审批自己的变更 → 403。
func TestApprove_ForbidSelfApprove(t *testing.T) {
	h, s := newChangeHandler(t)
	chg := &ChangeRequest{ProjectSpaceID: "ps1", UserID: "usr_alice", Kind: "code"}
	if err := s.Create(context.Background(), chg); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/changes/:id/approve", func(c *gin.Context) {
		c.Set(auth.CtxUserID, "alice")
		c.Set(auth.CtxUserDBID, "usr_alice")
		h.Approve(c)
	})
	req := httptest.NewRequest(http.MethodPost, "/changes/"+chg.ID+"/approve", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("作者自批应 403, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestApprove_AllowOtherReviewer 他人（usr_bob）审批 alice 的变更 → 200。
func TestApprove_AllowOtherReviewer(t *testing.T) {
	h, s := newChangeHandler(t)
	chg := &ChangeRequest{ProjectSpaceID: "ps1", UserID: "usr_alice", Kind: "code"}
	if err := s.Create(context.Background(), chg); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/changes/:id/approve", func(c *gin.Context) {
		c.Set(auth.CtxUserID, "bob")
		c.Set(auth.CtxUserDBID, "usr_bob")
		h.Approve(c)
	})
	req := httptest.NewRequest(http.MethodPost, "/changes/"+chg.ID+"/approve", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("他人审批应 200, got %d body=%s", w.Code, w.Body.String())
	}
}
