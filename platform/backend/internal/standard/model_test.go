package standard

import "testing"

func TestBuildPromptSection_Empty(t *testing.T) {
	if got := BuildPromptSection(nil); got != "" {
		t.Fatalf("空列表应返回空串，得到 %q", got)
	}
}

func TestBuildPromptSection_Mix(t *testing.T) {
	ps := "ps_1"
	list := []Standard{
		{ProjectSpaceID: nil, Category: "general", Content: "五约束"},
		{ProjectSpaceID: &ps, Category: "language", Content: "用 FastAPI"},
	}
	got := BuildPromptSection(list)
	want := "\n\n【编码规范·必须遵循】\n[全局][general] 五约束\n[项目][language] 用 FastAPI"
	if got != want {
		t.Fatalf("\n got: %q\nwant: %q", got, want)
	}
}
