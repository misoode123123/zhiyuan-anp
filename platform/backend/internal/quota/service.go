package quota

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// InstanceLookup 取项目 PG 实例的 admin_url（superuser 连接串）。
// pgsupply 提供 adapter（不导入 pgsupply 包，避免循环依赖）。
type InstanceLookup interface {
	// GetInstanceAdminURL 返回项目首个 active PG 实例的 admin_url。
	// 无实例返回 "" + nil（调用方按 0 处理；不阻塞建库）。
	GetInstanceAdminURL(ctx context.Context, psID string) (string, error)
}

// PGSizeChecker 查库大小（字节）。pgsupply.PGAdmin 实现。
type PGSizeChecker interface {
	// DatabaseSizes 连 adminURL，按 dbNames 查每库字节。
	// 不存在的库不出现在返回 map（不报错）。
	DatabaseSizes(ctx context.Context, adminURL string, dbNames []string) (map[string]int64, error)
}

// Service 配额业务逻辑：4 个 Check + Usage 查询。
type Service struct {
	store    *Store
	instances InstanceLookup
	pg       PGSizeChecker
}

// NewService 构造。instances/pg 可为 nil（相关 check 跳过；用于 appdeploy 等模块只查应用数的场景）。
func NewService(store *Store, instances InstanceLookup, pg PGSizeChecker) *Service {
	return &Service{store: store, instances: instances, pg: pg}
}

// ---------------- 4 个 Check ----------------

// CheckApps 应用数 check：超限返回 *QuotaExceededError。
func (s *Service) CheckApps(ctx context.Context, psID string) error {
	q, err := s.store.GetOrCreate(ctx, psID)
	if err != nil {
		return err
	}
	used, err := s.countApps(ctx, psID)
	if err != nil {
		return err
	}
	if used >= q.MaxApps {
		return &QuotaExceededError{Dimension: DimensionApps, Used: used, Limit: q.MaxApps}
	}
	return nil
}

// CheckDatabases 库数 check（status<>'deleted'）。
func (s *Service) CheckDatabases(ctx context.Context, psID string) error {
	q, err := s.store.GetOrCreate(ctx, psID)
	if err != nil {
		return err
	}
	used, err := s.countDatabases(ctx, psID)
	if err != nil {
		return err
	}
	if used >= q.MaxDatabases {
		return &QuotaExceededError{Dimension: DimensionDatabases, Used: used, Limit: q.MaxDatabases}
	}
	return nil
}

// CheckCapabilityToday 今日 AI 调用次数 check（created_at::date=CURRENT_DATE）。
func (s *Service) CheckCapabilityToday(ctx context.Context, psID string) error {
	q, err := s.store.GetOrCreate(ctx, psID)
	if err != nil {
		return err
	}
	used, err := s.countCapabilityToday(ctx, psID)
	if err != nil {
		return err
	}
	if used >= q.MaxCapabilityCallsPerDay {
		return &QuotaExceededError{Dimension: DimensionCapabilityToday, Used: used, Limit: q.MaxCapabilityCallsPerDay, Unit: "次"}
	}
	return nil
}

// CheckDBSize 库总大小 check（接近满则拦新建）。
// 当前总大小 >= max_total_db_mb → 拦截（不能再建新库，否则一建就超）。
// 无 PG 实例 / 无库记录 → 总大小 0，不拦（不阻塞首次建库）。
// 连不上实例 → 返回 nil 不阻塞（运维层告警，由 ops 排查）。
func (s *Service) CheckDBSize(ctx context.Context, psID string) error {
	if s.instances == nil || s.pg == nil {
		return nil // 未注入实例/PG 查询能力，跳过（部署方未启用 pgsupply 时）
	}
	q, err := s.store.GetOrCreate(ctx, psID)
	if err != nil {
		return err
	}
	usedMb, err := s.calcTotalDBSizeMb(ctx, psID)
	if err != nil {
		return err
	}
	if usedMb >= q.MaxTotalDBMb {
		return &QuotaExceededError{Dimension: DimensionDBSize, Used: usedMb, Limit: q.MaxTotalDBMb, Unit: "MB"}
	}
	return nil
}

