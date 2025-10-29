package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"
	mydb "github.com/stoneMan1982/workexperience/practice/golang/db"
	"github.com/stoneMan1982/workexperience/practice/golang/pkg/config"
	"github.com/stoneMan1982/workexperience/practice/golang/pkg/logx"
	"github.com/uptrace/bun"
)

const (
	FriendSeqKey      = "friend"
	FriendGroupSeqKey = "friendGroup"
)

func main() {
	var (
		cfgPath           string
		defaultName       string
		dryRun            bool
		lockWaitSec       int
		redisAddr         string
		redisPass         string
		redisDB           int
		friendSeqKey      string
		friendGroupSeqKey string
	)

	flag.StringVar(&cfgPath, "config", "../../config.yaml", "path to YAML config file")
	flag.StringVar(&defaultName, "default-name", "我的好友", "default friend group name")
	flag.BoolVar(&dryRun, "dry-run", false, "only show affected rows, do not commit")
	flag.IntVar(&lockWaitSec, "lock-wait-timeout", 0, "innodb_lock_wait_timeout in seconds (0 = unchanged)")
	flag.StringVar(&redisAddr, "redis-addr", "127.0.0.1:6379", "redis address host:port")
	flag.StringVar(&redisPass, "redis-password", "", "redis password")
	flag.IntVar(&redisDB, "redis-db", 0, "redis db index")
	flag.StringVar(&friendSeqKey, "friend-seq-key", "FriendSeqKey", "redis key for friend version sequence")
	flag.StringVar(&friendGroupSeqKey, "friend-group-seq-key", "FriendGroupSeqKey", "redis key for friend_group version sequence")
	flag.Parse()

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		slog.Error("load config failed", "path", cfgPath, "err", err)
		os.Exit(1)
	}
	logx.Setup(cfg.Logging.Level, cfg.Logging.Format, cfg.Logging.AddSource)

	if !strings.EqualFold(cfg.Database.Dialect, "mysql") {
		slog.Error("dialect must be mysql for this migration", "got", cfg.Database.Dialect)
		os.Exit(1)
	}

	db, err := mydb.OpenFromConfig(&cfg.Database)
	if err != nil {
		slog.Error("open db failed", "err", err)
		os.Exit(1)
	}
	defer mydb.Close(db)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Init Redis
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr, Password: redisPass})
	if err := rdb.Ping(ctx).Err(); err != nil {
		slog.Error("redis ping failed", "addr", redisAddr, "err", err)
		os.Exit(1)
	}

	if err := run(ctx, db, rdb, defaultName, dryRun, lockWaitSec, friendSeqKey, friendGroupSeqKey); err != nil {
		slog.Error("migration failed", "err", err)
		os.Exit(1)
	}
	slog.Info("migration finished")
}

