package task

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// Store 是 task 模块的 DB 访问层，读写 tasks / checkins / events_stream（打卡事件）。
type Store struct {
	db *gorm.DB
}

func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// CreateTask 创建打卡任务。
func (s *Store) CreateTask(ctx context.Context, t *Task) error {
	return s.db.WithContext(ctx).Create(t).Error
}

// FindTask 查 active 任务，未找到返回 (nil, nil)。
func (s *Store) FindTask(ctx context.Context, familyID, taskID uint64) (*Task, error) {
	var t Task
	err := s.db.WithContext(ctx).
		Where("family_id = ? AND task_id = ? AND status = ?", familyID, taskID, StatusActive).
		First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ListTasks 列家庭全部 active 任务。
func (s *Store) ListTasks(ctx context.Context, familyID uint64) ([]Task, error) {
	var rows []Task
	err := s.db.WithContext(ctx).
		Where("family_id = ? AND status = ?", familyID, StatusActive).
		Order("task_id DESC").
		Find(&rows).Error
	return rows, err
}

// UpdateTask 更新任务（部分更新）。
func (s *Store) UpdateTask(ctx context.Context, taskID, familyID uint64, updates map[string]any) error {
	res := s.db.WithContext(ctx).
		Model(&Task{}).
		Where("task_id = ? AND family_id = ? AND status = ?", taskID, familyID, StatusActive).
		Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrTaskNotFound
	}
	return nil
}

// RemoveTask 软删任务（status=removed）。
func (s *Store) RemoveTask(ctx context.Context, familyID, taskID uint64) error {
	res := s.db.WithContext(ctx).
		Model(&Task{}).
		Where("family_id = ? AND task_id = ? AND status = ?", familyID, taskID, StatusActive).
		Update("status", StatusRemoved)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrTaskNotFound
	}
	return nil
}

// CreateCheckinWithEvents 事务：写打卡记录 + 打卡事件（ADR-004，事件流只写不消费）。
func (s *Store) CreateCheckinWithEvents(ctx context.Context, c *Checkin, events []Event) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(c).Error; err != nil {
			return err
		}
		for i := range events {
			events[i].FamilyID = c.FamilyID
			events[i].RefID = &c.CheckinID
		}
		if len(events) > 0 {
			return tx.Create(&events).Error
		}
		return nil
	})
}

// CreateEvents 仅写事件流（打卡里程碑等后续事件，不关联新 checkin）。
func (s *Store) CreateEvents(ctx context.Context, events []Event) error {
	if len(events) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Create(&events).Error
}

// FindCheckinByIdempotency 幂等查询，未命中返回 (nil, nil)。
func (s *Store) FindCheckinByIdempotency(ctx context.Context, familyID uint64, key string) (*Checkin, error) {
	var c Checkin
	err := s.db.WithContext(ctx).
		Where("family_id = ? AND idempotency_key = ?", familyID, key).
		First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ListCheckinsByTask 列任务全部打卡（进步曲线/连续天数数据源）。
func (s *Store) ListCheckinsByTask(ctx context.Context, familyID, taskID uint64) ([]Checkin, error) {
	var rows []Checkin
	err := s.db.WithContext(ctx).
		Where("family_id = ? AND task_id = ?", familyID, taskID).
		Order("created_at ASC").
		Find(&rows).Error
	return rows, err
}

// ListCheckinsByFamily 列家庭打卡时间线；[start, end) 半开区间，可按孩子筛选。
func (s *Store) ListCheckinsByFamily(ctx context.Context, familyID uint64, start, end time.Time, childID uint64) ([]Checkin, error) {
	q := s.db.WithContext(ctx).
		Where("family_id = ? AND created_at >= ? AND created_at < ?", familyID, start, end)
	if childID != 0 {
		q = q.Where("child_member_id = ?", childID)
	}
	var rows []Checkin
	err := q.Order("created_at DESC").Find(&rows).Error
	return rows, err
}

// ListRemindableTasks 扫需提醒的任务：active 且 remind_time 非空（R3.3 打卡提醒数据源）。
func (s *Store) ListRemindableTasks(ctx context.Context) ([]Task, error) {
	var rows []Task
	err := s.db.WithContext(ctx).
		Where("status = ? AND remind_time IS NOT NULL AND remind_time <> ''", StatusActive).
		Find(&rows).Error
	return rows, err
}

// CountCheckinsSince 任务自 since（UTC，含）起的打卡次数（「每周 N 次」达标判定）。
func (s *Store) CountCheckinsSince(ctx context.Context, familyID, taskID uint64, since time.Time) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&Checkin{}).
		Where("family_id = ? AND task_id = ? AND created_at >= ?", familyID, taskID, since).
		Count(&n).Error
	return n, err
}

// HasCheckinOn 任务在某天（UTC 当天 [day, day+1) 半开区间）是否已打卡。
func (s *Store) HasCheckinOn(ctx context.Context, familyID, taskID uint64, day time.Time) (bool, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&Checkin{}).
		Where("family_id = ? AND task_id = ? AND created_at >= ? AND created_at < ?", familyID, taskID, day, day.AddDate(0, 0, 1)).
		Count(&n).Error
	return n > 0, err
}
