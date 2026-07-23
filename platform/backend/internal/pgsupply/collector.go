// collector.go —— 库大小定时采集（阶段3b）。
//
// 流程：遍历所有 active PG 实例 → 每实例取其下所有应用库 → 连实例一次查
// pg_database_size → UPDATE appdeploy_database.size_bytes。
// 采集后按项目聚合 SUM(size_bytes) vs project_quota.max_total_db_mb，超限 zap.Warn 告警
// （不硬拦，3a 建库时已拦；本处只对「已存在库涨满」做被动告警）。
package pgsupply

import (
	"context"

	"go.uber.org/zap"
)

// Collector 库大小定时采集器。main ticker 每 tick 调 CollectDBSizes。
type Collector struct {
	store  *Store
	pg     PGAdmin
	logger *zap.Logger
}

// NewCollector 构造。logger 可为 nil（内部用 nop）。
func NewCollector(store *Store, pg PGAdmin, logger *zap.Logger) *Collector {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Collector{store: store, pg: pg, logger: logger}
}

// CollectResult CollectDBSizes 的累计结果。
type CollectResult struct {
	Instances  int         `json:"instances"`  // 处理的 active 实例数
	Total      int         `json:"total"`      // 待采库数
	Updated    int         `json:"updated"`    // 成功更新 size_bytes 的库数
	Failed     int         `json:"failed"`     // 查询失败的库数
	Snapshots  int         `json:"snapshots"`  // 写入 db_size_snapshot 的项目数（3c 趋势数据源）
	Alerts     []AlertInfo `json:"alerts"`     // 超 max_total_db_mb 的项目
}

// AlertInfo 单个项目库大小超限告警信息。
type AlertInfo struct {
	ProjectSpaceID string `json:"project_space_id"`
	UsedMB         int64  `json:"used_mb"`
	LimitMB        int    `json:"limit_mb"`
}

// CollectDBSizes 遍历所有 active 实例，连各实例查 pg_database_size → 更新 size_bytes；
// 之后按项目聚合判超限 → 告警。单实例失败不中断（记日志继续下一个）。
func (c *Collector) CollectDBSizes(ctx context.Context) CollectResult {
	r := CollectResult{Alerts: []AlertInfo{}}

	instances, err := c.store.ListActiveInstances(ctx)
	if err != nil {
		c.logger.Error("CollectDBSizes 列实例失败", zap.Error(err))
		return r
	}
	r.Instances = len(instances)

	// psID → 该项目本轮采到的字节累计（用于告警比对）
	psBytes := map[string]int64{}

	for _, ins := range instances {
		if ctx.Err() != nil {
			break
		}
		list, err := c.store.ListAppDBsByInstance(ctx, ins.ID)
		if err != nil {
			c.logger.Warn("CollectDBSizes 列库失败，跳过实例",
				zap.String("instance_id", ins.ID), zap.Error(err))
			continue
		}
		if len(list) == 0 {
			continue // 实例无库，跳过（不连实例）
		}
		dbNames := make([]string, 0, len(list))
		for _, ad := range list {
			dbNames = append(dbNames, ad.DBName)
		}
		r.Total += len(list)

		sizes, err := c.pg.DatabaseSizes(ctx, ins.AdminURLRef, dbNames)
		if err != nil {
			c.logger.Warn("CollectDBSizes 连实例查 size 失败，跳过实例",
				zap.String("instance_id", ins.ID), zap.Error(err))
			r.Failed += len(list)
			continue
		}
		for _, ad := range list {
			n, ok := sizes[ad.DBName]
			if !ok {
				// 库在 appdeploy_database 有记录但 PG 里查不到（被外部删？）→ 记 0 + warn
				c.logger.Warn("CollectDBSizes 库在 PG 中未找到",
					zap.String("app_id", ad.AppID), zap.String("db_name", ad.DBName))
				n = 0
			}
			if err := c.store.UpdateAppDBSize(ctx, ad.ID, n); err != nil {
				c.logger.Warn("CollectDBSizes 更新 size_bytes 失败",
					zap.String("app_id", ad.AppID), zap.Error(err))
				r.Failed++
				continue
			}
			r.Updated++
			psBytes[ad.ProjectSpaceID] += n
		}
	}

	// 告警：按项目比对 max_total_db_mb；同时插一条 db_size_snapshot（3c 趋势数据源）。
	for psID, bytes := range psBytes {
		// 先写快照（无论是否超限，都留历史；3c 看板按日画增长）。
		if err := c.store.AddDBSizeSnapshot(ctx, psID, bytes); err != nil {
			c.logger.Warn("CollectDBSizes 写 db_size_snapshot 失败",
				zap.String("ps_id", psID), zap.Error(err))
		} else {
			r.Snapshots++
		}
		limit, err := c.store.MaxTotalDBMb(ctx, psID)
		if err != nil {
			c.logger.Warn("CollectDBSizes 查 max_total_db_mb 失败",
				zap.String("ps_id", psID), zap.Error(err))
			continue
		}
		if limit <= 0 {
			continue // 未设配额（行不存在或 0），跳过
		}
		const mb = 1024 * 1024
		usedMB := (bytes + mb - 1) / mb
		if usedMB > int64(limit) {
			r.Alerts = append(r.Alerts, AlertInfo{
				ProjectSpaceID: psID, UsedMB: usedMB, LimitMB: limit,
			})
			c.logger.Warn("db size 超配额（库大小告警）",
				zap.String("ps_id", psID),
				zap.Int64("used_mb", usedMB),
				zap.Int("limit_mb", limit))
		}
	}

	return r
}
