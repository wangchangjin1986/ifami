package schedule

import (
	"context"
	"encoding/json"
	"errors"
	drivermysql "github.com/go-sql-driver/mysql"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Store 是 schedule 模块的 DB 访问层，只读写本模块的表（schedules / schedule_instances）。
type Store struct {
	db *gorm.DB
}

func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// CreateSchedule 事务创建模板 + 物化实例。
func (s *Store) CreateSchedule(ctx context.Context, sch *Schedule, instances []ScheduleInstance) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(sch).Error; err != nil {
			return err
		}
		for i := range instances {
			instances[i].ScheduleID = sch.ScheduleID
		}
		if len(instances) > 0 {
			return tx.Create(&instances).Error
		}
		return nil
	})
}

// CreateSchedules 事务批量创建模板 + 物化实例，用于模板化录入确认入库。
func (s *Store) CreateSchedules(ctx context.Context, schedules []*Schedule, instances [][]ScheduleInstance, batchKey ...string) error {
	if len(schedules) == 0 || len(schedules) != len(instances) {
		return ErrBadParam
	}
	key := ""
	if len(batchKey) > 0 {
		key = batchKey[0]
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if key != "" {
			if err := tx.Create(&ScheduleBatch{FamilyID: schedules[0].FamilyID, BatchKey: key, ScheduleIDs: []uint64{}}).Error; err != nil {
				return err
			}
		}
		for i, sch := range schedules {
			if err := tx.Create(sch).Error; err != nil {
				return err
			}
			insts := instances[i]
			for j := range insts {
				insts[j].ScheduleID = sch.ScheduleID
			}
			if len(insts) > 0 {
				if err := tx.Create(&insts).Error; err != nil {
					return err
				}
			}
		}
		if key != "" {
			ids := make([]uint64, len(schedules))
			for i := range schedules {
				ids[i] = schedules[i].ScheduleID
			}
			encoded, err := json.Marshal(ids)
			if err != nil {
				return err
			}
			return tx.Model(&ScheduleBatch{}).Where("family_id = ? AND batch_key = ?", schedules[0].FamilyID, key).Update("schedule_ids", string(encoded)).Error
		}
		return nil
	})
}

func isDuplicateKey(err error) bool {
	var mysqlErr *drivermysql.MySQLError
	return errors.Is(err, gorm.ErrDuplicatedKey) || (errors.As(err, &mysqlErr) && mysqlErr.Number == 1062)
}

func (s *Store) FindBatchResult(ctx context.Context, familyID uint64, key string) ([]uint64, bool, error) {
	var row ScheduleBatch
	err := s.db.WithContext(ctx).Where("family_id = ? AND batch_key = ?", familyID, key).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if len(row.ScheduleIDs) == 0 {
		return nil, false, errors.New("committed schedule batch has no result")
	}
	return row.ScheduleIDs, true, nil
}

// FindSchedule 查 active 模板，未找到返回 (nil, nil)。
func (s *Store) FindSchedule(ctx context.Context, familyID, scheduleID uint64) (*Schedule, error) {
	var sch Schedule
	err := s.db.WithContext(ctx).
		Where("family_id = ? AND schedule_id = ? AND status = ?", familyID, scheduleID, StatusActive).
		First(&sch).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sch, nil
}

// FindScheduleByIdempotency 按客户端幂等键查已创建模板，未找到返回 (nil, nil)。
func (s *Store) FindScheduleByIdempotency(ctx context.Context, familyID uint64, key string) (*Schedule, error) {
	if key == "" {
		return nil, nil
	}
	var sch Schedule
	err := s.db.WithContext(ctx).
		Where("family_id = ? AND idempotency_key = ? AND status = ?", familyID, key, StatusActive).
		First(&sch).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sch, nil
}

// ListSchedules 列家庭全部 active 模板。
func (s *Store) ListSchedules(ctx context.Context, familyID uint64) ([]Schedule, error) {
	var rows []Schedule
	err := s.db.WithContext(ctx).
		Where("family_id = ? AND status = ?", familyID, StatusActive).
		Order("schedule_id DESC").
		Find(&rows).Error
	return rows, err
}

// ReplaceFutureInstances 事务：全量更新模板 + 删除 from 起的未来实例 + 重建。
// 重建前会保留按 occur_date 可映射的单次取消/完成与改派例外，避免模板编辑静默抹掉实例级调整。
func (s *Store) ReplaceFutureInstances(ctx context.Context, sch *Schedule, instances []ScheduleInstance, from time.Time, previousOwnerID uint64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(sch).Error; err != nil {
			return err
		}
		var existing []ScheduleInstance
		if err := tx.Where("schedule_id = ? AND occur_date >= ?", sch.ScheduleID, from).
			Find(&existing).Error; err != nil {
			return err
		}
		mergeInstanceExceptions(instances, existing, previousOwnerID)
		if err := tx.Where("schedule_id = ? AND occur_date >= ?", sch.ScheduleID, from).
			Delete(&ScheduleInstance{}).Error; err != nil {
			return err
		}
		for i := range instances {
			instances[i].ScheduleID = sch.ScheduleID
		}
		if len(instances) > 0 {
			return tx.Create(&instances).Error
		}
		return nil
	})
}

