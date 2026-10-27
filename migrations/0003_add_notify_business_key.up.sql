-- 0003_add_notify_business_key.sql — 触达任务业务幂等键
-- 定时触达（打卡提醒等）由 Worker 每轮扫描触发，同一任务同一天只发一条，靠 (family_id, business_key) 去重。
ALTER TABLE notify_jobs
  ADD COLUMN business_key VARCHAR(128) NULL COMMENT '业务幂等键，如 checkin:<task_id>:<YYYY-MM-DD>',
  ADD UNIQUE KEY uk_notify_bizkey (family_id, business_key);
