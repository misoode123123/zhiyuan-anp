package appdeploy

import (
	"strings"
	"testing"
)

// === parseImageVersion ===

func TestParseImageVersion(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"appdeploy/yxt-eino-v2-test:v10", 10, true},
		{"appdeploy/x-prod:v1", 1, true},
		{"appdeploy/x-test:latest", 0, false},
		{"appdeploy/x-test", 0, false},
		{"", 0, false},
		{"repo/img:v", 0, false},
	}
	for _, c := range cases {
		got, ok := parseImageVersion(c.in)
		if got != c.want || ok != c.ok {
			t.Fatalf("parseImageVersion(%q) got (%d,%v) want (%d,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

// === checkDrift ===

func TestCheckDrift_AllEqual(t *testing.T) {
	r := checkDrift("appdeploy/x-test:v8", "appdeploy/x-test:v8", "appdeploy/x-test:v8")
	if !r.OK {
		t.Fatalf("三方等应 OK got=%+v", r)
	}
}

func TestCheckDrift_DBNeqContainer(t *testing.T) {
	r := checkDrift("appdeploy/x-test:v8", "appdeploy/x-test:v10", "appdeploy/x-test:v10")
	if r.OK {
		t.Fatal("DB≠container 应不 OK")
	}
	if !strings.Contains(r.Reason, "DB记录") || !strings.Contains(r.Reason, "运行容器") {
		t.Fatalf("Reason 应描述 DB≠容器 got=%q", r.Reason)
	}
}

func TestCheckDrift_TwoWay_ContainerEmpty(t *testing.T) {
	// containerImg 空（部署时回读失败/Stats 容器查不到）：退化为 DB↔manifest 两方比。
	r := checkDrift("appdeploy/x-test:v8", "", "appdeploy/x-test:v10")
	if r.OK {
		t.Fatal("DB≠manifest（无容器）应不 OK")
	}
	// DB=manifest 且无容器 → 单源 → OK
	r2 := checkDrift("appdeploy/x-test:v8", "", "appdeploy/x-test:v8")
	if !r2.OK {
		t.Fatalf("DB=manifest 单源应 OK got=%+v", r2)
	}
}

func TestCheckDrift_AllEmpty(t *testing.T) {
	if r := checkDrift("", "", ""); !r.OK {
		t.Fatalf("全空应 OK（无信息不判漂移）got=%+v", r)
	}
}

// === highWaterMarkVersion（只升不降）===

func TestHighWaterMarkVersion(t *testing.T) {
	// 向上：cur=8 / 容器 v10 → (10,true)
	if v, ch := highWaterMarkVersion(8, "appdeploy/x-test:v10"); v != 10 || !ch {
		t.Fatalf("向上应 (10,true) got (%d,%v)", v, ch)
	}
	// 向下不降：cur=10 / 容器 v8 → (10,false)
	if v, ch := highWaterMarkVersion(10, "appdeploy/x-test:v8"); v != 10 || ch {
		t.Fatalf("向下应不降 (10,false) got (%d,%v)", v, ch)
	}
	// 等于：cur=10 / 容器 v10 → (10,false)
	if v, ch := highWaterMarkVersion(10, "appdeploy/x-test:v10"); v != 10 || ch {
		t.Fatalf("等于应 (10,false) got (%d,%v)", v, ch)
	}
	// 容器 tag 无法解析 → 不改 (cur,false)
	if v, ch := highWaterMarkVersion(5, "appdeploy/x-test:latest"); v != 5 || ch {
		t.Fatalf("无法解析应 (5,false) got (%d,%v)", v, ch)
	}
}

// === reconcileActual ===

func TestReconcileActual_Differ_WritesImageOnly(t *testing.T) {
	dir := t.TempDir()
	// seed：actual 是 v8 + 一条 host_port=9100 + mounts_src（确定性重放字段，须保留）
	if err := WriteDeployManifest(dir, &DeployManifest{
		Needs: NeedsSpec{Ports: []int{8080}},
		Actual: ActualSpec{
			ImageDigest: "appdeploy/x-test:v8", HostPort: 9100,
			MountsSrc: []ActualMount{{Src: "config.yaml", HostSrc: "/h/config.yaml"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	mf, _ := LoadDeployManifest(dir)
	if !reconcileActual(dir, mf, "appdeploy/x-test:v10") {
		t.Fatal("differ 应返回 true 并写回")
	}
	got, _ := LoadDeployManifest(dir)
	if got.Actual.ImageDigest != "appdeploy/x-test:v10" {
		t.Fatalf("ImageDigest 应改为 v10 got=%q", got.Actual.ImageDigest)
	}
	// 确定性重放字段须保留（不被覆写）
	if got.Actual.HostPort != 9100 || len(got.Actual.MountsSrc) != 1 {
		t.Fatalf("HostPort/mounts_src 应保留 got HostPort=%d mounts=%+v", got.Actual.HostPort, got.Actual.MountsSrc)
	}
	if got.Needs.Ports[0] != 8080 {
		t.Fatalf("Needs 段应不动 got=%+v", got.Needs)
	}
}

func TestReconcileActual_Same_NoOp(t *testing.T) {
	dir := t.TempDir()
	WriteDeployManifest(dir, &DeployManifest{Actual: ActualSpec{ImageDigest: "appdeploy/x:v9", HostPort: 9000}})
	mf, _ := LoadDeployManifest(dir)
	if reconcileActual(dir, mf, "appdeploy/x:v9") {
		t.Fatal("相同应返回 false（no-op）")
	}
}

func TestReconcileActual_NilManifest(t *testing.T) {
	if reconcileActual(t.TempDir(), nil, "appdeploy/x:v1") {
		t.Fatal("nil mf 应 false")
	}
}
