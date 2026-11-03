-- 拍照导入日程：一次导入为一个 ingest_job（docs/技术方案设计.md §5.5.3）。
-- 原图不入库：落 INGEST_DIR 本地临时目录，任务终态即删；images 只存序号与孩子标注。
CREATE TABLE ingest_jobs (
  job_id          BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  family_id       BIGINT UNSIGNED NOT NULL,
  creator_user_id BIGINT UNSIGNED NOT NULL,
  status          VARCHAR(16) NOT NULL COMMENT 'uploading|pending|running|done|failed|confirmed',
  images          JSON NOT NULL COMMENT '[{seq,child_member_id(0=自动识别),status}]，不存路径',
  result_enc      VARBINARY(65535) NULL COMMENT '草稿 JSON，字段级加密（含孩子姓名/原文片段）',
  stats           JSON NULL COMMENT '耗时/草稿数/低置信数/确认数',
  error_code      INT NOT NULL DEFAULT 0,
  attempts        INT NOT NULL DEFAULT 0 COMMENT '执行次数（Worker 接管重跑上限 2）',
  heartbeat_at    DATETIME NULL,
  created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  finished_at     DATETIME NULL,
  KEY idx_family_created (family_id, created_at),
  KEY idx_status_heartbeat (status, heartbeat_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
