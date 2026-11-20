# 小程序 / 后端兼容矩阵

更新日期：2026-11-20

| 小程序能力 | 后端接口 / 字段要求 | 旧后端或旧小程序行为 |
|---|---|---|
| 家庭加入码面板 | `GET/POST/DELETE /v1/families/:fid/join-code`；三者均要求 Bearer 与显式 `X-Family-Id=fid`；POST 要求 `Idempotency-Key`；DELETE body 仅含 `generation_id` | 旧 `/invitations` 不再签发，返回 `1015`；新版小程序不分享、复制或保存加入链接 |
| 加入码展示 | `has_code=true` 时才使用 `join_code/generation_id/qr_content/expires_at`；二维码协议 `IFAMI_JOIN_V1:<6位数字>:<32位小写hex generation>:<credential>` | `has_code=false` 的空字段和零时间不绘制；二维码完全在本地 canvas 生成 |
| 四步加入 | `POST /v1/family-join/preview` 与 `/confirm`；preview 手输仅发 `join_code`，扫码额外发 `generation_id/credential`；confirm 仅发五个冻结字段 | 旧 `/v1/families/join` 返回 `1015`；新版不发送 `role/is_admin/user_id/family_id/client_ip`，不接收新 session token |
| 加入结果恢复 | confirm 返回 `family_id/member_id/role/is_admin/relation_label/already_member`；随后刷新 `/v1/me` | confirm 网络失败或 HTTP 5xx（含 HTTP500/code5000）先用 `/v1/me` 核对目标家庭；查询不可用时保留已确认家庭、未知状态与重试入口，不重发 confirm；仅查询成功确认目标 membership 不存在才可重发。已加入但 `/me` 失败显示成功待刷新 |
| 实例详情 | `GET /v1/schedules/instances/:iid`，带当前 `X-Family-Id`；成功返回 `InstanceView`（含 `family_id/status`） | 新版详情不读取 URL 中的 title/location/note；`2004/403` 显示安全错误态；无 iid 的旧模板入口需先经授权模板列表确认 |
| 照护人今日 | `GET /v1/schedules/instances?start&end` 由服务端按当前 membership 约束 owner | 新版只请求当前家庭，不在客户端提供多家庭往返；空态为“今天没有分配给你的事项” |
| 普通家长权限 | `/v1/me.memberships[].is_admin` 是录入、编辑、成员管理入口的实际开关 | role=`parent` 但 `is_admin=false` 保留四 Tab、查看与打卡；不显示 FAB、导入、任务维护、添加孩子或成员管理；成员 DELETE 包括自身目标均返回 HTTP403/code1006，仅管理员可移除成员且保留末位保护 |
| 打卡纠错 | checkin 列表需返回稳定 `submit_member_id`；成员列表提供 `member_id/user_id` | 删除入口只对有效管理员或 `submit_member_id == 当前用户稳定 member_id` 显示；不按称谓或名称推断 |

泛化日程提醒（分工解除/旧提醒）业务 payload 仅保留 type/title/page；真实订阅模板 `thing1/time2/thing3` 中 `time2` 使用通知创建时间的上海 HH:MM（创建时间零值时用当前处理时间），不回填原日程时间、地点或备注。

家庭码管理操作回执窗口为 30 分钟。窗口内同一次未知结果重试复用原 `Idempotency-Key`；超过窗口或页面经历未知后台间隔时先 GET 当前码，确认无码后才生成新 key。

二维码生成器固定为 `qrcode-generator@1.4.4`，源码位于 `vendor/qrcode-generator/qrcode.js`，许可见同目录 `LICENSE`（MIT）。

## 独立复验修复补充（2026-11-20）

- 结果未知时，原提交家庭独立于当前输入和 preview 保存；重新校验、改码或扫码仍先核对原家庭。`/me` 不可用时保留恢复入口，仅查明原关系不存在后才允许新提交。
- 码管理 GET/POST/DELETE 共用序号、当前家庭及面板状态检查；关闭/后台使旧响应失效。
- 后端失败计数采用原子预算占用与完成归还；HTTP 字段、错误码和原阈值不变。详见 `docs/bugs/2026-11-20-家人加入独立复验修复.md`。
