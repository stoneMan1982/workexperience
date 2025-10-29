"""
MySQL migration script

目标：
1) 为每个用户创建默认好友分组（名称默认“我的好友”），若已存在则跳过（支持 is_deleted=1 过滤）。
2) 汇总有效分组成员(friend_group_member 和 friend_group 均过滤 is_deleted=1)，为每个 (uid, friend_uid) 选择一个分组（取最小 group_id）。
3) 将 friend.friend_group_id 批量更新为上述分组；若某好友没有成员记录，则回退到该用户的默认分组。

说明：
- 假定表结构/列如下（和当前项目一致/可按需修改）：
  - `user`(uid)
  - `friend_group`(id, uid, name, is_default, is_deleted)
  - `friend_group_member`(group_id, uid, friend_uid, is_deleted)
  - `friend`(uid, to_uid, friend_group_id, is_deleted)
  注意：`friend_group_member.friend_uid` 与 `friend.to_uid` 对应。
  注意：表名 `user` 在 MySQL 是保留字，使用反引号 `user`。

运行方式示例：
  python3 migration.py --host 127.0.0.1 --user root --password 123456 --db tsdd --ensure-indexes
可选参数： --dry-run 仅统计影响行数不提交； --lock-wait-timeout 变更锁等待秒数； --default-name 自定义默认分组名称。
"""

import argparse
from typing import List, Optional
import pymysql
from pymysql.cursors import DictCursor


def ensure_index(cur, db_name: str, table: str, index_name: str, columns: List[str]) -> None:
    """确保给定表存在指定名称的索引；若不存在则创建。
    在较老 MySQL 版本上不使用 IF NOT EXISTS，改为查询 INFORMATION_SCHEMA 以避免错误。
    """
    cols_tuple = tuple(columns)
    cur.execute(
        """
        SELECT 1
        FROM INFORMATION_SCHEMA.STATISTICS
        WHERE TABLE_SCHEMA=%s AND TABLE_NAME=%s AND INDEX_NAME=%s
        LIMIT 1
        """,
        (db_name, table, index_name),
    )
    if cur.fetchone():
        return
    cols_sql = ", ".join([f"`{c}`" for c in columns])
    sql = f"CREATE INDEX `{index_name}` ON `{table}` ({cols_sql})"
    cur.execute(sql)


def row_count(cur) -> int:
    """返回上一条 DML 语句真正影响的行数（受影响 Changed，而非匹配 Matched）。"""
    cur.execute("SELECT ROW_COUNT() AS c")
    r = cur.fetchone()
    return int(r["c"]) if r and "c" in r else 0


