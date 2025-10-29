# 分布式数据库事务处理指南

本文为工程实践导向的分布式事务设计与落地指南，面向 MySQL + Redis 常见组合，并给出在 Go 体系下的实现建议。文末提供最佳实践清单，后续你可以让我根据其中任意方案生成代码骨架。

## 什么时候需要分布式事务？

当一次业务操作需要跨越多个边界（多库/多表/多服务/数据库与消息系统/数据库与缓存）并要求一致性时，就涉及分布式事务。常见场景：

- 订单下单：写订单库 + 扣减库存 + 发消息通知
- 钱包扣款：写账本 + 第三方支付 + 回写状态
- 资料变更：主库写入 + 同步搜索引擎/缓存

关键问题：一致性模型选择（强一致 vs 最终一致）与可用性、性能的均衡。

## 基础概念速览

- CAP：在分区容忍性（P）前提下，强一致（C）与可用性（A）难两全；互联网系统多选 AP + 最终一致。
- ACID vs BASE：
  - ACID（强一致）：串行化、可回滚、复杂度高、性能牺牲大。
  - BASE（基本可用、软状态、最终一致）：通过重试、补偿来达成一致性。
- 幂等性：同一操作多次执行，结果不变。是抵御重试/重放的核心能力。

## 方案选型一览

- 2PC/XA（两阶段提交）：跨资源强一致，协议层由协调者驱动。
  - 优点：语义直观，强一致。
  - 缺点：性能/可用性差，长事务与锁持有久，协调者单点、脑裂与阻塞风险；在 MySQL/主从/云原生环境落地成本高。
- Saga（长事务拆分为有补偿动作的一系列本地事务）
  - Orchestration（编排）：中心流程引擎/服务驱动每步调用与补偿。
  - Choreography（舞蹈）：各服务基于事件/消息自组织推进与回滚。
  - 优点：去中心化长锁，提升可用与吞吐；
  - 难点：补偿逻辑复杂，异常分支多，要求强幂等与可观测性。
- TCC（Try-Confirm-Cancel）：业务显式资源预留/确认/取消。
  - 优点：语义清晰、时延低；
  - 难点：接口实现成本高，需要资源可冻结与可撤销。
- Outbox/Inbox + CDC（事务外盒 + 去重收件箱）
  - 本地事务一次写业务数据与 outbox 消息，随后由 CDC 或轮询将消息可靠投递到 MQ/缓存/搜索。
  - 消费端使用 Inbox（去重表）或幂等唯一键，保证 at-least-once 下的幂等。
  - 优点：实现简单，跨系统一致性的黄金路径；
  - 缺点：最终一致，存在短暂延迟。

> 一般性建议：优先 Outbox/Inbox（最终一致）或 Saga；除非强一致不可妥协，慎用 2PC/XA。

## 模式详解与落地要点

### 1) 2PC / XA（不首选）

- 适用：强一致硬需求、参与者支持 XA（如 MySQL XA、消息系统 XA）。
- MySQL 注意：XA 对复制、故障、超时处理敏感；长事务占锁，吞吐与可用性下降。
- 关键风险：
  - 协调者故障/网络分区导致参与者进入不一致状态；
  - 悬挂事务清理困难；
  - 云原生弹性扩缩与 XA 不友好。

### 2) Saga（编排/舞蹈）

- 思路：将业务拆分为 N 个本地事务 S1..Sn，每步成功后发布事件；失败则按逆序执行补偿 Cn..C1。
- 编排实现：中心引擎记录状态机（可存 DB），按步骤调用/补偿，可靠持久化每步状态。
- 舞蹈实现：各服务订阅上一步事件，执行本地事务并发布下一步事件；失败发补偿事件。
- 关键点：
  - 补偿设计：每步都要能“撤销/对冲”（无损/幂等）。
  - 幂等：所有正向、补偿动作可安全重试；利用唯一键/版本号/去重表。
  - 顺序与并发：可用业务键做分区顺序处理；跨键可并行。
  - 可观测性：trace-id 贯穿，审计与告警完善。

### 3) TCC（Try-Confirm-Cancel）

- 思路：Try 预留资源（冻结）、Confirm 确认、Cancel 取消。
- 适用：库存/余额/额度类资源，满足可冻结与撤销模型。
- 关键点：
  - 过期回收：Try 成功后未 Confirm 的冻结需要自动回收。
  - 幂等：三步都要具备幂等键。
  - 竞争：冻结额度的并发控制（行锁/乐观锁）。

### 4) Outbox/Inbox + CDC（强烈推荐）

- Producer 侧：本地事务中写业务数据 + outbox 表（message_id、event_type、payload、status=NEW、occurred_at）。
- 投递：
  - CDC（如 Debezium）监听 outbox 表变更并投递到 MQ/Redis Streams/
    其他下游；或
  - 轮询任务扫描 NEW -> PUBLISHED（带重试与退避）。
- Consumer 侧：Inbox（去重表）或在目标表上用幂等唯一键（幂等键=message_id 或业务 key），保证可重放。
- 去重/幂等技巧：
  - 唯一索引（message_id, target_id）
  - UPSERT（ON DUPLICATE KEY UPDATE / ON CONFLICT DO NOTHING/UPDATE）
  - 版本号 CAS（列 version + WHERE version = ?）
