package appdeploy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// stubCfgStore 内存实现 buildConfigGetter（避免依赖 DB）。
type stubCfgStore struct{ cfg *BuildConfig }

func (s *stubCfgStore) Get(_ context.Context, _ string) (*BuildConfig, error) { return s.cfg, nil }

// withFakeDocker 临时把 dockerRun 替换为不执行真实 docker 的桩。
func withFakeDocker(fn func()) {
	orig := dockerRun
	dockerRun = func(_ context.Context, _ string, _ ...string) (string, error) { return "", nil }
	defer func() { dockerRun = orig }()
	fn()
}

func TestDesktopBuilder_Build_ScansArtifacts(t *testing.T) {
	// 用 fake runDockerOn：把"产物"直接写进宿主产物目录模拟构建结果。
	// 约定：构建容器把源码挂到 /src，产物输出到 /src/dist（容器内），映射回宿主即 <repoDir>/dist。
	dir := t.TempDir()
	distDir := filepath.Join(dir, "dist")
	if err := os.MkdirAll(distDir, 0755); err != nil {
		t.Fatal(err)
	}
	// 模拟构建容器产出（落到 repoDir/dist，即容器内 /src/dist 的宿主映射）
	if err := os.WriteFile(filepath.Join(distDir, "myapp-win-x64.exe"), []byte("exe"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(distDir, "myapp-mac.dmg"), []byte("dmg"), 0644); err != nil {
		t.Fatal(err)
	}

	b := &DesktopBuilder{cfgStore: &stubCfgStore{cfg: &BuildConfig{
		AppKind: AppKindDesktop, BuildImage: "img", BuildCommand: "cmd",
		ArtifactDir: "/src/dist", Scaffold: "electron-react-ts",
	}}}
	// 注入 fake docker runner（dockerRun 变量替换）
	withFakeDocker(func() {
		outs, err := b.Build(context.Background(), &Application{ID: "app_1", RepoDir: dir, AppKind: AppKindDesktop})
		if err != nil {
			t.Fatal(err)
		}
		if len(outs) != 2 {
			t.Fatalf("got %d artifacts, want 2", len(outs))
		}
	})
}

func TestResolveScanDir(t *testing.T) {
	cases := []struct{ repoDir, artifactDir, want string }{
		{"/host/repo", "/src/dist", "/host/repo/dist"},
		{"/host/repo", "/src", "/host/repo"},
		{"/host/repo", "", "/host/repo"},
		{"/host/repo", "dist", "/host/repo/dist"},
		{"/host/repo", "/output/pkg", "/host/repo/output/pkg"},
	}
	for _, c := range cases {
		got := resolveScanDir(c.repoDir, c.artifactDir)
		// 统一用 filepath 清理后比较（跨平台分隔符差异）
		if filepath.Clean(got) != filepath.Clean(c.want) {
			t.Fatalf("resolveScanDir(%q,%q)=%q want %q", c.repoDir, c.artifactDir, got, filepath.Clean(c.want))
		}
	}
}
