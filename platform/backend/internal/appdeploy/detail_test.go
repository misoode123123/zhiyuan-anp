package appdeploy

import (
	"context"
	"testing"
)

// TestDetail_EmptySlicesNotNil 无需求/变更/发布/提交/实例的新应用,Detail 各切片须非 nil。
// Go nil 切片序列化为 JSON null,前端 detail.requirements.length 会崩(应用详情打不开)。
// 本测试是该根因修复的回归保护。
func TestDetail_EmptySlicesNotNil(t *testing.T) {
	s := newTestStore(t)
	ps := "ps_detail_nil"
	a := &Application{ProjectSpaceID: ps, Name: "empty-app", AppKind: AppKindWeb, InternalPort: 3000}
	if err := s.Create(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	d, err := s.Detail(context.Background(), ps, a.ID)
	if err != nil || d == nil {
		t.Fatalf("Detail err=%v d=%v", err, d)
	}
	// 直接比较类型化切片与 nil(不经 interface{},避免 nil-interface 陷阱)。
	if d.Requirements == nil {
		t.Fatal("Requirements 为 nil → JSON null → 前端 .length 崩")
	}
	if d.Changes == nil {
		t.Fatal("Changes 为 nil → JSON null → 前端 .length 崩")
	}
	if d.Releases == nil {
		t.Fatal("Releases 为 nil → JSON null → 前端 .length 崩")
	}
	if d.Commits == nil {
		t.Fatal("Commits 为 nil → JSON null → 前端 .map 崩")
	}
	if d.Instances == nil {
		t.Fatal("Instances 为 nil → JSON null → 前端 .map 崩")
	}
}
