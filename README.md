# workexperience

## 迁移指南

有关好友分组迁移（MySQL + Redis）的详细说明与运行方法，请参见 `MIGRATION_GUIDE.md`。

## 分布式事务

关于分布式数据库事务处理（2PC/XA、Saga/TCC、Outbox/Inbox、幂等与重试、MySQL+Redis 协同等）的工程化实践，请参见 `DISTRIBUTED_TRANSACTIONS.md`。



```shell

  python3 migration_with_version.py --db im-vqlhbm --seed-defaults-from friend

```

## Bun 事务管理器（TxManager）快速上手

仓库提供了一个基于 Bun 的轻量事务管理器，支持：

- 嵌套事务（Savepoint）与 RequiresNew 语义
- AfterCommit 钩子（仅最外层提交后执行）
- 每事务设置 PG 的 statement_timeout / lock_timeout（可选）
- 常见可重试错误的封装（另有 `WithSerializableRetry` 辅助）

示例（更多可见 `practice/golang/cmd/dbx-demo`）：

```go
tm := dbx.NewTxManager(db)

// 基本用法：在事务中执行
err := tm.Run(ctx, dbx.Options{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
  // 注册提交后回调（仅最外层事务 commit 后触发）
  dbx.AfterCommit(ctx, func(){ log.Println("outer committed") })

  // 业务写入 ...
  if _, err := tx.NewRaw("UPDATE foo SET bar=bar+1 WHERE id=?", 1).Exec(ctx); err != nil { return err }

  // 嵌套新事务（Savepoint），失败时仅回滚内层
  return tm.Run(ctx, dbx.Options{RequiresNew: true, SavepointNameHint: "inner"}, func(ctx context.Context, tx bun.Tx) error {
    // 内层写入 ...
    return nil
  })
})
if err != nil { log.Fatal(err) }

// 可重试的可串行化事务（PostgreSQL / Cockroach）
err = dbx.WithSerializableRetry(ctx, db, func(ctx context.Context, tx bun.Tx) error {
  // 严格隔离级别下的读写 ...
  return nil
}, &dbx.RetryOption{MaxAttempts: 3})
```

运行 Demo：

```bash
go run ./practice/golang/cmd/dbx-demo
```
