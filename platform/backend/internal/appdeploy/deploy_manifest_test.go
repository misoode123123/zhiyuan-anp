package appdeploy

import (
	"os"
	"path/filepath"
	"testing"
)

// === LoadDeployManifest ===

func TestLoadDeployManifest_Absent(t *testing.T) {
	mf, err := LoadDeployManifest(t.TempDir())
	if err != nil || mf != nil {
		t.Fatalf("无文件应返回 (nil,nil) got mf=%v err=%v", mf, err)
	}
}

func TestLoadWriteRoundtrip_PreservesNeeds(t *testing.T) {
	dir := t.TempDir()
	in := &DeployManifest{
		Needs: NeedsSpec{
			Mounts:  []MountSpec{{Src: "config.yaml", Dst: "/app/config.yaml", ReadOnly: true}},
			EnvKeys: []string{"CONFIG_PATH", "REDIS_ADDR"},
			Ports:   []int{8080},
			Command: "./app",
		},
	}
	if err := WriteDeployManifest(dir, in); err != nil {
		t.Fatal(err)
	}
	mf, err := LoadDeployManifest(dir)
	if err != nil || mf == nil {
		t.Fatalf("roundtrip 读回应非 nil err=%v", err)
	}
	if mf.Needs.Command != "./app" {
		t.Fatalf("needs.Command 丢失 got=%q", mf.Needs.Command)
	}
	if len(mf.Needs.Mounts) != 1 || mf.Needs.Mounts[0].Dst != "/app/config.yaml" {
		t.Fatalf("needs.mounts 丢失 got=%+v", mf.Needs.Mounts)
	}
	if len(mf.Needs.Ports) != 1 || mf.Needs.Ports[0] != 8080 {
		t.Fatalf("needs.ports 丢失 got=%+v", mf.Needs.Ports)
	}
	if len(mf.Needs.EnvKeys) != 2 {
		t.Fatalf("needs.env_keys 丢失 got=%+v", mf.Needs.EnvKeys)
	}
}

// === ResolveConfigMount ===

func TestResolveConfigMount_LegacyAutoDetect(t *testing.T) {
	// legacy 应用无 manifest：detectConfigPath 探测 config.yaml + toHostRepoDir。
	repoDir := t.TempDir()
	os.WriteFile(filepath.Join(repoDir, "config.yaml"), []byte("k: v"), 0o644)
	got, relSrc, ok := ResolveConfigMount(repoDir, nil)
	if !ok || relSrc != "config.yaml" {
		t.Fatalf("legacy 应 ok=true relSrc=config.yaml got=(%q,%q,%v)", got, relSrc, ok)
	}
	want := toHostRepoDir(filepath.Join(repoDir, "config.yaml"))
	if got != want {
		t.Fatalf("legacy 宿主源路径 got=%q want %q", got, want)
	}
}

func TestResolveConfigMount_None(t *testing.T) {
	// 无 config.yaml 且无 manifest 声明 → 无挂载。
	got, _, ok := ResolveConfigMount(t.TempDir(), nil)
	if ok || got != "" {
		t.Fatalf("无 config 应 ok=false 空路径 got=(%q,%v)", got, ok)
	}
}

func TestResolveConfigMount_ManifestDeclaresConfig(t *testing.T) {
	// manifest 声明 config 条目 → 按 needs.mounts 解析（无 actual 记录 → 重算）。
	repoDir := t.TempDir()
	os.WriteFile(filepath.Join(repoDir, "config.yaml"), []byte("k: v"), 0o644)
	mf := &DeployManifest{
		Needs: NeedsSpec{Mounts: []MountSpec{{Src: "config.yaml", Dst: "/app/config.yaml", ReadOnly: true}}},
	}
	got, relSrc, ok := ResolveConfigMount(repoDir, mf)
	if !ok || relSrc != "config.yaml" {
		t.Fatalf("manifest config 条目应 ok=true relSrc=config.yaml got=(%q,%q,%v)", got, relSrc, ok)
	}
	want := toHostRepoDir(filepath.Join(repoDir, "config.yaml"))
	if got != want {
		t.Fatalf("manifest 解析宿主源 got=%q want %q", got, want)
	}
}

func TestResolveConfigMount_ManifestWithoutConfig_FallsBackToDetect(t *testing.T) {
	// manifest 存在但只声明别的挂载（无 config 条目）→ 防御性仍 detectConfigPath（修回归兼容）。
	repoDir := t.TempDir()
	os.WriteFile(filepath.Join(repoDir, "config.yaml"), []byte("k: v"), 0o644)
	mf := &DeployManifest{
		Needs: NeedsSpec{Mounts: []MountSpec{{Src: "secrets/tls.crt", Dst: "/etc/tls/tls.crt"}}},
	}
	got, relSrc, ok := ResolveConfigMount(repoDir, mf)
	if !ok || relSrc != "config.yaml" {
		t.Fatalf("manifest 无 config 条目应回退探测 ok=true relSrc=config.yaml got=(%q,%q,%v)", got, relSrc, ok)
	}
}

