"""
MySQL + Redis migration script (per-row unique versioning)

目标：
- 为每个用户创建/修复默认分组（名称可配置，默认“我的好友”），逐行唯一 version（来自 Redis 的自增序列）。
- 聚合分组成员映射，批量更新 friend.friend_group_id；对需要更新的行逐行分配唯一 version。
- 支持 dry-run（不提交、不占用 Redis 自增）。
- 支持设置 innodb_lock_wait_timeout。

前提：MySQL 8.0+（使用窗口函数 ROW_NUMBER()）。

示例：
  python3 migration_redis.py --host 127.0.0.1 --user root --password 123456 --db tsdd \
      --default-name "我的好友" --redis-addr 127.0.0.1:6379 --friend-seq-key FriendSeqKey \
      --friend-group-seq-key FriendGroupSeqKey --lock-wait-timeout 60 --dry-run
"""

import argparse
from typing import Optional

import pymysql
from pymysql.cursors import DictCursor
import redis


def parse_args():
    p = argparse.ArgumentParser(description="Migrate friend groups with per-row unique versions (MySQL + Redis)")
    p.add_argument("--host", default="im-db.cluster-cn0iyusu6r8z.ap-east-1.rds.amazonaws.com")
    p.add_argument("--port", type=int, default=3306)
    p.add_argument("--user", default="root")
    p.add_argument("--password", default="960110C827DB47#1#EAAB2CEC765D5ED6D8")
    p.add_argument("--db", required=True)
    p.add_argument("--charset", default="utf8mb4")

    p.add_argument("--default-name", default="我的好友")
    p.add_argument("--dry-run", action="store_true")
    p.add_argument("--lock-wait-timeout", type=int, default=None)

    p.add_argument("--redis-addr", default="vqlhbm.node1.im.internal:6379")
    p.add_argument("--redis-password", default="H8HBW3opBmZ8jNhbKjV7X8")
    p.add_argument("--redis-db", type=int, default=0)
    p.add_argument("--friend-seq-key", default="seq:friend")
    p.add_argument("--friend-group-seq-key", default="seq:friendGroup")
    # Repair/targeted options
    p.add_argument("--skip-friend-group", action="store_true", help="Skip creating/updating default friend_group (only build temp tables)")
    p.add_argument("--only-deleted-friends", action="store_true", help="Only update friends with is_deleted=1")
    p.add_argument("--seed-defaults-from", choices=["friend", "user", "both"], default="friend", help="Seed default groups from distinct friend.uid, user table, or both (default: friend)")
    return p.parse_args()


