-- 修复存量 membership 种子错位：早期 SeedBootstrapMembers 把 user_id 列写成用户名
-- （admin/dev1/...），但 user.id 实为 usr_xxx，导致按 user_id 查询（如首页 my-tasks 的 roles）
-- 匹配不到。两步处理（避免 membership(project_space_id,user_id) 唯一约束冲突）：
--   ① 删"重复错位行"——同 project_space 下已有正确 usr_xxx 行的错位行（保留正确行）；
--   ② 把剩余"独有错位行"（无正确行）的 user_id 改为对应 user.id。

-- 步骤1：删除重复错位行（user_id 仍是用户名，且同空间已有该用户的正确 usr_xxx 行）
DELETE FROM membership d
WHERE d.user_id NOT LIKE 'usr_%'
  AND EXISTS (
    SELECT 1 FROM "user" u
    WHERE u.name = d.user_id
      AND EXISTS (SELECT 1 FROM membership m2
                  WHERE m2.project_space_id = d.project_space_id AND m2.user_id = u.id)
  );

-- 步骤2：剩余错位行（独有，无正确行）改为正确 user.id
UPDATE membership m
SET user_id = u.id
FROM "user" u
WHERE m.user_id = u.name;
