package appdeploy

import "testing"

func TestRedactOut(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		secrets []string
		want    string
	}{
		{"密钥被替换", "export KEY=sk-abc123 done", []string{"sk-abc123"}, "export KEY=*** done"},
		{"多密钥", "a=xxx b=yyy", []string{"xxx", "yyy"}, "a=*** b=***"},
		{"空 secret 跳过", "unchanged", []string{""}, "unchanged"},
		{"无密钥原样", "plain log", nil, "plain log"},
		{"重复出现全替换", "k k k", []string{"k"}, "*** *** ***"},
	}
	for _, c := range cases {
		if got := redactOut(c.in, c.secrets); got != c.want {
			t.Errorf("%s: redactOut(%q,%v)=%q want %q", c.name, c.in, c.secrets, got, c.want)
		}
	}
}
