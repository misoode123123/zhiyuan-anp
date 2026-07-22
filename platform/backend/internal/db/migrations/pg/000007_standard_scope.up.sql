-- 000007_standard_scope.up.sql
-- 开发规范分层：scope（platform/app/module）+ module（api/form/db/code/ui）。
-- 旧数据默认 scope=platform（兼容）；scope=module 时 module 指明子模块。
-- 旧 category 保留（general/security/...），作为分层下的补充分类。

ALTER TABLE coding_standard ADD COLUMN scope TEXT NOT NULL DEFAULT 'platform';
ALTER TABLE coding_standard ADD COLUMN module TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_std_scope ON coding_standard(scope, module);
