CREATE TABLE schedule_feedbacks (
  feedback_id        BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  family_id          BIGINT UNSIGNED NOT NULL,
  instance_id        BIGINT UNSIGNED NOT NULL,
  schedule_id        BIGINT UNSIGNED NOT NULL,
  reporter_member_id BIGINT UNSIGNED NOT NULL,
  reason             VARCHAR(64) NOT NULL,
  status             VARCHAR(16) NOT NULL DEFAULT 'pending',
  last_reported_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uk_instance_pending (family_id, instance_id, status),
  KEY idx_family_status_time (family_id, status, updated_at),
  KEY idx_instance (family_id, instance_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
