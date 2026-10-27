-- 0002_add_checkin_idempotency.sql — 打卡提交幂等（弱网重复提交是常态，api.md 写操作幂等要求）
-- uk 对 NULL 不约束：未传幂等键的记录/历史数据不受影响。
ALTER TABLE checkins
  ADD COLUMN idempotency_key VARCHAR(64) NULL COMMENT '客户端幂等键，弱网重试去重',
  ADD UNIQUE KEY uk_checkin_idem (family_id, idempotency_key);
