package appgw

import (
	"context"
	"fmt"
	"net/url"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Store appgw 数据访问（appdeploy_route 表）。
type Store struct{ db *sqlx.DB }

// NewStore 构造。
func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

// RouteWriter 部署侧写路由表的接口（appdeploy 持有，单向依赖 appdeploy→appgw）。
// appdeploy.Deploy 成功后 UpsertRoute；Delete 应用时 DeleteRouteByApp。
// external 应用（B 类轻接入）用 UpsertExternalRoute 写：直接反代 external_url，不走 host:port。
// *Store 实现此接口；appdeploy 不持整个 Store，只持写接口，便于解耦与 mock。
type RouteWriter interface {
	UpsertRoute(ctx context.Context, appID, psID, env, upstreamHost string, upstreamPort int) error
	UpsertExternalRoute(ctx context.Context, appID, psID, env, externalURL string) error
	DeleteRouteByApp(ctx context.Context, appID string) error
}

// routeCols 显式列。external_url 用 COALESCE 兜底（老数据为空串）。
const routeCols = `id, app_id, project_space_id, app_code, env, upstream_host, upstream_port, status, auth_required, COALESCE(external_url,'') AS external_url, created_at, updated_at`

// UpsertRoute 写/更新一条路由（按 app_id+env 唯一）。
// appdeploy.Deploy 成功后调用，把 app_id/env/upstream_host/upstream_port 落到 appdeploy_route。
// status 默认 active（部署成功即时对外可达）。
func (s *Store) UpsertRoute(ctx context.Context, appID, psID, env, upstreamHost string, upstreamPort int) error {
	if appID == "" || env == "" {
		return fmt.Errorf("appID/env 不能为空")
	}
	id := "rt_" + uuid.NewString()[:20]
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO appdeploy_route (id, app_id, project_space_id, app_code, env, upstream_host, upstream_port, status, auth_required)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,TRUE)
		 ON CONFLICT (app_code, env) DO UPDATE SET
		   upstream_host = excluded.upstream_host,
		   upstream_port = excluded.upstream_port,
		   status        = excluded.status,
		   external_url  = '',           -- managed 模式覆盖回空（从 external 切回 managed 的场景）
		   updated_at    = CURRENT_TIMESTAMP`,
		id, appID, psID, appID, env, upstreamHost, upstreamPort, StatusActive)
	return err
}

// UpsertExternalRoute 写/更新一条 external 应用路由（B 类轻接入）。
// 与 UpsertRoute 区别：external 应用无 host:port 概念，gateway 直接反代 external_url。
// upstream_host/port 仍按 URL 解析填一份（仅展示/兜底用，反代不走）。
func (s *Store) UpsertExternalRoute(ctx context.Context, appID, psID, env, externalURL string) error {
	if appID == "" || env == "" {
		return fmt.Errorf("appID/env 不能为空")
	}
	if externalURL == "" {
		return fmt.Errorf("external_url 不能为空")
	}
	// 解析 URL 填 host/port（展示用；反代直接用 external_url）
	host, port := parseHostPort(externalURL)
	id := "rt_" + uuid.NewString()[:20]
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO appdeploy_route (id, app_id, project_space_id, app_code, env, upstream_host, upstream_port, status, auth_required, external_url)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,TRUE,$9)
		 ON CONFLICT (app_code, env) DO UPDATE SET
		   upstream_host = excluded.upstream_host,
		   upstream_port = excluded.upstream_port,
		   status        = excluded.status,
		   external_url  = excluded.external_url,
		   updated_at    = CURRENT_TIMESTAMP`,
		id, appID, psID, appID, env, host, port, StatusActive, externalURL)
	return err
}

