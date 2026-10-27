# ifami Wiki 索引

> 面向 coding agent 的知识库。每页单一主题、可独立加载。总入口：[../AGENTS.md](../AGENTS.md)。

## product/ 产品知识

- [overview.md](product/overview.md) — 定位、目标用户、JTBD、V1 范围与路线图
- [roles.md](product/roles.md) — 角色模型、权限矩阵、注册路径、视角决定规则
- [glossary.md](product/glossary.md) — 术语表（代码命名对齐此表）
- [red-lines.md](product/red-lines.md) — 产品与合规红线

## architecture/ 架构与决策（ADR）

- [overview.md](architecture/overview.md) — 当前形态（Phase 0）与目标架构、演进阶段
- [adr-001-account-model.md](architecture/adr-001-account-model.md) — 人-家庭-成员三元组；孩子虚拟成员与认领
- [adr-002-notification-receipt.md](architecture/adr-002-notification-receipt.md) — 触达确认回执制；微信无已读回执
- [adr-003-schedule-materialization.md](architecture/adr-003-schedule-materialization.md) — RRULE 模板 + 90 天实例物化
- [adr-004-incentive-deferral.md](architecture/adr-004-incentive-deferral.md) — 激励后置与事件流钩子
- [adr-005-ai-draft-confirm.md](architecture/adr-005-ai-draft-confirm.md) — AI 只产草稿、人确认入库
- [adr-006-minimal-phase0.md](architecture/adr-006-minimal-phase0.md) — Phase 0 最小落地与简化项恢复时机

## conventions/ 编码规范

- [go.md](conventions/go.md) — Go 代码规范
- [api.md](conventions/api.md) — API 设计规范
- [database.md](conventions/database.md) — 数据库规范
- [miniapp.md](conventions/miniapp.md) — 小程序规范

## status/ 当前状态

- [current.md](status/current.md) — 当前阶段、在验假设、下一步