func mergeInstanceExceptions(instances []ScheduleInstance, existing []ScheduleInstance, templateOwnerID uint64) {
	byDate := make(map[string]ScheduleInstance, len(existing))
	for i := range existing {
		key := existing[i].OccurDate.Format("2006-01-02")
		if existing[i].Status != StatusActive || existing[i].OwnerMemberID != templateOwnerID {
			byDate[key] = existing[i]
		}
	}
	for i := range instances {
		old, ok := byDate[instances[i].OccurDate.Format("2006-01-02")]
		if !ok {
			continue
		}
		if old.Status != StatusActive {
			instances[i].Status = old.Status
		}
		if old.OwnerMemberID != templateOwnerID {
			instances[i].OwnerMemberID = old.OwnerMemberID
		}
	}
}

// RemoveSchedule 事务：软删模板（status=removed）+ 删除 from 当天及之后的已物化实例。
func (s *Store) RemoveSchedule(ctx context.Context, familyID, scheduleID uint64, from time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&Schedule{}).
			Where("family_id = ? AND schedule_id = ? AND status = ?", familyID, scheduleID, StatusActive).
			Update("status", StatusRemoved)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrScheduleNotFound
		}
		return tx.Where("family_id = ? AND schedule_id = ? AND occur_date >= ?", familyID, scheduleID, from).
			Delete(&ScheduleInstance{}).Error
	})
}

// ListInstances 按日期范围列家庭实例（今日/周/月视图读本表）。
func (s *Store) ListInstances(ctx context.Context, familyID uint64, start, end time.Time) ([]ScheduleInstance, error) {
	var rows []ScheduleInstance
	err := s.db.WithContext(ctx).
		Where("family_id = ? AND occur_date >= ? AND occur_date <= ?", familyID, start, end).
		Order("start_at ASC").
		Find(&rows).Error
	return rows, err
}

// ListReminderDueInstances 扫描即将开始且需要触达的实例（在校块不触达）。
func (s *Store) ListReminderDueInstances(ctx context.Context, now, before time.Time) ([]ScheduleInstance, error) {
	var rows []ScheduleInstance
	err := s.db.WithContext(ctx).
		Where("status = ? AND type <> ? AND owner_member_id <> 0", StatusActive, TypeSchool).
		Where("start_at > ? AND start_at <= ?", now, before).
		Order("start_at ASC, instance_id ASC").
		Limit(500).
		Find(&rows).Error
	return rows, err
}

// FindInstance 查实例（family_id + instance_id），未找到返回 (nil, nil)。
func (s *Store) FindInstance(ctx context.Context, familyID, instanceID uint64) (*ScheduleInstance, error) {
	var inst ScheduleInstance
	err := s.db.WithContext(ctx).
		Where("family_id = ? AND instance_id = ?", familyID, instanceID).
		First(&inst).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &inst, nil
}

// UpdateInstance 更新单实例（状态/责任人）。
func (s *Store) UpdateInstance(ctx context.Context, inst *ScheduleInstance, updates map[string]any) error {
	updates["change_version"] = inst.ChangeVersion + 1
	res := s.db.WithContext(ctx).
		Model(&ScheduleInstance{}).
		Where("instance_id = ? AND family_id = ? AND change_version = ?", inst.InstanceID, inst.FamilyID, inst.ChangeVersion).
		Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrBadParam // concurrent modification: reload before retrying
	}
	return nil
}

// CompleteInstanceWithEvent 事务：标记实例完成 + 写 schedule_done 事件。
func (s *Store) CompleteInstanceWithEvent(ctx context.Context, familyID, instanceID uint64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&ScheduleInstance{}).
			Where("instance_id = ? AND family_id = ?", instanceID, familyID).
			Updates(map[string]any{"status": StatusDone, "change_version": gorm.Expr("change_version + 1")})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrInstanceNotFound
		}
		ref := instanceID
		return tx.Create(&Event{FamilyID: familyID, Type: EventScheduleDone, RefID: &ref}).Error
	})
}

// UpsertPendingFeedbackWithEvent 事务：同一实例 pending 反馈去重更新 + 写 schedule_feedback 事件。
func (s *Store) UpsertPendingFeedbackWithEvent(ctx context.Context, f *ScheduleFeedback) error {
	now := time.Now().UTC()
	f.Status = StatusPending
	f.LastReportedAt = now
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "family_id"}, {Name: "pending_instance_id"}},
			DoUpdates: clause.Assignments(map[string]any{
				"reason":             f.Reason,
				"reporter_member_id": f.ReporterID,
				"last_reported_at":   now,
				"updated_at":         now,
			}),
		}).Create(f).Error; err != nil {
			return err
		}
		ref := f.InstanceID
		return tx.Create(&Event{FamilyID: f.FamilyID, Type: EventScheduleFeedback, RefID: &ref}).Error
	})
}

