ALTER TABLE members
  ADD COLUMN is_admin TINYINT(1) NOT NULL DEFAULT 0 COMMENT '管理员权限开关，独立于 role';

UPDATE members
SET is_admin = 1, role = 'parent'
WHERE role = 'admin';
