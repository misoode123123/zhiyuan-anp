-- 000032 回滚:删 restart_count 列。历史 reconcile 观测值丢失(可由下一轮 reconcile 重建)。
ALTER TABLE appdeploy_instance DROP COLUMN IF EXISTS restart_count;
