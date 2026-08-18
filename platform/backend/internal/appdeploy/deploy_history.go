package appdeploy

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode"
)

// DeployHistoryItem 部署历史行（一行=一次部署尝试）。
type DeployHistoryItem struct {
	ID           int64      `json:"id" db:"id"`
	AppID        string     `json:"app_id" db:"app_id"`
	Env          string     `json:"env" db:"env"`
	Version      int        `json:"version" db:"version"`
	Engine       string     `json:"engine" db:"engine"`
	Result       string     `json:"result" db:"result"`
	Operator     string     `json:"operator" db:"operator"`
	SHA          string     `json:"sha,omitempty" db:"sha"`
	Image        string     `json:"image,omitempty" db:"image"`
	HostPort     int        `json:"host_port,omitempty" db:"host_port"`
	DurationSec  *int       `json:"duration_sec,omitempty" db:"duration_sec"`
	ErrorSummary string     `json:"error_summary,omitempty" db:"error_summary"`
	Notes        string     `json:"notes,omitempty" db:"notes"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty" db:"finished_at"`
}

// orphanCond 孤儿行过滤条件：result=” 且创建超 30min = backend 重启杀 goroutine
// 留下的永不终结行。统计与时间线共用；在途（<30min）行保留（时间线展示「部署中…」）。
const orphanCond = `NOT (result = '' AND created_at < NOW() - interval '30 minutes')`

// InsertDeployHistory 部署开始时插入在途行（result=”）。operator 空归一 unknown。
func (s *Store) InsertDeployHistory(ctx context.Context, appID, env string, version int, engine, operator, sha string) error {
	if operator == "" {
		operator = "unknown"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO deploy_history (app_id, env, version, engine, operator, sha)
		 VALUES ($1, $2, $3, $4, $5, NULLIF($6,''))`,
		appID, env, version, engine, operator, sha)
	return err
}

// FinishDeployHistory 终结本次部署行：按 (app_id,env,version) 定位且仅终结在途行
// （result=” 守卫防重复覆写）。duration/finished_at 按 DB 服务器时钟计算（免传参免漂移）。
// 成功行传 image/hostPort 实态值；失败行传零值（列保持 NULL）。
func (s *Store) FinishDeployHistory(ctx context.Context, appID, env string, version int, result, errSummary, notes, image string, hostPort int) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE deploy_history SET result=$1, error_summary=NULLIF($2,''), notes=NULLIF($3,''),
		        image=COALESCE(NULLIF($4,''), image), host_port=CASE WHEN $5>0 THEN $5 ELSE host_port END,
		        duration_sec=EXTRACT(EPOCH FROM (NOW()-created_at))::INT, finished_at=NOW()
		 WHERE app_id=$6 AND env=$7 AND version=$8 AND result=''`,
		result, errSummary, notes, image, hostPort, appID, env, version)
	return err
}

// ListDeployHistoryByApp 某应用部署历史（新→旧，含在途行，孤儿过滤，默认条数由调用方传）。
func (s *Store) ListDeployHistoryByApp(ctx context.Context, appID string, limit int) ([]DeployHistoryItem, error) {
	var list []DeployHistoryItem
	err := s.db.SelectContext(ctx, &list,
		`SELECT id, app_id, env, version, engine, result, operator,
		        COALESCE(sha,'') AS sha, COALESCE(image,'') AS image,
		        COALESCE(host_port,0) AS host_port, duration_sec,
		        COALESCE(error_summary,'') AS error_summary, COALESCE(notes,'') AS notes,
		        created_at, finished_at
		 FROM deploy_history WHERE app_id=$1 AND `+orphanCond+`
		 ORDER BY created_at DESC LIMIT $2`, appID, limit)
	return list, err
}

// DeployStatsResult 部署统计聚合（按 engine 分组 + 每日计数）。
type DeployStatsResult struct {
	Engines []EngineStats `json:"engines"`
	Daily   []DailyCount  `json:"daily"`
}

// EngineStats 单引擎聚合。MedSec 为中位耗时（偶数个取中间偏大者：sorted[len/2]，展示用）。
type EngineStats struct {
	Engine    string    `json:"engine"`
	Success   int       `json:"success"`
	Failed    int       `json:"failed"`
	AvgSec    int       `json:"avg_sec"`
	MedSec    int       `json:"med_sec"`
	TopErrors []ErrFreq `json:"top_errors"`
}

// ErrFreq 失败原因片段词频。
type ErrFreq struct {
	Fragment string `json:"fragment"`
	Count    int    `json:"count"`
}

// DailyCount 每日每引擎计数（day=created_at 日期，按东八区取日界）。
type DailyCount struct {
	Day     string `json:"day"`
	Engine  string `json:"engine"`
	Success int    `json:"success"`
	Failed  int    `json:"failed"`
}

