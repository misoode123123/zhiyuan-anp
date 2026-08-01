package mwsupply

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DepsManifest 仓库根 .anp/deps.yaml 的依赖声明（opencode 适配回写；可手编）。
type DepsManifest struct {
	Services []DepService `yaml:"services"`
}

// DepService 单个中间件依赖声明。Strategy 可空（走默认 bind_existing）。
type DepService struct {
	Kind     string `yaml:"kind"`
	Strategy string `yaml:"strategy"` // 可选：bind_existing / shared / dedicated
}

// LoadDepsManifest 读 repoDir/.anp/deps.yaml。
// 无文件=空清单（不报错，应用无额外中间件依赖）；解析失败按空清单（best-effort，不阻塞部署）。
func LoadDepsManifest(repoDir string) (*DepsManifest, error) {
	data, err := os.ReadFile(filepath.Join(repoDir, ".anp", "deps.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return &DepsManifest{}, nil
		}
		return nil, err
	}
	var m DepsManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return &DepsManifest{}, nil
	}
	return &m, nil
}
