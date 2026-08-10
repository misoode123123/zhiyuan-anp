package appdeploy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// EngineVersion 部署引擎版本（main 可经 -ldflags "-X ...appdeploy.EngineVersion=v..."
// 注入）。默认 "dev"。部署成功后写 .anp/deploy.yaml.actual.engine_version，供回归定位
// 「这次部署是哪版引擎部署的」——出问题时能迅速判别是否引擎回归所致。
var EngineVersion = "dev"

// deployManifestRelPath 部署清单在仓库内的固定位置（与 deps.yaml 同级）。
const deployManifestRelPath = ".anp/deploy.yaml"

// DeployManifest .anp/deploy.yaml 的内存模型 —— 分「需求」与「实际」两段：
//   - Needs  （开发 / opencode 维护）：部署这个应用需要什么（挂载 / env / 端口 / 命令）。
//   - Actual （引擎成功后回填）   ：上次成功部署的实际值（镜像 / 已解析宿主源 / 宿主端口 / 引擎版本）。
//
// 设计意图：需求段是「声明」（人/AI 维护、引擎只读消费），实际段是「记录」（引擎回填、
// 下次部署确定性重放优先采用）。两段同处一文件，让部署需求与实际部署态可对照、可审计。
type DeployManifest struct {
	Needs  NeedsSpec  `yaml:"needs"`
	Actual ActualSpec `yaml:"actual,omitempty"`
}

// NeedsSpec 部署需求（开发侧声明，引擎只读消费）。
type NeedsSpec struct {
	Mounts  []MountSpec `yaml:"mounts,omitempty" json:"mounts"`             // 额外挂载：仓库相对源 → 容器目标（密钥/配置文件，不进镜像层）
	EnvKeys []string    `yaml:"env_keys,omitempty" json:"env_keys"`         // 需注入的 env key（值由 ANP 填/校验，如 CONFIG_PATH/REDIS_ADDR）
	Ports   []int       `yaml:"ports,omitempty" json:"ports"`               // 应用监听端口（与 Application.InternalPort 对照）
	Command string      `yaml:"command,omitempty" json:"command,omitempty"` // 覆盖启动命令（空=用镜像默认 ENTRYPOINT/CMD）
}

// MountSpec 一条挂载声明。Src 相对仓库根（如 config.yaml、secrets/db.crt）；
// Dst 容器内绝对路径（如 /app/config.yaml）。ReadOnly 默认按 true 处理（密钥类只读挂载）。
type MountSpec struct {
	Src      string `yaml:"src" json:"src"`
	Dst      string `yaml:"dst" json:"dst"`
	ReadOnly bool   `yaml:"readonly,omitempty" json:"readonly,omitempty"`
}

// ActualSpec 上次成功部署的实际值（引擎回填，开发只读）。
type ActualSpec struct {
	ImageDigest   string        `yaml:"image_digest,omitempty"`   // 镜像引用（appdeploy/<name>:v<n>）
	MountsSrc     []ActualMount `yaml:"mounts_src,omitempty"`     // 已解析的宿主源路径（下次部署优先用=确定性）
	HostPort      int           `yaml:"host_port,omitempty"`      // 分配的宿主端口
	EngineVersion string        `yaml:"engine_version,omitempty"` // 部署此版的引擎版本
}

// ActualMount 一条已解析挂载的宿主源路径记录（按 Src 索引，与 Needs.Mounts[].Src 对应）。
type ActualMount struct {
	Src        string `yaml:"src"`                   // 仓库相对源（与 needs.mounts[].src 对应）
	HostSrc    string `yaml:"host_src"`              // 解析出的宿主绝对路径（确定性重放基准）
	RecordedAt string `yaml:"recorded_at,omitempty"` // 记录时间（RFC3339，便于排查陈旧记录）
}

