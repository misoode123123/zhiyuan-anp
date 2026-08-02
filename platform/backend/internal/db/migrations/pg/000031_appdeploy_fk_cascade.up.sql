-- 000031_appdeploy_fk_cascade.up.sql
-- 收口 appdeploy_env / appdeploy_instance 缺 FK：删 app 后这两张表行变孤儿（000001 init schema 遗漏）。
-- 必须先清孤儿，否则 ADD CONSTRAINT 因现存违规行失败。
-- 不写显式 BEGIN/COMMIT：migrate runner 把每个迁移包一个事务（与既有迁移一致），
-- 文件内 DELETE→ALTER 顺序即执行顺序，孤儿清除与加约束原子生效。

DELETE FROM appdeploy_env
WHERE app_id NOT IN (SELECT id FROM appdeploy_application);

DELETE FROM appdeploy_instance
WHERE app_id NOT IN (SELECT id FROM appdeploy_application);

ALTER TABLE appdeploy_env
ADD CONSTRAINT appdeploy_env_app_fk
FOREIGN KEY (app_id) REFERENCES appdeploy_application(id) ON DELETE CASCADE;

ALTER TABLE appdeploy_instance
ADD CONSTRAINT appdeploy_instance_app_fk
FOREIGN KEY (app_id) REFERENCES appdeploy_application(id) ON DELETE CASCADE;
