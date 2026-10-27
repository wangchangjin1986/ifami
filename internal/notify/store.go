package notify

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
)

// Store 是 notify 模块的 DB 访问层，只读写本模块的表（notify_jobs）。
type Store struct {
	db *gorm.DB
}

func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// CreateJob 创建触达任务。
func (s *Store) CreateJob(ctx context.Context, j *NotifyJob) error {
	return s.db.WithContext(ctx).Create(j).Error
}

// FindJob 查触达任务，未找到返回 (nil, nil)。
func (s *Store) FindJob(ctx context.Context, jobID uint64) (*NotifyJob, error) {
	var j NotifyJob
	err := s.db.WithContext(ctx).First(&j, jobID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// FindJobByBusinessKey 按家庭+业务键查任务（幂等去重），未找到返回 (nil, nil)。
func (s *Store) FindJobByBusinessKey(ctx context.Context, familyID uint64, key string) (*NotifyJob, error) {
	var j NotifyJob
	err := s.db.WithContext(ctx).
		Where("family_id = ? AND business_key = ?", familyID, key).
		First(&j).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// ListDueJobs 扫到期任务（Worker 轮询）：status 匹配且 next_check_at <= before。
func (s *Store) ListDueJobs(ctx context.Context, status string, before time.Time) ([]NotifyJob, error) {
	var rows []NotifyJob
	err := s.db.WithContext(ctx).
		Where("status = ? AND next_check_at <= ?", status, before).
		Order("job_id ASC").
		Find(&rows).Error
	return rows, err
}

// UpdateJob 更新任务（状态/通道状态/下次检查时间）。
// channel_state/payload 是 JSON 列：map 直接传会被 GORM 当作原生 map 报错，
// 需先序列化为 JSON 文本（serializer:json 只对 struct 字段生效，不作用于 map 更新）。
func (s *Store) UpdateJob(ctx context.Context, jobID uint64, updates map[string]any) error {
	for _, k := range []string{"channel_state", "payload"} {
		if v, ok := updates[k]; ok {
			b, err := json.Marshal(v)
			if err != nil {
				return err
			}
			updates[k] = string(b)
		}
	}
	res := s.db.WithContext(ctx).
		Model(&NotifyJob{}).
		Where("job_id = ?", jobID).
		Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrJobNotFound
	}
	return nil
}
