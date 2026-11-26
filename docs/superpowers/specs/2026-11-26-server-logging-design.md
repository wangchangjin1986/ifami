# 服务端日志增强技术方案 A

日期：2026-11-26。代码基线：main / 05fcf878e8e95d2be139ec2a2ba2baec9a136863。
状态：用户已选择方案 A；本文件为待确认技术设计，尚未实现。

## 1. 目标与选项

目标：遇到“加入暂不可用”、登录失败、识别失败或提醒未发送时，能根据 request_id 或 job_id 找出具体失败操作、依赖、原因、耗时与后台执行结果；无需临时增加打印或查看敏感原始数据。

已讨论选项：
- A：结构化日志、请求与任务关联、关键依赖错误记录。运维成本较低，继续使用 systemd 文件日志；耗时分解依靠明确的操作日志。
- B：A + OpenTelemetry 与链路接收/查询端，可查看 span 时间线，但需额外依赖、采样和采集平台。

用户选择 A。本轮不接入 OpenTelemetry，不生成伪 trace/span，不安装采集器或查询平台，不引入迁移、任务业务状态或新的重试策略。HTTP 响应体、错误码、权限与原型不变。未承诺跨进程重启后的 request_id 连续传播：持久化 job_id 是跨进程关联依据。

## 2. 当前代码问题

- main.accessLog：已有 request_in/out、X-Request-Id、状态和耗时，但 ID 未传入业务 context。请求 path 拼接完整 query；family_id 直接取请求头。
- logx：Printf 文本格式，无统一结构化字段或 context 接口。
- httpx.Fail：普通 error 转5000，未记录根因；auth.BearerAuth 也会先转换成 ErrInternal，丢失底层错误。
- identity.JoinCodeRegistry：Redis Eval 错误、密文解码失败等直接转1012；客户端只收到通用报错，服务日志缺具体操作和原因。
- db.gormLog：已有200ms慢查询和错误日志，但忽略 context，并输出展开后的 SQL。
- notify/ingest/Worker：已有部分 job_id 错误日志，缺每次执行关联、关键结果与依赖耗时。ingest 使用 Background 启动/接管任务。
- wechat/ark：已有部分安全错误包装，但缺统一操作日志；Ark 错误体和 mock notify 的 payload/openid 输出需要收敛。

## 3. 日志基础与字段

采用 Go 1.23 标准库 log/slog 的 JSONHandler，不新增第三方依赖。保留现有 Infof/Warnf/Errorf/Fatalf 兼容入口，逐步将本轮关键路径改为 context + 显式字段接口。日志模块不依赖 auth/identity/业务模块，防止循环依赖。Hertz 自身诊断输出如需适配，只调整日志出口，不替换服务或恢复语义。

每条应用日志含：time（UTC RFC3339）、level、event、service（api/worker/migrate）、version、message。可选：request_id、batch_id、run_id、user_id、family_id、job_type、job_id、operation、duration_ms、outcome、code、error_kind、error_code、cause。字段保持稳定，不把任意 payload 展开为字段。

version 从 Go build info 的 vcs revision/modified 信息读取；缺失时为 unknown，不为取得版本执行 git 或新增部署脚本依赖。

request_id 是单次HTTP请求标识，batch_id 是单次扫描标识，run_id 是单次任务执行标识；它们与 job_id 不同。同一任务重试产生新 run_id，保留同一 job_id。服务端标识使用 crypto/rand，ID生成异常沿用现有非致命兜底思路，不影响请求受理。

## 4. API 请求关联

- 访问中间件将 request_id 写入不可变 context 元数据，并通过 X-Request-Id 响应头返回，日志上下文随 c.Next(ctx) 进入业务、GORM及外部调用。
- 合法客户端 X-Request-Id 可保留：长度1–64、字符限ASCII字母/数字/点/下划线/连字符；无效则生成新的。ID仅用于诊断，不能作为鉴权或幂等凭据；客户端重复ID可能产生多条请求，须结合时间和操作区分。
- request_in/out 保留。request_out 写 method、route（模板路由，避免资源ID混入事件名）、status、code、duration_ms、outcome。未匹配路由使用固定 unmatched 标识，不记录原始完整URL或query。
- code 由 httpx.OK/Fail 写入 RequestContext 的诊断元数据，不解析响应JSON。直接响应或框架错误未经过httpx时，code省略而非假定成功。
- BearerAuth 成功后将真实 user_id 放入日志context；同时通过本请求的 RequestContext 元数据提供给外层request_out。避免外层错误读取鉴权前context而漏掉身份。
- 请求头 X-Family-Id 最多记为 requested_family_id（只允许合法数字），明确未经验证；只有 membership/权限验证成功的操作才显式写 family_id。logger 不自动把客户端字段提升成已核验身份，也不保存可被多协程修改的共享字段map。
- 4xx预期拒绝通常INFO；基础设施失败/5xx ERROR；超过现有1s请求阈值但正常完成为WARN。限流事件使用WARN并限频，不逐次打印码或输入。

