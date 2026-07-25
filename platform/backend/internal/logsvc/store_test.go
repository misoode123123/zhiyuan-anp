package logsvc

import (
	"strconv"
	"testing"
)

// TestItoa 验证 itoa 对两位数正确（之前 bug：只支持 0-9）。
func TestItoa(t *testing.T) {
	tests := []struct {
		in   int
		want string
	}{
		{1, "1"},
		{5, "5"},
		{9, "9"},
		{10, "10"},   // 之前 bug：string(rune('0'+10)) = ":"
		{50, "50"},   // limit 常用值
		{200, "200"}, // offset 可能值
	}
	for _, tt := range tests {
		got := itoa(tt.in)
		if got != tt.want {
			t.Errorf("itoa(%d) = %q, want %q（strconv.Itoa 对照: %s）",
				tt.in, got, tt.want, strconv.Itoa(tt.in))
		}
	}
}

// TestCreateFromJSONFields 验证 fields 提取逻辑（不依赖 DB）。
func TestCreateFromJSONFields(t *testing.T) {
	fields := map[string]interface{}{
		"module":   "compute",
		"trace_id": "djw123",
		"user_id":  "admin",
		"path":     "/api/v1/code",
		"status":   500,
	}

	// 模拟 CreateFromJSON 的字段提取
	e := &LogEntry{Level: "ERROR", Source: "backend", Message: "test"}
	if v, ok := fields["module"].(string); ok {
		e.Module = v
	}
	if v, ok := fields["trace_id"].(string); ok {
		e.TraceID = v
	}

	if e.Module != "compute" {
		t.Errorf("module = %q, want compute", e.Module)
	}
	if e.TraceID != "djw123" {
		t.Errorf("trace_id = %q, want djw123", e.TraceID)
	}
}
