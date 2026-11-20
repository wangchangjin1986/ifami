# 家人加入去社交化 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** 实现24小时单一码、四步直接加入、单家庭归属、管理员录入权限与照护人本人分工。

**Architecture:** identity通过Redis原子登记短期码，用现有MySQL成员表建立关系。schedule通过membership生成实例scope，notify通过服务接口获取当前可见性；小程序使用服务端结果与实际权限。

**Tech Stack:** Go1.23/Hertz/GORM/MySQL/go-redis，原生微信小程序、本地QR工具。

**Spec:** docs/技术方案/2026-11-20-家人加入去社交化.md

## Global Constraints

- 数字码、二维码、签名到期与 Redis 活动登记统一24小时。
- 不增加管理员审批、复杂受理状态机、join_attempt、容量配额、关闭开关、多家庭入口、数据库迁移；不清理已有数据。
- 家长parent，祖辈/阿姨grandparent，新加入is_admin=false；祖辈与阿姨按方案A均仅本人分工。
- 新归属只允许一个active家庭；创建与加入锁同一users行。
- 普通家长可见四Tab并参与打卡，无录入/编辑/成员管理入口，后端对应写API拒绝。
- 跨模块只走接口，不跨表SQL；所有对外触达经notify。
- 改动前先同步原型；保留外部已有改动。中文提交，不push、不部署。
- 后端全量test/vet/build；真实隔离Redis/MySQL和小程序编译/入口/行为回归。

### Task 1: 原型与现状文档对齐

**Files:** prototype/index.html，docs/需求变更/2026-11-20-家人加入去社交化.md，docs/V1-需求列表.md，docs/V1-信息架构.md，wiki/product/roles.md，docs/技术方案/2026-11-20-家人加入去社交化.md。
**Interfaces:** 后续实现消费24小时、四步、单家庭、grandparent统一本人分工、普通家长仅查看与打卡的基线。

- [ ] 对照原型与需求消除“七天”“家长一起管理”及多家庭新增入口冲突；仅改本次相关口径。
- [ ] 将实现确认记为2026-11-20用户“按照设计方案实现”，A作为采用基线；保留历史差异在变更记录，不删历史数据。
- [ ] 原型家长文案使用`查看家庭安排、参与打卡`，照护人示例只包含当前owner事项；确认区用动态示例家庭而非宣称真实API。
- [ ] 提取inline JS跑node --check，校验关键DOM引用；git diff --check（Markdown硬换行可说明）。
- [ ] 提交本任务并自检，记录确切文件、命令及结果。

### Task 2: identity短期加入码与单家庭事务

**Files:** 新internal/identity/join_code.go、join_registry.go及测试；internal/pkg/invite新增join签名；identity handler/service/store/errors；cmd/ifami/main.go必要注入；config/env参数。
**Interfaces:** 输出`JoinCodeView{JoinCode,GenerationID,QrContent,ExpiresAt,HasCode}`，`JoinPreview{FamilyID,FamilyName,JoinCode,GenerationID,Credential,ExpiresAt}`，`JoinConfirmInput{JoinCode,GenerationID,Credential,Identity,RelationLabel}`；`GetJoinCode(ctx,userID,familyID)`、`GenerateJoinCode(ctx,userID,familyID,key)`、`RevokeJoinCode(ctx,userID,familyID,generation)`、`PreviewJoin(ctx,userID,in)`、`ConfirmJoin(ctx,userID,in)`，均返回DTO,error。新registry接口实现和测试在identity内，复用redis客户端。

- [ ] 定义DTO、registry接口及真实Redis测试：生成/读回/换码/作废/碰撞/24h过期/旧代失效/离线拒绝。签名只family_id/expires，QR协议`IFAMI_JOIN_V1:<six>:<generation>:<credential>`；HMAC比较与输入上限。
- [ ] Redis Lua原子写家庭指针、数字索引、操作回执；当前码读回加密；基本失败user/IP保护，无容量配额。过期使用服务端now>=expires，重复操作不能恢复旧代。
- [ ] 新GET/POST/DELETE `/families/:fid/join-code`管理员接口和Bearer `/family-join/preview`、`/confirm`；POST码在body，码管理header/path一致，confirm拒绝身份/权限伪造。
- [ ] 成员事务的关键顺序为：`tx.Clauses(clause.Locking{Strength:"UPDATE"}).First(&User{},userID)`，再查active memberships；已属目标返回真实成员，其他家庭拒绝，removed拒绝；无active创建普通成员。CreateFamily使用同一user锁。识别mysql1062回查，不依赖TranslateError。
- [ ] AddChild只管理员；旧CreateInvitation/JoinFamily接口返回明确旧客户端提示，停签发/兑换旧权限token。已有会话不变。
- [ ] 定向单测、实际Redis和MySQL唯一/并发/创建加入竞争检查，包测试后提交并记录结果。

