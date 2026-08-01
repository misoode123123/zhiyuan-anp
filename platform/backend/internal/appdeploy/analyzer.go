package appdeploy

import (
	"fmt"
	"os"
	"path/filepath"
)

// DeployAnalysis 导入部署分析的结构化结果（规则扫描产出，供编码适配/依赖注入/人工参考）。
type DeployAnalysis struct {
	Language     string          `json:"language"`
	Framework    string          `json:"framework"`
	Build        BuildAnalysis   `json:"build"`
	AppKindGuess string          `json:"app_kind_guess"`
	Deps         []DeployDep     `json:"deps"`
	Network      NetworkAnalysis `json:"network"`
	Ports        PortsAnalysis   `json:"ports"`
	Mismatches   []string        `json:"mismatches"`
	AdaptHints   []string        `json:"adapt_hints"`
}

// BuildAnalysis 构建方式检测。
type BuildAnalysis struct {
	Dockerfile      bool     `json:"dockerfile"`
	Compose         bool     `json:"compose"`
	ComposeServices []string `json:"compose_services"`
}

// DeployDep 外部依赖（中间件等）。
type DeployDep struct {
	Kind     string `json:"kind"`
	Addr     string `json:"addr"`
	Database string `json:"database,omitempty"`
}

// NetworkAnalysis 网络需求。
type NetworkAnalysis struct {
	HostModeRequired bool   `json:"host_mode_required"`
	Reason           string `json:"reason,omitempty"`
}

// PortsAnalysis 暴露端口。
type PortsAnalysis struct {
	Expose []int `json:"expose"`
}

// Analyze 扫描 repoDir 产出 DeployAnalysis（纯函数：只读、不联网、不写文件）。
// 扫描排除 .git/node_modules/vendor 等依赖与构建产物目录。
// deps/network/appkind/mismatches 由后续 Task 在本函数内补全。
func Analyze(repoDir string) (*DeployAnalysis, error) {
	root := filepath.Clean(repoDir)
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("仓库目录无效: %s", repoDir)
	}
	a := &DeployAnalysis{}
	a.Language, a.Framework = detectLanguage(root)
	a.Build = detectBuild(root)
	a.Ports = detectPorts(root)
	a.Deps = detectDeps(root)
	a.Network = detectNetwork(root)
	a.AppKindGuess = guessAppKind(root, a)
	a.Mismatches, a.AdaptHints = computeMismatches(a)
	return a, nil
}
