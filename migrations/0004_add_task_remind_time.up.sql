-- 0004_add_task_remind_time.sql — 打卡任务提醒时间（R3.3 到点未完成提醒责任父母）
ALTER TABLE tasks
  ADD COLUMN remind_time VARCHAR(5) NULL COMMENT '提醒时间 HH:MM，空=不提醒';
