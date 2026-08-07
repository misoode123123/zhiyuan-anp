-- 模型中心 · 用户模型授权（Plan 1 / Task 1）：用户 ↔ 模型 多对多授权关系。
-- user_id 来自 platform user 体系（TEXT，无 FK 强约束以兼容外部 ID 源）；
-- model_id REFERENCES compute_model(id) ON DELETE CASCADE —— 模型删除自动清理授权。
-- PK(user_id, model_id) 天然幂等（同一用户重复授权同一模型不产生重复行）。
CREATE TABLE IF NOT EXISTS user_model_grant (
    user_id     TEXT        NOT NULL,
    model_id    TEXT        NOT NULL REFERENCES compute_model(id) ON DELETE CASCADE,
    granted_by  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, model_id)
);
