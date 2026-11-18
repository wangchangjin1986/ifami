-- 未传创建幂等键必须为 NULL，解除 0014 升级后产生的空键占位。
UPDATE schedules SET idempotency_key = NULL WHERE idempotency_key = '';

-- 旧 confirming 没有批次结果，不能因为新表查无记录就认定未提交。
ALTER TABLE ingest_jobs
  ADD COLUMN confirmation_version TINYINT UNSIGNED NOT NULL DEFAULT 0;

-- 变更版本与实例写入一同持久化，用于区分后续有效改派和同次重试。
ALTER TABLE schedule_instances
  ADD COLUMN change_version BIGINT UNSIGNED NOT NULL DEFAULT 0;

-- 批次结果和模板/实例在同一事务提交；跨模块只通过接口查询。
CREATE TABLE schedule_batches (
  family_id BIGINT UNSIGNED NOT NULL,
  batch_key VARCHAR(64) NOT NULL,
  schedule_ids JSON NOT NULL,
  PRIMARY KEY (family_id, batch_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- NULL 不参与唯一冲突：同一实例可保留任意多条 handled/ignored 历史。
ALTER TABLE schedule_feedbacks
  DROP INDEX uk_instance_pending,
  ADD COLUMN pending_instance_id BIGINT UNSIGNED
    GENERATED ALWAYS AS (CASE WHEN status = 'pending' THEN instance_id ELSE NULL END) STORED,
  ADD UNIQUE KEY uk_instance_pending (family_id, pending_instance_id);
