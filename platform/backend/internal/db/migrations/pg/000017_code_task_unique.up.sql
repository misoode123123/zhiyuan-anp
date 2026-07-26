-- 派发编码幂等兜底：同一来源（source_id = requirement_id）同时只允许一个 running 编码任务，
-- 防止"派发编码"连点生成多个并发 opencode 抢同一仓库导致互相卡死。
-- 步骤1：先清理陈年 running——历史重复提交/卡死留下的同源多条 running，否则建唯一索引会冲突。
UPDATE code_task
SET status = 'failed',
    output  = COALESCE(output, '') ||
              COALESCE(chr(10) || '[系统]运行超时未结束，迁移 000017 自动标记失败', '')
WHERE status = 'running'
  AND created_at < NOW() - INTERVAL '6 hours';

-- 步骤2：部分唯一索引，仅约束 status='running' 且有非空 source_id 的行
-- （完成/失败的不约束；kind=code 的手动派发 source_id 为空，排除避免误伤）。
CREATE UNIQUE INDEX IF NOT EXISTS uq_code_task_running_source
    ON code_task (source_id)
    WHERE status = 'running' AND source_id IS NOT NULL AND source_id <> '';
