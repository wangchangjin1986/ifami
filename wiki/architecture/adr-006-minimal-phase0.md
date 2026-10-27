# ADR-006：Phase 0 最小落地与简化项恢复时机

## 决策

Phase 0 只有创始人家一个用户，按最小落地执行：**1 台 ECS + 本机原生部署（systemd）**，单二进制双进程（api + worker）+ 本机 MySQL + 本机 Redis，**不装 Docker**。托管只保留 TOS；MySQL 起步自托管、规模化再切 RDS（见「修订记录」）。

## 简化清单与恢复时机

| 砍掉 | 替代方案 | 恢复时机 |
|---|---|---|
| VKE | ECS + systemd 本机部署 | Phase 1（朋友 5–10 家） |
| RocketMQ/BMQ | `notify_jobs.next_check_at` + Worker 30s 轮询 | Phase 1（换延迟消息） |
| 托管 Redis | 本机 Redis（纯缓存可丢，不持久化） | Phase 2 |
| CLB/APIG | Caddy 反代 + 自动 HTTPS（Let's Encrypt） | Phase 1 |
| TLS/APMPlus | 云监控免费探测 + 应用自建指标 | Phase 1 |
| 触达看板前端 | 每日 21:00 汇总推到创始人服务号 | Phase 2 |
| 小程序上架 | 体验版（100 体验成员够用） | Phase 1 结束前提审 |

## 不能简化的事

- **MySQL 备份 + 恢复演练**：自托管起步，也要每日 mysqldump 备份 + 恢复演练；规模上量后切 RDS 自动备份。
- **TOS 存照片**：不落 ECS 磁盘。
- **模块边界**：内部模块只走接口通信——"能拆"的唯一保证，写代码时就要守住。
- **分片键**：`family_id`/`user_id` Day 1 入模。
- **触达指标**：分通道成功率/回执率/升级率从第一天采集。

## 成本

≈ ¥200/月以内（ECS ~150 + EIP ~20 + TOS/短信按量；MySQL 自托管 ¥0，切 RDS 时再 +~¥100）。详见 [../../docs/部署方案.md](../../docs/部署方案.md)。

## 修订记录

- 2026-10-27：MySQL 由「托管 RDS」改判为「Phase 0 自托管（ECS 本机安装 + deploy/backup.sh 每日备份）」。原因：起步仅创始人家一个用户，先用自托管省钱快跑；数据可靠性改由定期备份 + 恢复演练兜底，规模化上量后切回 RDS 自动备份。
- 2026-10-27：编排由「Docker Compose」改判为「本机原生 systemd 部署，不装 Docker」。原因：单台 ECS 上组件就 MySQL/Redis/Caddy + 单二进制双进程，用 systemd 守护即可，省掉容器层运维与磁盘开销；Redis/Caddy 也从容器改为本机安装。规模化要迁 VKE 时再引入容器化。
