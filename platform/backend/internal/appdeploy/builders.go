package appdeploy

import "context"

// 桩：Task 7 补 Build 实现。
type WebBuilder struct{ deployer *Deployer }

func (b *WebBuilder) Name() string { return "WebBuilder" }
func (b *WebBuilder) Build(ctx context.Context, app *Application) ([]ArtifactOutput, error) {
	return nil, nil
}

type DesktopBuilder struct{ cfgStore *BuildConfigStore }

func (b *DesktopBuilder) Name() string { return "DesktopBuilder" }
func (b *DesktopBuilder) Build(ctx context.Context, app *Application) ([]ArtifactOutput, error) {
	return nil, nil
}

type MobileBuilder struct{ cfgStore *BuildConfigStore }

func (b *MobileBuilder) Name() string { return "MobileBuilder" }
func (b *MobileBuilder) Build(ctx context.Context, app *Application) ([]ArtifactOutput, error) {
	return nil, nil
}

type CLIBuilder struct{ cfgStore *BuildConfigStore }

func (b *CLIBuilder) Name() string { return "CLIBuilder" }
func (b *CLIBuilder) Build(ctx context.Context, app *Application) ([]ArtifactOutput, error) {
	return nil, nil
}
