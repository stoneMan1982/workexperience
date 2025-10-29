-- =============================
-- MySQL 迁移脚本：默认分组生成 + 好友分组回填
-- 目标：
-- 1) 为每个用户生成默认分组（名称“我的好友”，is_default=1），若已存在则更新为未删除；
-- 2) 聚合有效的分组成员 (过滤 is_deleted=1)；
-- 3) 批量更新 friend.friend_group_id，优先使用成员映射，否则回退到默认分组。
-- 说明：使用临时表以提升性能；整块脚本可反复执行（幂等）。
-- =============================

START TRANSACTION;

-- Step 1: 为所有用户确保存在默认分组（若重复则更新为未删除、默认）
INSERT INTO friend_group (uid, name, is_default)
SELECT u.uid, '我的好友', 1
FROM `user` AS u
ON DUPLICATE KEY UPDATE
  is_default = VALUES(is_default),
  is_deleted = 0;

-- 准备：每个用户的默认分组
DROP TEMPORARY TABLE IF EXISTS tmp_defaults;
CREATE TEMPORARY TABLE tmp_defaults
(
  PRIMARY KEY(uid)
)
AS
SELECT fg.uid, fg.id AS default_group_id
FROM friend_group fg
WHERE fg.is_default = 1
  AND COALESCE(fg.is_deleted, 0) = 0;

-- 准备：按 (uid, friend_uid) 聚合后的有效成员映射
DROP TEMPORARY TABLE IF EXISTS tmp_member;
CREATE TEMPORARY TABLE tmp_member
(
  PRIMARY KEY(uid, friend_uid)
)
AS
SELECT fgm.uid, fgm.friend_uid, MIN(fgm.group_id) AS target_group_id
FROM friend_group_member fgm
JOIN friend_group fg ON fg.id = fgm.group_id
WHERE COALESCE(fgm.is_deleted, 0) = 0
  AND COALESCE(fg.is_deleted, 0) = 0
GROUP BY fgm.uid, fgm.friend_uid;

-- Step 3: 批量更新 friend 表的分组ID（仅更新确实变化的记录）
UPDATE friend f
JOIN tmp_defaults d ON d.uid = f.uid
LEFT JOIN tmp_member m ON m.uid = f.uid AND m.friend_uid = f.to_uid
SET f.friend_group_id = COALESCE(m.target_group_id, d.default_group_id)
WHERE COALESCE(f.is_deleted, 0) = 0
  AND COALESCE(f.friend_group_id, 0) <> COALESCE(m.target_group_id, d.default_group_id);

COMMIT;

-- 可选：如需控制锁等待时间，可在事务前设置（单位秒）
-- SET SESSION innodb_lock_wait_timeout = 60;

-- 可选：为大表补充索引以提升执行速度（若不存在）
-- CREATE INDEX idx_fg_uid_default ON friend_group(uid, is_default);
-- CREATE INDEX idx_fg_id_deleted  ON friend_group(id, is_deleted);
-- CREATE INDEX idx_fgm_uid_friend ON friend_group_member(uid, friend_uid);
-- CREATE INDEX idx_fgm_group_del  ON friend_group_member(group_id, is_deleted);
-- CREATE INDEX idx_friend_uid_to  ON friend(uid, to_uid);
-- CREATE INDEX idx_friend_deleted ON friend(is_deleted);
-- CREATE INDEX idx_friend_group   ON friend(friend_group_id);