// parseHostPort 从 http(s)://host[:port][/path] 解析出 host + port（无端口时按 scheme 默认 80/443）。
// 供 UpsertExternalRoute 填展示用 upstream_host/port；解析失败回 ("",0)。
func parseHostPort(rawURL string) (string, int) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "", 0
	}
	host := u.Hostname()
	port := 0
	if p := u.Port(); p != "" {
		for _, ch := range p {
			if ch < '0' || ch > '9' {
				return host, 0
			}
			port = port*10 + int(ch-'0')
		}
	} else if u.Scheme == "https" {
		port = 443
	} else {
		port = 80
	}
	return host, port
}

// GetRoute 按 app_code+env 取一条路由。无则返回 nil,nil。
// appgw.ReverseProxy 用此查 upstream。
func (s *Store) GetRoute(ctx context.Context, appCode, env string) (*Route, error) {
	var r Route
	err := s.db.GetContext(ctx, &r,
		`SELECT `+routeCols+` FROM appdeploy_route WHERE app_code=$1 AND env=$2`,
		appCode, env)
	if err != nil {
		return nil, err // sql.ErrNoRows 向上传递，调用方判 nil
	}
	return &r, nil
}

// DeleteRouteByApp 删除该应用所有环境的路由（删应用时调用）。
// FK CASCADE 在 appdeploy_application 删除时会自动清，但显式清更稳（不依赖 FK）。
func (s *Store) DeleteRouteByApp(ctx context.Context, appID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM appdeploy_route WHERE app_id=$1`, appID)
	return err
}

// SetRouteStatus 更新路由状态（active/inactive）。
// 停止应用时可置 inactive 让 appgw 503；启动时置 active 恢复。
func (s *Store) SetRouteStatus(ctx context.Context, appID, env, status string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE appdeploy_route SET status=$1, updated_at=CURRENT_TIMESTAMP WHERE app_id=$2 AND env=$3`,
		status, appID, env)
	return err
}

// accessLogCols 显式列（COALESCE 处理 caller/trace_id 可空）。
const accessLogCols = `id, project_space_id, app_id, app_code, env,
	COALESCE(caller,'') AS caller, method, path, status, latency_ms,
	COALESCE(trace_id,'') AS trace_id, created_at`

// LogAccess 写一条 appgw 调用日志。
// gateway.ReverseProxy 在 ModifyResponse / ErrorHandler 调用；id 内部生成（"al_"+uuid）。
// 记日志失败不阻塞请求 —— gateway 用异步 goroutine + ignore error 调本方法。
func (s *Store) LogAccess(ctx context.Context, al *AccessLog) error {
	if al.ID == "" {
		al.ID = "al_" + uuid.NewString()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO appgw_access_log
		 (id, project_space_id, app_id, app_code, env, caller, method, path, status, latency_ms, trace_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		al.ID, al.ProjectSpaceID, al.AppID, al.AppCode, al.Env, al.Caller,
		al.Method, al.Path, al.Status, al.LatencyMs, al.TraceID)
	return err
}

// ListAccessLogs 列某应用最近的 appgw 调用日志（按时间倒序）。
// limit<=0 或 >500 取 50（防前端一次拉爆）。
func (s *Store) ListAccessLogs(ctx context.Context, appID string, limit int) ([]AccessLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	var list []AccessLog
	err := s.db.SelectContext(ctx, &list,
		`SELECT `+accessLogCols+` FROM appgw_access_log WHERE app_id=$1 ORDER BY created_at DESC LIMIT $2`,
		appID, limit)
	return list, err
}

// PurgeAccessLogs 清理超过 retainDays 天的 appgw 调用日志，返回删除行数。
// main ticker 每天 1 次跑（保留窗口 env ACCESS_LOG_RETAIN_DAYS 控制，默认 30），
// 防止 appgw_access_log 表无限增长（每请求一条）。
// retainDays<=0 视为不清理（调用方判断后跳过）。
func (s *Store) PurgeAccessLogs(ctx context.Context, retainDays int) (int64, error) {
	if retainDays <= 0 {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM appgw_access_log WHERE created_at < now() - make_interval(days => $1)`, retainDays)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
