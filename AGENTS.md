# AGENTS.md — ifami 编码代理指南

> 本文件是 coding agent 的入口。**改动代码前，先按「Wiki 导读」读相关页面**——产品红线、架构决策和编码规范都在 wiki 里，不在本文件里展开。

## 项目是什么

ifami：以"家庭"为账户单位的学习与生活协作工具。微信小程序（主端）+ 微信服务号（触达）+ Go 后端 + 火山引擎。

V1 = 家庭日历分工 + 打卡记录 + 分级触达。明确**不做**：广场、排名、IM、激励体系、家校沟通（见 [wiki/product/red-lines.md](wiki/product/red-lines.md)）。

## 快速命令

```bash
go run ./cmd/ifami serve api       # 后端 API，:8080，/healthz
go run ./cmd/ifami serve worker    # 异步 Worker（触达轮询/事件消费/实例续展）
go build ./... && go vet ./...     # 构建与静态检查
go run ./cmd/ifami migrate          # 数据库迁移（golang-migrate，读 RDS_DSN，迁移目录 MIGRATIONS_DIR）
open prototype/index.html          # 交互原型（界面与交互的唯一权威参照）
```

## 仓库地图

| 路径 | 内容 |
|---|---|
| `cmd/ifami/` | 单二进制入口（serve api / serve worker / migrate） |
| `internal/` | 业务模块。**模块边界=未来微服务边界** |
| `migrations/` | 数据库迁移（golang-migrate 格式） |
| `miniapp/` | 微信小程序（4 Tab + 子页面） |
| `h5/` | 服务号 H5（周报/邀请落地页） |
| `deploy/` | Dockerfile / docker-compose / Caddyfile / deploy.sh |
| `prototype/` | 可点击 HTML 原型 |
| `docs/` | PRD、需求、IA、技术方案、部署方案、前置事项 |
| `wiki/` | **本知识库**（agent 按需加载） |

## 硬性规则（违反即返工）

1. `internal/` 模块间只通过接口和事件通信，**禁止跨模块直连数据库表**。
2. 所有业务表必须带 `family_id`（或 `user_id`）分片键——建表前先读 [wiki/conventions/database.md](wiki/conventions/database.md)。
3. 手机号、孩子姓名等敏感字段**字段级加密存储，展示默认脱敏**。
4. 孩子是虚拟成员：无独立登录、无陌生人触达、数据操作需监护人 membership 鉴权。
5. UI 不出现排名、跨家庭对比、积分勋章等激励元素。
6. 一切对外触达走 notify 模块统一路由，业务模块**禁止直接调用**微信/短信 API。
7. 微信没有"已读回执"——强触达确认机制是**点击回执制**（[wiki/architecture/adr-002-notification-receipt.md](wiki/architecture/adr-002-notification-receipt.md)）。

## Wiki 导读（按任务类型）

| 你要做的事 | 先读 |
|---|---|
| 任何代码改动 | [wiki/status/current.md](wiki/status/current.md)（当前阶段与在验假设） |
| 写账号/家庭/成员相关 | [wiki/product/roles.md](wiki/product/roles.md) + [ADR-001](wiki/architecture/adr-001-account-model.md) |
| 写触达/提醒相关 | [ADR-002](wiki/architecture/adr-002-notification-receipt.md) + [wiki/conventions/api.md](wiki/conventions/api.md) |
| 写日程/日历相关 | [ADR-003](wiki/architecture/adr-003-schedule-materialization.md) |
| 写打卡/激励相关 | [ADR-004](wiki/architecture/adr-004-incentive-deferral.md) |
| 写 AI 识别相关 | [ADR-005](wiki/architecture/adr-005-ai-draft-confirm.md) |
| 加表/改表 | [wiki/conventions/database.md](wiki/conventions/database.md) |
| 加接口 | [wiki/conventions/api.md](wiki/conventions/api.md) |
| 写小程序页面 | [wiki/conventions/miniapp.md](wiki/conventions/miniapp.md) + `prototype/index.html` |
| 部署相关 | [ADR-006](wiki/architecture/adr-006-minimal-phase0.md) + [docs/部署方案.md](docs/部署方案.md) |

## 工作方式约定

- 术语以 [wiki/product/glossary.md](wiki/product/glossary.md) 为准，代码命名与术语表对齐。
- 界面与交互以 `prototype/index.html` 为唯一权威参照；改交互先改原型。
- 需求与验收标准以 [docs/V1-需求列表.md](docs/V1-需求列表.md) 为准；新增需求先改文档再写码。
- **新增功能流程**：先改原型 + 需求文档（PRD），**等用户 check 确认后再写实现代码**（bug 修复/运维类不受此限）。
- 提交信息用中文，格式 `<type>: <一句话>`（feat/fix/docs/chore/refactor）。
