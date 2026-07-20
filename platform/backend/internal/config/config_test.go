package config

import (
	"reflect"
	"testing"
)

// TestSplitCSV 纯逻辑：逗号分隔 → trim → 去空段。
// 注：用 splitCSV 而非 viper.GetStringSlice，因后者对字符串值 cast 不分割，多 origin 会失效。
func TestSplitCSV(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , b , c ", []string{"a", "b", "c"}}, // 两侧空格 trim
		{"a,,b", []string{"a", "b"}},             // 空段过滤
		{",,,", []string{}},                      // 全空段
		{"", []string{}},                         // 空串返回空切片
		{"single", []string{"single"}},           // 无分隔符
		{"  ", []string{}},                       // 仅空白
	}
	for _, c := range cases {
		got := splitCSV(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Fatalf("splitCSV(%q) = %#v, want %#v", c.in, got, c.want)
		}
	}
}
