package appdeploy

import (
	"context"
	"fmt"
	"path/filepath"
)

// buildConfigGetter 构建配置读取抽象。
// 生产由 *BuildConfigStore 满足（按 app_kind 查 DB）；测试可注入 stub 返回内存配置，避免依赖 DB。
type buildConfigGetter interface {
	Get(ctx context.Context, appKind string) (*BuildConfig, error)
}

// WebBuilder web/service 形态构建器。
// 本期 web/service 部署即产物（容器镜像+URL），不走 artifact 表；
// 实际 web 构建仍由 handler 直接调 Deployer.Build，Builder 仅作接口壳。
type WebBuilder struct{ deployer *Deployer }

func (b *WebBuilder) Name() string { return "WebBuilder" }

// Build web/service 不产出产物文件，返回 nil,nil。
func (b *WebBuilder) Build(ctx context.Context, app *Application) ([]ArtifactOutput, error) {
	return nil, nil
}

// containerBuilder 非共用：非 web 形态（desktop/mobile/cli）跑预置构建容器 + 扫产物目录。
// cfgStore 用接口以便测试注入 stub；kind 用于查对应形态的构建配置。
type containerBuilder struct {
	cfgStore buildConfigGetter
	kind     string
	name     string
}

func (b *containerBuilder) Name() string { return b.name }

func (b *containerBuilder) Build(ctx context.Context, app *Application) ([]ArtifactOutput, error) {
	cfg, err := b.cfgStore.Get(ctx, b.kind)
	if err != nil {
		return nil, fmt.Errorf("read build config: %w", err)
	}
	if cfg == nil {
		return nil, fmt.Errorf("build config missing for kind %s", b.kind)
	}
	if app.RepoDir == "" {
		return nil, fmt.Errorf("app repo_dir empty")
	}
	// docker run --rm -v <repo>:/src <image> sh -c <command>
	// 把宿主 repoDir 挂载进容器 /src，构建命令在容器内产出文件到 artifact_dir（容器内路径）。
	out, err := dockerRun(ctx, "", "run", "--rm",
		"-v", fmt.Sprintf("%s:/src", app.RepoDir),
		cfg.BuildImage, "sh", "-c", cfg.BuildCommand)
	if err != nil {
		return nil, fmt.Errorf("build failed: %s: %w", out, err)
	}
	// 扫产物目录：artifact_dir 是容器内路径（如 /src/dist），映射回宿主路径再扫描。
	scanDir := resolveScanDir(app.RepoDir, cfg.ArtifactDir)
	return ScanArtifacts(scanDir)
}

// resolveScanDir 把容器内 artifact_dir（如 /src/dist）映射回宿主路径（如 <repoDir>/dist）。
// 约定：构建容器把源码挂到 /src，故 artifact_dir 以 /src/ 开头时去掉该前缀拼到 repoDir。
// 其他情况：空或 "/src" 当作 repoDir 本身；绝对/相对路径都去掉前导 / 后拼到 repoDir。
func resolveScanDir(repoDir, artifactDir string) string {
	rel := artifactDir
	if rel == "/src" || rel == "" {
		return repoDir
	}
	if len(rel) > 5 && rel[:5] == "/src/" {
		rel = rel[5:]
	} else if rel[0] == '/' {
		rel = rel[1:]
	}
	return filepath.Join(repoDir, rel)
}

type DesktopBuilder struct{ cfgStore buildConfigGetter }

func (b *DesktopBuilder) Name() string { return "DesktopBuilder" }
func (b *DesktopBuilder) Build(ctx context.Context, app *Application) ([]ArtifactOutput, error) {
	return (&containerBuilder{cfgStore: b.cfgStore, kind: AppKindDesktop, name: "DesktopBuilder"}).Build(ctx, app)
}

type MobileBuilder struct{ cfgStore buildConfigGetter }

func (b *MobileBuilder) Name() string { return "MobileBuilder" }
func (b *MobileBuilder) Build(ctx context.Context, app *Application) ([]ArtifactOutput, error) {
	return (&containerBuilder{cfgStore: b.cfgStore, kind: AppKindMobile, name: "MobileBuilder"}).Build(ctx, app)
}

type CLIBuilder struct{ cfgStore buildConfigGetter }

func (b *CLIBuilder) Name() string { return "CLIBuilder" }
func (b *CLIBuilder) Build(ctx context.Context, app *Application) ([]ArtifactOutput, error) {
	return (&containerBuilder{cfgStore: b.cfgStore, kind: AppKindCLI, name: "CLIBuilder"}).Build(ctx, app)
}
