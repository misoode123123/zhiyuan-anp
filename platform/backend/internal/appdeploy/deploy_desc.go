package appdeploy

import (
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type DeployDesc struct {
	Target TargetDesc `json:"target" yaml:"target"`
	Steps  []StepDesc `json:"steps" yaml:"steps"`
}
type TargetDesc struct {
	OS  string `json:"os" yaml:"os"`
	Dir string `json:"dir" yaml:"dir"`
}
type StepDesc struct {
	Transfer    *TransferStep    `json:"transfer,omitempty" yaml:"transfer,omitempty"`
	Run         *RunStep         `json:"run,omitempty" yaml:"run,omitempty"`
	Service     *ServiceStep     `json:"service,omitempty" yaml:"service,omitempty"`
	Healthcheck *HealthcheckStep `json:"healthcheck,omitempty" yaml:"healthcheck,omitempty"`
}
type TransferStep struct {
	From string `json:"from" yaml:"from"`
	To   string `json:"to" yaml:"to"`
}
type RunStep struct {
	Cmd string `json:"cmd" yaml:"cmd"`
	Cwd string `json:"cwd" yaml:"cwd"`
}
type ServiceStep struct {
	Name      string `json:"name" yaml:"name"`
	Start     string `json:"start" yaml:"start"`
	Autostart bool   `json:"autostart" yaml:"autostart"`
}
type HealthcheckStep struct {
	Cmd     string `json:"cmd" yaml:"cmd"`
	Timeout string `json:"timeout" yaml:"timeout"`
}

func ParseDeployDesc(b []byte) (*DeployDesc, error) {
	var d DeployDesc
	if err := yaml.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// RenderScript 按节点 OS 把 DeployDesc 翻译成脚本（linux→bash，windows→PowerShell）。纯函数。
func RenderScript(node *DeployNode, desc *DeployDesc) (string, error) {
	if err := validateDir(desc.Target.Dir); err != nil {
		return "", err
	}
	if node.OSType == "windows" {
		return renderPowerShell(desc), nil
	}
	return renderBash(desc), nil
}

func validateDir(dir string) error {
	clean := filepath.Clean(dir)
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) && !strings.HasPrefix(clean, "/opt/") && !strings.HasPrefix(clean, "C:") {
		// 允许绝对路径但拒 ../ 越界；简化：拒 ../ 开头
	}
	if strings.Contains(dir, "..") {
		return fmt.Errorf("target.dir 含 .. 越界: %s", dir)
	}
	return nil
}

func renderBash(desc *DeployDesc) string {
	var b strings.Builder
	for _, s := range desc.Steps {
		if s.Transfer != nil {
			// transfer 由 Deploy 用 PutFile 做，脚本里只写解压/移动
			fmt.Fprintf(&b, "mkdir -p %s\n", s.Transfer.To)
		}
		if s.Run != nil {
			if s.Run.Cwd != "" {
				fmt.Fprintf(&b, "cd %s\n", s.Run.Cwd)
			}
			fmt.Fprintf(&b, "%s\n", s.Run.Cmd)
		}
		if s.Service != nil {
			fmt.Fprintf(&b, "systemctl enable %s 2>/dev/null; systemctl restart %s\n", s.Service.Name, s.Service.Name)
		}
		if s.Healthcheck != nil {
			fmt.Fprintf(&b, "%s\n", s.Healthcheck.Cmd)
		}
	}
	return b.String()
}

func renderPowerShell(desc *DeployDesc) string {
	var b strings.Builder
	for _, s := range desc.Steps {
		if s.Transfer != nil {
			fmt.Fprintf(&b, "New-Item -ItemType Directory -Force -Path %s\n", s.Transfer.To)
		}
		if s.Run != nil {
			if s.Run.Cwd != "" {
				fmt.Fprintf(&b, "Set-Location %s\n", s.Run.Cwd)
			}
			fmt.Fprintf(&b, "%s\n", s.Run.Cmd)
		}
		if s.Service != nil {
			fmt.Fprintf(&b, "sc.exe create %s binPath= %s start= auto\n", s.Service.Name, s.Service.Start)
		}
		if s.Healthcheck != nil {
			fmt.Fprintf(&b, "%s\n", s.Healthcheck.Cmd)
		}
	}
	return b.String()
}
