package appdeploy

import "context"

// Provisioner 按节点 OS 搭建 ANP 需要的运行环境。
type Provisioner struct{}

const linuxProvisionScript = `. /etc/os-release
if command -v docker >/dev/null 2>&1; then echo "docker already installed";
elif command -v apt-get >/dev/null 2>&1; then
  apt-get update -qq && apt-get install -y -qq docker.io && systemctl enable --now docker
elif command -v yum >/dev/null 2>&1; then
  yum install -y docker && systemctl enable --now docker
else echo "unsupported distro"; exit 1; fi
docker version --format '{{.Server.Version}}'`

const windowsProvisionScript = `$env:COMPUTERNAME
Get-Command dotnet -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Source
Get-Service W3SVC -ErrorAction SilentlyContinue | Select-Object Status`

// Provision 搭建环境，返回日志。Linux 装 Docker；Windows 只验连通+探测（不装 Docker）。
func (p *Provisioner) Provision(ctx context.Context, n *DeployNode, exec RemoteExecutor) (string, error) {
	script := linuxProvisionScript
	if n.OSType == "windows" {
		script = windowsProvisionScript
	}
	out, _, exit, err := exec.Run(ctx, script)
	if err != nil {
		return out, err
	}
	_ = exit
	return out, nil
}
