package pgsupply

import "context"

// QuotaAdapter 把 pgsupply.Store 包装成 quota.InstanceLookup（不导入 quota 包，避免循环依赖）。
// 用法：main 把 QuotaAdapter 实例传给 quota.NewService，由 quota 调 GetInstanceAdminURL。
type QuotaAdapter struct {
	store *Store
}

// NewQuotaAdapter 构造。
func NewQuotaAdapter(store *Store) *QuotaAdapter { return &QuotaAdapter{store: store} }

// GetInstanceAdminURL 取项目首个 active PG 实例的 admin_url。
// 无实例（sql.ErrNoRows）→ 返回 "" + nil（调用方按 0 处理，不阻塞建库）。
func (a *QuotaAdapter) GetInstanceAdminURL(ctx context.Context, psID string) (string, error) {
	ins, err := a.store.GetInstanceByProject(ctx, psID)
	if err != nil {
		// sql.ErrNoRows 在 store 层返回（GetInstanceByProject 不吞），上层按"无实例"处理
		return "", nil
	}
	if ins == nil {
		return "", nil
	}
	return ins.AdminURLRef, nil
}