// ---------------- Usage ----------------

// Usage 取配额 + 4 维度当前用量（管理 UI / 看板用）。
func (s *Service) Usage(ctx context.Context, psID string) (*Usage, error) {
	q, err := s.store.GetOrCreate(ctx, psID)
	if err != nil {
		return nil, err
	}
	u := &Usage{Quota: *q}
	if u.UsedApps, err = s.countApps(ctx, psID); err != nil {
		return nil, err
	}
	if u.UsedDatabases, err = s.countDatabases(ctx, psID); err != nil {
		return nil, err
	}
	if u.UsedCapabilityToday, err = s.countCapabilityToday(ctx, psID); err != nil {
		return nil, err
	}
	if u.UsedDBSizeMb, err = s.calcTotalDBSizeMb(ctx, psID); err != nil {
		return nil, err
	}
	return u, nil
}

// ---------------- 3c 用量趋势 ----------------

// 趋势查询天数上下界：<=0 或 >90 夹到 30（前端 days 选择器只给 7/30/90，仍兜底防越界）。
const (
	trendDaysDefault = 30
	trendDaysMax     = 90
)

func clampDays(days int) int {
	if days <= 0 || days > trendDaysMax {
		return trendDaysDefault
	}
	return days
}

// UsageTrend 取用量趋势：AI 调用 / 应用 API 调用 / 库大小 三条日级趋势 + 当前用量（复用 3a）。
// days 控制时间窗（now()-N days 到 now）。空数据返回空切片（前端按空趋势渲染）。
func (s *Service) UsageTrend(ctx context.Context, psID string, days int) (*UsageTrend, error) {
	days = clampDays(days)
	t := &UsageTrend{Days: days, AITrend: []AITrendPoint{}, APITrend: []APITrendPoint{}, DBSizeTrend: []DBSizeTrendPoint{}}

	// AI 调用趋势（capability_usage by day）
	if err := s.store.db.SelectContext(ctx, &t.AITrend,
		`SELECT created_at::date AS day,
		        COUNT(*) AS calls,
		        COALESCE(SUM(input_tokens),0) AS input_tokens,
		        COALESCE(SUM(output_tokens),0) AS output_tokens,
		        COALESCE(AVG(latency_ms),0)::int AS avg_latency_ms,
		        CASE WHEN COUNT(*)>0 THEN SUM(CASE WHEN success THEN 1 ELSE 0 END)::float8/COUNT(*) ELSE 0 END AS success_rate
		 FROM capability_usage
		 WHERE project_space_id=$1 AND created_at >= now() - make_interval(days => $2)
		 GROUP BY created_at::date
		 ORDER BY created_at::date ASC`, psID, days); err != nil {
		return nil, err
	}

	// 应用 API 调用趋势（appgw_access_log by day）
	if err := s.store.db.SelectContext(ctx, &t.APITrend,
		`SELECT created_at::date AS day,
		        COUNT(*) AS calls,
		        COALESCE(AVG(latency_ms),0)::int AS avg_latency_ms,
		        CASE WHEN COUNT(*)>0 THEN SUM(CASE WHEN status<400 THEN 1 ELSE 0 END)::float8/COUNT(*) ELSE 0 END AS success_rate,
		        SUM(CASE WHEN status>=400 THEN 1 ELSE 0 END) AS error_count
		 FROM appgw_access_log
		 WHERE project_space_id=$1 AND created_at >= now() - make_interval(days => $2)
		 GROUP BY created_at::date
		 ORDER BY created_at::date ASC`, psID, days); err != nil {
		return nil, err
	}

	// 库大小趋势（db_size_snapshot 当日末值 by day）
	var raw []struct {
		Day       time.Time `db:"day"`
		SizeBytes int64     `db:"size_bytes"`
	}
	if err := s.store.db.SelectContext(ctx, &raw,
		`SELECT created_at::date AS day, total_size_bytes AS size_bytes FROM (
		   SELECT DISTINCT ON (created_at::date) created_at, total_size_bytes
		   FROM db_size_snapshot
		   WHERE project_space_id=$1 AND created_at >= now() - make_interval(days => $2)
		   ORDER BY created_at::date ASC, created_at DESC
		 ) t
		 ORDER BY created_at ASC`, psID, days); err != nil {
		return nil, err
	}
	for _, r := range raw {
		t.DBSizeTrend = append(t.DBSizeTrend, DBSizeTrendPoint{
			Day: r.Day, SizeBytes: r.SizeBytes, SizeMB: bytesToMb(r.SizeBytes),
		})
	}

	// 当前总大小 + 当前用量（复用 3a）
	cur, err := s.calcTotalDBSizeMb(ctx, psID)
	if err != nil {
		return nil, err
	}
	t.DBSizeCurrentMB = cur
	u, err := s.Usage(ctx, psID)
	if err != nil {
		return nil, err
	}
	t.Usage = u
	return t, nil
}

