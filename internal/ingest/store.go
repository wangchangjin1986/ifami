package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
)

// Store 只读写本模块的 ingest_jobs 表。
type Store struct {
	db *gorm.DB
}

func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

func (s *Store) CreateJob(ctx context.Context, j *Job) error {
	return s.db.WithContext(ctx).Create(j).Error
}

// FindJob 按 family_id + job_id 查任务，未找到返回 (nil, nil)。
func (s *Store) FindJob(ctx context.Context, familyID, jobID uint64) (*Job, error) {
	var j Job
	err := s.db.WithContext(ctx).Where("family_id = ? AND job_id = ?", familyID, jobID).First(&j).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// FindJobByID Worker / 后台执行用（不带家庭约束）。
func (s *Store) FindJobByID(ctx context.Context, jobID uint64) (*Job, error) {
	var j Job
	err := s.db.WithContext(ctx).Where("job_id = ?", jobID).First(&j).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// ListJobs 列家庭近 7 天的任务（可按状态过滤），新的在前。
func (s *Store) ListJobs(ctx context.Context, familyID uint64, status string) ([]Job, error) {
	q := s.db.WithContext(ctx).
		Where("family_id = ? AND created_at >= ?", familyID, time.Now().UTC().Add(-7*24*time.Hour))
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var rows []Job
	err := q.Order("job_id DESC").Limit(20).Find(&rows).Error
	return rows, err
}

// gormJSON 把值序列化为 JSON 文本：map 形式的 Updates 不经过 serializer:json，需手动序列化。
func gormJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

// SaveImages 覆盖 images 字段（仅 uploading 状态下）。
func (s *Store) SaveImages(ctx context.Context, j *Job) error {
	res := s.db.WithContext(ctx).Model(&Job{}).
		Where("job_id = ? AND status = ?", j.JobID, StatusUploading).
		Update("images", gormJSON(j.Images))
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrBadState
	}
	return nil
}

// Transition 条件状态迁移（CAS）：仅当当前状态 ∈ from 时更新，返回是否成功。
func (s *Store) Transition(ctx context.Context, jobID uint64, from []string, fields map[string]any) (bool, error) {
	res := s.db.WithContext(ctx).Model(&Job{}).
		Where("job_id = ? AND status IN ?", jobID, from).
		Updates(fields)
	return res.RowsAffected > 0, res.Error
}

// Claim 抢占执行权（CAS）：pending → running；或心跳早于 staleBefore 的 running → running（Worker 接管）。
// 同一任务在 API 与 Worker 之间只会有一方抢到。
func (s *Store) Claim(ctx context.Context, jobID uint64, now time.Time, staleBefore *time.Time) (bool, error) {
	q := s.db.WithContext(ctx).Model(&Job{}).Where("job_id = ?", jobID)
	if staleBefore == nil {
		q = q.Where("status = ?", StatusPending)
	} else {
		q = q.Where("(status = ? AND heartbeat_at < ?) OR (status = ? AND heartbeat_at < ?)",
			StatusPending, *staleBefore, StatusRunning, *staleBefore)
	}
	res := q.Updates(map[string]any{
		"status": StatusRunning, "heartbeat_at": now, "attempts": gorm.Expr("attempts + 1"),
	})
	return res.RowsAffected > 0, res.Error
}

// Heartbeat 刷新执行心跳。
func (s *Store) Heartbeat(ctx context.Context, jobID uint64, at time.Time) error {
	return s.db.WithContext(ctx).Model(&Job{}).
		Where("job_id = ? AND status = ?", jobID, StatusRunning).
		Update("heartbeat_at", at).Error
}

// SaveFinal 写入终态结果（running → done/failed）。
func (s *Store) SaveFinal(ctx context.Context, j *Job) (bool, error) {
	res := s.db.WithContext(ctx).Model(&Job{}).
		Where("job_id = ? AND status = ?", j.JobID, StatusRunning).
		Updates(map[string]any{
			"status":      j.Status,
			"images":      gormJSON(j.Images),
			"result_enc":  j.ResultEnc,
			"stats":       gormJSON(j.Stats),
			"error_code":  j.ErrorCode,
			"finished_at": j.FinishedAt,
		})
	return res.RowsAffected > 0, res.Error
}

// ListStale 需 Worker 接管的任务：pending / running 且心跳（pending 时为开始时间）早于 before。
func (s *Store) ListStale(ctx context.Context, pendingBefore, runningBefore time.Time) ([]Job, error) {
	var rows []Job
	err := s.db.WithContext(ctx).
		Where("(status = ? AND heartbeat_at < ?) OR (status = ? AND heartbeat_at < ?)",
			StatusPending, pendingBefore, StatusRunning, runningBefore).
		Limit(20).Find(&rows).Error
	return rows, err
}

// CountImagesToday 家庭当天（UTC）已提交识别的图片数，用于额度控制。
func (s *Store) CountImagesToday(ctx context.Context, familyID uint64, dayStart time.Time) (int, error) {
	var rows []Job
	err := s.db.WithContext(ctx).Select("images").
		Where("family_id = ? AND created_at >= ? AND status <> ?", familyID, dayStart, StatusUploading).
		Find(&rows).Error
	if err != nil {
		return 0, err
	}
	n := 0
	for i := range rows {
		n += len(rows[i].Images)
	}
	return n, nil
}
