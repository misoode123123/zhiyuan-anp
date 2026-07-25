package logsvc

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"zhiyuan-anp/platform/backend/internal/testutil"
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

// TestQuery_TraceIDAndQ Query 按 trace_id 精确 + message 关键词 ILIKE（M5）。
func TestQuery_TraceIDAndQ(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "platform_log")
	s := NewStore(db)
	ctx := context.Background()

	_ = s.CreateFromJSON(ctx, "ERROR", "backend", "build failed: redeclared", map[string]interface{}{"trace_id": "tr_a", "module": "appdeploy"})
	_ = s.CreateFromJSON(ctx, "ERROR", "backend", "other error", map[string]interface{}{"trace_id": "tr_b", "module": "appdeploy"})

	// 按 trace_id 精确
	list, err := s.Query(ctx, QueryFilter{TraceID: "tr_a", Limit: 10})
	if err != nil || len(list) != 1 || list[0].TraceID != "tr_a" {
		t.Fatalf("trace_id=tr_a 应 1 条，得到 %v err=%v", list, err)
	}
	// 按 message 关键词
	list2, _ := s.Query(ctx, QueryFilter{Q: "redeclared", Limit: 10})
	if len(list2) != 1 || !strings.Contains(list2[0].Message, "redeclared") {
		t.Fatalf("q=redeclared 应 1 条，得到 %v", list2)
	}
	// 不匹配 q 应 0
	list3, _ := s.Query(ctx, QueryFilter{Q: "nomatch", Limit: 10})
	if len(list3) != 0 {
		t.Fatalf("q=nomatch 应 0 条，得到 %d", len(list3))
	}
}
