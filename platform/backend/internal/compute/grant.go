package compute

import (
	"context"
)

// ListGrants 返回某用户已授权的模型（JOIN compute_model 取详情），按 created_at 排序
// （与 Store.ListModels 的 ORDER BY created_at 保持一致，前端两个下拉排序统一）。
func (s *Store) ListGrants(ctx context.Context, userID string) ([]Model, error) {
	var out []Model
	const q = `SELECT m.id, m.provider_id, m.name, m.display_name, m.modality,
	                  m.context_window, m.max_output, m.cost_input, m.cost_output,
	                  m.capabilities, m.enabled, m.created_at
	           FROM user_model_grant g JOIN compute_model m ON m.id = g.model_id
	           WHERE g.user_id = $1 ORDER BY m.created_at`
	if err := s.db.SelectContext(ctx, &out, q, userID); err != nil {
		return nil, err
	}
	return out, nil
}

// GrantModels 批量授权（幂等：ON CONFLICT DO NOTHING）。空列表直接返回 0。
// 起事务保证多 model 原子：要么全进，要么全回滚。
// 返回实际新增行数：PG 的 ON CONFLICT DO NOTHING 对冲突行 RowsAffected=0、
// 新增行=1，故累加值即真实新增授权数（非请求数），供 handler 准确回执与审计。
func (s *Store) GrantModels(ctx context.Context, userID string, modelIDs []string, grantedBy string) (int, error) {
	if len(modelIDs) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	const q = `INSERT INTO user_model_grant (user_id, model_id, granted_by)
	           VALUES ($1, $2, $3) ON CONFLICT (user_id, model_id) DO NOTHING`
	granted := 0
	for _, mid := range modelIDs {
		res, err := tx.ExecContext(ctx, q, userID, mid, grantedBy)
		if err != nil {
			return 0, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		granted += int(n)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return granted, nil
}

// RevokeModel 收回单个授权。
func (s *Store) RevokeModel(ctx context.Context, userID, modelID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM user_model_grant WHERE user_id = $1 AND model_id = $2`,
		userID, modelID)
	return err
}

// IsGranted 单点校验：该用户是否被授权此模型。
func (s *Store) IsGranted(ctx context.Context, userID, modelID string) (bool, error) {
	var exists bool
	err := s.db.GetContext(ctx, &exists,
		`SELECT EXISTS(SELECT 1 FROM user_model_grant WHERE user_id = $1 AND model_id = $2)`,
		userID, modelID)
	return exists, err
}
