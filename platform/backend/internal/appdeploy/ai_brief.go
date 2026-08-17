package appdeploy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// deployResultRelPath AI 回报文件（AI 引擎专用；验证后由 aiDeploy 删除）。
const deployResultRelPath = ".anp/deploy-result.yaml"

// BriefInput BuildDeployBrief 的入参（全部已解析值：端口已预分配、版本号已自增）。
type BriefInput struct {
	AppName  string
	Slug     string // dockerSlug(app.Name)
	Env      string
	RepoDir  string // 主仓（.anp 目录所在）
	BuildDir string // 构建目录（from_workspace 时为 dev-<user> worktree；否则同 RepoDir）
	Version  string // 本次版本号（十进制字符串，无 v 前缀）
	Port     int    // 平台预分配宿主端口
	EnvKeys  []string
	Needs    *NeedsSpec  // 可 nil（legacy 无 manifest）
	Actual   *ActualSpec // 可 nil（首次部署）
}

// BuildDeployBrief 组装部署简报（平台 → AI 的输入契约，spec §2）。
// 硬性规则四条 + 应用上下文（needs/actual）+ 任务清单。纯函数。
func BuildDeployBrief(in BriefInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 部署简报：%s → %s v%s\n\n", in.AppName, in.Env, in.Version)
	b.WriteString("## 硬性规则（违反=部署判失败）\n\n")
	fmt.Fprintf(&b, "- 容器名必须: appdeploy-%s-%s-v%s\n", in.Slug, in.Env, in.Version)
	fmt.Fprintf(&b, "- 镜像 tag 必须: appdeploy/%s-%s:v%s\n", in.Slug, in.Env, in.Version)
	if in.Port > 0 {
		fmt.Fprintf(&b, "- 宿主端口必须用: %d（已预留，不许自选）\n", in.Port)
	} else {
		b.WriteString("- headless 应用：无端口发布（不加 -p，不占宿主端口）\n")
	}
	fmt.Fprintf(&b, "- 只能操作 name 前缀 appdeploy-%s- 的容器（先清理旧版本再起新的）\n", in.Slug)
	if len(in.EnvKeys) > 0 {
		fmt.Fprintf(&b, "- 密钥 %s 已注入你的环境变量，禁止把值写进任何文件/日志\n", strings.Join(in.EnvKeys, ", "))
	}
	b.WriteString("\n## 应用上下文\n\n")
	fmt.Fprintf(&b, "- 仓库: %s\n", in.RepoDir)
	fmt.Fprintf(&b, "- 构建目录: %s\n", in.BuildDir)
	if in.Needs != nil {
		fmt.Fprintf(&b, "- 声明(needs): 监听端口 %v / 启动命令 %q / 挂载 %d 条 / env_keys %v\n",
			in.Needs.Ports, in.Needs.Command, len(in.Needs.Mounts), in.Needs.EnvKeys)
		for _, m := range in.Needs.Mounts {
			fmt.Fprintf(&b, "  - %s → %s (readonly=%v)\n", m.Src, m.Dst, m.ReadOnly)
		}
	} else {
		b.WriteString("- 声明(needs): 无 .anp/deploy.yaml（请自行分析代码推断端口/命令）\n")
	}
	if in.Actual != nil && in.Actual.ImageDigest != "" {
		fmt.Fprintf(&b, "- 上次成功部署(actual,供参考): 镜像 %s / 宿主端口 %d\n", in.Actual.ImageDigest, in.Actual.HostPort)
	}
	b.WriteString("\n## 你的任务\n\n")
	b.WriteString("1. 分析构建目录代码与简报\n")
	b.WriteString("2. docker build 镜像（tag 用上面规定的）\n")
	b.WriteString("3. 清理旧版本容器（限本应用前缀）\n")
	b.WriteString("4. docker run 新容器（名称/端口按硬性规则；needs 声明的挂载逐条 -v；env 用你环境变量里的密钥）\n")
	b.WriteString("5. 自检：容器 Up、docker inspect 端口绑定正确\n")
	fmt.Fprintf(&b, "6. 写 %s（字段 container/image/listen_port/command/mounts/notes）\n", deployResultRelPath)
	return b.String()
}

// DeployResult AI 的结构化回报（.anp/deploy-result.yaml）。
type DeployResult struct {
	Container  string        `yaml:"container"`
	Image      string        `yaml:"image"`
	ListenPort int           `yaml:"listen_port"`
	Command    string        `yaml:"command"`
	Mounts     []ResultMount `yaml:"mounts"`
	Notes      string        `yaml:"notes"`
}

// ResultMount AI 回报的一条挂载（src=仓库相对源，dst=容器目标）。
type ResultMount struct {
	Src string `yaml:"src"`
	Dst string `yaml:"dst"`
}

// LoadDeployResult 读 AI 回报；不存在 (nil,nil)（AI 未回报，验证步 1 判失败）。
func LoadDeployResult(repoDir string) (*DeployResult, error) {
	b, err := os.ReadFile(filepath.Join(repoDir, deployResultRelPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var r DeployResult
	if err := yaml.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", deployResultRelPath, err)
	}
	return &r, nil
}

// ValidateResult 静态合规校验（纯函数）：必要字段 + 容器名/镜像 tag 与平台规则一致。
// 宿主端口/Running/镜像实态由 verifyAIResult 从容器实态查（不信自报）。
func ValidateResult(r *DeployResult, slug, env, version string, port int) error {
	if r == nil {
		return fmt.Errorf("deploy-result 为空")
	}
	if r.Container == "" || r.Image == "" {
		return fmt.Errorf("deploy-result 缺 container/image")
	}
	wantName := fmt.Sprintf("appdeploy-%s-%s-v%s", slug, env, version)
	if r.Container != wantName {
		return fmt.Errorf("容器名 %q 不合规（须 %q）", r.Container, wantName)
	}
	wantImage := fmt.Sprintf("appdeploy/%s-%s:v%s", slug, env, version)
	if r.Image != wantImage {
		return fmt.Errorf("镜像 tag %q 不合规（须 %q）", r.Image, wantImage)
	}
	return nil
}
