# Database  


## ACID

- Atomicity(原子性)  
  - 事务里的操作要么全部成功，要么全部失败回滚。  
- Consistency(一致性)  
  - 事务前后，数据满足定义的约束（类型、唯一性、外键、业务不变式）。一致性是应用+数据库共同维护的属性  
- Isolation(隔离性)  
  - 并发事务之间互不干扰，表现的就像串行执行一样（不同级别提供不同强度）。  
  常见隔离级别与并发现象：  
    - Read Uncommitted: 可能脏读（基本不用）  
    - Read Committed: 不会读到未提交的数据，可能不可重复读、幻读  
    - Repeatable Read:  
      - PG的Repeatable Read基本可以避免幻读（快照隔离+predicate locking），MYSQL的RR借助间隙锁避免幻读。  
    - Serializable: 最强隔离，等价于串行  
- Durability(持久性)  
  - 提交后的数据在崩溃后依然存在

## structure language  

- 内连和外连：  
  - inner join:
    - 只保留“能匹配上的两边的行”，不匹配的行被丢弃。
  - outer join(left join/right join/full join):
    - 在匹配的基础上，“保留一侧或两侧的全部行”，另一侧没有匹配时用NULL补齐。  
    - 常见三种：  
      - left outer join: 保留左表全部行（右表没匹配的列为NULL）
      - right outer join: 保留右表全部行  
      - full outer join: 保留两边全部行  
  - 例子：  
  表a与表b：  
    - a: (id) = {1,2}  
    - b: (id) = {2,3}
  连接条件：a.id=b.id  
    - inner join: {{2,2}}只保留交集id=2  
    - left join: {{1,NULL},{2,2}}保留a的全部，id=1用NULL补齐b  
    - right join: {{2,2},{NULL,3}}保留b的全部，id=3用NULL补齐a   
    - full join: {{1,NULL},{2,2},{NULL,3}}保留两边全部  


- WHERE和ON的区别：  
  - ON: 定义表与表之间如何匹配的条件（连接谓词），在执行连接期就参与决定哪行匹配与保留。  
  - WHERE: 对连接结果做“行过滤”对条件（选择谓词），连接完成后继续筛掉不符合条件的行

在inner join下，很多时候把条件放ON还是WHERE结果一样；但是在outer join下差异很大：错误地把条件放在WHERE会把外连接“内连化”，丢掉本应保留的一侧的行。


## 乐观锁和悲观锁  

核心区别  

- 悲观锁（Pessimistic lock）  
  - 思想：先上锁在操作，别人等我释放。并发高时用等待（或立即失败）换确定性  
  - 常用手段：SELECT ... FOR UPDATE/SHARE/NOWAIT/SKIP LOCKED; 长事务会持有行锁/间隙锁  

- 乐观锁（Optimistic Lock）  
  - 思想：不先锁，提交时校验有没有别人改过；若被改过则失败并重试  
  - 常用手段：版本号version、更新时间updated_at或哈希签名做CAS（Compare-And-Swap）。

使用建议：  

- 冲突少、热点低：优先使用乐观锁（高并发更友好）。  
- 冲突多、必须一次拿到“唯一正确资源”的短操作：悲观锁（或队列化、分片）  
- 关键一致性+高并发：常用“乐观为主+冲突重试；对热点场景局部降级为悲观锁/队列”



悲观锁实现  

PostgreSQL  

- 行级排他锁：阻塞等待  
  - SELECT ... FOR UPDATE

- 非阻塞立即失败  
  - SELECT ... FOR UPDATE NOWAIT  

- 跳过被锁行  
  - SELECT ... FOR UPDATE SKIP LOCKED

SQL示例：  

```sql
BEGIN;
  -- 锁住目标行（阻塞等待）
  SELECT * FROM inventory WHERE id = 42 FOR UPDATE;

  -- 修改
  UPDATE inventory SET stock = stock - 1 WHERE id = 42;
COMMIT;

-- 非阻塞：立刻失败
SELECT * FROM inventory WHERE id = 42 FOR UPDATE NOWAIT;

-- 队列拉任务：跳过已被其它 worker 锁住的行
SELECT * FROM jobs
WHERE status = 'pending'
ORDER BY id
LIMIT 10
FOR UPDATE SKIP LOCKED;
```
锁超时（避免长等待）：  

```sql
SET LOCAL lock_timeout = '3s';
```

**注意：**  

- 必须走索引命中行，避免全表扫描导致大量行/间隙被锁。  
- 控制事务边界“锁定后尽快提交”，别在持锁状态做外部调用。  

乐观锁实现（版本号CAS）

基本流程：  

- 读出记录，拿到当前version（或updated_at）  
- 提交时 update ...where id = ? and version=?(或时间戳)  
- 检查 RowsAffected：  
  - 1行->更新成功并把version+1  
  - 0行->表示冲突（有人改过），需要重读并重试或直接返回冲突错误

