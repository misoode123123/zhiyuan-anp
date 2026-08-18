package appdeploy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// shimDir 容器内 shim 安装目录（PATH 前置它，遮蔽 /usr/bin/docker）。
const shimDir = "/usr/local/bin/anp-docker-shim"

// shimScript POSIX sh 白名单脚本（与 deploy/anp-docker-shim 同内容；嵌入字符串而非
// go:embed，避免构建上下文耦合 deploy/ 目录——Dockerfile.backend 只 COPY platform/backend）。
const shimScript = `#!/bin/sh
# ANP docker shim：AI 部署的命令白名单（spec §3）。同内容副本在 deploy/anp-docker-shim。
set -u
PREFIX="${ANP_CONTAINER_PREFIX:-}"
REAL="/usr/bin/docker"
sub="${1:-}"
[ "$sub" != "" ] && shift
case "$sub" in
  build|run|inspect|logs|ps)
    exec "$REAL" "$sub" "$@"
    ;;
  stop|rm)
    if [ -z "$PREFIX" ]; then
      echo "[anp-shim] 拒绝：未配置 ANP_CONTAINER_PREFIX" >&2; exit 127
    fi
    skip=""
    violation=""
    for a in "$@"; do
      if [ "$skip" != "" ]; then skip=""; continue; fi
      case "$a" in
        -t|--time)
          skip="1" ;;   # 带值 flag：跳过其值
        -*) ;;          # 其余 flag（-f/-l/-v...）不含值
        *)
          case "$a" in
            "$PREFIX"*) ;;                    # 每个非 flag 参数（容器名）逐一校验
            *) [ -z "$violation" ] && violation="$a" ;;  # 记首个越权名；标志独立于名字，空串参数覆不垮
          esac
          ;;
      esac
    done
    if [ "$violation" != "" ]; then
      echo "[anp-shim] 拒绝：容器 '$violation' 不在前缀 '$PREFIX' 内（stop/rm 仅限本应用容器）" >&2; exit 127
    fi
    exec "$REAL" "$sub" "$@"
    ;;
  *)
    echo "[anp-shim] 拒绝：docker 子命令 '$sub' 不在白名单（build/run/inspect/logs/ps/stop/rm）" >&2
    exit 127
    ;;
esac
`

// InstallShim 把 shim 脚本写到 dir/docker 并 chmod 0755；返回 dir。
// aiDeploy 每次执行前调用（幂等；写到固定路径 /usr/local/bin/anp-docker-shim）。
func InstallShim(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, "docker")
	if err := os.WriteFile(p, []byte(shimScript), 0o755); err != nil {
		return "", fmt.Errorf("写 shim: %w", err)
	}
	return dir, nil
}

// shimAllow 白名单判定（Go 侧镜像实现，供单测文档化规则；真正的拦截在 sh 脚本）。
// 规则与 shimScript 一致：build/run/inspect/logs/ps 放行；stop/rm 每个（跳过 -t 的值）
// 非 flag 参数（容器名）都须以 appdeploy-{slug}- 开头，任一越权即拒绝；其余拒绝。
func shimAllow(args []string, slug string) error {
	if len(args) < 2 {
		return fmt.Errorf("空命令")
	}
	sub := args[1]
	prefix := "appdeploy-" + slug + "-"
	switch sub {
	case "build", "run", "inspect", "logs", "ps":
		return nil
	case "stop", "rm":
		// 多目标逐参数校验：此前只查首个非 flag 参数，`stop 合规名 deploy_backend_1`
		// 可夹带走平台容器（docker stop 语义=全部停掉）。
		hasTarget := false
		skipValue := false
		for _, a := range args[2:] {
			if skipValue { // -t 的值不是容器名
				skipValue = false
				continue
			}
			if a == "-t" || a == "--time" {
				skipValue = true
				continue
			}
			if strings.HasPrefix(a, "-") {
				continue
			}
			hasTarget = true
			if !strings.HasPrefix(a, prefix) {
				return fmt.Errorf("容器 %q 不在前缀 %q 内", a, prefix)
			}
		}
		if !hasTarget {
			return fmt.Errorf("%s 无目标容器", sub)
		}
		return nil
	default:
		return fmt.Errorf("子命令 %q 不在白名单", sub)
	}
}

// restrictedEnv 组装 AI 进程环境：PATH 前置 shimDir + 注入 ANP_CONTAINER_PREFIX。
// base 保留（含密钥 env——AI run 容器要用；密钥绝不进简报/build_log，由 redactOut 保证）。
// PATH 前置：base 已有 PATH 则改写首个；无则兜底注入纯 shimDir（防子 shell 落默认
// PATH 解析到真身 docker，白名单整体绕过）。
// ANP_CONTAINER_PREFIX：base 已含该键时跳过（glibc getenv 首键优先，重复追加会让
// 预置值遮蔽注入值），最后统一 append。
func restrictedEnv(base []string, shimDirPath, slug string) []string {
	prefix := "appdeploy-" + slug + "-"
	out := make([]string, 0, len(base)+2)
	hasPath := false
	for _, e := range base {
		switch {
		case strings.HasPrefix(e, "PATH="):
			if !hasPath {
				out = append(out, "PATH="+shimDirPath+":"+strings.TrimPrefix(e, "PATH="))
				hasPath = true
			}
		case strings.HasPrefix(e, "ANP_CONTAINER_PREFIX="):
			// 跳过预置值，末尾统一注入（幂等，防首键遮蔽）
		default:
			out = append(out, e)
		}
	}
	if !hasPath {
		out = append(out, "PATH="+shimDirPath)
	}
	out = append(out, "ANP_CONTAINER_PREFIX="+prefix)
	return out
}
