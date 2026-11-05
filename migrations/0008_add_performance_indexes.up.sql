-- 后端性能优化索引：日程冲突检测、实例列表、触达到期批处理。
ALTER TABLE schedule_instances
  ADD KEY idx_family_status_start (family_id, status, start_at),
  ADD KEY idx_family_date_start (family_id, occur_date, start_at);

ALTER TABLE notify_jobs
  ADD KEY idx_poll_order (status, next_check_at, job_id);
