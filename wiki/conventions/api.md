# API 设计规范

## 基本约定

- 路径前缀 `/v1/`，资源用名词复数：`/v1/families/{id}/schedules`。
- **家庭上下文**：业务请求带 Header `X-Family-Id`，服务端校验 `(user_id, family_id)` membership 与角色（权限中间件统一做，业务 handler 不重复写）。
- 鉴权：会话 token（Redis，7 天滑动过期），`Authorization: Bearer <token>`。
- 接口只增不改；废弃字段保留一个版本周期。兼容矩阵记录于 `miniapp/COMPAT.md`。

## 响应格式

```json
{ "code": 0, "data": { }, "message": "" }
```

- `code=0` 成功；业务错误码 4 位，按模块分段：1xxx identity / 2xxx schedule / 3xxx task / 4xxx notify。
- HTTP 状态码只表达传输层结果（200/400/401/403/500），业务结果一律走 `code`。

## 写操作

- 幂等：创建类接口接受客户端生成的 `Idempotency-Key`（打卡提交、日程保存必须支持——弱网重复提交是常态）。
- 批量入库走事务（AI 识别确认批量写 schedules 必须整体成功或回滚）。
- 冲突检测在写路径同步返回：保存日程时返回冲突列表 + 允许 `force=true` 强制保存。

## 触达相关

- 业务模块**不直接**调微信/短信；产生触达需求 = 写 `notify_jobs`（见 [../architecture/adr-002-notification-receipt.md](../architecture/adr-002-notification-receipt.md)）。
- 回执上报接口：`POST /v1/notify/receipt`（消息跳转落地页加载时调用），必须无鉴权门槛（祖辈点开即算）。

## 限流与安全

- 登录、取号、邀请接口按 IP+用户双维度限流。
- 所有接口入参服务端校验；孩子相关操作二次校验监护人 membership。
