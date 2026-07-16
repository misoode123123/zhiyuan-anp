package codews

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// TestInitSession 预创建会话: POST /session 返回的 id 被正确解析。
func TestInitSession(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		buf := make([]byte, 64)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"ses_abc","projectID":"p1","directory":"/data/repos/snake"}`)
	}))
	defer srv.Close()
	port := portOf(t, srv.URL)

	id := initSession(port)
	if gotMethod != "POST" || gotPath != "/session" {
		t.Errorf("请求 = %s %s, want POST /session", gotMethod, gotPath)
	}
	if gotBody != "{}" {
		t.Errorf("body = %q, want {}", gotBody)
	}
	if id != "ses_abc" {
		t.Errorf("initSession = %q, want ses_abc", id)
	}
}

// TestInitSessionRetryTransient serve 刚 listen 但 API 未就绪时重试,最终拿到 id。
func TestInitSessionRetryTransient(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits < 2 {
			http.Error(w, "not ready", 500)
			return
		}
		fmt.Fprint(w, `{"id":"ses_after_retry"}`)
	}))
	defer srv.Close()
	port := portOf(t, srv.URL)

	id := initSession(port)
	if id != "ses_after_retry" {
		t.Errorf("initSession = %q, want ses_after_retry(应重试后成功)", id)
	}
	if hits < 2 {
		t.Errorf("应至少重试一次, hits=%d", hits)
	}
}

// TestInitSessionFailure serve 持续失败 → 返回空串(非致命,不 panic)。
func TestInitSessionFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", 500)
	}))
	defer srv.Close()
	port := portOf(t, srv.URL)
	if id := initSession(port); id != "" {
		t.Errorf("持续失败时应返回空串, got %q", id)
	}
}

// TestInitSessionUnreachable 端口无服务 → 返回空串(非致命)。
func TestInitSessionUnreachable(t *testing.T) {
	if id := initSession(1); id != "" {
		t.Errorf("不可达端口应返回空串, got %q", id)
	}
}

// TestSessionDeepURL 深链接 slug = base64url(repoDir) 无 padding,
// 与 opencode web UI 的 bn(worktree) 一致, 使打开即进预创建会话。
func TestSessionDeepURL(t *testing.T) {
	b64 := func(s string) string {
		return strings.TrimRight(base64.URLEncoding.EncodeToString([]byte(s)), "=")
	}
	cases := []struct{ base, repo, sid string }{
		{"http://10.10.0.28:9400", "/data/repos/snake", "ses_abc"},
		{"http://h:9401", "/data/repos/待办管理", "ses_xy"}, // 含中文, 验证 UTF-8 编码
	}
	for _, c := range cases {
		want := fmt.Sprintf("%s/%s/session/%s", c.base, b64(c.repo), c.sid)
		if got := sessionDeepURL(c.base, c.repo, c.sid); got != want {
			t.Errorf("sessionDeepURL(%q,%q,%q)\n got %q\nwant %q", c.base, c.repo, c.sid, got, want)
		}
	}
	// 固定锚定值, 防 base64 算法漂移(与 opencode bn("/data/repos/snake") 对齐)。
	if g := sessionDeepURL("http://10.10.0.28:9400", "/data/repos/snake", "ses_abc"); g !=
		"http://10.10.0.28:9400/L2RhdGEvcmVwb3Mvc25ha2U/session/ses_abc" {
		t.Errorf("锚定值不符: %q", g)
	}
}

func portOf(t *testing.T, url string) int {
	t.Helper()
	port, err := strconv.Atoi(strings.TrimPrefix(url, "http://127.0.0.1:"))
	if err != nil {
		t.Fatalf("解析 httptest 端口失败: %v (%s)", err, url)
	}
	return port
}
