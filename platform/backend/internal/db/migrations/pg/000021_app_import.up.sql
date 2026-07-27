-- 导入已有项目（方案2）：标记 Application 代码来源，区别于 EnsureRepo 新建空仓。
-- import_source ''(新建)/'git'(远程仓库)/'dir'(本地目录: zip上传 或 服务器目录)
-- import_ref    git=仓库URL / dir=来源标识
-- imported_at   导入完成时间(进行中 NULL)
ALTER TABLE appdeploy_application
    ADD COLUMN IF NOT EXISTS import_source TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS import_ref    TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS imported_at   TIMESTAMPTZ;
