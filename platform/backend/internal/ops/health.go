package ops

import (
	"context"
	"net/http"
	"os/exec"
	"time"

	"github.com/jmoiron/sqlx"
)

// CheckComponents 探测平台核心组件健康度。
//   - db：SELECT 1
//   - agent-runtime：HTTP GET {agentRuntimeURL}/healthz（2s 超时）
//   - opencode：exec.LookPath（编码引擎必须在 PATH）
func CheckComponents(ctx context.Context, db *sqlx.DB, agentRuntimeURL string) []ComponentHealth {
	comps := make([]ComponentHealth, 0, 3)
	comps = append(comps, checkDB(ctx, db))
	comps = append(comps, checkAgentRuntime(agentRuntimeURL))
	comps = append(comps, checkOpencode())
	return comps
}

// Components 经 Store 持有的 db 探测组件健康（handler 入口）。
func (s *Store) Components(ctx context.Context, agentRuntimeURL string) []ComponentHealth {
	return CheckComponents(ctx, s.db, agentRuntimeURL)
}

// OverallHealth 由各组件状态聚合总览：任一 down→down；任一 degraded→degraded；否则 healthy。
func OverallHealth(comps []ComponentHealth) string {
	hasDown, hasDegraded := false, false
	for _, c := range comps {
		switch c.Status {
		case "down":
			hasDown = true
		case "degraded":
			hasDegraded = true
		}
	}
	switch {
	case hasDown:
		return "down"
	case hasDegraded:
		return "degraded"
	default:
		return "healthy"
	}
}

func checkDB(ctx context.Context, db *sqlx.DB) ComponentHealth {
	start := time.Now()
	var n int
	if err := db.GetContext(ctx, &n, `SELECT 1`); err != nil {
		return ComponentHealth{Name: "db", Status: "down", Detail: "SELECT 1 失败: " + err.Error()}
	}
	return ComponentHealth{Name: "db", Status: "healthy", Detail: "SQLite 可用", Latency: time.Since(start).Milliseconds()}
}

func checkAgentRuntime(baseURL string) ComponentHealth {
	if baseURL == "" {
		return ComponentHealth{Name: "agent-runtime", Status: "degraded", Detail: "未配置 agent_runtime_url"}
	}
	client := &http.Client{Timeout: 2 * time.Second}
	start := time.Now()
	resp, err := client.Get(baseURL + "/healthz")
	if err != nil {
		return ComponentHealth{Name: "agent-runtime", Status: "down", Detail: "不可达: " + err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ComponentHealth{Name: "agent-runtime", Status: "degraded", Detail: "健康检查状态码异常: " + resp.Status}
	}
	return ComponentHealth{Name: "agent-runtime", Status: "healthy", Detail: "AI 运行时可达", Latency: time.Since(start).Milliseconds()}
}

func checkOpencode() ComponentHealth {
	if _, err := exec.LookPath("opencode"); err != nil {
		return ComponentHealth{Name: "opencode", Status: "down", Detail: "opencode CLI 不在 PATH（编码引擎不可用）"}
	}
	return ComponentHealth{Name: "opencode", Status: "healthy", Detail: "编码引擎就绪"}
}
