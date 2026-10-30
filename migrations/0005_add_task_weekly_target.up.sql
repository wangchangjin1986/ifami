-- 0005_add_task_weekly_target.sql — 打卡任务「每周 N 次」达标次数（R3.1/R3.3）
ALTER TABLE tasks
  ADD COLUMN weekly_target INT NOT NULL DEFAULT 0 COMMENT 'freq=weekly 时的每周完成目标次数，0=无';
