# 架构总览

## 当前形态（Phase 0 最小落地）

- **1 台 ECS + 本机原生部署（systemd）**：单二进制双进程（`serve api` / `serve worker`）+ 本机 MySQL / Redis / Caddy（自动 HTTPS），**不装 Docker**。
- 托管只保留 **TOS**（对象存储）；MySQL、Redis 起步本机安装（ECS 上 + 定期备份），MySQL 规模化后切 RDS。
- 明确不买：VKE、CLB/APIG、托管 Redis、BMQ/RocketMQ、TLS/APMPlus。
- 延迟任务用 `notify_jobs.next_check_at` + Worker 轮询（30s）替代 MQ 延迟消息。
- 完整部署手册：[../../docs/部署方案.md](../../docs/部署方案.md)。

## 目标架构（百万用户）

- VKE 多可用区无状态服务（HPA）+ APIG；Kitex 微服务（schedule/task/notify/…）；BMQ 事件流；RDS 读写分离→逻辑分库（分片键 family_id/user_id Day 1 就位）。
- 目标架构全文：[../../docs/技术方案设计.md](../../docs/技术方案设计.md)。

## 演进阶段

| 阶段 | 架构形态 | 触发条件 |
|---|---|---|
| Phase 0 | ECS + systemd 本机部署（当前） | 自己家 dogfood |
| Phase 1 | 迁 VKE、引入 MQ、拆 schedule/task 服务 | 朋友 5–10 家 |
| Phase 2 | notify 强化（App push）、读写分离、托管 Redis | 半公开 50–200 家 |
| 规模化 | 逻辑分库、多 AZ 双活 | 10 万+ 家庭 |

## 模块地图（internal/）

| 模块 | 职责 | 拆分时机 |
|---|---|---|
| identity | 微信登录、unionid 合并、成员/角色/权限 | 最后拆 |
| schedule | 日程/重复规则/冲突检测 | 第一个拆（读多） |
| task | 打卡任务/提交/月历统计 | 第二个拆 |
| notify | 触达引擎：路由、升级、通道适配、回执 | Worker 起步即独立部署 |
| growth | 时间线、周报 | Phase 1 拆 |
| ingest | 课表识别/NL 解析（火山方舟·豆包） | Phase 1 拆（独立伸缩） |

铁律：模块间只走接口 + 事件，禁止跨模块直连表。
