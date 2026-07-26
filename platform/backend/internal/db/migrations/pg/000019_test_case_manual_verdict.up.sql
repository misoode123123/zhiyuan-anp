-- 000019_test_case_manual_verdict.up.sql
-- 测试中心人工验收：test_case 加备注/验收人/验收时间三列。

ALTER TABLE test_case ADD COLUMN IF NOT EXISTS manual_note TEXT DEFAULT '';
ALTER TABLE test_case ADD COLUMN IF NOT EXISTS verifier_id  TEXT DEFAULT '';
ALTER TABLE test_case ADD COLUMN IF NOT EXISTS verified_at  TIMESTAMPTZ;
