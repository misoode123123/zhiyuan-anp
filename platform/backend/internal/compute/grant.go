package compute

import (
	"context"
)

// ListGrants 返回某用户已授权的模型（JOIN compute_model 取详情），按 model name 排序。
func (s *Store) ListGrants(ctx context.Context, userID string) ([]Model, error) {
	var out []Model
	const q = `SELECT m.id, m.provider_id, m.name, m.display_name, m.modality,
	                  m.context_window, m.max_output, m.cost_input, m.cost_output,
	                  m.capabilities, m.enabled, m.created_at
	           FROM user_model_grant g JOIN compute_model m ON m.id = g.model_id
	           WHERE g.user_id = $1 ORDER BY m.name`
	if err := s.db.SelectContext(ctx, &out, q, userID); err != nil {
		return nil, err
	}
	return out, nil
}

// GrantModels 批量授权（幂等：ON CONFLICT DO NOTHING）。空列表直接返回 nil。
// 起事务保证多 model 原子：要么全进，要么全回滚。
func (s *Store) GrantModels(ctx context.Context, userID string, modelIDs []string, grantedBy string) error {
	if len(modelIDs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	const q = `INSERT INTO user_model_grant (user_id, model_id, granted_by)
	           VALUES ($1, $2, $3) ON CONFLICT (user_id, model_id) DO NOTHING`
	for _, mid := range modelIDs {
		if _, err := tx.ExecContext(ctx, q, userID, mid, grantedBy); err != nil {
			return err
		}
	}
	return tx.Commit()
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
