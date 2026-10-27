# Go 编码规范

## 工程结构

- 单二进制：`cmd/ifami/main.go` 只做命令分发（serve api / serve worker / migrate），业务不进 main。
- 业务代码在 `internal/<module>/`：identity / schedule / task / notify / growth / ingest。
- 模块内部分层建议：`handler`（HTTP）→ `service`（业务）→ `store`（DB）。跨层只向下调用。
- 公共代码进 `internal/pkg/`（db、redis、wechat、auth 中间件），不含业务语义。

## 模块边界（铁律）

- 跨模块调用只能走对方导出的 service 接口，**禁止 import 对方的 store、禁止跨模块 SQL**。
- 异步协作走事件（写 `events_stream` 由消费者处理），不直接调对方接口扛流量。

## 代码风格

- `gofmt` + `go vet` 必须干净；提交前 `go build ./... && go vet ./...`。
- 错误处理：显式 `if err != nil`，不吞错；业务错误用自定义错误码（见 [api.md](api.md)）。
- 命名与 [../product/glossary.md](../product/glossary.md) 对齐：family/member/schedule/task/checkin/notify_job。
- 时间一律 UTC 存储、东八区展示；DB 字段用 `DATETIME`，Go 侧 `time.Time`。
- 注释用中文，注释解释"为什么"，不复述代码。
- 依赖克制：Phase 0 标准库优先；引入第三方库需在 PR 描述里说明理由。Hertz/GORM 在正式业务开发启动时引入。

## 测试

- store 层和含分支逻辑的 service 必须有单测；`go test ./...` 必须全绿。
- 触达状态机、RRULE 物化、冲突检测三个组件的测试是硬要求（漏发/错发是事故）。
