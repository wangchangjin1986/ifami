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
go test ./...                      # 单测（store 层与含分支逻辑的 service 必须有单测）
go run ./cmd/ifami migrate          # 数据库迁移（golang-migrate，读 RDS_DSN，迁移目录 MIGRATIONS_DIR）
open prototype/index.html          # 交互原型（界面与交互的唯一权威参照）
```

## 本地运行前置

运行前需配置环境变量（清单见 [deploy/.env.example](deploy/.env.example)），关键项：

- `RDS_DSN`：MySQL 连接串，本机 `ifami:密码@tcp(127.0.0.1:3306)/ifami?parseTime=true`
- `FIELD_ENC_KEY`：字段级加密密钥，`openssl rand -base64 32` 生成（AES-256-GCM，敏感字段铁律）
- `INVITE_SECRET`：邀请 token 签名密钥，`openssl rand -hex 32` 生成
- `WX_MINIAPP_APPID` / `WX_MINIAPP_SECRET`：微信小程序凭据（登录链路需要）
- `REDIS_ADDR`：Redis 地址，默认 `127.0.0.1:6379`

首次运行先执行 `go run ./cmd/ifami migrate` 建表。

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
8. **开发完成必须做完整回归测试**：不能只做语法/单测；还要覆盖功能完整性、入口链路、空状态/异常状态、权限边界、可用性与原型一致性。小程序改动必须至少校验 `app.json` 页面注册、页面四件套存在、WXML 结构可编译、关键入口可达；后端改动必须跑 `go test ./... && go vet ./... && go build ./...`。

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

- **最高优先级流程门禁**：凡涉及产品行为、交互、权限、数据模型/迁移、架构、缓存/存储、部署策略、稳定性/性能优化等非纯排障观察事项，必须先输出「产品/技术方案 + 影响范围 + 风险 + 验证方式」（涉及界面/交互还必须同步原型），等用户明确确认后才能改业务代码、迁移、配置脚本或提交实现。即使是线上故障，如果修复会改变架构/数据模型/依赖关系，也只能先做临时运维止血与方案说明，不能擅自落代码。
- **禁止把用户方向当作实现确认**：用户描述目标、现象、想法、命令参数或使用 `/using-superpowers`，都不等于批准改代码。只有用户明确表达「按这个方案实现 / 可以改代码 / 确认方案」后，才能进入实现。
- 术语以 [wiki/product/glossary.md](wiki/product/glossary.md) 为准，代码命名与术语表对齐。
- 界面与交互以 `prototype/index.html` 为唯一权威参照；改交互先改原型。
- 需求与验收标准以 [docs/V1-需求列表.md](docs/V1-需求列表.md) 为准；新增需求先改文档再写码。
- **新增功能流程**：先改原型 + 需求文档（PRD），**等用户 check 确认后再写实现代码**（bug 修复/运维类不受此限）。
- **优化类变更流程**：性能、体验、稳定性、架构等优化类任务，必须先给出具体方案（目标、改动范围、预期收益、风险与验证方式），**等用户确认后再进行代码实现**；线上故障止血、明确 bug 修复、运维抢修可先做最小修复并说明原因。
- **收尾验证流程**：每次实现后必须主动做完整测试并在回复里列出结果，包括功能完整性、可用性、权限/空状态、前后端回归；如果本地无法执行真机/开发者工具测试，必须说明已做的替代验证和剩余风险。
- 提交信息用中文，格式 `<type>: <一句话>`（feat/fix/docs/chore/refactor）。
