-- 0010_add_checkin_soft_delete.sql — 打卡记录软删除，保留历史引用并支持统计过滤
ALTER TABLE checkins
  ADD COLUMN status VARCHAR(16) NOT NULL DEFAULT 'active' COMMENT 'active|removed',
  ADD COLUMN deleted_at DATETIME NULL COMMENT '软删除时间',
  ADD COLUMN deleted_by_member_id BIGINT UNSIGNED NULL COMMENT '执行删除的家庭成员',
  ADD KEY idx_family_status_time (family_id, status, created_at),
  ADD KEY idx_task_status_time (task_id, status, created_at);
