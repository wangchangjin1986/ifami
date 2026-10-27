# ifami

以家庭为单位的协作工具：排课分工、打卡记录、触达必达。微信小程序 + Go 后端 + 火山引擎。

## 文档

| 文档 | 内容 |
|---|---|
| [AGENTS.md](AGENTS.md) | **编码代理入口**（硬性规则 + wiki 导读） |
| [wiki/](wiki/README.md) | LLM 知识库：产品知识 / 架构决策(ADR) / 编码规范 / 当前状态 |
| [docs/PRD-v0.1.md](docs/PRD-v0.1.md) | 定位、账号体系、触达架构、路线图、合规红线 |
| [docs/V1-需求列表.md](docs/V1-需求列表.md) | 需求条目、验收标准、V1 边界 |
| [docs/V1-信息架构.md](docs/V1-信息架构.md) | 页面清单、主屏线框、关键流程 |
| [docs/技术方案设计.md](docs/技术方案设计.md) | 目标架构与演进方向（百万用户规模） |
| [docs/部署方案.md](docs/部署方案.md) | Phase 0 最小落地：前后端完整部署手册 |
| [docs/前置事项.md](docs/前置事项.md) | 备案与资源申请清单（有外部审核周期，最先启动） |

## 仓库结构

```
cmd/ifami/      后端单二进制入口（serve api | serve worker | migrate）
internal/       业务模块（边界=未来微服务边界，禁止跨模块直连表）
migrations/     数据库迁移（0001_init.sql = V1 核心表）
deploy/         Dockerfile / docker-compose.yml / Caddyfile / deploy.sh / .env.example
miniapp/        微信小程序（4 主 Tab + 子页面骨架）
h5/             服务号 H5（周报/邀请落地页，Caddy 直出）
prototype/      可点击 HTML 原型（设计参照）
```

## 本地开发

```bash
# 后端（骨架阶段，标准库即可跑）
go run ./cmd/ifami serve api      # :8080，/healthz
go run ./cmd/ifami serve worker   # 触达任务轮询骨架

# 数据库
mysql -h ... < migrations/0001_init.sql

# 小程序
# 微信开发者工具导入 miniapp/（开发阶段 appid 用测试号，urlCheck 已关）

# 原型
open prototype/index.html
```

## 部署

见 [docs/部署方案.md](docs/部署方案.md)：`DEPLOY_HOST=<ecs-ip> ./deploy/deploy.sh`
