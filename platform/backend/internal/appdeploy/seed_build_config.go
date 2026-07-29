// seed_build_config.go 各非 web 形态的默认构建配置 + 形态编码规范 seed（启动时调一次，幂等）。
package appdeploy

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// defaultBuildConfigs 各非 web 形态默认构建配置。web 不需要（走自带 Dockerfile）。
func defaultBuildConfigs() []BuildConfig {
	return []BuildConfig{
		{AppKind: AppKindDesktop, BuildImage: "anp/builder-electron:latest",
			BuildCommand: "cd /src && npm ci && npx electron-builder --win --mac --linux",
			ArtifactDir:  "/src/dist", Scaffold: "electron-react-ts"},
		{AppKind: AppKindMobile, BuildImage: "anp/builder-android:latest",
			BuildCommand: "cd /src && ./gradlew assembleRelease",
			ArtifactDir:  "/src/app/build/outputs/apk/release", Scaffold: "android-flutter"},
		{AppKind: AppKindCLI, BuildImage: "anp/builder-go-cross:latest",
			BuildCommand: "cd /src && go build -o dist/mycli-linux-x64 . && GOOS=darwin GOARCH=amd64 go build -o dist/mycli-darwin-x64 .",
			ArtifactDir:  "/src/dist", Scaffold: "go-cli"},
		{AppKind: AppKindService, BuildImage: "anp/builder-go-cross:latest",
			BuildCommand: "cd /src && go build -o dist/svc .",
			ArtifactDir:  "/src/dist", Scaffold: "web"}, // service 本期等同 web，仅占位
	}
}

// SeedBuildConfigs 幂等写入默认构建配置。
//
// appdeploy_build_config 以 app_kind 为主键，故用 ON CONFLICT (app_kind) DO NOTHING
// 兜底重复（SQLite 3.24+ / PostgreSQL 均支持）。CURRENT_TIMESTAMP 跨库通用。
func SeedBuildConfigs(ctx context.Context, db *sqlx.DB) error {
	for _, c := range defaultBuildConfigs() {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO appdeploy_build_config (app_kind, build_image, build_command, artifact_dir, scaffold, created_at)
			 VALUES ($1,$2,$3,$4,$5,CURRENT_TIMESTAMP)
			 ON CONFLICT (app_kind) DO NOTHING`,
			c.AppKind, c.BuildImage, c.BuildCommand, c.ArtifactDir, c.Scaffold); err != nil {
			return fmt.Errorf("seed build_config %s: %w", c.AppKind, err)
		}
	}
	return nil
}

// kindStandardDef 形态编码规范定义（全局，project_space_id IS NULL）。
type kindStandardDef struct {
	name     string
	category string // 强制 / 应遵循
	content  string
}

// defaultKindStandards 各非 web 形态的全局编码规范。
func defaultKindStandards() []kindStandardDef {
	return []kindStandardDef{
		{"desktop-packaging", "强制", "Electron 应用：主进程/渲染进程分离；禁用 nodeIntegration + 开启 contextIsolation；打包配置须含 win/mac/linux 三目标；产物文件名须含平台与架构段（如 *-win-x64.exe）。"},
		{"mobile-android", "强制", "Android 应用：权限最小化（仅声明必需）；禁止主线程做耗时操作；apk 构建用 release 变体；产物文件名须含 -release.apk。"},
		{"cli-cross-platform", "强制", "CLI 工具：跨平台路径用 filepath 不拼死分隔符；退出码语义明确（0 成功/非 0 失败）；无外部运行时依赖；产物为单文件二进制，文件名含 os-arch 段。"},
		{"service-build", "应遵循", "后端服务：构建产出可执行二进制或容器镜像；本期等同 web 构建，遵循 web 编码规范。"},
	}
}

// SeedKindStandards 向 coding_standard seed 各形态编码规范（全局，project_space_id IS NULL）。
//
// 方案说明（详见 task-9-report.md）：
//   - standard.Store 未暴露 ExistsByName/CreateGlobal，其 db 字段未导出，无法从本包复用。
//   - 为不擅自改 standard 包公开 API，退化为直接 db.Exec 写 coding_standard 表。
//   - coding_standard 表无 name 唯一约束（仅 id 主键），故用
//     `INSERT ... WHERE NOT EXISTS(SELECT 1 ... WHERE name=$1 AND project_space_id IS NULL)`
//     实现幂等（PG/SQLite 均支持）。
//
// 规范层级 scope=platform（全平台生效），module=""（非 module 层）。
func SeedKindStandards(ctx context.Context, db *sqlx.DB) error {
	for _, d := range defaultKindStandards() {
		id := "std_" + uuid.NewString()[:21]
		if _, err := db.ExecContext(ctx,
			`INSERT INTO coding_standard (id, project_space_id, name, category, content, priority, enabled, scope, module)
			 SELECT $1, NULL, $2, $3, $4, 100, TRUE, 'platform', ''
			 WHERE NOT EXISTS (
			   SELECT 1 FROM coding_standard WHERE name=$2 AND project_space_id IS NULL)`,
			id, d.name, d.category, d.content); err != nil {
			return fmt.Errorf("seed kind standard %s: %w", d.name, err)
		}
	}
	return nil
}
