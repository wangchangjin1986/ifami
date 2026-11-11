-- 0011_create_auth_sessions.sql — 登录会话 MySQL 兜底表
-- Redis 在 Phase 0 小内存 ECS 上可能不可用；会话采用本地缓存 + Redis + MySQL 兜底。

CREATE TABLE auth_sessions (
  token      VARCHAR(128) NOT NULL PRIMARY KEY,
  user_id    BIGINT UNSIGNED NOT NULL,
  expires_at DATETIME NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  KEY idx_expires_at (expires_at),
  KEY idx_user_expires (user_id, expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
