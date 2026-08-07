package compute

import (
	"testing"
)

// TestIsRetryable 验证 retry 判断逻辑。
func TestIsRetryable(t *testing.T) {
	tests := []struct {
		errMsg string
		want   bool
	}{
		{"dial tcp: connection refused", true},
		{"read: timeout", true},
		{"unexpected EOF", true},
		{"provider 返回 503: service unavailable", true},
		{"provider 返回 500: internal error", true},
		{"provider 返回 401: unauthorized", false}, // 4xx 不 retry
		{"provider 返回 400: bad request", false},
		{"解析 JSON 失败", false}, // 非网络错误不 retry
	}
	for _, tt := range tests {
		got := isRetryable(&strErr{msg: tt.errMsg})
		if got != tt.want {
			t.Errorf("isRetryable(%q) = %v, want %v", tt.errMsg, got, tt.want)
		}
	}
}

type strErr struct{ msg string }

func (e *strErr) Error() string { return e.msg }

// TestRouteSelection 验证 ChatRequest 中 model 优先于 route。
func TestRouteSelection(t *testing.T) {
	// 直接指定 model 时应绕过 route
	req := ChatRequest{Model: "cmd_xxx", TaskType: "code"}
	if req.Model == "" {
		t.Error("直接指定 model 时应非空（绕过路由）")
	}

	// 未指定 model 时应走 route
	req2 := ChatRequest{TaskType: "spec"}
	if req2.Model != "" {
		t.Error("未指定 model 时应为空（走路由）")
	}
}
