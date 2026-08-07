package appdeploy

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
)

// ServerMetric 单次服务器指标采样。
type ServerMetric struct {
	NodeID         string    `json:"node_id" db:"node_id"`
	CapturedAt     time.Time `json:"captured_at" db:"captured_at"`
	CPUPercent     float64   `json:"cpu_percent" db:"cpu_percent"`
	MemTotal       int64     `json:"mem_total" db:"mem_total"`
	MemUsed        int64     `json:"mem_used" db:"mem_used"`
	DiskTotal      int64     `json:"disk_total" db:"disk_total"`
	DiskUsed       int64     `json:"disk_used" db:"disk_used"`
	LoadAvg        float64   `json:"load_avg,omitempty" db:"load_avg"`
	Uptime         string    `json:"uptime,omitempty" db:"uptime"`
	AppCount       int       `json:"app_count" db:"app_count"`
	ContainerCount int       `json:"container_count,omitempty" db:"container_count"`
}

// MetricStore 服务器指标持久化（appdeploy_server_metric 表）。
type MetricStore struct{ db *sqlx.DB }

func NewMetricStore(db *sqlx.DB) *MetricStore { return &MetricStore{db: db} }

func (s *MetricStore) Insert(ctx context.Context, m *ServerMetric) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO appdeploy_server_metric (node_id, captured_at, cpu_percent, mem_total, mem_used, disk_total, disk_used, load_avg, uptime, app_count, container_count)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		m.NodeID, m.CapturedAt, m.CPUPercent, m.MemTotal, m.MemUsed, m.DiskTotal, m.DiskUsed, m.LoadAvg, m.Uptime, m.AppCount, m.ContainerCount)
	return err
}

func (s *MetricStore) Latest(ctx context.Context, nodeID string) (*ServerMetric, error) {
	var m ServerMetric
	err := s.db.GetContext(ctx, &m,
		`SELECT node_id, captured_at, cpu_percent, mem_total, mem_used, disk_total, disk_used, load_avg, uptime, app_count, container_count
		 FROM appdeploy_server_metric WHERE node_id=$1 ORDER BY captured_at DESC LIMIT 1`, nodeID)
	return &m, err
}

func (s *MetricStore) History(ctx context.Context, nodeID string, limit int) ([]ServerMetric, error) {
	var list []ServerMetric
	if limit <= 0 {
		limit = 60
	}
	err := s.db.SelectContext(ctx, &list,
		`SELECT node_id, captured_at, cpu_percent, mem_total, mem_used, disk_total, disk_used, load_avg, uptime, app_count, container_count
		 FROM appdeploy_server_metric WHERE node_id=$1 ORDER BY captured_at DESC LIMIT $2`, nodeID, limit)
	return list, err
}
