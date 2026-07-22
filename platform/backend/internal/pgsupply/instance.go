package pgsupply

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// InstanceManager 每个项目空间取/建一个独立 PG 实例（managed: docker 起容器）。
type InstanceManager struct {
	store  *Store
	docker DockerRunner
	admin  PGAdmin
	host   string // 宿主对外地址（AppDeployHost）
}

// NewInstanceManager 构造。
func NewInstanceManager(store *Store, docker DockerRunner, admin PGAdmin, host string) *InstanceManager {
	return &InstanceManager{store: store, docker: docker, admin: admin, host: host}
}

// GetOrCreate 项目有 active 实例则复用；无（sql.ErrNoRows）则 managed 起容器；DB 错误则返回。
func (m *InstanceManager) GetOrCreate(ctx context.Context, psID string) (*PGInstance, error) {
	ins, err := m.store.GetInstanceByProject(ctx, psID)
	if err == nil && ins != nil {
		return ins, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("查项目 PG 实例: %w", err)
	}
	return m.provision(ctx, psID)
}

// provision 起新 PG 容器并登记。
func (m *InstanceManager) provision(ctx context.Context, psID string) (*PGInstance, error) {
	name := InstanceName(psID) + "-" + genShortID()
	pwd := genPassword()
	port := allocPort(m.docker.UsedPorts(ctx), pgPortMin, pgPortMax)
	if port == 0 {
		return nil, fmt.Errorf("无可用 PG 端口（%d-%d 已满）", pgPortMin, pgPortMax)
	}
	if err := m.docker.RunPGContainer(ctx, name, pwd, port); err != nil {
		return nil, fmt.Errorf("起 PG 容器: %w", err)
	}
	adminURL := DSN(m.host, port, "postgres", pwd, "postgres")
	if err := waitForReady(ctx, m.admin, adminURL); err != nil {
		_ = m.docker.RmForce(ctx, name) // 清理半成品容器
		return nil, fmt.Errorf("PG 未就绪: %w", err)
	}
	ins := &PGInstance{
		ID:             "pgi_" + genShortID(),
		ProjectSpaceID: psID,
		Host:           m.host,
		Port:           port,
		AdminURLRef:    adminURL,
		DeployMode:     DeployManaged,
		Status:         StatusActive,
		ContainerName:  name,
	}
	if err := m.store.CreateInstance(ctx, ins); err != nil {
		_ = m.docker.RmForce(ctx, name)
		return nil, fmt.Errorf("登记实例: %w", err)
	}
	return ins, nil
}

// waitForReady 轮询 Ping 直到成功或 ctx 取消（由调用方传超时 ctx 控制时长）。
func waitForReady(ctx context.Context, admin PGAdmin, adminURL string) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	// 首次立即试
	if err := admin.Ping(ctx, adminURL); err == nil {
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := admin.Ping(ctx, adminURL); err == nil {
				return nil
			}
		}
	}
}
