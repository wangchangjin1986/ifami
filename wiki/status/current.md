# 当前状态（2026-10-29）

> coding agent 动手前先读这页。每次里程碑更新此文件。

## 阶段

**Phase 0 部署联调期**：仓库骨架 + 核心四模块（identity/schedule/notify/task）已实现，小程序四个 Tab 页已接真实接口。**服务已部署到 ECS（118.196.79.193，systemd 原生部署）**，备案完成前走公网 IP 纯 HTTP 直连（Caddy `:80`），公网链路已验证可用。**主体 = 纯个人开发者**（触达用订阅消息、无短信，后期升个体工商户解锁服务号/短信，见 ADR-002）。

## 部署现状（2026-10-29）

- ECS：MySQL（root 本机免密 socket）+ Redis + Caddy + ifami(api/worker)，全部 systemd 管理。
- 入口：`http://118.196.79.193`（Caddy `:80` → 127.0.0.1:8080）；域名 ifami.site **备案中**，完成后切 `api.ifami.site` 自动 HTTPS（切换清单见 bootstrap 输出与部署操作手册）。
- 脚本：`deploy/bootstrap.sh`（只检测中间件不安装 + 初始化）、`deploy/deploy.sh`（本机发布）、`deploy/service.sh`（启停/日志）。
- 日志：api/worker 落盘 `/var/log/ifami-{api,worker}.log` + logrotate（14 天），journald 兜底。
- 小程序：apiBase 指向公网 IP；**体验版必须每台设备「打开调试」才能跳过域名校验**（HTTP+IP 不被正式校验接受）。

## 等待中的外部事项（不阻塞编码）

域名 ICP 备案（进行中）、订阅消息模板申请——清单与进度：[../../docs/前置事项.md](../../docs/前置事项.md)。服务号/开放平台/短信签名报备**暂缓至升个体工商户**。

## 下一步（建议顺序）

1. ~~`internal/identity`：微信登录 + 家庭/成员 CRUD + 权限中间件~~ ✅ 已实现并验证
2. ~~`internal/schedule`：日程 CRUD + RRULE 物化 + 冲突检测~~ ✅ 已实现并验证
3. ~~`internal/notify`：触达状态机 + 订阅消息通道 + 回执上报~~ ✅ 已实现并验证（pending→sent→receipted / reddot→escalated 状态机 E2E 通过）
4. ~~`internal/task`：打卡任务 + 提交 + 事件流写入~~ ✅ 已实现并验证
5. ~~小程序：今日/日历/打卡/家庭四个 Tab 页接真实接口~~ ✅ 已接真实接口

### 已接入的触达链路（2026-10-26）

1. **schedule → notify**：接送类（pickup）日程创建/变更后立即强触达执行端 owner（R4.3「创建→收到<1min」）；课程/活动/提醒类不触达。
2. **task → notify**：打卡提醒（R3.3）——任务加 `remind_time`（HH:MM），Worker 每 30s 扫「到点未完成」的任务给责任父母发中等触达，`business_key` 幂等保证同一天只发一次。

### 尚未串起的链路

1. ~~**migrate 接入 golang-migrate**~~ ✅ 已接入并在 ECS 上跑通（2026-10-29）
2. **真实订阅消息通道**：notify 的 `Channel` 目前是 `MockSubscribe`，待模板申请后接微信订阅消息真实发送。
3. **备份联系人真实发送**：`escalateBackup` 解析配偶 user_id 的 TODO 未做（需 membership 提供监护人关系查询）。
4. **日程到点提醒**：接送前 X 分钟的定时提醒需 Worker 扫描 `schedule_instances`（ADR-003「提醒打在实例表」），当前只做写路径立即触达。

## 在验假设（种子期要验的，写代码时记得埋点）

1. 纯记录型打卡的 30 天留存率（决定激励中台是否提前）
2. 拍照识别对激活率的拉动
3. 订阅消息触达真实到达率与回执率（纯个人主体下到达率是最大风险）
4. 祖辈"去不了"反馈回路的使用率
5. 周视图"责任人优先"vs"课名优先"的偏好

## 北极星（Phase 2 起正式看）

周活跃协作家庭数；次级：单家庭平均成员数、强触达确认率。
