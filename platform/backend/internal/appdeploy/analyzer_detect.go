package appdeploy

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// analyzerSkipDirs 扫描时跳过的目录（依赖/构建产物/版本库/挂载数据）。
var analyzerSkipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true,
	"build": true, "target": true, "__pycache__": true, ".next": true,
	".worktrees": true, "docker-data": true,
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// detectLanguage 按仓库根特征文件推断语言。
func detectLanguage(root string) (lang, framework string) {
	has := func(names ...string) bool {
		for _, n := range names {
			if fileExists(filepath.Join(root, n)) {
				return true
			}
		}
		return false
	}
	switch {
	case has("go.mod", "main.go"):
		return "go", ""
	case has("package.json"):
		return "node", ""
	case has("requirements.txt", "pyproject.toml"):
		return "python", ""
	case has("pom.xml", "build.gradle"):
		return "java", ""
	}
	return "", ""
}

// detectBuild 检测有无 Dockerfile / docker-compose 及 compose 服务名。
func detectBuild(root string) BuildAnalysis {
	b := BuildAnalysis{}
	if fileExists(filepath.Join(root, "Dockerfile")) {
		b.Dockerfile = true
	}
	for _, n := range []string{"docker-compose.yml", "docker-compose.yaml"} {
		p := filepath.Join(root, n)
		if fileExists(p) {
			b.Compose = true
			b.ComposeServices = parseComposeServices(p)
			break
		}
	}
	return b
}

var serviceKeyRe = regexp.MustCompile(`^([a-zA-Z0-9_.-]+):`)

// parseComposeServices 解析 compose services: 块下的一级缩进键（服务名）。
// 用"services: 下首个子键的缩进"作为服务名缩进，避免误收 image/ports 等更深层属性。
func parseComposeServices(p string) []string {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	var services []string
	inServices := false
	serviceIndent := -1
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent == 0 {
			inServices = trimmed == "services:" || strings.HasPrefix(trimmed, "services:")
			serviceIndent = -1
			continue
		}
		if !inServices {
			continue
		}
		if serviceIndent == -1 {
			serviceIndent = indent
		}
		if indent == serviceIndent {
			if m := serviceKeyRe.FindStringSubmatch(trimmed); m != nil {
				services = append(services, m[1])
			}
		}
	}
	return services
}

var exposePortsRe = regexp.MustCompile(`(?m)^\s*EXPOSE\s+(.*)`)

// detectPorts 从 Dockerfile EXPOSE 解析端口（支持 `EXPOSE 9090 9091`、`8080/tcp`）。
func detectPorts(root string) PortsAnalysis {
	pa := PortsAnalysis{}
	data, err := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if err != nil {
		return pa
	}
	seen := map[int]bool{}
	for _, m := range exposePortsRe.FindAllStringSubmatch(string(data), -1) {
		for _, tok := range strings.Fields(m[1]) {
			tok = strings.Split(tok, "/")[0]
			if port, e := strconv.Atoi(tok); e == nil && !seen[port] {
				seen[port] = true
				pa.Expose = append(pa.Expose, port)
			}
		}
	}
	sort.Ints(pa.Expose)
	return pa
}

// analyzerConfigExts 扫描的配置/文本文件扩展名（读依赖地址用）。
var analyzerConfigExts = map[string]bool{
	".yaml": true, ".yml": true, ".env": true, ".ini": true,
	".conf": true, ".toml": true, ".properties": true,
}

// portKindMap 已知中间件默认端口 → kind。
var portKindMap = map[int]string{
	6379: "redis", 19530: "milvus", 5432: "postgres", 3306: "mysql",
	27017: "mongo", 9092: "kafka", 9200: "elasticsearch", 9000: "minio",
	5672: "rabbitmq",
}

var (
	depKeywordRe = regexp.MustCompile(`(?i)\b(redis|milvus|postgres(?:ql)?|mysql|mongo(?:db)?|kafka|rabbitmq|elasticsearch)\b`)
	// 127.0.0.1:6379 / localhost:5432
	localAddrRe = regexp.MustCompile(`(?:127\.0\.0\.1|localhost):(\d{1,5})`)
)

// scanConfigFiles 遍历 repoDir（跳过 skip 目录），返回所有配置文本文件内容。
func scanConfigFiles(root string) []string {
	var contents []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			if analyzerSkipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !analyzerConfigExts[filepath.Ext(info.Name())] {
			return nil
		}
		if info.Size() > 1<<20 { // 单文件 >1MB 跳过（防爆扫描）
			return nil
		}
		if b, e := os.ReadFile(path); e == nil {
			contents = append(contents, string(b))
		}
		return nil
	})
	return contents
}

// detectDeps 扫配置文件：localhost:PORT 按端口表定 kind+addr，关键词兜底（如 pgsql 段 host/port 分行）。
func detectDeps(root string) []DeployDep {
	blobs := scanConfigFiles(root)
	byKind := map[string]*DeployDep{}
	ensure := func(kind string) *DeployDep {
		if d, ok := byKind[kind]; ok {
			return d
		}
		d := &DeployDep{Kind: kind}
		byKind[kind] = d
		return d
	}
	for _, s := range blobs {
		for _, m := range localAddrRe.FindAllStringSubmatch(s, -1) {
			if port, err := strconv.Atoi(m[1]); err == nil {
				if kind, ok := portKindMap[port]; ok {
					ensure(kind).Addr = m[0]
				}
			}
		}
		for _, m := range depKeywordRe.FindAllString(s, -1) {
			kind := strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(m, "ql"), "db"))
			if kind == "elasticsearch" {
				kind = "es"
			}
			ensure(kind)
		}
	}
	deps := make([]DeployDep, 0, len(byKind))
	for _, d := range byKind {
		deps = append(deps, *d)
	}
	sort.Slice(deps, func(i, j int) bool { return deps[i].Kind < deps[j].Kind })
	return deps
}

var composeHostNetRe = regexp.MustCompile(`(?m)^\s*network_mode:\s*host\b`)

// detectNetwork 检测 compose 是否声明 host 网络。
func detectNetwork(root string) NetworkAnalysis {
	n := NetworkAnalysis{}
	for _, name := range []string{"docker-compose.yml", "docker-compose.yaml"} {
		p := filepath.Join(root, name)
		if !fileExists(p) {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if composeHostNetRe.Match(data) {
			n.HostModeRequired = true
			n.Reason = "compose network_mode:host"
		}
	}
	return n
}
