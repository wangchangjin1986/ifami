ALTER TABLE checkins
  ADD KEY idx_family_child_time (family_id, child_member_id, created_at);

ALTER TABLE tasks
  ADD KEY idx_family_status_task (family_id, status, task_id);