- 失败与重试：指数退避、最大重试、死信队列（DLQ）。

## 与 MySQL + Redis 的协同

- 不要试图对 MySQL 与 Redis 做跨资源强一致提交。推荐：
  1) 将需要写入 Redis 的变更作为 outbox 事件持久化在 MySQL；
  2) 由后台 Worker/CDC 异步写入 Redis；
  3) 以幂等键保证多次写入相同结果（例如 Redis HSET 覆盖、或使用 Lua + 版本比较）。
- Redis 原子性：
  - 简单写入：天然幂等（如 SET k v 覆盖）
  - 需要比较/累加：使用 Lua 脚本保证 read-modify-write 原子；
  - 版本序列：采用 INCR/INCRBY 生成，若需要精准一次语义，在 MySQL 记录“已分配区间”，用唯一键防重复。
- 分布式锁：
  - 谨慎使用 Redlock；更推荐使用数据库行级锁/乐观锁保证关键一致性；
  - 若使用 Redis 锁，仅用于短临界区、可容忍偶发失效的场景，并配合续期与保护时长。
- 缓存一致性：
  - 写后删（先写库后删缓存）或延迟双删；
  - 利用 outbox 驱动 Cache Invalidation 事件；
  - 对读路径做“缓存未命中回源 + 短期过期 + 隔离热点”治理。

## 事务边界与超时

- 业务规则：DB 本地事务只包裹纯数据库操作，不在事务中调用外部服务。
- 超时：设置锁等待/语句超时（如 MySQL innodb_lock_wait_timeout、max_execution_time），尽快释放锁。
- 失败处理：将“外部调用”放在事务外，通过消息驱动/补偿来达成最终一致。

## 错误模式与演练

- 典型错误：重复投递、乱序、网络抖动、幂等键冲突、部分失败、补偿失败。
- 设计演练：为每步标出失败点与恢复策略（重试/回滚/人工介入）。
- 数据追踪：全链路 trace-id、业务键可查询、审计日志可回放。

## Go 实现建议（结合本仓库）

- 事务与锁：
  - 使用 `practice/golang/pkg/dbx` 中的事务封装与超时控制（如 SetLocalLockTimeout/Serializable Retry）；
  - 热点行使用悲观锁（SELECT ... FOR UPDATE）或乐观锁（version 列）；
- Outbox 表建议 Schema：

  ```sql
  CREATE TABLE outbox (
    id            BIGINT PRIMARY KEY AUTO_INCREMENT,
    message_id    CHAR(26) NOT NULL UNIQUE, -- 雪花/ULID
    event_type    VARCHAR(64) NOT NULL,
    aggregate_id  VARCHAR(64) NOT NULL,
    payload       JSON NOT NULL,
    status        TINYINT NOT NULL DEFAULT 0, -- 0 NEW, 1 PUBLISHED, 2 FAILED
    retry_count   INT NOT NULL DEFAULT 0,
    next_try_at   DATETIME NULL,
    occurred_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    KEY idx_next_try (status, next_try_at)
  );
  ```

- 生产侧（同一事务内）：写业务表 + 写 outbox（message_id 唯一）；
- 投递器（轮询方案）：批量拉取 NEW/到期条目 -> 投递 MQ/Redis -> 标记 PUBLISHED；失败则退避重试；
- 消费侧：
  - Inbox 去重表（message_id 唯一），或在目标表使用 UPSERT 幂等键；
  - 所有处理逻辑须幂等；
- Saga 编排器：
  - 保存在单表的状态机（saga_id, step, state, payload, compensation_cursor...），
  - Worker 读取 PENDING，按 step 执行并记录结果；错误则调补偿路径。
- 幂等实现清单：
  - 唯一索引 + UPSERT；
  - 版本列 CAS（WHERE version = ?）；
  - 去重表（Inbox）保存已处理的 message_id；
  - 业务侧幂等键（例如订单号、请求号）。

## 最佳实践清单（可作代码验收项）

1) 明确一致性等级（强一致/最终一致）并评审 SLA 与背压策略。
2) 优先选择 Outbox/Inbox 或 Saga，避免 2PC/XA；
3) 所有跨系统写入经由消息驱动，生产侧使用本地事务写 outbox；
4) 全链路幂等：唯一键/去重表/版本 CAS；
5) 读写隔离：短事务、尽量不在事务内做外部调用；
6) 重试有上限 + 指数退避 + 死信队列；
7) 完善可观测性：trace-id、审计日志、补偿可回放；
8) 对 Redis 仅做派生/缓存/序列等用途，不与 MySQL 做跨资源强一致；
9) 对分布式锁保持审慎：优先数据库锁；
10) 灰度演练：注入失败场景，验证补偿与幂等。

---

需要我基于本指南为 Outbox/Inbox、Saga、TCC 任一模式生成 Go 代码骨架（含表结构、DAO、worker、重试与幂等）吗？告诉我你的偏好与目标栈（MySQL、MQ/Redis Streams、或 Kafka 等），我就开始动手。