// Set 更新配额（admin 通过管理 UI 调；不存在则 GetOrCreate 后再 Set）。
func (s *Service) Set(ctx context.Context, psID string, maxApps, maxDatabases, maxTotalDBMb, maxCapabilityCallsPerDay int) (*Quota, error) {
	// 不存在则先建默认（再覆盖；保证 admin PUT 不报错）
	if _, err := s.store.GetOrCreate(ctx, psID); err != nil {
		return nil, err
	}
	if err := s.store.Set(ctx, psID, maxApps, maxDatabases, maxTotalDBMb, maxCapabilityCallsPerDay); err != nil {
		return nil, err
	}
	return s.store.Get(ctx, psID)
}

// ---------------- 计数实现 ----------------

func (s *Service) countApps(ctx context.Context, psID string) (int, error) {
	var n int
	err := s.store.db.GetContext(ctx, &n,
		`SELECT COUNT(*) FROM appdeploy_application WHERE project_space_id=$1`, psID)
	return n, err
}

func (s *Service) countDatabases(ctx context.Context, psID string) (int, error) {
	var n int
	err := s.store.db.GetContext(ctx, &n,
		`SELECT COUNT(*) FROM appdeploy_database WHERE project_space_id=$1 AND status<>'deleted'`, psID)
	return n, err
}

func (s *Service) countCapabilityToday(ctx context.Context, psID string) (int, error) {
	var n int
	err := s.store.db.GetContext(ctx, &n,
		`SELECT COUNT(*) FROM capability_usage WHERE project_space_id=$1 AND created_at::date=CURRENT_DATE`, psID)
	return n, err
}

// calcTotalDBSizeMb 该项目所有非 deleted 库的总大小（MB，向上取整）。
// 流程：取 db_name 列表 → 取项目实例 admin_url → 连实例查每库字节 → SUM 折 MB。
// 无实例 / 无库 → 0；连不上实例 → 0 + 不报错（运维层告警）。
func (s *Service) calcTotalDBSizeMb(ctx context.Context, psID string) (int, error) {
	if s.instances == nil || s.pg == nil {
		return 0, nil
	}
	var dbNames []string
	if err := s.store.db.SelectContext(ctx, &dbNames,
		`SELECT db_name FROM appdeploy_database WHERE project_space_id=$1 AND status<>'deleted'`, psID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	if len(dbNames) == 0 {
		return 0, nil
	}
	adminURL, err := s.instances.GetInstanceAdminURL(ctx, psID)
	if err != nil {
		return 0, err
	}
	if adminURL == "" {
		return 0, nil // 无 active 实例（外部纳管未注册或刚建空间未起容器）→ 0，不阻塞
	}
	sizes, err := s.pg.DatabaseSizes(ctx, adminURL, dbNames)
	if err != nil {
		// 连不上实例：返回 0 不阻塞业务，记录由 ops 排查
		return 0, nil
	}
	var total int64
	for _, b := range sizes {
		total += b
	}
	return bytesToMb(total), nil
}

// bytesToMb 字节 → MB（向上取整，避免少量字节被舍入为 0 看似未用）。
func bytesToMb(b int64) int {
	if b <= 0 {
		return 0
	}
	const mb = 1024 * 1024
	return int((b + mb - 1) / mb)
}

