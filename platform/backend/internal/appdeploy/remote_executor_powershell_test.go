package appdeploy

import (
	"testing"
)

func TestJoinRemotePath(t *testing.T) {
	cases := []struct {
		to, base, osType, want string
	}{
		{`C:\a\b`, "x.ps1", "windows", `C:\a\b\x.ps1`},
		{`C:\a\b\`, "x.ps1", "windows", `C:\a\b\x.ps1`},  // 已带尾分隔符不双补
		{`C:\a\b/`, "x.ps1", "windows", `C:\a\b/x.ps1`},  // 混用 / 也算尾分隔符
		{"/opt/a", "x", "linux", "/opt/a/x"},
		{"/opt/a/", "x", "linux", "/opt/a/x"},
		{"", "x", "linux", "/x"}, // to 空:补分隔符 + base(边界)
	}
	for _, c := range cases {
		if got := joinRemotePath(c.to, c.base, c.osType); got != c.want {
			t.Errorf("joinRemotePath(%q,%q,%q) = %q, want %q", c.to, c.base, c.osType, got, c.want)
		}
	}
}