// LoadDeployManifest 从应用仓库读 .anp/deploy.yaml；不存在返回 (nil, nil)（legacy 应用，
// 引擎回退自动探测 + toHostRepoDir）。解析失败返回 error（调用方决定是否阻塞）。
func LoadDeployManifest(repoDir string) (*DeployManifest, error) {
	b, err := os.ReadFile(filepath.Join(repoDir, deployManifestRelPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var mf DeployManifest
	if err := yaml.Unmarshal(b, &mf); err != nil {
		return nil, fmt.Errorf("解析 .anp/deploy.yaml: %w", err)
	}
	return &mf, nil
}

// normalizeNeeds 归一化 NeedsSpec 的切片为非 nil 空数组，供 JSON 序列化出 [] 而非 null/省略。
// .anp/deploy.yaml 的 needs:{} 反序列化得 nil 切片——前端 detail.deploy_needs.ports.length
// 命中 undefined（null 或省略）会崩整个详情组件（yxt-eino-v2 回归根因）。配合 NeedsSpec 的
// json tag（Mounts/EnvKeys/Ports 无 omitempty）双重保障：归一保证非 nil，去 omitempty 保证出现。
func normalizeNeeds(n NeedsSpec) NeedsSpec {
	if n.Mounts == nil {
		n.Mounts = []MountSpec{}
	}
	if n.EnvKeys == nil {
		n.EnvKeys = []string{}
	}
	if n.Ports == nil {
		n.Ports = []int{}
	}
	return n
}

// WriteDeployManifest 把 manifest 写回仓库 .anp/deploy.yaml（Needs+Actual 整体写）。
// 调用方须先 Load 再回填 Actual，以保留 opencode 维护的 Needs 段。
func WriteDeployManifest(repoDir string, mf *DeployManifest) error {
	if mf == nil {
		return fmt.Errorf("manifest 为 nil")
	}
	if err := os.MkdirAll(filepath.Join(repoDir, ".anp"), 0o755); err != nil {
		return err
	}
	b, err := yaml.Marshal(mf)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(repoDir, deployManifestRelPath), b, 0o644)
}

// findActualMount 在 Actual.MountsSrc 中按 Src 查记录；无则 nil。nil-safe（mf 可为 nil）。
func (mf *DeployManifest) findActualMount(src string) *ActualMount {
	if mf == nil {
		return nil
	}
	for i := range mf.Actual.MountsSrc {
		if mf.Actual.MountsSrc[i].Src == src {
			return &mf.Actual.MountsSrc[i]
		}
	}
	return nil
}

// upsertActualMount 按 Src 更新或追加一条 actual 挂载记录。
func (mf *DeployManifest) upsertActualMount(src, hostSrc, recordedAt string) {
	for i := range mf.Actual.MountsSrc {
		if mf.Actual.MountsSrc[i].Src == src {
			mf.Actual.MountsSrc[i].HostSrc = hostSrc
			mf.Actual.MountsSrc[i].RecordedAt = recordedAt
			return
		}
	}
	mf.Actual.MountsSrc = append(mf.Actual.MountsSrc, ActualMount{Src: src, HostSrc: hostSrc, RecordedAt: recordedAt})
}

// resolveMountHostSrc 解析一条挂载的宿主源路径（resolved-priority）：
//  1. Actual 已记录该 Src 的宿主路径，且当前可读 → 用记录（确定性：抗引擎回归——
//     即使 toHostRepoDir 逻辑日后变错，也能命中上次成功部署的真实宿主路径）。
//  2. 否则 → 仓库相对源 → toHostRepoDir 翻译成宿主路径（修 config 挂载回归：
//     原直接用容器路径 /data/repos/... 作 -v 源，宿主不存在致 docker 建空目录 → 应用读「is a directory」崩）。
//
// stat 失败（本容器看不到宿主路径，未挂载 /opt/anp/data/repos）也落回重算——
// 重算结果正确即可，只是不享受记录缓存，不影响部署成功。
func resolveMountHostSrc(repoDir, relSrc string, recorded *ActualMount) string {
	if recorded != nil && recorded.HostSrc != "" {
		if _, err := os.Stat(recorded.HostSrc); err == nil {
			return recorded.HostSrc
		}
	}
	return toHostRepoDir(filepath.Join(repoDir, relSrc))
}

// ResolveConfigMount 解析 config 挂载（dst=/app/config.yaml）的宿主源路径与相对源：
//   - manifest 驱动：Needs.Mounts 找 dst=/app/config.yaml 条目 → resolved-priority 解析其 src。
//   - 无 manifest，或 manifest 无 config 条目：detectConfigPath + toHostRepoDir（修回归 + 兼容 legacy 应用）。
//
// ok=false 表示该应用无 config 挂载。返回的 hostPath 是宿主绝对路径，直接喂给 Deployer 的 -v；
// relSrc 是仓库相对源（config.yaml），供成功后 RecordActuals 记录确定性基准。
func ResolveConfigMount(repoDir string, mf *DeployManifest) (hostPath, relSrc string, ok bool) {
	if mf != nil {
		for _, m := range mf.Needs.Mounts {
			if m.Dst == "/app/config.yaml" && m.Src != "" {
				return resolveMountHostSrc(repoDir, m.Src, mf.findActualMount(m.Src)), m.Src, true
			}
		}
	}
	if p := detectConfigPath(repoDir); p != "" {
		// legacy / 未声明：detectConfigPath 返容器路径，toHostRepoDir 翻成宿主路径（核心修复）。
		return toHostRepoDir(p), "config.yaml", true
	}
	return "", "", false
}

// RecordActuals 部署成功后回填 Actual（镜像 / 已解析宿主源 / 宿主端口 / 引擎版本），并写回仓库。
// Needs 段保持不变（opencode 维护）。mf 为 nil 时（legacy 应用首次成功部署）建一份仅含 Actual 的记录。
// recordedAt 由调用方传入（time.Now().Format(RFC3339)）——注入而非函数内生成，使单测可重放。失败返回 error。
func RecordActuals(repoDir string, mf *DeployManifest, image string, hostPort int, configSrc, recordedAt string) error {
	if mf == nil {
		mf = &DeployManifest{}
	}
	mf.Actual.ImageDigest = image
	mf.Actual.HostPort = hostPort
	mf.Actual.EngineVersion = EngineVersion
	// 记录 config 挂载的已解析宿主源（下次确定性重放优先采用）。
	if configSrc != "" {
		hostSrc := resolveMountHostSrc(repoDir, configSrc, mf.findActualMount(configSrc))
		mf.upsertActualMount(configSrc, hostSrc, recordedAt)
	}
	return WriteDeployManifest(repoDir, mf)
}

// ResolvedMount 一条已解析到宿主源的挂载（Deployer.Deploy 据此拼 docker -v）。
type ResolvedMount struct {
	HostSrc  string // 宿主绝对路径（resolveMountHostSrc 解析：actual 记录优先 → toHostRepoDir 重算）
	Dst      string // 容器内目标路径
	ReadOnly bool   // 只读挂载（密钥/配置类默认 true）
}

// ResolveExtraMounts 解析 needs.mounts 中**非 config** 的挂载（dst != /app/config.yaml）为宿主源。
// config 挂载由 ResolveConfigMount 专门处理（含 legacy 探测 + 确定性 actual 重放），此处跳过避免重复/冲突。
// mf 为 nil（legacy 无 manifest）返回 nil。每条按 resolved-priority 解析。
//
// 注：extra 挂载的 actual 记录 v1 不回填（RecordActuals 仍只记 config），故恒走重算路径——
// 结果正确，只是不享受确定性缓存；extra 挂载的确定性重放为 follow-up。
func ResolveExtraMounts(repoDir string, mf *DeployManifest) []ResolvedMount {
	if mf == nil {
		return nil
	}
	var out []ResolvedMount
	for _, m := range mf.Needs.Mounts {
		if m.Dst == "/app/config.yaml" || m.Src == "" {
			continue
		}
		out = append(out, ResolvedMount{
			HostSrc:  resolveMountHostSrc(repoDir, m.Src, mf.findActualMount(m.Src)),
			Dst:      m.Dst,
			ReadOnly: m.ReadOnly,
		})
	}
	return out
}

// missingEnvKeys 返回 needs.env_keys 中无值来源的 key：
//   - envPairs 含 KEY=…（应用运行时变量，含密钥）
//   - 自动注入的 PORT（ensurePortEnv 恒注入）；hasConfig 时 CONFIG_PATH 也注入
//
// 其余声明但无值来源的 key 视为「缺值」（中间件未绑定 / 密钥未配）。
// 纯函数，供 validateEnvKeys 软校验单测。mf 为 nil 返回 nil。
func missingEnvKeys(mf *DeployManifest, envPairs []string, hasConfig bool) []string {
	if mf == nil || len(mf.Needs.EnvKeys) == 0 {
		return nil
	}
	present := map[string]bool{"PORT": true} // ensurePortEnv 恒注入 PORT
	if hasConfig {
		present["CONFIG_PATH"] = true
	}
	for _, kv := range envPairs {
		if i := strings.IndexByte(kv, '='); i > 0 {
			present[kv[:i]] = true
		}
	}
	var missing []string
	for _, k := range mf.Needs.EnvKeys {
		if !present[k] {
			missing = append(missing, k)
		}
	}
	return missing
}
