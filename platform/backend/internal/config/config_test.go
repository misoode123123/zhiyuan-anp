package config

import (
	"reflect"
	"testing"
)

// TestSplitCSV 纯逻辑：逗号分隔 → trim → 去空段。
// 注：用 splitCSV 而非 viper.GetStringSlice，因后者对字符串值 cast 不分割，多 origin 会失效。
func TestSplitCSV(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , b , c ", []string{"a", "b", "c"}}, // 两侧空格 trim
		{"a,,b", []string{"a", "b"}},             // 空段过滤
		{",,,", []string{}},                      // 全空段
		{"", []string{}},                         // 空串返回空切片
		{"single", []string{"single"}},           // 无分隔符
		{"  ", []string{}},                       // 仅空白
	}
	for _, c := range cases {
		got := splitCSV(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Fatalf("splitCSV(%q) = %#v, want %#v", c.in, got, c.want)
		}
	}
}

// TestLoad_LogConfigDefaults Load 在未设 LOG_* env 时，日志配置应有合理默认。
func TestLoad_LogConfigDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://test:test@localhost:5432/test")
	t.Setenv("BACKEND_HTTP_ADDR", ":9090")
	// LOG_* env 不主动设（测 SetDefault 默认值；viper 空串视为设值会覆盖默认，故不设）
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LogFormat != "console" {
		t.Errorf("LogFormat 默认 console，得到 %q", cfg.LogFormat)
	}
	if cfg.LogOutput != "stdout" {
		t.Errorf("LogOutput 默认 stdout，得到 %q", cfg.LogOutput)
	}
	if cfg.LogFile != "/data/logs/app.log" {
		t.Errorf("LogFile 默认 /data/logs/app.log，得到 %q", cfg.LogFile)
	}
	if cfg.LogErrorFile != "/data/logs/error.log" {
		t.Errorf("LogErrorFile 默认 /data/logs/error.log，得到 %q", cfg.LogErrorFile)
	}
	if cfg.LogMaxSizeMB != 100 {
		t.Errorf("LogMaxSizeMB 默认 100，得到 %d", cfg.LogMaxSizeMB)
	}
	if cfg.LogMaxBackups != 7 {
		t.Errorf("LogMaxBackups 默认 7，得到 %d", cfg.LogMaxBackups)
	}
	if cfg.LogMaxAgeDays != 30 {
		t.Errorf("LogMaxAgeDays 默认 30，得到 %d", cfg.LogMaxAgeDays)
	}
}
