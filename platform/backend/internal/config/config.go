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
	DatabaseURL     string   // 必须 postgres://（开发/生产禁 SQLite;测试用 sqlite :memory: 不走 config.Open）
	AgentRuntimeURL string   // Python AI 运行时地址（Go 经 HTTP 调用）
	// opencode 编码引擎（研发工作台）
	ZhipuAPIKey        string // 智谱 API Key（注入 opencode 子进程）
	OpencodeConfigPath string // opencode.json 路径
	GitBashPath        string // Windows 下 opencode 所需 git bash 路径
	// 应用部署引擎（板块06 M2）：产出应用构建部署后，以此 host 拼 URL
	AppDeployHost string
}

// Load 从环境变量（及可选的 .env 文件）读取配置。
func Load() (*Config, error) {
	v := viper.New()
	v.AutomaticEnv()

	v.SetDefault("env", "dev")
	v.SetDefault("log_level", "info")
	v.SetDefault("backend_http_addr", ":8080")
	v.SetDefault("backend_cors_origins", "http://localhost:3000,http://127.0.0.1:3000,http://[::1]:3000")
	// database_url 不设默认:开发/生产强制 PostgreSQL(禁 SQLite)。无 DATABASE_URL 或 sqlite:// → Load 报错。
	v.SetDefault("agent_runtime_url", "http://127.0.0.1:8001") // 用 IPv4 直连，避免 Go 把 localhost 解析成 [::1] 而 agent-runtime 只监听 IPv4
	v.SetDefault("opencode_config", "../opencode.json")        // 相对 backend cwd → platform/opencode.json
	// git bash 路径仅 Windows 下 opencode 需要；Linux/macOS 留空。
	gitBashDefault := ""
	if runtime.GOOS == "windows" {
		gitBashDefault = `C:\Program Files\Git\bin\bash.exe`
	}
	v.SetDefault("opencode_git_bash_path", gitBashDefault)
	v.SetDefault("appdeploy_host", "localhost") // 产出应用部署后拼 URL 的主机；生产设为对外 IP/域名

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
		AppDeployHost:      v.GetString("appdeploy_host"),
	}
	if cfg.HTTPAddr == "" {
		return nil, fmt.Errorf("backend_http_addr must not be empty")
	}
	// 运行时强制 PG,禁 SQLite(开发/生产都不允许 sqlite 文件库;单测的 :memory: 不走 config.Open,不受影响)
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("database_url 必须配置(开发: postgres://anp:anp_dev_pwd@10.10.0.28:5432/anp_dev?sslmode=disable; 生产: anp PG)")
	}
	if strings.HasPrefix(cfg.DatabaseURL, "sqlite://") {
		return nil, fmt.Errorf("运行时禁用 SQLite(仅允许 postgres://); 收到 %q(测试 :memory: 不受影响)", cfg.DatabaseURL)
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
