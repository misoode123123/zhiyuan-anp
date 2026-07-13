// Package config 负责加载平台后端配置（环境变量 + 可选 .env）。
package config

import (
	"fmt"
	"os"

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
	v.SetDefault("backend_cors_origins", "http://localhost:3000")
	v.SetDefault("database_url", "sqlite://./tmp/anp.db")
	v.SetDefault("agent_runtime_url", "http://localhost:8001")
	v.SetDefault("opencode_config", "../opencode.json")                       // 相对 backend cwd → platform/opencode.json
	v.SetDefault("opencode_git_bash_path", `C:\Program Files\Git\bin\bash.exe`)

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
		CORSOrigins:        v.GetStringSlice("backend_cors_origins"),
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