## 5. 错误原因与依赖操作

保留现有对外错误和返回值。转换业务错误之前，在拥有根因的边界记录一次安全的 operation_failed；HTTP出口记录 request_out 总结，不再重复同一cause。

- Redis：identity registry 的 current/rotate/resolve/revoke/reserve/finish，auth会话缓存读写。记录operation、稳定script_name、duration_ms、error_kind、必要的安全Redis错误码/描述；不记录key、Lua全文、ARGV、凭证或加入码。记录逻辑不能额外执行预算命令，也不能触发新的重试；Reserve/Finish原有幂等和失败关闭行为不变。
- MySQL：GORM错误与慢查询日志携带context、数据库操作/表名（静态元信息）、rows、duration_ms、MySQL错误编号和分类。取消默认展开SQL输出，包含debug模式；不要依靠正则从展开SQL还原安全参数。日志接口允许无法提取时省略表名，不输出原始SQL、DSN、参数或driver错误中的数据值。
- 微信/方舟：命名operation（code2session/token/subscribe_send/image_check/recognize），记录duration_ms、HTTP status、厂商业务错误码和经白名单处理的厂商request_id（有则记）。HTTP200但业务错误仍记失败。网络错误只记录超时/取消/DNS/连接等分类与安全描述，避免url.Error泄漏query密钥。
- httpx.Fail：只有未被转换的普通error在统一出口记录一次安全错误；已知业务错误保留业务code及类别。auth转换ErrInternal前记录底层失败。无需扩大公共Err结构或把根因返回客户端。
- 默认成功依赖操作为DEBUG，错误为ERROR，慢操作为WARN；关键业务步骤成功为INFO。保留现有SLOW_SQL_MS=200；请求慢阈值1s，整轮Worker慢阈值5s。其他依赖阈值由实现者根据既有超时选择，不能改变调用超时。

## 6. Worker 与业务关键步骤

首批范围：家庭码生成/换码/作废及preview/confirm结果；微信登录；notify任务发送、升级和回执；ingest开始、识别结果持久化、确认与恢复；schedule/task提醒扫描与实例续展摘要。

- 每轮扫描生成batch_id，各扫描步骤记录耗时和失败摘要。空扫描成功降为DEBUG，不每30秒输出大量INFO。
- notify/ingest单条执行生成run_id，记录job_type/job_id、family_id（来自读取的持久化任务）、已知stage、已知旧/新状态、duration_ms和outcome。
- 仅在数据库更新确认成功后记录state_changed/succeeded；更新失败不能打印成功。幂等回放、无需发送、membership拒绝等记录replayed/suppressed，不能混同发送失败。
- retry/attempt字段仅使用已经存在且语义明确的计数；notify没有持久化次数时省略，不新增业务字段或通过额外读写计算次数。
- ingest后台启动保留最小日志元数据，创建独立run_id；继续脱离HTTP取消，使用独立context，不能直接保留请求完整context或持有Hertz RequestContext。只复制安全标识，不延长认证token/图片/请求body生命周期。
- Worker重启后只保证job_id关联：API任务创建日志和Worker处理日志均输出同一job_type/job_id，再由各自request_id/run_id细查。
- 不为日志新增数据库查询；使用操作已有输入和已读取数据。任务并发、锁、heartbeat、取消、幂等与重试状态不变。

## 7. 数据边界、输出及成本

不记录token/session_key/access_token、AppSecret/APIKey、加入码/credential、手机号、孩子姓名、openid、图片/模型原文、业务payload、请求/响应body、SQL参数、完整URL query或含个人数据的第三方错误体。

