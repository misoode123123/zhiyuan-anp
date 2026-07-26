-- 数据修复不可逆（步骤1删除的重复错位行无法还原）。
-- 步骤2可部分反向：把 user_id 还原为 user.name。
UPDATE membership m
SET user_id = u.name
FROM "user" u
WHERE m.user_id = u.id;