def main():
    args = parse_args()

    # Connect Redis
    host_port = args.redis_addr.split(":", 1)
    r_host = host_port[0]
    r_port = int(host_port[1]) if len(host_port) == 2 else 6379
    rdb = redis.Redis(host=r_host, port=r_port, password=args.redis_password or None, db=args.redis_db, decode_responses=True)
    try:
        rdb.ping()
    except Exception as e:
        print(f"[ERROR] Redis ping failed: {e}")
        return 1

    # Connect MySQL
    conn = None
    try:
        conn = pymysql.connect(
            host=args.host,
            port=args.port,
            user=args.user,
            password=args.password,
            db=args.db,
            charset=args.charset,
            autocommit=False,
            cursorclass=DictCursor,
        )
        cur = conn.cursor()
        print("[OK] MySQL connected", {"host": args.host, "db": args.db})

        if args.lock_wait_timeout is not None:
            cur.execute("SET SESSION innodb_lock_wait_timeout=%s", (int(args.lock_wait_timeout),))

        print("[... ] BEGIN")
        conn.begin()

        # Step 1: friend_group per-row unique version (can be skipped for repair-only runs)
        default_name = args.default_name
        if not args.skip_friend_group:
            # 1.0 seed candidate uids for defaults
            cur.execute("DROP TEMPORARY TABLE IF EXISTS tmp_seed_uids")
            seed_mode = args.seed_defaults_from
            if seed_mode == "friend":
                print("[... ] Seed defaults from distinct friend.uid")
                cur.execute(
                    """
                    CREATE TEMPORARY TABLE tmp_seed_uids (uid VARCHAR(128) PRIMARY KEY)
                    AS
                    SELECT DISTINCT f.uid AS uid
                    FROM friend f
                    """
                )
            elif seed_mode == "user":
                print("[... ] Seed defaults from user.uid (requires column 'uid')")
                cur.execute(
                    """
                    CREATE TEMPORARY TABLE tmp_seed_uids (uid VARCHAR(128) PRIMARY KEY)
                    AS
                    SELECT u.uid AS uid FROM `user` u
                    """
                )
            else:  # both
                print("[... ] Seed defaults from friend.uid UNION user.uid (requires column 'uid')")
                cur.execute(
                    """
                    CREATE TEMPORARY TABLE tmp_seed_uids (uid VARCHAR(128) PRIMARY KEY)
                    AS
                    SELECT DISTINCT uid FROM (
                      SELECT f.uid AS uid FROM friend f
                      UNION
                      SELECT u.uid AS uid FROM `user` u
                    ) t
                    """
                )

            # 1.1 snapshot existing default groups (active defaults)
            print("[... ] Step1: snapshot existing default groups")
            cur.execute("DROP TEMPORARY TABLE IF EXISTS tmp_fg_existing")
            cur.execute(
                """
                CREATE TEMPORARY TABLE tmp_fg_existing (PRIMARY KEY(id))
                AS
                SELECT fg.id
                FROM friend_group fg
                WHERE COALESCE(fg.is_deleted,0) = 0
                  AND fg.is_default = 1
                """
            )
            cur.execute("SELECT COUNT(*) AS c FROM tmp_fg_existing")
            exist_cnt = int(cur.fetchone()["c"])

            # 1.2 count missing defaults
            cur.execute(
                """
                SELECT COUNT(*) AS c
                FROM tmp_seed_uids su
                LEFT JOIN friend_group fg 
                  ON fg.uid = su.uid 
                 AND fg.is_default = 1 
                 AND COALESCE(fg.is_deleted,0) = 0
                WHERE fg.id IS NULL
                """
            )
            miss_cnt = int(cur.fetchone()["c"]) if cur.rowcount else 0
            print(f"[OK] Step1 counts: existing={exist_cnt}, missing={miss_cnt}")

            # 1.3 insert missing with per-row unique versions
            if miss_cnt > 0:
                if args.dry_run:
                    print(f"[DRY-RUN] reserve versions for missing: {miss_cnt}")
                    start_missing = 0
                else:
                    end = rdb.incrby(args.friend_group_seq_key, miss_cnt)
                    start_missing = end - miss_cnt + 1
                print(f"[... ] insert missing defaults: {miss_cnt}")
                if not args.dry_run:
                    cur.execute(
                        """
                        INSERT INTO friend_group (uid, name, is_default, is_deleted, version)
                        SELECT t.uid, %s, 1, 0, (%s + t.rn - 1)
                        FROM (
                            SELECT su.uid, ROW_NUMBER() OVER (ORDER BY su.uid) AS rn
                            FROM tmp_seed_uids su
                            LEFT JOIN friend_group fg 
                              ON fg.uid = su.uid 
                             AND fg.is_default = 1 
                             AND COALESCE(fg.is_deleted,0) = 0
                            WHERE fg.id IS NULL
                        ) AS t
                        """,
                        (default_name, start_missing),
                    )

            # 1.4 update existing with per-row unique versions
            if exist_cnt > 0:
                if args.dry_run:
                    print(f"[DRY-RUN] reserve versions for existing: {exist_cnt}")
                    start_exist = 0
                else:
                    end2 = rdb.incrby(args.friend_group_seq_key, exist_cnt)
                    start_exist = end2 - exist_cnt + 1
                print(f"[... ] update existing defaults: {exist_cnt}")
                if not args.dry_run:
                    cur.execute(
                        """
                        UPDATE friend_group fg
                        JOIN (
                            SELECT e.id, ROW_NUMBER() OVER (ORDER BY e.id) AS rn
                            FROM tmp_fg_existing e
                        ) AS t ON t.id = fg.id
                        SET fg.is_default = 1,
                            fg.is_deleted = 0,
                            fg.version = (%s + t.rn - 1)
                        """,
                        (start_exist,),
                    )
        else:
            print("[SKIP] Step1 friend_group changes skipped (using existing defaults)")

        # tmp_defaults
        print("[... ] build tmp_defaults")
        cur.execute("DROP TEMPORARY TABLE IF EXISTS tmp_defaults")
        cur.execute(
            """
            CREATE TEMPORARY TABLE tmp_defaults (PRIMARY KEY(uid)) AS
            SELECT fg.uid, MIN(fg.id) AS default_group_id
            FROM friend_group fg
            WHERE fg.is_default = 1 AND COALESCE(fg.is_deleted,0) = 0
            GROUP BY fg.uid
            """
        )

        # tmp_member
        print("[... ] build tmp_member")
        cur.execute("DROP TEMPORARY TABLE IF EXISTS tmp_member")
        cur.execute(
            """
            CREATE TEMPORARY TABLE tmp_member (PRIMARY KEY(uid, friend_uid)) AS
            SELECT fgm.uid, fgm.friend_uid, MIN(fgm.group_id) AS target_group_id
            FROM friend_group_member fgm
            JOIN friend_group fg ON fg.id = fgm.group_id
            WHERE COALESCE(fgm.is_deleted,0) = 0
              AND COALESCE(fg.is_deleted,0) = 0
            GROUP BY fgm.uid, fgm.friend_uid
            """
                )

        # count friend rows needing update (including is_deleted=1 optionally)
        extra_friend_cond = " AND COALESCE(f.is_deleted,0) = 1" if args.only_deleted_friends else ""
        count_sql = (
                """
                SELECT COUNT(*) AS cnt
                FROM friend f
                JOIN tmp_defaults d ON d.uid = f.uid
                LEFT JOIN tmp_member m ON m.uid = f.uid AND m.friend_uid = f.to_uid
                WHERE COALESCE(f.friend_group_id,0) <> COALESCE(m.target_group_id, d.default_group_id)
                    AND f.to_uid NOT IN ('u_10000','fileHelper')
                """
                + extra_friend_cond
        )
        cur.execute(count_sql)
        to_update = int(cur.fetchone()["cnt"]) if cur.rowcount else 0
        print(f"[OK] friends needing update: {to_update}")

        if args.dry_run:
            print("[DRY-RUN] rollback")
            conn.rollback()
            return 0

        # reserve per-row versions for friend updates
        if to_update > 0:
            end3 = rdb.incrby(args.friend_seq_key, to_update)
            start_friend = end3 - to_update + 1
            print(f"[... ] update friends per-row versions: {to_update}")
            update_inner_where = (
                """
                    WHERE COALESCE(f.friend_group_id,0) <> COALESCE(m.target_group_id, d.default_group_id)
                      AND f.to_uid NOT IN ('u_10000','fileHelper')
                """
                + extra_friend_cond
            )
            update_sql = (
                """
                UPDATE friend f
                JOIN (
                    SELECT f.id,
                           COALESCE(m.target_group_id, d.default_group_id) AS new_gid,
                           ROW_NUMBER() OVER (ORDER BY f.id) AS rn
                    FROM friend f
                    JOIN tmp_defaults d ON d.uid = f.uid
                    LEFT JOIN tmp_member m ON m.uid = f.uid AND m.friend_uid = f.to_uid
                """
                + update_inner_where
                + """
                ) t ON t.id = f.id
                SET f.friend_group_id = t.new_gid,
                    f.version = (%s + t.rn - 1)
                """
            )
            cur.execute(update_sql, (start_friend,))

        conn.commit()
        print("[SUCCESS] committed")
        return 0

    except Exception as e:
        print(f"[ERROR] failed: {e}")
        if conn:
            conn.rollback()
            print("[INFO] rolled back")
        return 1
    finally:
        if conn:
            conn.close()
            print("[INFO] MySQL closed")


if __name__ == "__main__":
    raise SystemExit(main())


