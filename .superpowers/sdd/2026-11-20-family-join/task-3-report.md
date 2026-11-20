# Task 3 实施报告：本人分工与日程通知可见性

状态：实现完成、定向回归通过，交给主代理评审及最终全仓回归。工作目录 `/tmp/ifami-join-20261120`，基线 Task 2 `ccf247a`。已读取 Task 3 brief、确认后的技术方案、AGENTS、current、ADR-003、ADR-002、API/数据库规范。

## 实现与范围

- `internal/schedule/service.go`：从 `RequireActive` 的真实 member_id/effective role 生成 scope。admin/parent 全家庭读取；其他非管理员仅当前 owner。模板读取拒绝照护人；新增 GetInstance 与 NotificationVisibility；InstanceView 增加 family_id。
- `internal/schedule/store.go`：列表在数据库查询中同时限制 family/date/可选 owner，复用原索引。完成和反馈先在同一事务 `SELECT ... FOR UPDATE`，再校验 owner/status，再写实例、反馈与事件。done 重放也先授权。管理员 UpdateInstance 原有 change_version 条件更新继续与实例行锁串行。
- `internal/schedule/handler.go`：注册鉴权 GET `/v1/schedules/instances/:iid`，校验非零实例与家庭 ID。
- `internal/notify/service.go`：消费方定义最小可见性接口，读取红点、发送 pending、sent 升红点和 reddot 升备份前复查当前 scope；不可见 jobs 只在正常 worker 处理时终止为 done，不清理历史行。解除分工固定泛化提示；无法解析稳定 ID 的历史照护人日程提醒（包括旧备份 payload）泛化为中等提示，不继续升级。task 通知保留原处理。
- `cmd/ifami/main.go`：API 与 Worker 构造 schedule 后均通过 SetScheduleVisibility 注入 notify，接口实现无需跨模块 import/跨模块表查询。
- 测试：service_test、store_integration_test、join_scope_integration_test、join_http_test 与 notify/service_test。并发 fake notifier 增加 mutex；原 pending 反馈多轮测试补真实实例 fixture，以符合新增实例锁校验。

没有修改 identity、task、miniapp、模型或迁移；未使用原仓库做测试写入，没有部署或外部消息发送。

## 前端契约

GET `/v1/schedules/instances/:iid`，Header `Authorization: Bearer <session>`、`X-Family-Id: <fid>`。成功示例：

```json
{
  "code": 0,
  "data": {
    "family_id": 100,
    "instance_id": 7,
    "schedule_id": 3,
    "child_member_id": 10,
    "title": "接孩子",
    "type": "pickup",
    "rrule": "FREQ=DAILY",
    "occur_date": "2026-11-20",
    "start_at": "2026-11-20T08:00:00Z",
    "end_at": null,
    "location": "东门",
    "note": "带水",
    "owner_member_id": 20,
    "status": "active"
  },
  "message": ""
}
```

列表沿用相同 InstanceView，新增 family_id。照护人没有本人分工时返回 `data: []`。伪造 owner_member_id 查询参数不参与生成 scope；日期只缩小/变换本人的发生日期范围。

错误：当前家庭非 owner 的照护人详情/完成/反馈 `code=2004`、HTTP403（复用原 schedule 权限错误）；实例不属于当前家庭/不存在 `2003`；详情缺家庭 Header、非数字/零 iid `2001`；无会话沿用 auth `4001`。实际 identity 拒绝家庭 membership 时沿用 identity 错误；测试 scopedMembership 使用 2004 表达拒绝。

新日程通知含 `type`、`instance_id`、`schedule_id`，落地路径只含 `pages/event-detail/index?instance_id=<iid>&family_id=<fid>`，通道仍追加 job_id 回执参数。创建/模板更新的 owner 提醒先解析数据库中首个未来本人实例，再生成 `schedule_updated`，不再创建无稳定 ID 的新 job。前端必须用 GET 结果展示详情，不能沿用 URL title/location/note。

解除分工 payload 仅 `{type: "schedule_unassigned", title: "分工已调整", page: "pages/gp-today/index"}`；notify 按实际有效角色为 parent/admin 选 `pages/today/index`，照护人选 `gp-today`。历史无稳定 ID 的照护人日程 jobs 同样仅三个安全字段，按 medium 发送/完成，不升级。

模块接口：

```go
// notify 定义，schedule.Service 实现
NotificationVisibility(ctx context.Context, userID, familyID, instanceID uint64) (bool, error)
// 构造后、运行请求前，API/Worker 均执行
notifSvc.SetScheduleVisibility(schedSvc)
```

## RED → GREEN 与验证结果

环境：使用 `/tmp/ifami_join_delivery_20261120/test-environment.md` 中专用 TEST_MYSQL_DSN；每个 MySQL 测试创建/删除独立 `ifami_test_<nanosecond>` 数据库，并按顺序执行全部已有 migrations。没有访问正常 RDS_DSN/REDIS_ADDR；本任务不新增 Redis 依赖。