func (s *Store) ListFeedbacks(ctx context.Context, familyID uint64, status string) ([]ScheduleFeedback, error) {
	var rows []ScheduleFeedback
	err := s.db.WithContext(ctx).
		Where("family_id = ? AND status = ?", familyID, status).
		Order("updated_at DESC, feedback_id DESC").
		Limit(100).
		Find(&rows).Error
	return rows, err
}

func (s *Store) UpdateFeedbackStatus(ctx context.Context, familyID, feedbackID uint64, status string) error {
	res := s.db.WithContext(ctx).Model(&ScheduleFeedback{}).
		Where("family_id = ? AND feedback_id = ? AND status = ?", familyID, feedbackID, StatusPending).
		Updates(map[string]any{"status": status, "updated_at": time.Now().UTC()})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrBadParam
	}
	return nil
}

// FindConflicts 冲突检测：同孩子或同责任人时间重叠的 active 实例（排除指定模板）。
// 时间重叠：对方 start_at < endAt 且 COALESCE(对方 end_at, 对方 start_at) > startAt。
// ownerMemberID=0（在校块）时只按孩子判定，避免把其他孩子的在校块误判为同责任人。
func (s *Store) FindConflicts(ctx context.Context, familyID, childMemberID, ownerMemberID uint64, startAt, endAt time.Time, excludeScheduleID uint64) ([]ScheduleInstance, error) {
	q := s.db.WithContext(ctx).
		Where("family_id = ? AND status = ?", familyID, StatusActive).
		Where("start_at < ? AND COALESCE(end_at, start_at) > ?", endAt, startAt)
	if ownerMemberID == 0 {
		q = q.Where("child_member_id = ?", childMemberID)
	} else {
		q = q.Where("(child_member_id = ? OR owner_member_id = ?)", childMemberID, ownerMemberID)
	}
	if excludeScheduleID != 0 {
		q = q.Where("schedule_id <> ?", excludeScheduleID)
	}
	var rows []ScheduleInstance
	err := q.Order("start_at ASC").Find(&rows).Error
	return rows, err
}

// FindConflictCandidates 批量拉取冲突候选实例。调用方再在内存中按 child/owner 与精确时间区间过滤。
// 使用 <= / >= 是为了把零时长提醒（end_at=NULL）同一时间点也纳入候选；相邻非重叠会在内存过滤掉。
func (s *Store) FindConflictCandidates(ctx context.Context, familyID uint64, childMemberIDs, ownerMemberIDs []uint64, minStart, maxEnd time.Time, excludeScheduleID uint64) ([]ScheduleInstance, error) {
	q := s.db.WithContext(ctx).
		Where("family_id = ? AND status = ?", familyID, StatusActive).
		Where("start_at <= ? AND COALESCE(end_at, start_at) >= ?", maxEnd, minStart)
	if len(childMemberIDs) > 0 && len(ownerMemberIDs) > 0 {
		q = q.Where("(child_member_id IN ? OR owner_member_id IN ?)", childMemberIDs, ownerMemberIDs)
	} else if len(childMemberIDs) > 0 {
		q = q.Where("child_member_id IN ?", childMemberIDs)
	} else if len(ownerMemberIDs) > 0 {
		q = q.Where("owner_member_id IN ?", ownerMemberIDs)
	} else {
		return nil, nil
	}
	if excludeScheduleID != 0 {
		q = q.Where("schedule_id <> ?", excludeScheduleID)
	}
	var rows []ScheduleInstance
	err := q.Order("start_at ASC").Find(&rows).Error
	return rows, err
}

// ListRecurringSchedules 列全部 active 且带重复规则的模板（Worker 续展用，跨家庭扫描）。
func (s *Store) ListRecurringSchedules(ctx context.Context) ([]Schedule, error) {
	var rows []Schedule
	err := s.db.WithContext(ctx).
		Where("status = ? AND rrule IS NOT NULL AND rrule <> ''", StatusActive).
		Find(&rows).Error
	return rows, err
}

// MaxOccurDates 返回各模板已物化的最后一个发生日期；无实例的模板不在结果中。
func (s *Store) MaxOccurDates(ctx context.Context, scheduleIDs []uint64) (map[uint64]*time.Time, error) {
	type row struct {
		ScheduleID uint64
		Last       time.Time
	}
	var rows []row
	err := s.db.WithContext(ctx).Model(&ScheduleInstance{}).
		Select("schedule_id, MAX(occur_date) AS last").
		Where("schedule_id IN ?", scheduleIDs).
		Group("schedule_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[uint64]*time.Time, len(rows))
	for i := range rows {
		t := rows[i].Last
		out[rows[i].ScheduleID] = &t
	}
	return out, nil
}

// InsertInstances 批量写入续展实例。
func (s *Store) InsertInstances(ctx context.Context, instances []ScheduleInstance) error {
	if len(instances) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).CreateInBatches(&instances, 200).Error
}
