# 当前状态（2026-11-17）

> coding agent 动手前先读这页。每次里程碑更新此文件。

## 阶段

**Phase 0 部署联调期**：仓库骨架 + 核心四模块（identity/schedule/notify/task）已实现，小程序四个 Tab 页已接真实接口。**服务已部署到 ECS（118.196.79.193，systemd 原生部署）**，备案完成前走公网 IP 纯 HTTP 直连（Caddy `:80`），公网链路已验证可用。**主体 = 纯个人开发者**（触达用小程序一次性订阅消息 + 应用内红点，无短信，后期升个体工商户解锁服务号/短信，见 ADR-002）。近期重点从“接口能跑”推进到“触达闭环、打卡照片持久化、失败态可解释”。

## 当前协作约束

- **强制门禁（最高优先级）**：任何产品行为、交互、权限、数据模型/迁移、架构、缓存/存储、部署策略、稳定性或性能优化变更，都必须先给出方案（目标、方案选项、影响范围、风险、验证方式；涉及 UI/交互还要先改原型），待用户明确确认后才能改代码。用户描述目标、故障现象或技术方向不等于授权实现。
- **线上故障例外边界**：允许先做不改代码的运维止血、日志排查、回滚、重启、配置核验；如果止血方案需要新增/修改业务代码、迁移、存储依赖或架构降级，仍必须先方案确认。
- 后续性能、体验、稳定性、架构等优化类任务，必须先给出具体方案（目标、改动范围、预期收益、风险与验证方式），待用户确认后再进行代码实现；线上故障止血、明确 bug 修复、运维抢修除外。

## 部署现状（2026-10-29）

- ECS：MySQL（root 本机免密 socket）+ Redis + Caddy + ifami(api/worker)，全部 systemd 管理。
- 入口：`http://118.196.79.193`（Caddy `:80` → 127.0.0.1:8080）；域名 ifami.site **备案中**，完成后切 `api.ifami.site` 自动 HTTPS（切换清单见 bootstrap 输出与部署操作手册）。
- 脚本：`deploy/bootstrap.sh`（只检测中间件不安装 + 初始化）、`deploy/deploy.sh`（本机发布）、`deploy/service.sh`（启停/日志）。
- 日志：api/worker 落盘 `/var/log/ifami-{api,worker}.log` + logrotate（14 天），journald 兜底。
- 小程序：apiBase 指向公网 IP；**体验版必须每台设备「打开调试」才能跳过域名校验**（HTTP+IP 不被正式校验接受）。

## 等待中的外部事项（不阻塞编码）

域名 ICP 备案（进行中）、小程序订阅消息模板 ID 配置/审核——清单与进度：[../../docs/前置事项.md](../../docs/前置事项.md)。服务号/开放平台/短信签名报备**暂缓至升个体工商户**。

## 下一步（建议顺序）

1. ~~`internal/identity`：微信登录 + 家庭/成员 CRUD + 权限中间件~~ ✅ 已实现并验证
2. ~~`internal/schedule`：日程 CRUD + RRULE 物化 + 冲突检测~~ ✅ 已实现并验证
3. ~~`internal/notify`：触达状态机 + 订阅消息通道 + 回执上报~~ ✅ 已实现并验证（pending→sent→receipted / reddot→escalated 状态机 E2E 通过）
4. ~~`internal/task`：打卡任务 + 提交 + 事件流写入~~ ✅ 已实现并验证
5. ~~小程序：今日/日历/打卡/家庭四个 Tab 页接真实接口~~ ✅ 已接真实接口

### 已接入的触达链路（2026-11-17）

1. **真实小程序订阅消息通道**：`NOTIFY_CHANNEL=wechat_subscribe` 默认启用，模板 ID 由 `WX_SUBSCRIBE_TEMPLATE_SCHEDULE` / `WX_SUBSCRIBE_TEMPLATE_TASK` / `WX_SUBSCRIBE_TEMPLATE_FEEDBACK` 配置；前端通过 `/v1/notify/config` 拉取模板 ID 后再 `wx.requestSubscribeMessage`。
2. **schedule → notify**：日程创建/变更后强触达执行端 owner；实例改分工（任意日程类型，只要新 owner 是真实成员）会触发 `schedule_reassigned`；Worker 每 30s 扫未来 15 分钟内即将开始的非在校实例，创建 `schedule_reminder`，`business_key=schedule_remind:<instance_id>:<user_id>` 幂等。
3. **task → notify**：打卡提醒（R3.3）——任务加 `remind_time`（HH:MM），Worker 每 30s 按东八区家庭日历扫「到点未完成」的任务给责任父母发中等触达，`business_key=checkin:<task_id>:<YYYY-MM-DD>` 幂等保证同一天只发一次。
4. **应用内红点与回执**：`/v1/notify/reddots` 返回当前用户待确认红点，今日页/照护人今日页展示待确认提醒；消息落地页和红点点击都会上报 `/v1/notify/receipt`，避免无回执继续升级。
5. **备份联系人升级**：强触达超时后进入 reddot，再超时后解析家庭内备份联系人（管理员优先、父母兜底，跳过原目标）创建 `backup_notification` 中等触达。

