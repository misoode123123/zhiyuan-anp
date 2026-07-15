package appdeploy

import "testing"

func TestAllocFreePort(t *testing.T) {
	used := map[int]struct{}{9100: {}, 9101: {}, 9105: {}}
	// 9100/9101 占用 → 首个空闲是 9102
	if p := AllocFreePort(used, 9100, 9110); p != 9102 {
		t.Fatalf("首个空闲应为 9102，得到 %d", p)
	}
	// 区间全占用 → 返回 0
	full := map[int]struct{}{}
	for i := 9100; i <= 9103; i++ {
		full[i] = struct{}{}
	}
	if p := AllocFreePort(full, 9100, 9103); p != 0 {
		t.Fatalf("全占用应返回 0，得到 %d", p)
	}
	// 空占用表 → 返回 min
	if p := AllocFreePort(map[int]struct{}{}, 9100, 9110); p != 9100 {
		t.Fatalf("空表应返回 min 9100，得到 %d", p)
	}
}

func TestHostPortRegex(t *testing.T) {
	cases := map[string]int{
		"0.0.0.0:9123->80/tcp":        9123,
		"10.10.0.28:9150->8080/tcp":   9150,
		"0.0.0.0:9101->3000/tcp, 9102->80/tcp": 9101,
	}
	for line, want := range cases {
		m := hostPortRe.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("未匹配到端口: %s", line)
		}
		got := 0
		for _, ch := range m[1] {
			got = got*10 + int(ch-'0')
		}
		if got != want {
			t.Fatalf("端口解析: line=%s got=%d want=%d", line, got, want)
		}
	}
	// 无端口映射的行不匹配
	if hostPortRe.MatchString("some-container") {
		t.Fatal("无端口映射不应匹配")
	}
}