### Task 3: schedule本人scope与日程通知可见性

**Files:** schedule service/store/handler及测试；notify service及测试；cmd/ifami/main.go适配。
**Interfaces:** 消费identity.RequireActive的memberID/effectiveRole。新增`GetInstance(ctx,userID,familyID,instanceID)`，新增schedule导出`NotificationVisibility(ctx,userID,familyID,instanceID)`供notify消费；notify定义最小读接口并提供注入函数，使用bool/最小提示而非跨模块表读取。

- [ ] 加入行为测试：grandparent仅owner_member_id==自己的列表/详情；parent/admin全家庭读取，grandparent全模板接口拒绝；伪造日期/owner/family不能扩大。
- [ ] ListInstances Store接收server-generated owner可选值，加family/date/owner条件，复用idx_owner_date。新增GET `/schedules/instances/:iid`，保留已有parent/admin行为。
- [ ] 完成/反馈在同一实例事务锁行、先owner/status校验再写事件，done重放也先授权。管理员改派的行更新与事务锁互斥；拒绝后无事件/通知。
- [ ] 日程通知的待发送与红点读取使用当前scope；解除分工保留泛化提示并去地点备注/详情地址；其他job类型保持既有处理。适配器不能造成import循环。
- [ ] 定向Go用例、实际MySQL改派与执行交错检查、包test后提交并记录结果。

### Task 4: 小程序四步加入、码面板与实际权限

**Files:** family/invite/app/event-detail/gp-today，普通家长录入入口所在页/custom-tab-bar，新utils/qrcode及文档COMPAT；miniapp四件套。
**Interfaces:** 消费Task2 DTO和API路径/协议，以及Task3详情。`app.request`继续统一Bearer/X-Family-Id；码管理请求带Idempotency-Key。二维码工具本地生成矩阵/canvas，记录来源和许可，不能调用在线生成服务。

- [ ] 将family邀请选择/share/clipboard改为当前码面板：24小时、真实QR、六位、加载/过期/错误、换码/作废；无成员页面提供创建/加入，已有成员无另一个家庭入口。
- [ ] invite页面四块：数字或camera QR、身份、称谓、实际家庭确认；后台ensureLogin后preview。数字改动清旧preview；扫码协议全部匹配且不开放外链；手机号不必填。确认提交无审批/attempt。
- [ ] 网络失败保留输入，提交防重。返回actual成员权限；refreshMe失败不能误报加入失败；码失效响应丢失时/me核对目标成员。
- [ ] 删除邀请open-type=share、copyInvite、邀请onShareAppMessage，关闭相关分享菜单。显示QR，不提供保存/复制链接。
- [ ] 普通家长四Tab可查看/打卡，FAB/录入编辑/导入/成员管理入口按actual is_admin。详情加载Task3服务端实例前不渲染URL业务数据，改派后拒绝页面错误态。
- [ ] gp-today只消费当前家庭本人scope列表/明日预告，空文案为`今天没有分配给你的事项`；不新增多家庭往返。
- [ ] Node行为测试覆盖状态/入口/权限/响应丢失及QR解码；18页面四件套/handler/routes/官方WXML和WXSS编译；提交并记录结果。

### Task 5: 完整回归与收尾文档

**Files:** 测试证据与docs/bugs本次验收记录，wiki/status/current.md，miniapp/COMPAT.md，必要修复沿原任务返回worker。
**Interfaces:** 集成Task2/3/4行为，不能mock通过替代真实数据库与QR校验。

- [ ] 运行`go test ./... && go vet ./... && go build ./...`，必要schedule/identity/notify race。
- [ ] 隔离实际Redis/MySQL：24h/代际/单家庭并发/removed不恢复/有效码直接加入及改派竞态；不使用真实用户数据或真实微信发送。
- [ ] 小程序Node行为、官方编译、页面注册/四件套、关键入口；用独立QR解码器核实生成矩阵对应完整凭据。
- [ ] 汇总测试结果与真机/实际微信剩余项，更新当前状态和接口兼容；不宣称提审或线上部署完成。
- [ ] 最终独立审查完整实现，处理阻断发现后把本轮改动同步回原工作目录，保留原先未提交改动。交付变更与验证结果，不push或部署。
