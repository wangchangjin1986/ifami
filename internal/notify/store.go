package notify

import (
	"context"
	"encoding/json"
	"errors"
	"ifami/internal/pkg/logx"
	"os"
	"strconv"
	"time"

	"gorm.io/gorm"
)

var dueJobBatchSize = envInt("NOTIFY_DUE_JOB_BATCH_SIZE", 200)

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
		Order("next_check_at ASC, job_id ASC").
		Limit(dueJobBatchSize).
		Find(&rows).Error
	return rows, err
}

// ListReddotJobs 返回当前用户待确认的应用内红点任务。
func (s *Store) ListReddotJobs(ctx context.Context, familyID, targetUserID uint64) ([]NotifyJob, error) {
	var rows []NotifyJob
	err := s.db.WithContext(ctx).
		Where("family_id = ? AND target_user_id = ? AND status = ?", familyID, targetUserID, StatusReddot).
		Order("created_at DESC, job_id DESC").
		Limit(50).
		Find(&rows).Error
	return rows, err
}

func envInt(key string, def int) int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil || v <= 0 {
		return def
	}
	return v
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
	if status, ok := updates["status"].(string); ok {
		logx.Info(ctx, "job_state_changed", "job_type", "notify", "job_id", jobID, "state", status, "outcome", "persisted")
	}
	return nil
}