cause不是任意err.Error()：优先使用错误类型/厂商码/固定安全描述；确需Redis语法等原因时，只保留已知安全片段并截断256字节。未知错误只保留分类及操作，避免“先原样输出再截断”伪脱敏。message也受同样约束，禁止在Printf兼容调用中保留敏感原文。

默认不记录原始IP及User-Agent；认证后的数字user_id/family_id/job_id仅用于服务器诊断。限制字段长度，JSON编码处理控制字符；不输出客户端自定义任意键值。

继续stdout/stderr → /var/log/ifami-api.log /var/log/ifami-worker.log，由现有systemd与logrotate管理。默认INFO；DEBUG仅短期本地或排障使用，仍遵守脱敏规则。配置如需新增LOG_LEVEL，仅影响日志，不要求部署新增服务。

JSON会增加单行大小，取消全量SQL/query、空扫描INFO及重复cause可控制日志量。采用同步slog文件出口，首期不引入日志缓冲队列；文件I/O失败不改变业务返回或追加重试，但磁盘慢/满仍是运维风险。logrotate的copytruncate原有丢行窗口保持并在操作手册说明，不声称日志等同事务审计。

## 8. 启动诊断、影响范围与回滚

启动输出版本和配置是否存在（仅布尔，不输出密钥长度/值）。Redis INFO server 探测只读取版本，最多增加1s启动诊断等待；无权限或不可达时记录unknown及警告，不增加启动门禁，不改变原有降级能力。MySQL打开失败仍按现有流程退出；不扩大healthz为依赖健康接口。

实现影响：
- cmd/ifami/main.go：日志初始化、请求关联/摘要、Worker批次、启动诊断。
- internal/pkg/logx、httpx、auth、db、wechat：上下文、公共字段、错误转换与安全日志。
- internal/identity/join_registry.go、join_code.go及登录相关边界：根因与业务结果。
- internal/notify、ingest、schedule/task扫描边界：任务阶段、持久化结果及依赖耗时。
- internal/pkg/config及deploy/.env.example：如增加LOG_LEVEL配置，默认INFO。
- docs/部署操作手册.md：JSON日志查询示例、字段解释、日志失败与轮转边界；wiki/status/current.md记录验收状态。

无数据模型、迁移、小程序或原型改动。保持模块接口边界。原共享工作区已有修改不得覆盖，实施从已核对main的隔离工作树开展。

回滚：恢复前一二进制即可，无数据库回滚。LOG_LEVEL不能恢复旧文本格式；如人工脚本依赖文本格式，应在验收前改为JSON字段查询。发布/推送按用户明确授权执行。

## 9. 验收标准

1. HTTP：合法/非法/缺失/重复request_id均可处理；返回ID与请求/根因/SQL日志关联；正常、4xx、5xx、未匹配路由、取消与并发场景没有串号或误写已鉴权family_id。
2. 错误：真实Redis6.0用外置故障脚本复现syntax error，生产脚本保持兼容修复；断连/超时、Reserve/Finish丢响应重试、MySQL错误/慢查询、微信HTTP失败及200业务失败可定位operation和安全原因。
3. Worker：任务创建→执行→成功/失败/抑制/重试/回执日志可由job_id关联；重启后继续关联；失败更新不打印成功，不新增预算/任务写操作。
4. 安全：使用含token、加入码、手机号、孩子姓名、图片内容、SQL字面量、带密钥URL及第三方错误回显的输入，检查所有应用日志不包含这些值；DEBUG亦同。无法安全解析的原因按固定分类输出。
5. 回归：go test ./...、go vet ./...、go build ./...、identity/notify/ingest相关race、真实MySQL/Redis6及现有Redis7测试、公开API角色与异常链路、页面行为27项；无前端修改无需重复官方页面编译，已有页面行为用于验证响应契约。
6. 开销：固定数据/并发/LOG_LEVEL对照接入前后p50/p95、CPU、RSS与日志字节量。建议目标为p95增幅不超过5%（低延迟场景同时报告绝对毫秒值），未达目标先减少成功日志和重复处理再复测；此为待测目标，不是当前结论。
7. 运维：确认每行应用JSON可解析、level可过滤、version可定位；用request_id/job_id查询能在测试故障中定位根因。文件写入失败测试不改变业务结果；保留14天轮转方案但不能保证磁盘容量足以承载任意DEBUG日志量。

本阶段只提交技术设计。用户确认本设计后，再编写实现计划并开始业务/配置代码修改。
