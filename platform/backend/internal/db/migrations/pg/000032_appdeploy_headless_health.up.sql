-- 000032: headless 运行态健康监控——appdeploy_instance 加 restart_count 列。
-- 存 HealthReconciler 上次观测的 docker RestartCount,做 crash-loop 增量判定(非粘)。
-- 不写显式 BEGIN/COMMIT:migrate runner 每迁移包一事务(与既有迁移一致)。
ALTER TABLE appdeploy_instance ADD COLUMN IF NOT EXISTS restart_count INT NOT NULL DEFAULT 0;
