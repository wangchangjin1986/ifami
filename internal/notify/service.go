package notify

import (
	"context"
	"errors"
	"log"
	"time"

	"gorm.io/gorm"
)

// Identity 是 notify 对 identity 模块的最小依赖（消费方定义接口，发订阅消息需 openid）。
type Identity interface {
	OpenID(ctx context.Context, userID uint64) (string, error)
}

// Service 是 notify 模块业务层：触达路由、升级状态机、通道适配、回执。
// 业务模块（schedule/task）通过本 Service 创建触达任务，禁止直接调微信/短信（硬性规则 6）。
type Service struct {
	store   *Store
	id      Identity
	channel Channel
}

func NewService(store *Store, id Identity, ch Channel) *Service {
	return &Service{store: store, id: id, channel: ch}
}

// CreateJobInput 创建触达任务的入参。
type CreateJobInput struct {
	FamilyID     uint64         `json:"family_id"`
	TargetUserID uint64         `json:"target_user_id"`
	Level        string         `json:"level"`
	Payload      map[string]any `json:"payload"`
	BusinessKey  string         `json:"business_key"` // 业务幂等键，同 family 内去重（定时触达 Worker 每轮扫描靠它）
}

// CreateJob 创建触达任务，返回 job_id。立即到期（next_check_at=now），由 Worker 处理。
func (s *Service) CreateJob(ctx context.Context, in *CreateJobInput) (uint64, error) {
	if in.FamilyID == 0 || in.TargetUserID == 0 || !validLevel(in.Level) {
		return 0, ErrBadLevel
	}
	// 业务幂等键：先查后建，命中直接返回已有任务（Worker 每轮扫描都会尝试创建）。
	if in.BusinessKey != "" {
		if existing, err := s.store.FindJobByBusinessKey(ctx, in.FamilyID, in.BusinessKey); err != nil {
			return 0, err
		} else if existing != nil {
			return existing.JobID, nil
		}
	}
	now := time.Now().UTC()
	job := &NotifyJob{
		FamilyID:     in.FamilyID,
		TargetUserID: in.TargetUserID,
		Level:        in.Level,
		Payload:      in.Payload,
		BusinessKey:  in.BusinessKey,
		Status:       StatusPending,
		NextCheckAt:  &now,
		ChannelState: map[string]any{},
	}
	if err := s.store.CreateJob(ctx, job); err != nil {
		// 并发下唯一键兜底：回查已有任务返回（与 task 打卡幂等同一模式）。
		if errors.Is(err, gorm.ErrDuplicatedKey) && in.BusinessKey != "" {
			if existing, e2 := s.store.FindJobByBusinessKey(ctx, in.FamilyID, in.BusinessKey); e2 == nil && existing != nil {
				return existing.JobID, nil
			}
		}
		return 0, err
	}
	return job.JobID, nil
}

// ReportReceipt 回执上报（落地页加载时调用，无鉴权）。sent/reddot → receipted，取消升级。
func (s *Service) ReportReceipt(ctx context.Context, jobID uint64) error {
	job, err := s.store.FindJob(ctx, jobID)
	if err != nil {
		return err
	}
	if job == nil {
		return ErrJobNotFound
	}
	if job.Status != StatusSent && job.Status != StatusReddot {
		return nil // 幂等：非等待回执状态，忽略
	}
	cs := cloneState(job.ChannelState)
	cs["receipted_at"] = time.Now().UTC().Format(time.RFC3339)
	return s.store.UpdateJob(ctx, jobID, map[string]any{
		"status":        StatusReceipted,
		"channel_state": cs,
	})
}

