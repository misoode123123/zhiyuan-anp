package appdeploy

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnsureDockerfile 若 repoDir 无 Dockerfile，按检测到的项目类型生成一个最小可构建 Dockerfile。
// 返回：(是否新生成, 应使用的内部端口, 错误)。已有 Dockerfile 则原样使用。
//
// 端口优先级：调用方显式传入的端口(fallbackPort>0) > 类型默认；避免 buildpack 把
// opencode 实际监听端口(如 8080)误覆盖成类型惯例(如 node 的 3000)导致端口不匹配。
func EnsureDockerfile(repoDir string, fallbackPort int) (generated bool, port int, err error) {
	df := filepath.Join(repoDir, "Dockerfile")
	if _, e := os.Stat(df); e == nil {
		return false, fallbackPort, nil
	}
	t := detectType(repoDir)
	port = fallbackPort
	if port <= 0 {
		port = defaultPortForType(t)
	}
	content := dockerfileFor(t, port)
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		return false, port, err
	}
	if err := os.WriteFile(df, []byte(content), 0o644); err != nil {
		return false, port, err
	}
	return true, port, nil
}

// detectType 按仓库特征推断项目类型。
func detectType(repoDir string) string {
	has := func(names ...string) bool {
		for _, n := range names {
			if _, err := os.Stat(filepath.Join(repoDir, n)); err == nil {
				return true
			}
		}
		return false
	}
	switch {
	case has("go.mod", "main.go"):
		return "go"
	case has("package.json"):
		return "node"
	case has("requirements.txt", "app.py", "main.py"):
		return "python"
	case has("index.html"):
		return "static"
	default:
		// 空仓库/无识别特征时兜底 static（nginx）：可空构建成功，避免误判 go 导致
		// "cannot find main module" 构建失败。真正的 go 服务会有 go.mod/main.go 被准确识别。
		return "static"
	}
}

// defaultPortForType 各类型默认端口（仅当未显式指定端口时用）。
func defaultPortForType(t string) int {
	switch t {
	case "node":
		return 3000
	case "static":
		return 80
	default:
		return 8080
	}
}

// dockerfileFor 按类型生成 Dockerfile（国内镜像源加速）。
func dockerfileFor(t string, port int) string {
	head := fmt.Sprintf("# 由 ANP buildpack 自动生成（类型 %s）\n", t)
	var body string
	switch t {
	case "go":
		body = fmt.Sprintf(`FROM golang:1.25-alpine AS build
WORKDIR /src
COPY . .
ENV GOPROXY=https://goproxy.cn,direct
RUN go mod download 2>/dev/null; CGO_ENABLED=0 go build -o /server .
FROM alpine:3.19
RUN sed -i 's#https://dl-cdn.alpinelinux.org#https://mirrors.aliyun.com#g' /etc/apk/repositories && \
    apk add --no-cache ca-certificates
COPY --from=build /server /server
EXPOSE %d
CMD ["/server"]`, port)
	case "node":
		body = fmt.Sprintf(`FROM node:20-alpine
WORKDIR /app
COPY package*.json ./
RUN npm install --registry https://registry.npmmirror.com || true
COPY . .
RUN npm run build --registry https://registry.npmmirror.com 2>/dev/null || true
EXPOSE %d
CMD ["npm", "start"]`, port)
	case "python":
		body = fmt.Sprintf(`FROM python:3-alpine
WORKDIR /app
COPY . .
RUN pip install -r requirements.txt -i https://mirrors.aliyun.com/pypi/simple/ 2>/dev/null || true
EXPOSE %d
CMD ["python", "app.py"]`, port)
	case "static":
		// nginx 默认监听 80，EXPOSE 不会改变其监听口；必须覆盖 default.conf 让 nginx
		// 监听指定端口，否则 docker run -p host:port 时容器内无人监听 port → 连不上。
		body = fmt.Sprintf(`FROM nginx:alpine
COPY . /usr/share/nginx/html
RUN echo "server { listen %d; root /usr/share/nginx/html; index index.html; }" > /etc/nginx/conf.d/default.conf
EXPOSE %d
CMD ["nginx", "-g", "daemon off;"]`, port, port)
	default:
		body = fmt.Sprintf("FROM busybox\nEXPOSE %d\n", port)
	}
	return head + body
}
