# 好友分组迁移指南（MySQL + Redis）

本指南说明如何在 MySQL 8.0+ 上为每个用户补齐/修复默认好友分组，并批量回填 `friend.friend_group_id`。迁移使用 Redis 作为逐行唯一的 version 序列来源，支持 Go 命令行工具与 Python 脚本两种方式。

## 背景与目标

- 为每个用户确保存在一个默认分组（名称可配置，默认“我的好友”），并将该分组标记为 `is_default=1`、`is_deleted=0`。
- 聚合分组成员关系，计算每条 `friend` 记录应归属的分组，回填 `friend.friend_group_id`。
- 对所有“插入或更新”的行，version 都必须变化，且逐行唯一。唯一版本号由 Redis 自增序列保证。
- 全过程在单个事务内执行；支持 dry-run（不提交、不占用 Redis 序列）。

## 表与字段假设

- `user(uid, ...)`
- `friend_group(id, uid, name, is_default, is_deleted, version, ...)`
- `friend_group_member(id, uid, friend_uid, group_id, is_deleted, ...)`
- `friend(id, uid, to_uid, friend_group_id, is_deleted, version, ...)`

> 注意：脚本/程序使用 MySQL 窗口函数 `ROW_NUMBER()`，要求 MySQL 8.0+。

## 环境要求

- MySQL 8.0+（启用 InnoDB）
- Redis（用于递增 version 序列）
- 任选其一：
  - Go 1.21+（仓库已包含可编译源码与 Linux 二进制）
  - Python 3.10+（需安装依赖 `PyMySQL`、`redis`）

## 方案概览（Version 与幂等）

- 逐行唯一 version：对待插入/更新的行，按固定顺序分配 `ROW_NUMBER()`，再用 Redis `INCRBY` 一次性预留连续区间，计算 `start + rn - 1`，确保每行唯一且单调递增。
- 幂等：
  - 默认分组插入仅对“缺失者”执行；对“已存在者”只做 `is_default=1,is_deleted=0,version=新值` 的更新。
  - `friend` 更新仅对“需要变化”的行执行（当前 `friend_group_id` 与目标值不同）。
- 事务与 dry-run：
  - 所有 SQL 在单事务内；`--dry-run`/`-dry-run` 会回滚，不写库且不占用 Redis 序列。

## 使用方式 A：Go 命令行

源码位置：`practice/golang/cmd/migrate-friend-groups/main.go`

已提供 Linux 可执行文件（如存在）：`dist/migrate-friend-groups_linux_amd64`、`dist/migrate-friend-groups_linux_arm64`

- 配置文件示例：`practice/golang/config.yaml`

  ```yaml
  database:
    dialect: mysql
    host: localhost
    port: 3306
    user: root
    password: 123456
    dbname: tsdd
  logging:
    level: info
    format: json
    add_source: true
  ```

- 常用参数（Go）：
  - `-config` 配置文件路径（默认 `../../config.yaml`，建议显式传入）
  - `-default-name` 默认分组名称（默认：我的好友）
  - `-dry-run` 只统计，不提交
  - `-lock-wait-timeout` 设置 `innodb_lock_wait_timeout`（秒）
  - `-redis-addr`、`-redis-password`、`-redis-db` Redis 连接
  - `-friend-seq-key`、`-friend-group-seq-key` Redis 序列键（Go 默认分别为 `FriendSeqKey`、`FriendGroupSeqKey`）

- 运行示例（zsh）：

  ```zsh
  # 使用源码构建后运行（或直接运行 dist 二进制）
  ./dist/migrate-friend-groups_linux_amd64 \
    -config ./practice/golang/config.yaml \
    -default-name "我的好友" \
    -lock-wait-timeout 60 \
    -redis-addr 127.0.0.1:6379 \
    -friend-seq-key FriendSeqKey \
    -friend-group-seq-key FriendGroupSeqKey \
    -dry-run

  # 去掉 -dry-run 以真正写入：
  ./dist/migrate-friend-groups_linux_amd64 \
    -config ./practice/golang/config.yaml \
    -default-name "我的好友" \
    -lock-wait-timeout 60 \
    -redis-addr 127.0.0.1:6379 \
    -friend-seq-key FriendSeqKey \
    -friend-group-seq-key FriendGroupSeqKey
  ```

> 说明：当前 Go 版本对 `friend` 更新没有排除特定 `to_uid`；如需与 Python 一致（跳过 `u_10000`/`fileHelper`），可在后续迭代中加入对应过滤开关。

## 使用方式 B：Python 脚本

脚本位置：`migration_redis.py`

- 安装依赖：

  ```zsh
  pip3 install pymysql redis
  ```

- 常用参数（Python）：
  - `--host/--port/--user/--password/--db/--charset` MySQL 连接（`--db` 必填）
  - `--default-name` 默认分组名称（默认：我的好友）
  - `--dry-run` 只统计，不提交
  - `--lock-wait-timeout` 设置 `innodb_lock_wait_timeout`
  - `--redis-addr/--redis-password/--redis-db` Redis 连接
  - `--friend-seq-key/--friend-group-seq-key` Redis 序列键（Python 默认：`seq:friend`、`seq:friend_group`）

- 运行示例（zsh）：

  ```zsh
  python3 migration_redis.py \
    --host 127.0.0.1 --user root --password 123456 --db tsdd \
    --default-name "我的好友" \
    --redis-addr 127.0.0.1:6379 \
    --friend-seq-key FriendSeqKey \
    --friend-group-seq-key FriendGroupSeqKey \
    --lock-wait-timeout 60 \
    --dry-run

  # 去掉 --dry-run 以真正写入
  ```

- 特殊排除：Python 版本会在回填 `friend.friend_group_id` 时跳过 `to_uid IN ('u_10000','fileHelper')` 的记录，避免改动这些特殊账号的分组归属。

## 使用方式 C：仅 SQL（无 Redis）

文件：`migration_mysql.sql`

- 作用：仅靠 SQL 事务式地补齐默认分组和回填分组；适合不要求“逐行唯一 version”的场景。
- 运行：在目标库执行该 SQL（建议先备份并在测试环境验证）。

## Dry-run 与安全性

- Dry-run：
  - Go：`-dry-run` 会在事务结束时回滚；不会写库，也不会占用 Redis 序列。
  - Python：`--dry-run` 同上。
- 失败回滚：发生错误会回滚整个事务；日志/输出会打印失败原因。
- 锁等待：可通过 `-lock-wait-timeout/--lock-wait-timeout` 调整，避免长时间阻塞。

## 故障排查

- MySQL 版本过低：报错涉及 `ROW_NUMBER()` 或窗口函数，请升级到 MySQL 8.0+。
- Redis 连接失败：检查地址、密码、DB 索引；尝试 `PING`。
- 权限不足：确保 MySQL 账户具备创建临时表、读写目标表的权限。
- 版本键不统一：Go 与 Python 的默认 Redis 键不同，建议显式传入统一的键名（如 `FriendSeqKey` / `FriendGroupSeqKey`）。

## 小结

- 推荐先 dry-run，确认影响行数符合预期后再执行真实写入。
- 若需两端（Go/Python）完全一致的行为（例如过滤某些 `to_uid`），可以将规则抽到配置/参数中统一控制；目前 Python 已默认跳过 `u_10000` / `fileHelper`。
