-- 手动创建日程幂等：弱网重试同一 Idempotency-Key 只创建一个模板。
-- uk 对 NULL 不约束：未传幂等键的历史/内部批量创建不受影响。
ALTER TABLE schedules
  ADD COLUMN idempotency_key VARCHAR(64) NULL COMMENT '客户端幂等键，弱网重试去重' AFTER owner_member_id,
  ADD UNIQUE KEY uk_schedule_idem (family_id, idempotency_key);