func TestResolveConfigMount_Determinism_RecordedWins(t *testing.T) {
	// actual 记录的宿主源存在 → 优先用记录（确定性：抗 toHostRepoDir 日后回归）。
	recordedHost := filepath.Join(t.TempDir(), "config.yaml") // 一个真实存在的宿主路径
	os.WriteFile(recordedHost, []byte("k: v"), 0o644)
	mf := &DeployManifest{
		Needs:  NeedsSpec{Mounts: []MountSpec{{Src: "config.yaml", Dst: "/app/config.yaml"}}},
		Actual: ActualSpec{MountsSrc: []ActualMount{{Src: "config.yaml", HostSrc: recordedHost}}},
	}
	got, relSrc, ok := ResolveConfigMount("/data/repos/app1", mf)
	if !ok || relSrc != "config.yaml" || got != recordedHost {
		t.Fatalf("确定性应返回记录宿主路径 got=(%q,%q,%v) want (%q,config.yaml,true)", got, relSrc, ok, recordedHost)
	}
}

func TestResolveConfigMount_RecordedStale_FallsBack(t *testing.T) {
	// actual 记录的宿主源不存在（stat 失败）→ 落回 toHostRepoDir(src) 重算。
	repoDir := "/data/repos/app1"
	mf := &DeployManifest{
		Needs:  NeedsSpec{Mounts: []MountSpec{{Src: "config.yaml", Dst: "/app/config.yaml"}}},
		Actual: ActualSpec{MountsSrc: []ActualMount{{Src: "config.yaml", HostSrc: "/nonexistent/zzz/config.yaml"}}},
	}
	got, _, ok := ResolveConfigMount(repoDir, mf)
	if !ok {
		t.Fatal("应 ok=true")
	}
	want := toHostRepoDir(filepath.Join(repoDir, "config.yaml"))
	if got != want {
		t.Fatalf("陈旧记录应回退重算 got=%q want %q", got, want)
	}
}

// === RecordActuals ===

func TestRecordActuals_NilManifest_CreatesWithActualOnly(t *testing.T) {
	dir := t.TempDir()
	if err := RecordActuals(dir, nil, "appdeploy/x:v1", 9100, "config.yaml", "2026-08-09T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	mf, err := LoadDeployManifest(dir)
	if err != nil || mf == nil {
		t.Fatalf("legacy 首次成功应建 manifest err=%v", err)
	}
	if mf.Actual.ImageDigest != "appdeploy/x:v1" {
		t.Fatalf("actual.image_digest got=%q", mf.Actual.ImageDigest)
	}
	if mf.Actual.HostPort != 9100 {
		t.Fatalf("actual.host_port got=%d", mf.Actual.HostPort)
	}
	if mf.Actual.EngineVersion != EngineVersion {
		t.Fatalf("actual.engine_version got=%q want %q", mf.Actual.EngineVersion, EngineVersion)
	}
	if len(mf.Actual.MountsSrc) != 1 || mf.Actual.MountsSrc[0].Src != "config.yaml" {
		t.Fatalf("config 挂载记录缺失 got=%+v", mf.Actual.MountsSrc)
	}
}

func TestRecordActuals_PreservesNeeds(t *testing.T) {
	dir := t.TempDir()
	// opencode 先写了 needs 段
	if err := WriteDeployManifest(dir, &DeployManifest{
		Needs: NeedsSpec{Command: "./run", EnvKeys: []string{"A"}, Ports: []int{9000}},
	}); err != nil {
		t.Fatal(err)
	}
	mf, _ := LoadDeployManifest(dir)
	if err := RecordActuals(dir, mf, "img", 9200, "config.yaml", "ts"); err != nil {
		t.Fatal(err)
	}
	mf2, _ := LoadDeployManifest(dir)
	if mf2.Needs.Command != "./run" || len(mf2.Needs.EnvKeys) != 1 || mf2.Needs.EnvKeys[0] != "A" {
		t.Fatalf("回填 actual 后 needs 段被破坏 got=%+v", mf2.Needs)
	}
	if len(mf2.Needs.Ports) != 1 || mf2.Needs.Ports[0] != 9000 {
		t.Fatalf("needs.ports 被破坏 got=%+v", mf2.Needs.Ports)
	}
	if mf2.Actual.HostPort != 9200 {
		t.Fatalf("actual 未回填 got=%d", mf2.Actual.HostPort)
	}
}

func TestRecordActuals_UpsertUpdatesExisting(t *testing.T) {
	dir := t.TempDir()
	// 已有一条 config 记录 → upsert 更新而非 append
	if err := WriteDeployManifest(dir, &DeployManifest{
		Actual: ActualSpec{MountsSrc: []ActualMount{{Src: "config.yaml", HostSrc: "/old/path"}}},
	}); err != nil {
		t.Fatal(err)
	}
	loaded, _ := LoadDeployManifest(dir)
	if err := RecordActuals(dir, loaded, "img", 9300, "config.yaml", "ts2"); err != nil {
		t.Fatal(err)
	}
	mf2, _ := LoadDeployManifest(dir)
	if len(mf2.Actual.MountsSrc) != 1 {
		t.Fatalf("同 src 应 upsert 非 append got 记录数=%d %+v", len(mf2.Actual.MountsSrc), mf2.Actual.MountsSrc)
	}
}