初始 RED（实现前）：

1. `go test ./internal/schedule -run 'TestCaregiver' -count=1`，exit1：照护人模板读取实际执行 schedules 查询；foreign complete 在事务开始前查询实例，无法满足事务锁内授权/拒绝。
2. `go test ./internal/notify -run 'TestStaleSchedule' -count=1`，exit1：stale pending 仍发送，sent 进入 reddot，reddot 进入 escalated。

GREEN 定向证据：

- `go test ./internal/schedule -run 'TestCaregiver' -count=1`：PASS，0.808s。
- `go test ./internal/notify -run 'TestStaleSchedule' -count=1`：PASS，0.836s。
- 实际 MySQL `TestMySQLJoinInstanceScopeAndActions`：本人/他人/parent/admin 查询、任意日期/家庭伪造、详情完整字段/rrule、模板限制、拒绝反馈零事件零反馈零通知、本人反馈/完成、done 幂等、改派后旧 owner 拒绝、新 owner 接受及通知可见性。
- 实际 MySQL `TestMySQLJoinReassignmentBlocksExecutionAndDenies` complete/feedback：管理员事务先更新 owner 持锁；旧执行人等待，提交改派后拒绝。实际 events_stream 与 schedule_feedbacks 数量均为0；notifier 无 job。
- 实际 MySQL `TestMySQLJoinExecutionLockSerializesReassignment` complete/feedback：执行 SELECT FOR UPDATE 先持锁；管理员更新等待。完成提交后原版本改派拒绝（保留既有 CAS 重试语义）；反馈提交后改派成功；事件/反馈数量为预期单行，没有丢失或越权写。
- 实际 MySQL `TestMySQLJoinNewOwnerNotificationsHaveStableLanding`：创建 pickup 解析实际物化实例 ID；消息 schedule/instance ID 与 Store 查询一致；改派消息真实目标分别为1020/1021，旧 owner payload 只有泛化三个字段。
- 直接 HTTP `TestMySQLJoinScheduleHTTPContract`：鉴权、注册路由、详情 URL 伪造数据不覆盖服务端、owner/date/family 边界、模板/complete/feedback/PATCH 拒绝、管理员改派、done 重放和改派后新旧详情。
- notify 用例：可见/不可见红点与发送；可见性接口实际调用参数 user20/family100/instance7；stale 三阶段无发送/备份；解除分工与 legacy/旧 backup 去详情且不升级；task 原 payload 保持。

包级最终验证（均 exit0）：

```text
# TEST_MYSQL_DSN 设置为上述专用环境；无测试 skip 依赖缺失
$ go test ./internal/schedule ./internal/notify ./cmd/ifami -count=1
ok  ifami/internal/schedule  6.761s
ok  ifami/internal/notify    1.323s
?   ifami/cmd/ifami          [no test files]

$ go test -race ./internal/schedule ./internal/notify -run 'TestMySQLJoin|TestCaregiver|TestScheduleVisibility|TestScheduleGeneric|TestStaleSchedule|TestTaskNotification' -count=1
ok  ifami/internal/schedule  7.221s
ok  ifami/internal/notify    2.440s

$ go vet ./internal/schedule ./internal/notify ./cmd/ifami
$ go build ./cmd/ifami
$ git diff --check
# 均无错误输出
```

随后补充 legacy backup 分类后，对改动的 notify 包复验：定向四组用例 PASS 0.803s；`go test ./internal/notify -count=1` PASS 0.695s，`go vet ./internal/notify` 通过，diff-check 通过。主代理负责最终 `go test ./... && go vet ./... && go build ./...` 与 Task4 小程序完整回归。

## 自检与限制

- 所有 owner 范围来自消费方 membership 接口，不按称谓判断、不跨模块查成员表。列表先过滤，再补模板 RRULE；详情拒绝后不补模板数据。
- row lock 覆盖授权到事件提交；done 重放不跳过 owner；CAS 版本机制保持原状。反馈通知仅在 Store 事务成功后创建。
- notify 的历史详细 payload 保留在数据库历史记录，但后续公开读取/发送路径会过滤或泛化，不批量改历史；已发送历史消息无法撤回。
- 待发送/红点/升级 scope 读取与外部发送不是跨模块长事务；本实现遵循确认方案，在每次操作前重验，不持 MySQL 锁跨网络发送。成员撤权与业务事务也未引入跨模块锁。
- 真实 MySQL 用于实例事务、持锁交错、HTTP业务数据、事件/反馈实际行数与新消息稳定 ID；notification 出站通过 fake notifier/channel 验证，未发送微信订阅消息，未声称真机到达/回执通过。
- 无小程序代码改动；Task4 必须同步新详情加载契约、空态与失效消息状态。此任务没有真机/开发者工具测试。
