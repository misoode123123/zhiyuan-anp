package appdeploy

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnsureDockerfile 若 repoDir 无 Dockerfile，按检测到的项目类型生成一个最小可构建 Dockerfile。
// 返回：(是否新生成, 应使用的内部端口, 错误)。已有 Dockerfile 则原样使用。
//
// 让"研发产出但缺 Dockerfile 的源码"也能被部署引擎构建——opencode 产出的代码只需是
// 可运行服务(有 main/入口)，Dockerfile 由 buildpack 兜底生成。
func EnsureDockerfile(repoDir string, fallbackPort int) (generated bool, port int, err error) {
	if fallbackPort <= 0 {
		fallbackPort = 8080
	}
	df := filepath.Join(repoDir, "Dockerfile")
	if _, e := os.Stat(df); e == nil {
		return false, fallbackPort, nil
	}
	t, p := detectType(repoDir, fallbackPort)
	content := dockerfileFor(t, p)
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		return false, p, err
	}
	if err := os.WriteFile(df, []byte(content), 0o644); err != nil {
		return false, p, err
	}
	return true, p, nil
}

// detectType 按仓库特征推断项目类型与默认端口。
func detectType(repoDir string, fallbackPort int) (string, int) {
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
		return "go", 8080
	case has("package.json"):
		return "node", 3000
	case has("requirements.txt", "app.py", "main.py"):
		return "python", 8080
	case has("index.html"):
		return "static", 80
	default:
		return "go", fallbackPort // 默认按 Go（平台主语言）
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
		body = fmt.Sprintf(`FROM nginx:alpine
COPY . /usr/share/nginx/html
EXPOSE %d
CMD ["nginx", "-g", "daemon off;"]`, port)
	default:
		body = fmt.Sprintf("FROM busybox\nEXPOSE %d\n", port)
	}
	return head + body
}
