package appdeploy

import (
	"context"
	"fmt"
)

// Builder 把应用源码构建为产物。按 AppKind 分派：web 走原有容器逻辑，其余走预置构建容器。
type Builder interface {
	// Build 在构建环境里跑构建，返回产出的产物描述列表（可能多个平台/架构）。
	// web/service 返回 nil（部署即产物，不走 artifact 表）。
	Build(ctx context.Context, app *Application) ([]ArtifactOutput, error)
	// Name 构建器名称（用于日志/测试断言）。
	Name() string
}

// DispatchBuilder 按 app_kind 选构建器。
// deployer：web/service 用（现有容器构建逻辑）；cfgStore：非 web 读构建配置。
func DispatchBuilder(appKind string, deployer *Deployer, cfgStore *BuildConfigStore) (Builder, error) {
	switch appKind {
	case AppKindWeb, AppKindService:
		return &WebBuilder{deployer: deployer}, nil
	case AppKindDesktop:
		return &DesktopBuilder{cfgStore: cfgStore}, nil
	case AppKindMobile:
		return &MobileBuilder{cfgStore: cfgStore}, nil
	case AppKindCLI:
		return &CLIBuilder{cfgStore: cfgStore}, nil
	}
	return nil, fmt.Errorf("unsupported app_kind: %s", appKind)
}
