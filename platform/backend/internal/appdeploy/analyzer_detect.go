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