// ProcessDue Worker 轮询入口（30s 粒度）：发送到期任务 + 升级无回执任务。
// 单条处理失败不中断批次（log 后继续）。
func (s *Service) ProcessDue(ctx context.Context) error {
	now := time.Now().UTC()

	pending, err := s.store.ListDueJobs(ctx, StatusPending, now)
	if err != nil {
		return err
	}
	for i := range pending {
		if err := s.processPending(ctx, &pending[i], now); err != nil {
			log.Printf("[notify] process pending job=%d err=%v", pending[i].JobID, err)
		}
	}

	sent, err := s.store.ListDueJobs(ctx, StatusSent, now)
	if err != nil {
		return err
	}
	for i := range sent {
		if err := s.escalateReddot(ctx, &sent[i], now); err != nil {
			log.Printf("[notify] escalate reddot job=%d err=%v", sent[i].JobID, err)
		}
	}

	reddot, err := s.store.ListDueJobs(ctx, StatusReddot, now)
	if err != nil {
		return err
	}
	for i := range reddot {
		if err := s.escalateBackup(ctx, &reddot[i], now); err != nil {
			log.Printf("[notify] escalate backup job=%d err=%v", reddot[i].JobID, err)
		}
	}
	return nil
}

// processPending 处理待发送任务：weak 直接完成；medium 发送后完成；strong 发送后进升级计时。
func (s *Service) processPending(ctx context.Context, job *NotifyJob, now time.Time) error {
	switch job.Level {
	case LevelWeak:
		return s.store.UpdateJob(ctx, job.JobID, map[string]any{"status": StatusDone})

	case LevelMedium:
		cs := cloneState(job.ChannelState)
		if err := s.send(ctx, job); err != nil {
			return err
		}
		cs["sent_at"] = now.Format(time.RFC3339)
		return s.store.UpdateJob(ctx, job.JobID, map[string]any{
			"status":        StatusDone, // medium：当天看到即可，不回执不升级
			"channel_state": cs,
		})

	case LevelStrong:
		cs := cloneState(job.ChannelState)
		if err := s.send(ctx, job); err != nil {
			return err
		}
		cs["sent_at"] = now.Format(time.RFC3339)
		next := now.Add(escalateReddotAfter)
		return s.store.UpdateJob(ctx, job.JobID, map[string]any{
			"status":        StatusSent,
			"next_check_at": &next,
			"channel_state": cs,
		})
	}
	return ErrBadLevel
}

// escalateReddot 强触达 15min 无回执 → 应用内待办红点（通道2），30min 时升级备份。
func (s *Service) escalateReddot(ctx context.Context, job *NotifyJob, now time.Time) error {
	cs := cloneState(job.ChannelState)
	cs["reddot_at"] = now.Format(time.RFC3339)
	next := now.Add(escalateBackupAfter - escalateReddotAfter) // 再过 15min 达 30min
	return s.store.UpdateJob(ctx, job.JobID, map[string]any{
		"status":        StatusReddot,
		"next_check_at": &next,
		"channel_state": cs,
	})
}

// escalateBackup 强触达 30min 无回执 → 备份联系人（通道3，终端）。
// 备份联系人订阅消息的真实发送（需解析配偶 user_id）留 TODO，先标记状态。
func (s *Service) escalateBackup(ctx context.Context, job *NotifyJob, now time.Time) error {
	cs := cloneState(job.ChannelState)
	cs["backup_at"] = now.Format(time.RFC3339)
	return s.store.UpdateJob(ctx, job.JobID, map[string]any{
		"status":        StatusEscalated,
		"channel_state": cs,
	})
}

// send 经通道适配层发送订阅消息（Phase 0 mock；真实微信发送见 channel.go 注释）。
func (s *Service) send(ctx context.Context, job *NotifyJob) error {
	openid, err := s.id.OpenID(ctx, job.TargetUserID)
	if err != nil {
		return err
	}
	return s.channel.Send(ctx, openid, job)
}

func validLevel(l string) bool {
	return l == LevelStrong || l == LevelMedium || l == LevelWeak
}

// cloneState 浅拷贝 channel_state，避免原地修改共享 map。
func cloneState(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}
