# 数据库规范

## 铁律

1. **分片键 Day 1 就位**：业务表必须带 `family_id`（用户维度表带 `user_id`）。没有分片键的表不许进 migrations。
2. **敏感字段字段级加密**：`phone_enc`、`VARBINARY` 存储，KMS 托管密钥；代码里不出现明文手机号写库。
3. 所有表 `ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`。
4. 时间字段 `DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP`，存储 UTC。

## 迁移

- 文件命名 `NNNN_<动词>_<对象>.sql`（`0001_init.sql`、`0002_add_xxx.sql`），只增不改。
- 已合入主干的迁移文件**禁止修改**；改表写新迁移。
- DDL 必须在线可执行（gh-ost 友好）：避免大表直接 `ALTER` 加锁操作。

## 索引

- 命名：`uk_<列>`（唯一）、`idx_<用途>`（普通）。
- 高频查询模式（见 [../architecture/adr-003-schedule-materialization.md](../architecture/adr-003-schedule-materialization.md)）：
  - 日程实例：`(family_id, occur_date)`、`(owner_member_id, occur_date)`
  - 触达轮询：`(status, next_check_at)`
  - 打卡：`(task_id, created_at)`、`(family_id, created_at)`
- 新查询模式进代码前先确认有索引支撑，没有就同 PR 加迁移。

## 数据边界

- 跨模块查询禁止——各模块只读写自己的表（模块-表对应关系见 [../architecture/overview.md](../architecture/overview.md)）。
- 孩子档案（members 中 type=virtual）的删除必须软删除（`status` 标记），物理删除禁止。