def migrate(
    host: str,
    port: int,
    user: str,
    password: str,
    db: str,
    charset: str,
    dry_run: bool,
    ensure_indexes: bool,
    default_name: str,
    lock_wait_timeout: Optional[int],
):
    conn = None
    try:
        conn = pymysql.connect(
            host=host,
            port=port,
            user=user,
            password=password,
            db=db,
            charset=charset,
            cursorclass=DictCursor,
            autocommit=False,
        )
        cur = conn.cursor()
        print("[OK] 已连接 MySQL", {"host": host, "db": db})

        if lock_wait_timeout is not None:
            cur.execute("SET SESSION innodb_lock_wait_timeout=%s", (int(lock_wait_timeout),))

        if ensure_indexes:
            print("[... ] 检查/创建必要索引以提升迁移性能…")
            # 读取当前库名（避免外部传参大小写/反引号问题）
            cur.execute("SELECT DATABASE() AS db")
            dbname = cur.fetchone()["db"]
            # friend_group
            ensure_index(cur, dbname, "friend_group", "idx_fg_uid_default", ["uid", "is_default"])
            ensure_index(cur, dbname, "friend_group", "idx_fg_id_deleted", ["id", "is_deleted"])
            # friend_group_member
            ensure_index(cur, dbname, "friend_group_member", "idx_fgm_uid_friend", ["uid", "friend_uid"])
            ensure_index(cur, dbname, "friend_group_member", "idx_fgm_group_deleted", ["group_id", "is_deleted"])
            # friend
            ensure_index(cur, dbname, "friend", "idx_friend_uid_to", ["uid", "to_uid"])
            ensure_index(cur, dbname, "friend", "idx_friend_deleted", ["is_deleted"])
            ensure_index(cur, dbname, "friend", "idx_friend_group_id", ["friend_group_id"])
            print("[OK] 索引检查完成")

                print("[... ] 开始事务…")
                conn.begin()

                # Step 1: 为每个用户生成/修复默认分组（使用 UPSERT，避免因软删除造成唯一键冲突）
                print("[... ] Step1: 确保默认分组存在（UPSERT）…")
                sql_step1 = (
                        """
                        INSERT INTO friend_group (uid, name, is_default)
                        SELECT u.uid, %s, 1
                        FROM `user` AS u
                        ON DUPLICATE KEY UPDATE
                            is_default = VALUES(is_default),
                            is_deleted = 0
                        """
                )
                cur.execute(sql_step1, (default_name,))
                inserted_defaults = row_count(cur)
                print(f"[OK] Step1: 默认分组插入/修复 受影响行数：{inserted_defaults}")

        # 准备默认分组临时表
        print("[... ] 构建临时表: tmp_defaults …")
        cur.execute("DROP TEMPORARY TABLE IF EXISTS tmp_defaults")
        cur.execute(
            """
            CREATE TEMPORARY TABLE tmp_defaults
            (PRIMARY KEY(uid))
            AS
            SELECT fg.uid, fg.id AS default_group_id
            FROM friend_group fg
            WHERE fg.is_default = 1 AND COALESCE(fg.is_deleted,0) = 0
            """
        )

                # 准备按 (uid, friend_uid) 聚合后的成员临时表
                print("[... ] 构建临时表: tmp_member …")
                cur.execute("DROP TEMPORARY TABLE IF EXISTS tmp_member")
                cur.execute(
                        """
                        CREATE TEMPORARY TABLE tmp_member
                        (PRIMARY KEY(uid, friend_uid))
                        AS
                        SELECT fgm.uid, fgm.friend_uid, MIN(fgm.group_id) AS target_group_id
                        FROM friend_group_member fgm
                        JOIN friend_group fg ON fg.id = fgm.group_id
                        WHERE COALESCE(fgm.is_deleted,0) = 0
                            AND COALESCE(fg.is_deleted,0) = 0
                        GROUP BY fgm.uid, fgm.friend_uid
                        """
                )

        # 统计即将更新的行数（仅改变值的行）
        print("[... ] 统计需要变更的 friend 行…")
        cur.execute(
            """
            SELECT COUNT(*) AS cnt
            FROM friend f
            JOIN tmp_defaults d ON d.uid = f.uid
            LEFT JOIN tmp_member m ON m.uid = f.uid AND m.friend_uid = f.to_uid
            WHERE COALESCE(f.is_deleted,0) = 0
              AND COALESCE(f.friend_group_id,0) <> COALESCE(m.target_group_id, d.default_group_id)
            """
        )
        to_update = int(cur.fetchone()["cnt"]) if cur.rowcount else 0
        print(f"[OK] 预计更新 friend 记录数：{to_update}")

        if dry_run:
            print("[DRY-RUN] 仅统计，不执行更新与提交。回滚…")
            conn.rollback()
            return

        # Step 3: 批量更新 friend.friend_group_id
        print("[... ] Step3: 更新 friend.friend_group_id …")
        cur.execute(
            """
            UPDATE friend f
            JOIN tmp_defaults d ON d.uid = f.uid
            LEFT JOIN tmp_member m ON m.uid = f.uid AND m.friend_uid = f.to_uid
            SET f.friend_group_id = COALESCE(m.target_group_id, d.default_group_id)
            WHERE COALESCE(f.is_deleted,0) = 0
              AND COALESCE(f.friend_group_id,0) <> COALESCE(m.target_group_id, d.default_group_id)
            """
        )
        updated_rows = row_count(cur)
        print(f"[OK] Step3: 实际更新 friend 记录数：{updated_rows}")

        conn.commit()
        print("[SUCCESS] 事务已提交")
    except Exception as e:
        print(f"[ERROR] 执行失败: {e}")
        if conn:
            conn.rollback()
            print("[INFO] 事务已回滚")
    finally:
        if conn:
            conn.close()
            print("[INFO] 数据库连接已关闭")


def parse_args():
    p = argparse.ArgumentParser(description="Migrate friend groups (MySQL)")
    p.add_argument("--host", default="127.0.0.1")
    p.add_argument("--port", type=int, default=3306)
    p.add_argument("--user", default="root")
    p.add_argument("--password", default="123456")
    p.add_argument("--db", required=True)
    p.add_argument("--charset", default="utf8mb4")
    p.add_argument("--dry-run", action="store_true", help="仅统计变更数量，不提交")
    p.add_argument("--ensure-indexes", action="store_true", help="迁移前检查并创建必要索引")
    p.add_argument("--default-name", default="我的好友", help="默认分组名称")
    p.add_argument("--lock-wait-timeout", type=int, default=None, help="事务锁等待秒数")
    return p.parse_args()


if __name__ == "__main__":
    args = parse_args()
    migrate(
        host=args.host,
        port=args.port,
        user=args.user,
        password=args.password,
        db=args.db,
        charset=args.charset,
        dry_run=args.dry_run,
        ensure_indexes=args.ensure_indexes,
        default_name=args.default_name,
        lock_wait_timeout=args.lock_wait_timeout,
    )
