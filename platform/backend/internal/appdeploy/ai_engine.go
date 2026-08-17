package appdeploy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

// aiTimeout AI 部署整体超时（spec §3：15 分钟；opencode 分析+构建+启动+自检）。
const aiTimeout = 15 * time.Minute

// aiOpencodeRun 可注入的 opencode 执行函数（生产=真实 exec；测试换 fake 写 deploy-result）。
// 返回 stdout（已含 stderr 合并）。env 为受限环境（PATH 前置 shim + 密钥 + ANP_CONTAINER_PREFIX）。
var aiOpencodeRun = func(ctx context.Context, dir, prompt string, env []string) (string, error) {
	cmd := exec.CommandContext(ctx, "opencode", "run", prompt, "-m", "zai-coding/glm-5.1", "--auto", "--dir", dir)
	cmd.Dir = dir
	cmd.Env = env
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

// aiModel 读部署模型配置（可测：cfg nil → 默认）。
func (h *Handler) aiModel() string {
	if h.cfg != nil {
		if m := h.cfg.Get("ai_deploy_model", ""); m != "" {
			return m
		}
	}
	return "zai-coding/glm-5.1"
}

// verifyInput 五步验证输入（result 自报 + 容器实态三读数）。
type verifyInput struct {
	Result             *DeployResult
	Slug, Env, Version string
	Port               int // 平台预分配宿主端口
	InspectRunning     bool
	InspectHostPort    int
	InspectImage       string
}

// verifyAIResult 五步验证（spec §4，纯函数）：返回失败项清单（空=全过）。
// 1 result 存在且合规；2 容器 Running；3 容器名/宿主端口 == 平台规则；
// 4 Running 即存活（InspectHealth 语义，见计划「设计精化」第 3 条）；5 容器实态镜像 == result.image。
func verifyAIResult(in verifyInput) []string {
	var fails []string
	if err := ValidateResult(in.Result, in.Slug, in.Env, in.Version, in.Port); err != nil {
		return append(fails, "验证1 deploy-result: "+err.Error())
	}
	if !in.InspectRunning {
		fails = append(fails, fmt.Sprintf("验证2 容器 %s 未运行（Running=false）", in.Result.Container))
	}
	if in.InspectHostPort != in.Port {
		fails = append(fails, fmt.Sprintf("验证3 宿主端口不符：容器绑定 %d，平台预分配 %d", in.InspectHostPort, in.Port))
	}
	if in.InspectImage != in.Result.Image {
		fails = append(fails, fmt.Sprintf("验证5 镜像不符：容器实态 %s，result 自报 %s", in.InspectImage, in.Result.Image))
	}
	return fails
}

// parseHostPortInspect 解析 docker inspect 端口输出（如 "0.0.0.0:9101"）。纯函数。
func parseHostPortInspect(out string) int {
	s := strings.TrimSpace(out)
	if i := strings.LastIndex(s, ":"); i >= 0 {
		if p, err := strconv.Atoi(s[i+1:]); err == nil {
			return p
		}
	}
	return 0
}

// hostPortOf 读容器宿主端口绑定（docker inspect --format ...）。0=读不到。
func (h *Handler) hostPortOf(ctx context.Context, container string) int {
	out, err := runDocker(ctx, "inspect", "--format",
		"{{range $p, $b := .NetworkSettings.Ports}}{{(index $b 0).HostPort}}{{end}}", container)
	if err != nil {
		return 0
	}
	return parseHostPortInspect(out)
}

// aiDeploy AI 引擎部署主流程（goroutine 入口；Deploy handler 分流调用）。
// 镜像 buildAndDeploy 的 panic 兜底与 markFailed 约定。
func (h *Handler) aiDeploy(psID, aid, env, buildDir string) {
	ctx := context.Background()
	defer func() {
		if r := recover(); r != nil {
			zap.L().Error("[appdeploy] aiDeploy panic", zap.String("app", aid), zap.Any("panic", r))
			h.markFailed(ctx, psID, aid, fmt.Sprintf("[panic] AI 部署异常崩溃: %v", r), "")
		}
	}()
	a, err := h.store.Get(ctx, psID, aid)
	if err != nil || a == nil || a.ID == "" {
		h.markFailed(ctx, psID, aid, "[系统]应用记录不存在", "")
		return
	}
	slug := dockerSlug(a.Name)
	ins, err := h.store.GetOrCreateInstance(ctx, a.ID, env)
	if err != nil || ins == nil {
		h.markFailed(ctx, psID, a.ID, "[系统]部署实例创建失败", "")
		return
	}
	if buildDir == "" {
		buildDir = a.RepoDir
	}
	log := &aiLog{}

	// 1. 版本自增 + 端口预分配（平台保留端口治理权，spec §2 要点）。
	ins.Version++
	version := ins.Version
	minP, maxP := h.deployer.envPortRange(env)
	used := h.deployer.usedPortsOn(ctx, "")
	port := AllocFreePort(used, minP, maxP)
	if port == 0 {
		h.aiFail(ctx, psID, a, ins, log, fmt.Sprintf("无可用宿主端口（%s 环境 %d-%d 已满）", env, minP, maxP))
		return
	}
	log.line(fmt.Sprintf("[平台] 版本 v%d，预分配宿主端口 %d", version, port))

	// 2. 组装简报 + 写 deploy-brief.md
	mf, _ := LoadDeployManifest(a.RepoDir)
	var needs *NeedsSpec
	var actual *ActualSpec
	if mf != nil {
		needs, actual = &mf.Needs, &mf.Actual
	}
	envPairs, _ := h.store.EnvPairs(ctx, a.ID)
	var envKeys []string
	for _, kv := range envPairs {
		if i := strings.IndexByte(kv, '='); i > 0 {
			envKeys = append(envKeys, kv[:i])
		}
	}
	brief := BuildDeployBrief(BriefInput{
		AppName: a.Name, Slug: slug, Env: env, RepoDir: a.RepoDir, BuildDir: buildDir,
		Version: strconv.Itoa(version), Port: port, EnvKeys: envKeys, Needs: needs, Actual: actual,
	})
	briefPath := filepath.Join(buildDir, ".anp", "deploy-brief.md")
	if err := os.MkdirAll(filepath.Dir(briefPath), 0o755); err != nil {
		h.aiFail(ctx, psID, a, ins, log, "写简报目录失败: "+err.Error())
		return
	}
	if err := os.WriteFile(briefPath, []byte(brief), 0o644); err != nil {
		h.aiFail(ctx, psID, a, ins, log, "写简报失败: "+err.Error())
		return
	}
	log.line("[平台] 部署简报已写入 " + briefPath)
	defer os.Remove(briefPath) // 临时文件：验证后删（spec §6）

	// 3. 装 shim + 受限执行 opencode（15min 超时）
	if _, err := InstallShim(shimDir); err != nil {
		h.aiFail(ctx, psID, a, ins, log, "安装 docker shim 失败: "+err.Error())
		return
	}
	prompt := fmt.Sprintf(
		"你是部署执行代理。阅读构建目录下的 .anp/deploy-brief.md 部署简报，严格按其硬性规则执行部署（docker 命令构建镜像、清理旧容器、启动新容器），完成自检后在构建目录写 .anp/deploy-result.yaml 回报。不要做简报以外的任何事。")
	deployCtx, cancel := context.WithTimeout(ctx, aiTimeout)
	defer cancel()
	zhipuKey := ""
	if h.cfg != nil {
		zhipuKey = h.cfg.Get("zhipuai_api_key", "")
	}
	base := os.Environ()
	if zhipuKey != "" {
		base = append(base, "ZHIPUAI_API_KEY="+zhipuKey)
	}
	runEnv := restrictedEnv(base, shimDir, slug)
	out, runErr := aiOpencodeRun(deployCtx, buildDir, prompt, runEnv)
	secrets := secretValues(envPairs)
	if zhipuKey != "" {
		secrets = append(secrets, zhipuKey)
	}
	log.line(redactOut(out, secrets))
	if runErr != nil {
		h.aiFailWithRollback(ctx, psID, a, ins, log, mf, env, slug,
			fmt.Sprintf("AI 执行失败: %v", runErr))
		return
	}

	// 4. 五步验证（容器实态，不信自报）
	result, _ := LoadDeployResult(buildDir)
	defer os.Remove(filepath.Join(buildDir, deployResultRelPath)) // 回报临时文件
	container := fmt.Sprintf("appdeploy-%s-%s-v%d", slug, env, version)
	running := false
	if ch, err := h.deployer.InspectHealth(ctx, container); err == nil {
		running = ch.Running
	}
	hostPort := h.hostPortOf(ctx, container)
	img, _ := h.deployer.InspectImage(ctx, container)
	fails := verifyAIResult(verifyInput{
		Result: result, Slug: slug, Env: env, Version: strconv.Itoa(version), Port: port,
		InspectRunning: running, InspectHostPort: hostPort, InspectImage: img,
	})
	for _, f := range fails {
		log.line("[验证失败] " + f)
	}
	if len(fails) > 0 {
		h.aiFailWithRollback(ctx, psID, a, ins, log, mf, env, slug,
			"平台验证未通过: "+strings.Join(fails, "; "))
		return
	}
	log.line("[平台] 五步验证全过")

	// 5. 成功：从容器实态回填（spec §4——非 AI 自报）
	ins.Image = img
	ins.ContainerName = container
	ins.HostPort = hostPort
	ins.URL = fmt.Sprintf("http://%s:%d", h.deployer.host, hostPort)
	ins.Status = "running"
	ins.LastError = ""
	ins.BuildLog = log.tail()
	_ = h.store.UpdateInstance(ctx, ins)
	_ = h.store.SetStatus(ctx, psID, a.ID, "running", "", ins.BuildLog)
	configSrc := ""
	if mf != nil {
		for _, m := range mf.Needs.Mounts {
			if m.Dst == "/app/config.yaml" {
				configSrc = m.Src
			}
		}
	}
	if rErr := RecordActuals(a.RepoDir, mf, ins.Image, ins.HostPort, configSrc, time.Now().Format(time.RFC3339)); rErr != nil {
		zap.L().Warn("AI 部署回填 actual 失败（不阻塞）", zap.String("app", a.Name), zap.Error(rErr))
	}
	if h.routeWriter != nil && a.AppKind != AppKindHeadless {
		if err := h.routeWriter.UpsertRoute(ctx, a.ID, a.ProjectSpaceID, env, h.deployer.host, ins.HostPort); err != nil {
			zap.L().Warn("appgw 路由表写入失败（部署仍成功）", zap.String("app_id", a.ID), zap.Error(err))
		}
	}
	zap.L().Info("[appdeploy] AI 部署成功", zap.String("app", a.Name), zap.String("env", env), zap.Int("version", version))
}

// aiFail AI 部署失败（无回滚场景：端口/简报/实例等部署前失败）。
func (h *Handler) aiFail(ctx context.Context, psID string, a *Application, ins *AppInstance, log *aiLog, reason string) {
	ins.Status = "failed"
	ins.LastError = reason
	ins.BuildLog = log.tail()
	_ = h.store.UpdateInstance(ctx, ins)
	h.markFailed(ctx, psID, a.ID, reason, ins.BuildLog)
}

// aiFailWithRollback AI 部署失败且已可能动过容器：平台清理残留 + 回滚上一版（spec §4/§5）。
// 回滚=固定引擎重放上一版镜像（旧容器已被 AI 清理，无法重启）；首版（无上一版）仅清残留。
// 版本计数器不回退（与 DriftReconciler「只升不降防版本号复用」一致）：本次 v{N} 失败，
// 下次成功部署会是 v{N+1}——中间空号无害（镜像 tag 唯一性优先于连续性）。
func (h *Handler) aiFailWithRollback(ctx context.Context, psID string, a *Application, ins *AppInstance, log *aiLog, mf *DeployManifest, env, slug, reason string) {
	log.line("[平台] 开始回滚（平台执行，非 AI）")
	// 清理本次失败残留（AI 可能起了半截容器）
	if _, err := h.deployer.RemoveByPrefix(ctx, "appdeploy-"+slug+"-"+env+"-"); err != nil {
		log.line("[回滚] 清理残留失败: " + err.Error())
	}
	// 有上一版镜像 → 固定引擎重放（docker run 上一版 tag）
	// Deployer.Deploy 不自增 Version（自增只在 Build），故直接把 prevIns 指到 v{N-1}。
	if ins.Version > 1 {
		prevVer := ins.Version - 1
		prevIns := *ins
		prevIns.Version = prevVer
		prevIns.Image = fmt.Sprintf("appdeploy/%s-%s:v%d", slug, ins.Env, prevVer)
		log.line("[回滚] 重放上一版 " + prevIns.Image)
		opts := DeployOpts{Mounts: ResolveExtraMounts(a.RepoDir, mf)}
		configHost, _, hasCfg := ResolveConfigMount(a.RepoDir, mf)
		if hasCfg {
			opts.ConfigPath = configHost
		}
		if mf != nil {
			if len(mf.Needs.Ports) > 0 {
				opts.Port = mf.Needs.Ports[0]
			}
			opts.Command = mf.Needs.Command
		}
		envPairs, _ := h.store.EnvPairs(ctx, a.ID)
		if hasCfg {
			envPairs = append(envPairs, "CONFIG_PATH=/app/config.yaml")
		}
		runCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		if err := h.deployer.Deploy(runCtx, a, &prevIns, envPairs, "", opts); err != nil {
			cancel()
			log.line("[回滚] 上一版重放失败: " + err.Error())
		} else {
			cancel()
			log.line(fmt.Sprintf("[回滚] 上一版已恢复: %s (端口 %d)", prevIns.ContainerName, prevIns.HostPort))
			// DB 回写上一版实态（version 保持本版 N——只升不降；镜像/容器指向 v{N-1}）
			ins.Image = prevIns.Image
			ins.ContainerName = prevIns.ContainerName
			ins.HostPort = prevIns.HostPort
			ins.URL = prevIns.URL
			reason += fmt.Sprintf("（已自动回滚到 v%d 服务，本次 v%d 未生效）", prevVer, prevVer+1)
		}
	}
	ins.Status = "failed"
	ins.LastError = reason
	ins.BuildLog = log.tail()
	_ = h.store.UpdateInstance(ctx, ins)
	h.markFailed(ctx, psID, a.ID, reason, ins.BuildLog)
}

// aiLog AI 部署过程日志累积器（tail 2000 行进 build_log，同 buildAndDeploy 约定）。
type aiLog struct{ lines []string }

func (l *aiLog) line(s string) { l.lines = append(l.lines, s) }
func (l *aiLog) tail() string  { return strings.Join(l.lines, "\n") }

// secretValues 从 envPairs 提取密钥值（redactOut 入参；空值过滤）。
func secretValues(envPairs []string) []string {
	var out []string
	for _, kv := range envPairs {
		if i := strings.IndexByte(kv, '='); i > 0 && kv[i+1:] != "" {
			out = append(out, kv[i+1:])
		}
	}
	return out
}
