package schedule

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
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
// 已取消的单实例会随重建丢失（Phase 0 简化，见 ADR-003 关键推论 3）。
func (s *Store) ReplaceFutureInstances(ctx context.Context, sch *Schedule, instances []ScheduleInstance, from time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(sch).Error; err != nil {
			return err
		}
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

// RemoveSchedule 事务：软删模板（status=removed）+ 删除未来实例。
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
		return tx.Where("schedule_id = ? AND occur_date >= ?", scheduleID, from).
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
func (s *Store) UpdateInstance(ctx context.Context, instanceID, familyID uint64, updates map[string]any) error {
	res := s.db.WithContext(ctx).
		Model(&ScheduleInstance{}).
		Where("instance_id = ? AND family_id = ?", instanceID, familyID).
		Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrInstanceNotFound
	}
	return nil
}

// FindConflicts 冲突检测：同孩子或同责任人时间重叠的 active 实例（排除指定模板）。
// 时间重叠：对方 start_at < endAt 且 COALESCE(对方 end_at, 对方 start_at) > startAt。
func (s *Store) FindConflicts(ctx context.Context, familyID, childMemberID, ownerMemberID uint64, startAt, endAt time.Time, excludeScheduleID uint64) ([]ScheduleInstance, error) {
	q := s.db.WithContext(ctx).
		Where("family_id = ? AND status = ?", familyID, StatusActive).
		Where("start_at < ? AND COALESCE(end_at, start_at) > ?", endAt, startAt).
		Where("(child_member_id = ? OR owner_member_id = ?)", childMemberID, ownerMemberID)
	if excludeScheduleID != 0 {
		q = q.Where("schedule_id <> ?", excludeScheduleID)
	}
	var rows []ScheduleInstance
	err := q.Order("start_at ASC").Find(&rows).Error
	return rows, err
}