// DeployStats 近 N 天（钳制 1-90）按引擎聚合：计数/耗时/每日 trend。空表返回零值结构。
// 失败 top 原因在 Go 侧分词（SQL 不做文本处理）：按标点切 + 取 ≥4 字（rune 计）片段，词频 top5。
func (s *Store) DeployStats(ctx context.Context, days int) (*DeployStatsResult, error) {
	if days < 1 {
		days = 1
	}
	if days > 90 {
		days = 90
	}
	res := &DeployStatsResult{Engines: []EngineStats{}, Daily: []DailyCount{}}
	since := time.Now().AddDate(0, 0, -days)

	// 每 engine 终态计数
	var agg []struct {
		Engine string `db:"engine"`
		Status string `db:"status"`
		N      int    `db:"n"`
	}
	if err := s.db.SelectContext(ctx, &agg,
		`SELECT engine, result AS status, COUNT(*)::INT AS n
		 FROM deploy_history
		 WHERE result IN ('success','failed') AND created_at >= $1 AND `+orphanCond+`
		 GROUP BY engine, result`, since); err != nil {
		return nil, err
	}
	byEngine := map[string]*EngineStats{}
	for _, r := range agg {
		e := byEngine[r.Engine]
		if e == nil {
			e = &EngineStats{Engine: r.Engine, TopErrors: []ErrFreq{}}
			byEngine[r.Engine] = e
		}
		if r.Status == "success" {
			e.Success = r.N
		} else {
			e.Failed = r.N
		}
	}
	// 耗时集合（success+failed 一起算均值/中位）+ 失败摘要集合
	var rows []struct {
		Engine string `db:"engine"`
		Dur    *int   `db:"duration_sec"`
		Err    string `db:"error_summary"`
	}
	if err := s.db.SelectContext(ctx, &rows,
		`SELECT engine, duration_sec, COALESCE(error_summary,'') AS error_summary
		 FROM deploy_history
		 WHERE result IN ('success','failed') AND created_at >= $1 AND `+orphanCond, since); err != nil {
		return nil, err
	}
	durs := map[string][]int{}
	errs := map[string][]string{}
	for _, r := range rows {
		if r.Dur != nil {
			durs[r.Engine] = append(durs[r.Engine], *r.Dur)
		}
		if r.Err != "" {
			errs[r.Engine] = append(errs[r.Engine], r.Err)
		}
	}
	for eng, e := range byEngine {
		if ds := durs[eng]; len(ds) > 0 {
			sum := 0
			for _, d := range ds {
				sum += d
			}
			e.AvgSec = sum / len(ds)
			sorted := append([]int(nil), ds...)
			sort.Ints(sorted)
			e.MedSec = sorted[len(sorted)/2]
		}
		e.TopErrors = topErrorFragments(errs[eng], 5)
	}
	// 引擎稳定排序（fixed 在前 ai 在后，其余按字典序）
	order := map[string]int{"fixed": 0, "ai": 1}
	engines := make([]string, 0, len(byEngine))
	for eng := range byEngine {
		engines = append(engines, eng)
	}
	sort.Slice(engines, func(i, j int) bool {
		oi, oj := order[engines[i]], order[engines[j]]
		if oi != oj {
			return oi < oj
		}
		return engines[i] < engines[j]
	})
	for _, eng := range engines {
		res.Engines = append(res.Engines, *byEngine[eng])
	}

	// 每日 trend（FILTER 宽行；东八区取日界——.28 部署与用户时区一致）
	var wide []struct {
		Day     string `db:"day"`
		Engine  string `db:"engine"`
		Success int    `db:"success"`
		Failed  int    `db:"failed"`
	}
	if err := s.db.SelectContext(ctx, &wide,
		`SELECT to_char((created_at AT TIME ZONE 'Asia/Shanghai')::DATE, 'YYYY-MM-DD') AS day,
		        engine,
		        COUNT(*) FILTER (WHERE result='success')::INT AS success,
		        COUNT(*) FILTER (WHERE result='failed')::INT AS failed
		 FROM deploy_history
		 WHERE result IN ('success','failed') AND created_at >= $1 AND `+orphanCond+`
		 GROUP BY 1, 2 ORDER BY 1 DESC, 2`, since); err != nil {
		return nil, err
	}
	for _, w := range wide {
		res.Daily = append(res.Daily, DailyCount{Day: w.Day, Engine: w.Engine, Success: w.Success, Failed: w.Failed})
	}
	return res, nil
}

// topErrorFragments 失败原因简单分词词频：按标点/空白切，取 rune 长度 ≥4 的片段，
// 计频取 top n。中文错误（如「docker build 失败: exit 1」）切出「docker」「build」等英文
// 片段与「失败」等短词——≥4 字过滤掉短中文词，保留可读英文词。
func topErrorFragments(errs []string, n int) []ErrFreq {
	freq := map[string]int{}
	for _, e := range errs {
		fields := strings.FieldsFunc(e, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		for _, f := range fields {
			if len([]rune(f)) >= 4 {
				freq[strings.ToLower(f)]++
			}
		}
	}
	out := make([]ErrFreq, 0, len(freq))
	for k, v := range freq {
		out = append(out, ErrFreq{Fragment: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Fragment < out[j].Fragment
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}
