-- 000019_test_case_manual_verdict.down.sql
ALTER TABLE test_case DROP COLUMN IF EXISTS verified_at;
ALTER TABLE test_case DROP COLUMN IF EXISTS verifier_id;
ALTER TABLE test_case DROP COLUMN IF EXISTS manual_note;
