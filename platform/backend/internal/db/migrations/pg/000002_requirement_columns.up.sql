-- 000002: 补 requirement 表遗漏的 application_id / priority / fixed_version 三列。
-- 背景：000001_init 的 requirement 表由 SQLite schema 转 PG 时遗漏此三列，
-- 但 repository.go 的 reqCols/INSERT 已引用 → 生产 PG 缺列会致 INSERT/SELECT 报
-- "column does not exist"。由 requirement 包补单测时暴露（T1 测试巩固）。
-- 用 IF NOT EXISTS 幂等：全新库已由 000001 建好则跳过，已跑过旧 000001 的库补齐。
ALTER TABLE requirement ADD COLUMN IF NOT EXISTS application_id TEXT;
ALTER TABLE requirement ADD COLUMN IF NOT EXISTS priority TEXT;
ALTER TABLE requirement ADD COLUMN IF NOT EXISTS fixed_version TEXT;
