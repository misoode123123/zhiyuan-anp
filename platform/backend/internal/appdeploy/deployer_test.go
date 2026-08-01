package appdeploy

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

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
		"0.0.0.0:9123->80/tcp":                 9123,
		"10.10.0.28:9150->8080/tcp":            9150,
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

func TestEnsurePortEnv(t *testing.T) {
	// 无 PORT → 补 PORT=internal
	got := ensurePortEnv([]string{"FOO=bar"}, 9101)
	if len(got) != 2 || got[1] != "PORT=9101" {
		t.Fatalf("未注入 PORT=9101: %v", got)
	}
	// 已有 PORT → 不覆盖(尊重应用显式设置)
	got = ensurePortEnv([]string{"PORT=8080", "X=1"}, 9101)
	if len(got) != 2 || got[0] != "PORT=8080" {
		t.Fatalf("不应覆盖已有 PORT: %v", got)
	}
	// 空 env → 补 PORT
	got = ensurePortEnv(nil, 3000)
	if len(got) != 1 || got[0] != "PORT=3000" {
		t.Fatalf("空 env 应补 PORT=3000: %v", got)
	}
}

func TestParseContainerNames(t *testing.T) {
	got := parseContainerNames("appdeploy-snake-prod-v7\nappdeploy-snake-prod-v8\n\n")
	want := []string{"appdeploy-snake-prod-v7", "appdeploy-snake-prod-v8"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	// 空输出/纯空白
	if n := len(parseContainerNames("")); n != 0 {
		t.Fatalf("空输出应返回空切片, got %d", n)
	}
}

// TestNewDeployer 构造函数注入 host 字段。
func TestNewDeployer(t *testing.T) {
	d := NewDeployer("10.10.0.28")
	if d == nil {
		t.Fatal("NewDeployer 不应返回 nil")
	}
	if d.host != "10.10.0.28" {
		t.Fatalf("host 字段应注入，得到 %q", d.host)
	}
}

// TestEnvPortRange 按环境返回互不冲突的端口区间：test 9100-9199，prod 9200-9300。
func TestEnvPortRange(t *testing.T) {
	d := NewDeployer("h")
	cases := []struct {
		env      string
		min, max int
	}{
		{EnvTest, portTestMin, portTestMax},
		{EnvProd, portProdMin, portProdMax},
	}
	for _, c := range cases {
		min, max := d.envPortRange(c.env)
		if min != c.min || max != c.max {
			t.Fatalf("env=%s got [%d,%d] want [%d,%d]", c.env, min, max, c.min, c.max)
		}
	}
	// 未知环境也走 test 区间（兜底）
	min, max := d.envPortRange("staging")
	if min != portTestMin || max != portTestMax {
		t.Fatalf("未知环境应兜底 test 区间，得到 [%d,%d]", min, max)
	}
	// 两环境区间不重叠（关键不变式：test 与 prod 端口互不冲突）
	_, testMax := d.envPortRange(EnvTest)
	prodMin, _ := d.envPortRange(EnvProd)
	if testMax >= prodMin {
		t.Fatalf("test 与 prod 端口段重叠: testMax=%d >= prodMin=%d", testMax, prodMin)
	}
}

// TestPortRangeConstants 端口段常量数值校验（防误改）。
func TestPortRangeConstants(t *testing.T) {
	if portTestMin != 9100 || portTestMax != 9199 {
		t.Fatalf("test 端口段应是 9100-9199，得到 %d-%d", portTestMin, portTestMax)
	}
	if portProdMin != 9200 || portProdMax != 9300 {
		t.Fatalf("prod 端口段应是 9200-9300，得到 %d-%d", portProdMin, portProdMax)
	}
}

// TestAllocFreePort_PortExhaustion 边界：min==max 且占用 → 0。
func TestAllocFreePort_PortExhaustion(t *testing.T) {
	used := map[int]struct{}{5: {}}
	if p := AllocFreePort(used, 5, 5); p != 0 {
		t.Fatalf("min==max 且占用应 0，得到 %d", p)
	}
	if p := AllocFreePort(map[int]struct{}{}, 5, 5); p != 5 {
		t.Fatalf("min==max 且空闲应返回 min，得到 %d", p)
	}
}

// TestAllocFreePort_MinGtMax 异常：min>max → 空循环返回 0。
func TestAllocFreePort_MinGtMax(t *testing.T) {
	if p := AllocFreePort(map[int]struct{}{}, 10, 5); p != 0 {
		t.Fatalf("min>max 应返回 0，得到 %d", p)
	}
}

// TestDockerSlug 应用名 → docker 合法 tag/容器名片段。
// 中文等非 ASCII 字符须被替换；纯中文（替换后为空）退回名 sha256 前缀且稳定。
func TestDockerSlug(t *testing.T) {
	cases := map[string]string{
		"snake":        "snake",      // 纯 ASCII 原样保留
		"hello-go":     "hello-go",   // 连字符名不变
		"cli-e2e-test": "cli-e2e-test",
		"ncc_deploy":   "ncc-deploy", // 下划线 → -
		"A.B":          "a-b",        // 大写/点 → 小写/-
		"  spa cing ":  "spa-cing",   // 空白 → -，折叠并去首尾
		"café":         "caf",        // 带变音符，仅 ASCII 字母保留（é 非法 → -，折叠去尾）
	}
	for in, want := range cases {
		if got := dockerSlug(in); got != want {
			t.Errorf("dockerSlug(%q) = %q, want %q", in, got, want)
		}
	}
	// 纯中文：slug 必须合法 + 稳定（同入参每次相同，RemoveByPrefix 才能匹配历史容器）
	cn := dockerSlug("客服机器人")
	if !slugValidRe.MatchString(cn) {
		t.Fatalf("中文 slug 非法（须匹配 %s）: %q", slugValidRe, cn)
	}
	if dockerSlug("客服机器人") != cn {
		t.Fatalf("slug 不稳定: 首次 %q 再次 %q", cn, dockerSlug("客服机器人"))
	}
	// 两个不同纯中文名 → 不同 slug，避免容器名撞车
	if dockerSlug("客服机器人") == dockerSlug("运维机器人") {
		t.Fatal("两个不同中文应用名不应生成相同 slug")
	}
}

// TestDockerSlug_ImageTagValidForChineseName 回归「客服机器人」构建失败：
// 修复前 ins.Image = appdeploy/客服机器人-test:v4 → docker invalid reference format（exit 125）；
// 修复后用 dockerSlug，tag 的路径段须合法。
func TestDockerSlug_ImageTagValidForChineseName(t *testing.T) {
	ins := &AppInstance{Env: "test", Version: 4}
	image := fmt.Sprintf("appdeploy/%s-%s:v%d", dockerSlug("客服机器人"), ins.Env, ins.Version)
	// 路径段 = image 去掉 "appdeploy/" 前缀和 ":v4" 后缀
	repo := strings.TrimSuffix(strings.TrimPrefix(image, "appdeploy/"), ":v4")
	if !slugValidRe.MatchString(repo) {
		t.Fatalf("中文应用镜像路径段非法: image=%q repo=%q", image, repo)
	}
	// 容器名同理
	container := fmt.Sprintf("appdeploy-%s-%s-v%d", dockerSlug("客服机器人"), ins.Env, ins.Version)
	// 容器名允许 [a-zA-Z0-9_.-]，这里全是小写 ASCII + -，用宽松校验
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`).MatchString(container) {
		t.Fatalf("中文应用容器名非法: %q", container)
	}
}
