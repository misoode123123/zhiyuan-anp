package appdeploy

import (
	"context"
	"database/sql"
	"encoding/json"
)

// SaveAnalysis 写入/覆盖某应用的部署分析（按 app_id 主键 upsert）。
func (s *Store) SaveAnalysis(ctx context.Context, appID string, a *DeployAnalysis) error {
	if a == nil {
		a = &DeployAnalysis{}
	}
	b, err := json.Marshal(a)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO appdeploy_deploy_analysis (app_id, analysis, analyzed_at)
		VALUES ($1, $2, CURRENT_TIMESTAMP)
		ON CONFLICT (app_id) DO UPDATE SET analysis = EXCLUDED.analysis, analyzed_at = CURRENT_TIMESTAMP`,
		appID, b)
	return err
}

// GetAnalysis 读某应用最新部署分析；无记录返回 (nil, nil)。
func (s *Store) GetAnalysis(ctx context.Context, appID string) (*DeployAnalysis, error) {
	var raw []byte
	err := s.db.GetContext(ctx, &raw, `SELECT analysis FROM appdeploy_deploy_analysis WHERE app_id=$1`, appID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	var a DeployAnalysis
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	return &a, nil
}
