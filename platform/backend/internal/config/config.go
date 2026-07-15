// Package config 负责加载平台后端配置（环境变量 + 可选 .env）。
package config

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/viper"
)

// Config 是后端运行配置。
type Config struct {
	Env             string   // dev / staging / prod
	LogLevel        string   // debug / info / warn / error
	HTTPAddr        string   // 监听地址，如 :8080
	CORSOrigins     []string // 允许的前端来源
	DatabaseURL     string   // M0: sqlite://... ; 后续: postgres://...
	AgentRuntimeURL string   // Python AI 运行时地址（Go 经 HTTP 调用）
	// opencode 编码引擎（研发工作台）
	ZhipuAPIKey        string // 智谱 API Key（注入 opencode 子进程）
	OpencodeConfigPath string // opencode.json 路径
	GitBashPath        string // Windows 下 opencode 所需 git bash 路径
}

// Load 从环境变量（及可选的 .env 文件）读取配置。
func Load() (*Config, error) {
	v := viper.New()
	v.AutomaticEnv()

	v.SetDefault("env", "dev")
	v.SetDefault("log_level", "info")
	v.SetDefault("backend_http_addr", ":8080")
	v.SetDefault("backend_cors_origins", "http://localhost:3000,http://127.0.0.1:3000,http://[::1]:3000")
	v.SetDefault("database_url", "sqlite://./tmp/anp.db")
	v.SetDefault("agent_runtime_url", "http://127.0.0.1:8001") // 用 IPv4 直连，避免 Go 把 localhost 解析成 [::1] 而 agent-runtime 只监听 IPv4
	v.SetDefault("opencode_config", "../opencode.json") // 相对 backend cwd → platform/opencode.json
	// git bash 路径仅 Windows 下 opencode 需要；Linux/macOS 留空。
	gitBashDefault := ""
	if runtime.GOOS == "windows" {
		gitBashDefault = `C:\Program Files\Git\bin\bash.exe`
	}
	v.SetDefault("opencode_git_bash_path", gitBashDefault)

	// 可选：读取同目录下的 .env
	if _, err := os.Stat(".env"); err == nil {
		v.SetConfigFile(".env")
		v.SetConfigType("env")
		_ = v.ReadInConfig()
	}

	cfg := &Config{
		Env:                v.GetString("env"),
		LogLevel:           v.GetString("log_level"),
		HTTPAddr:           v.GetString("backend_http_addr"),
		CORSOrigins:        splitCSV(v.GetString("backend_cors_origins")),
		DatabaseURL:        v.GetString("database_url"),
		AgentRuntimeURL:    v.GetString("agent_runtime_url"),
		ZhipuAPIKey:        v.GetString("zhipuai_api_key"),
		OpencodeConfigPath: v.GetString("opencode_config"),
		GitBashPath:        v.GetString("opencode_git_bash_path"),
	}
	if cfg.HTTPAddr == "" {
		return nil, fmt.Errorf("backend_http_addr must not be empty")
	}
	return cfg, nil
}

// splitCSV 把逗号分隔字符串切成 trim 后的非空切片。
// 用它而非 viper.GetStringSlice：后者对字符串值返回整串作为单元素（cast 不分割），多 origin 会失效。
func splitCSV(s string) []string {
	out := []string{}
	for _, o := range strings.Split(s, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}
