# workexperience

## 迁移指南

有关好友分组迁移（MySQL + Redis）的详细说明与运行方法，请参见 `MIGRATION_GUIDE.md`。

## 分布式事务

关于分布式数据库事务处理（2PC/XA、Saga/TCC、Outbox/Inbox、幂等与重试、MySQL+Redis 协同等）的工程化实践，请参见 `DISTRIBUTED_TRANSACTIONS.md`。



```shell

  python3 migration_with_version.py --db im-vqlhbm --seed-defaults-from friend

```
