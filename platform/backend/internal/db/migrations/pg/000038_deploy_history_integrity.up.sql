-- 000038_deploy_history_integrity.up.sql
-- 收口 deploy_history 表完整性约束（000037 建表遗漏两处）：
-- 1) app_id 无 FK：删 app 后历史行变孤儿，残留行继续进统计（与 000031 修 env/instance 同源）；
-- 2) 无在途唯一约束：并发双击/复合故障下同键 (app_id,env,version) 可能双 INSERT。
-- 必须先清孤儿，否则 ADD CONSTRAINT 因现存违规行失败（.28 现无孤儿，空跑无害）。
-- 不写显式 BEGIN/COMMIT：migrate runner 把每个迁移包一个事务（与既有迁移一致），
-- 文件内 DELETE→ALTER→CREATE INDEX 顺序即执行顺序，原子生效。
-- INSERT 撞唯一索引报错由既有调用点 zap.Warn 吞掉继续——best-effort 红线不破。

DELETE FROM deploy_history WHERE app_id NOT IN (SELECT id FROM appdeploy_application);

-- 删应用级联清历史（与其余 5 张 app 关联表对齐）
ALTER TABLE deploy_history
ADD CONSTRAINT deploy_history_app_fk
FOREIGN KEY (app_id) REFERENCES appdeploy_application(id) ON DELETE CASCADE;

-- 同键 (app_id,env,version) 至多一行在途：并发双击/复合故障双 INSERT 从源头消灭。
-- 终态行（success/failed）不占位：同版本失败重试仍可再插在途行。
CREATE UNIQUE INDEX uq_dephist_inflight ON deploy_history(app_id, env, version) WHERE result='';