SQL示例：  

```sql
select id, stock, version from inventory where id = 42;

update inventory
set stock = stock - 1
    version = version + 1
where id = 42 and version = 7
```


```sql
select
    case
        when g.grade >= 8 then s.name
        else NULL
    end as name,
    g.grade,
    s.marks
from students s 
inner join grades  g on s.marks between g.min_mark and g.max_mark
order by
    g.grade DESC,
    case when g.grade >= 8 then s.name end asc,
    case when g.grade < 8 then s.marks end asc;
```

```sql

SELECT h.hacker_id, h.name
FROM hackers h
JOIN submissions s ON h.hacker_id = s.hacker_id
JOIN challenges c ON s.challenge_id = c.challenge_id
JOIN difficulty d ON c.difficulty_level = d.difficulty_level
WHERE s.score = d.score  
GROUP BY h.hacker_id, h.name
HAVING COUNT(DISTINCT c.challenge_id) > 1
ORDER BY 
    COUNT(DISTINCT c.challenge_id) DESC,
    h.hacker_id;
```


## MYSQL 8.0

MySQL 8.0 默认使用 utf8mb4_0900_ai_ci 字符集，其中：

- 0900 = Unicode 9.0 规范
- ai = 口音不敏感 (accent insensitive)
- ci = 大小写不敏感 (case insensitive)

因此对大小写的敏感的字段需要单独指定字符集，对已经存在的表结构，可以仿照下面例子修改：  

```sql

-- 只修改 uid 和 short_no 字段为大小写敏感，其他字段保持不变
ALTER TABLE `user` 
MODIFY COLUMN `uid` varchar(40) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '';

ALTER TABLE `user`
MODIFY COLUMN `short_no` varchar(40) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '';

-- 验证修改结果
SELECT COLUMN_NAME, COLLATION_NAME 
FROM information_schema.COLUMNS 
WHERE TABLE_NAME = 'user' 
AND COLUMN_NAME IN ('uid', 'short_no');

```

## Indices  

**Core Concepts:**  

- What an index does: accelerates lookups by maintaining an ordered or hashed structure pointing to table rows.  
- Trade-offs:  
  - Faster reads, slower writes(INSERT/UPDATE/DELETE must maintain indices).  
  - Extra storage and background maintenance(stats, vacuumiing/rebuilds in some engines).  
- Pick by query shape: equality vs range vs full-text vs spatial, and whether ORDER BY can be satisfied by index order. 


### By data structure/capability  

- B-Tree/B+Tree(default in most DBs)  
  - Best for equality(=) and range queries(>,<,BETWEEN), prefix LIKE('abc%').  
  - Supports ORDER BY via index order and can avoid sort.  
  - PostgreSQL: default index type. MySQL/InnoDB: all secondary indices are B+Tree.  

- Hash index  
  - Equality only: no ordering or range scans.  
  - PostgresSQL has Hash indidces(less common; B-Tree usually preferred).  
  - MySQL Memory engine uses hash per-table, but InnoDB secondary indices are B+Tree.  

- GiST/SP-GiST(PostgreSQL)  
  - Framework for custom access methods: geometric ranges, full text with GiST, KNN searches.

- GIN(PostgreSQL)  
  - Inverted indices for document-like data: arrays, jsonb, full-text tsvector.  
  - Great for "contains element/key" queries; slower to update than B-tree.

- Full-text index  
  - Token-based inverted indices. MySQL: FULLTEXT; PostgreSQL: GIN/GiST on tsvector.  

### By logical behavior  

- Primary key vs unique index  
  - PK implies NOT NULL + unique + clustered table organization.  
  - Unique index enforces uniqueness; may be multi-column.  

- Clustered vs non-clustered  
  - InnoDB tables are clustered on the PK: the table's row storage is ordered by PK.  
  - Secondary indices store the PK at the leaf, so lookup is "secondary->PK->row".  
  - Pick a compact, stable PK to reduce secondary indices size(e.g., BIGINT Snowflake instead of UUID strings).  

- Composite(multi column)indices  
  - Leftmost prefix rule  
  - Ordering matters: put "most selective" or most frequently filtered columns left; consider query patterns and ORDER BY.  

- Covering Indices  
  - Index that includes all columns needed by the query so the engine doesn't touch the heap/clustered table("index-only scan").  

### Query pattern -> index choice  

- Equality lookups: B-tree on the predicate columns(unique if possible).  
- Equality + range:  
  - e.g., WHERE user_id=? AND created_at BETWEEN ... ORDER BY created_at DESC  
  - Composite(user_id, create_at DESC). PostgreSQL can specify DESC; MySQL can often still benefit.  




## Redis  