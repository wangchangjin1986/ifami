-- 0001_init.sql — ifami V1 核心表（docs/技术方案设计.md §4）
-- 分片键 Day 1 就位：业务表 family_id，用户维度表 user_id

CREATE TABLE users (
  user_id    BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  phone_enc  VARBINARY(255) NULL COMMENT '字段级加密',
  nick       VARCHAR(64) NOT NULL DEFAULT '',
  avatar     VARCHAR(255) NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE identities (
  id         BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  user_id    BIGINT UNSIGNED NOT NULL,
  channel    VARCHAR(16) NOT NULL COMMENT 'miniapp|offiaccount|app',
  appid      VARCHAR(64) NOT NULL,
  openid     VARCHAR(64) NOT NULL,
  unionid    VARCHAR(64) NULL COMMENT '预留：升个体户绑定开放平台后回填，跨端合并用',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE KEY uk_channel_openid (channel, appid, openid),
  KEY idx_user (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE families (
  family_id  BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  name       VARCHAR(64) NOT NULL,
  creator_id BIGINT UNSIGNED NOT NULL,
  settings   JSON NULL COMMENT '含主理人模式 owner_ids 等',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE members (
  member_id      BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  family_id      BIGINT UNSIGNED NOT NULL,
  user_id        BIGINT UNSIGNED NULL COMMENT 'NULL=虚拟成员（孩子），认领后回填',
  type           VARCHAR(8) NOT NULL COMMENT 'real|virtual',
  role           VARCHAR(16) NOT NULL COMMENT 'admin|parent|grandparent|child',
  relation_label VARCHAR(16) NOT NULL DEFAULT '' COMMENT '称谓：奶奶/外公…',
  name_enc       VARBINARY(255) NULL COMMENT '孩子姓名，字段级加密；真实成员 NULL（姓名在 users.nick）',
  phone_enc      VARBINARY(255) NULL COMMENT '孩子手机号可选',
  status         VARCHAR(16) NOT NULL DEFAULT 'active',
  invited_by     BIGINT UNSIGNED NULL,
  created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE KEY uk_family_user (family_id, user_id),
  KEY idx_family (family_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE schedules (
  schedule_id     BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  family_id       BIGINT UNSIGNED NOT NULL,
  child_member_id BIGINT UNSIGNED NOT NULL,
  title           VARCHAR(64) NOT NULL,
  type            VARCHAR(16) NOT NULL COMMENT 'course|pickup|activity|reminder',
  start_date      DATE NOT NULL COMMENT '首日；重复日程物化起点（单次=发生日）',
  rrule           VARCHAR(255) NULL COMMENT '重复规则 RFC5545 子集（FREQ=WEEKLY;BYDAY=…）；单次为 NULL',
  start_time      TIME NOT NULL,
  end_time        TIME NULL COMMENT 'NULL=无结束时间（纯提醒/截止点）',
  location        VARCHAR(128) NOT NULL DEFAULT '',
  note            VARCHAR(255) NOT NULL DEFAULT '' COMMENT '携带物品等',
  owner_member_id BIGINT UNSIGNED NOT NULL COMMENT '责任人',
  status          VARCHAR(16) NOT NULL DEFAULT 'active',
  created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_family (family_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE schedule_instances (
  instance_id     BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  schedule_id     BIGINT UNSIGNED NOT NULL,
  family_id       BIGINT UNSIGNED NOT NULL,
  child_member_id BIGINT UNSIGNED NOT NULL,
  title           VARCHAR(64) NOT NULL COMMENT '冗余自 schedules（物化快照，视图只读本表）',
  type            VARCHAR(16) NOT NULL,
  occur_date      DATE NOT NULL,
  start_at        DATETIME NOT NULL,
  end_at          DATETIME NULL,
  location        VARCHAR(128) NOT NULL DEFAULT '',
  note            VARCHAR(255) NOT NULL DEFAULT '',
  owner_member_id BIGINT UNSIGNED NOT NULL,
  status          VARCHAR(16) NOT NULL DEFAULT 'active' COMMENT 'active|cancelled|done',
  KEY idx_schedule (schedule_id),
  KEY idx_family_date (family_id, occur_date),
  KEY idx_child_date (child_member_id, occur_date),
  KEY idx_owner_date (owner_member_id, occur_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE tasks (
  task_id         BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  family_id       BIGINT UNSIGNED NOT NULL,
  child_member_id BIGINT UNSIGNED NOT NULL,
  name            VARCHAR(64) NOT NULL,
  goal_type       VARCHAR(16) NOT NULL COMMENT 'photo|timer|count',
  goal_value      INT NOT NULL DEFAULT 0,
  freq            VARCHAR(32) NOT NULL DEFAULT 'daily',
  owner_member_id BIGINT UNSIGNED NOT NULL COMMENT '责任父母（提醒对象）',
  status          VARCHAR(16) NOT NULL DEFAULT 'active',
  created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_family (family_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE checkins (
  checkin_id       BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  task_id          BIGINT UNSIGNED NOT NULL,
  family_id        BIGINT UNSIGNED NOT NULL,
  child_member_id  BIGINT UNSIGNED NOT NULL,
  submit_member_id BIGINT UNSIGNED NOT NULL COMMENT '实际提交人（代打=父母）',
  payload          JSON NULL COMMENT '计时/计数/照片',
  media_tos_key    VARCHAR(255) NULL,
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_task_time (task_id, created_at),
  KEY idx_family_time (family_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE notify_jobs (
  job_id         BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  family_id      BIGINT UNSIGNED NOT NULL,
  target_user_id BIGINT UNSIGNED NOT NULL,
  level          VARCHAR(8) NOT NULL COMMENT 'strong|medium|weak',
  channel_state  JSON NULL COMMENT '各通道发送/回执状态',
  payload        JSON NULL,
  status         VARCHAR(16) NOT NULL DEFAULT 'pending',
  next_check_at  DATETIME NULL COMMENT 'Worker 轮询：升级策略计时',
  created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_poll (status, next_check_at),
  KEY idx_family (family_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE events_stream (
  id         BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  family_id  BIGINT UNSIGNED NOT NULL,
  type       VARCHAR(32) NOT NULL COMMENT 'checkin_done|streak|goal_hit|schedule_done',
  ref_id     BIGINT UNSIGNED NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '千人千面激励预留钩子，V1 只写不消费',
  KEY idx_family_time (family_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