func run(ctx context.Context, db *bun.DB, rdb *redis.Client, defaultName string, dryRun bool, lockWaitSec int, friendSeqKey, friendGroupSeqKey string) error {
	// We want a rollback when dry-run; use a sentinel error to prevent commit.
	var errDryRun = errors.New("dry-run rollback")
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if lockWaitSec > 0 {
			if _, err := tx.ExecContext(ctx, "SET SESSION innodb_lock_wait_timeout = ?", lockWaitSec); err != nil {
				return err
			}
		}

		// Step 1: friend_group per-row unique version
		// 1.1 snapshot existing defaultName rows
		slog.Info("step1: snapshot existing default groups")
		if _, err := tx.ExecContext(ctx, `DROP TEMPORARY TABLE IF EXISTS tmp_fg_existing`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			CREATE TEMPORARY TABLE tmp_fg_existing (PRIMARY KEY(id))
			AS
			SELECT fg.id
			FROM friend_group fg
			WHERE fg.name = ?
		`, defaultName); err != nil {
			return err
		}
		var existCnt int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tmp_fg_existing`).Scan(&existCnt); err != nil {
			return err
		}

		// 1.2 count missing default rows
		var missCnt int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM `+"`user`"+` u
			LEFT JOIN friend_group fg ON fg.uid = u.uid AND fg.name = ?
			WHERE fg.id IS NULL
		`, defaultName).Scan(&missCnt); err != nil {
			return err
		}
		slog.Info("step1 counts", "existing", existCnt, "missing", missCnt)

		// 1.3 insert missing with per-row unique versions
		if missCnt > 0 {
			var startMissing int64
			if !dryRun {
				end, err := rdb.IncrBy(ctx, friendGroupSeqKey, int64(missCnt)).Result()
				if err != nil {
					return err
				}
				startMissing = end - int64(missCnt) + 1
			} else {
				startMissing = 0 // not used in dry-run
			}
			slog.Info("step1 insert missing", "count", missCnt)
			if !dryRun {
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO friend_group (uid, name, is_default, is_deleted, version)
					SELECT t.uid, ?, 1, 0, (? + t.rn - 1)
					FROM (
						SELECT u.uid, ROW_NUMBER() OVER (ORDER BY u.uid) AS rn
						FROM `+"`user`"+` u
						LEFT JOIN friend_group fg ON fg.uid = u.uid AND fg.name = ?
						WHERE fg.id IS NULL
					) AS t
				`, defaultName, startMissing, defaultName); err != nil {
					return err
				}
			}
		}

		// 1.4 update existing (pre-snapshot) with per-row unique versions
		if existCnt > 0 {
			var startExist int64
			if !dryRun {
				end, err := rdb.IncrBy(ctx, friendGroupSeqKey, int64(existCnt)).Result()
				if err != nil {
					return err
				}
				startExist = end - int64(existCnt) + 1
			}
			slog.Info("step1 update existing", "count", existCnt)
			if !dryRun {
				if _, err := tx.ExecContext(ctx, `
					UPDATE friend_group fg
					JOIN (
						SELECT e.id, ROW_NUMBER() OVER (ORDER BY e.id) AS rn
						FROM tmp_fg_existing e
					) AS t ON t.id = fg.id
					SET fg.is_default = 1,
						fg.is_deleted = 0,
						fg.version = (? + t.rn - 1)
				`, startExist); err != nil {
					return err
				}
			}
		}

		// tmp_defaults
		slog.Info("build tmp_defaults")
		if _, err := tx.ExecContext(ctx, `DROP TEMPORARY TABLE IF EXISTS tmp_defaults`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			CREATE TEMPORARY TABLE tmp_defaults
			(PRIMARY KEY(uid))
			AS
			SELECT fg.uid, MIN(fg.id) AS default_group_id
			FROM friend_group fg
			WHERE fg.is_default = 1 AND COALESCE(fg.is_deleted,0) = 0
			GROUP BY fg.uid
		`); err != nil {
			return err
		}

		// tmp_member
		slog.Info("build tmp_member")
		if _, err := tx.ExecContext(ctx, `DROP TEMPORARY TABLE IF EXISTS tmp_member`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
            CREATE TEMPORARY TABLE tmp_member
            (PRIMARY KEY(uid, friend_uid))
            AS
            SELECT fgm.uid, fgm.friend_uid, MIN(fgm.group_id) AS target_group_id
            FROM friend_group_member fgm
            JOIN friend_group fg ON fg.id = fgm.group_id
            WHERE COALESCE(fgm.is_deleted,0) = 0
              AND COALESCE(fg.is_deleted,0) = 0
            GROUP BY fgm.uid, fgm.friend_uid
        `); err != nil {
			return err
		}

		// count updates (friends needing group id change)
		var toUpdate int
		if err := tx.QueryRowContext(ctx, `
            SELECT COUNT(*) AS cnt
            FROM friend f
            JOIN tmp_defaults d ON d.uid = f.uid
            LEFT JOIN tmp_member m ON m.uid = f.uid AND m.friend_uid = f.to_uid
            WHERE COALESCE(f.is_deleted,0) = 0
              AND COALESCE(f.friend_group_id,0) <> COALESCE(m.target_group_id, d.default_group_id)
        `).Scan(&toUpdate); err != nil {
			return err
		}
		slog.Info("rows needing update", "count", toUpdate)

		if dryRun {
			slog.Info("dry-run requested; rollback")
			return errDryRun
		}

		// reserve per-row versions for friend updates
		var startFriend int64
		if toUpdate > 0 {
			end, err := rdb.IncrBy(ctx, friendSeqKey, int64(toUpdate)).Result()
			if err != nil {
				return err
			}
			startFriend = end - int64(toUpdate) + 1

			// update friends with per-row unique versions
			slog.Info("step3: update friend.friend_group_id per-row versions", "count", toUpdate)
			if _, err := tx.ExecContext(ctx, `
				UPDATE friend f
				JOIN (
					SELECT f.id,
						   COALESCE(m.target_group_id, d.default_group_id) AS new_gid,
						   ROW_NUMBER() OVER (ORDER BY f.id) AS rn
					FROM friend f
					JOIN tmp_defaults d ON d.uid = f.uid
					LEFT JOIN tmp_member m ON m.uid = f.uid AND m.friend_uid = f.to_uid
					WHERE COALESCE(f.is_deleted,0) = 0
					  AND COALESCE(f.friend_group_id,0) <> COALESCE(m.target_group_id, d.default_group_id)
				) t ON t.id = f.id
				SET f.friend_group_id = t.new_gid,
					f.version = (? + t.rn - 1)
			`, startFriend); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if err.Error() == "dry-run rollback" {
			slog.Info("dry-run completed (transaction rolled back)")
			return nil
		}
		return err
	}
	return nil
}