### 尚未串起的链路

1. ~~**migrate 接入 golang-migrate**~~ ✅ 已接入并在 ECS 上跑通（2026-10-29）
2. **订阅消息模板审核/配置**：真实发送链路已接通；如果模板 ID 为空或微信后台模板未审核，前端授权列表为空、后端发送会失败，需要在部署环境补齐模板 ID。
3. **短信/服务号模板消息兜底**：纯个人主体阶段仍不可用，升个体工商户后补 `wechat_template` / `sms` 通道。
4. **照片预览 UI**：后端已提供 `photo_url` 与图片读取接口，当前重点是拍照打卡原图持久化；时间线/周报里的图片预览可后续按需要补。

## 近期变更盘点（2026-11-17）

### 触达与回执闭环

- notify 从纯 mock 推进到真实小程序订阅消息适配：统一 access_token 缓存、`subscribe.send` 发送、模板 ID 下发、模板字段约定（`thing1/time2/thing3`）。
- 前端订阅授权从硬编码模板 ID 改为后端配置拉取并缓存；录日程、改分工给当前用户、打卡等关键动作前会尽量前置授权。
- 红点链路补齐：后端按用户列 `reddot` 状态任务，今日页/照护人今日页展示，点击后回执并跳转业务页。
- 强触达升级链路补齐到备份联系人：应用内红点仍无回执时，按家庭管理员/父母顺序创建备份提醒，避免只停留在单一执行人。
- HTTP 创建 notify job 增加 actor 与 target 的家庭 membership 校验；业务模块内部仍通过接口写 job，保持模块边界。

### 日程与分工

- 实例改分工不再只覆盖接送类；任何日程类型改 owner 都会通知新责任人，且 owner 必须是真实家庭成员。
- Worker 接入日程到点提醒扫描：非在校实例开始前 15 分钟创建强触达，落地页带实例上下文并支持回执。
- 日程编辑支持“在校”类型的历史记录编辑；在校块可无责任人，避免被普通日程 owner 校验误拦。

### 打卡与任务

- 拍照任务从占位变为真实提交：小程序 `wx.chooseMedia` 后通过 `wx.uploadFile` 上传到 `/v1/tasks/:tid/checkins/photo`。
- 拍照打卡原图二进制存 MySQL：`checkins.photo_data MEDIUMBLOB` + `photo_content_type`，列表接口只返回 `has_photo/photo_url`，不直接拉 blob。
- 后端对拍照打卡做双重校验：任务必须是 `photo`，大小不超过 5MB，真实二进制内容必须是 JPEG/PNG（不只信客户端 Content-Type）。
- 手动打卡允许计时/计数互切：timer 任务可提交 count，count 任务可提交 seconds，避免前端切换打卡方式后被后端拒绝。
- 打卡流支持切换孩子：按同名任务优先匹配，找不到则退到该孩子任一任务；任务模板新增“拍照记录”。

### AI 导入与内容安全

- 课表/图片导入上传原图前增加微信图片内容安全检测，不通过时返回 `6008`，避免违规原图进入临时存储。
- 导入确认增加 `confirming` 中间状态与 CAS 转换，防止用户重复点击导致重复入库；确认失败会尽量恢复到 `done` 以便用户重试。

### 周报、设置与可用性

- 周报页不再把接口失败/超时当成“暂无数据”：主数据失败显示错误卡片和重试；下周提醒失败单独提示，不污染本周数据判断。
- 设置页补齐孩子数据导出与孩子档案删除入口：导出为 JSON 并复制到剪贴板；删除孩子档案为软删除，历史日程/任务/打卡事实保留。

### 配置与部署注意

- 新增/补齐环境变量：`NOTIFY_CHANNEL`、`WX_SUBSCRIBE_TEMPLATE_SCHEDULE`、`WX_SUBSCRIBE_TEMPLATE_TASK`、`WX_SUBSCRIBE_TEMPLATE_FEEDBACK`；`.env.example` 已给出三类模板内容样例。
- 微信客户端新增可注入 URL：`WX_ACCESS_TOKEN_URL`、`WX_SUBSCRIBE_SEND_URL`、`WX_IMG_SEC_CHECK_URL`，用于本地/测试替身服务。
- 新增迁移 `0013_add_checkin_photo_blob.up.sql`；上线前需确保迁移跑完，否则拍照上传接口会因缺列失败。

### 已验证

- 后端：`go test ./...`、`go vet ./...`、`go build ./...`。
- 小程序关键 JS：`node --check miniapp/pages/daka-flow/index.js`。
- Diff 格式：`git diff --check`。

## 在验假设（种子期要验的，写代码时记得埋点）

1. 纯记录型打卡的 30 天留存率（决定激励中台是否提前）
2. 拍照识别对激活率的拉动
3. 订阅消息触达真实到达率与回执率（纯个人主体下到达率是最大风险）
4. 祖辈"去不了"反馈回路的使用率
5. 周视图"责任人优先"vs"课名优先"的偏好

## 北极星（Phase 2 起正式看）

周活跃协作家庭数；次级：单家庭平均成员数、强触达确认率。
