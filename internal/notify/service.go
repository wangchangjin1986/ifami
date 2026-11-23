package notify

import (
	"context"
	"encoding/json"
	"errors"
	"ifami/internal/pkg/httpx"
	"ifami/internal/pkg/logx"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Identity 是 notify 对 identity 模块的最小依赖（消费方定义接口，发订阅消息需 openid）。
type Identity interface {
	OpenID(ctx context.Context, userID uint64) (string, error)
	// RequireActive returns public identity membership denial codes 1005/1014;
	// operational errors remain retryable and must propagate.
	RequireActive(ctx context.Context, userID, familyID uint64) (uint64, string, error)
	AdminUserIDs(ctx context.Context, familyID uint64) ([]uint64, error)
	BackupUserIDs(ctx context.Context, familyID uint64) ([]uint64, error)
}

type notifyStore interface {
	CreateJob(ctx context.Context, j *NotifyJob) error
	FindJob(ctx context.Context, jobID uint64) (*NotifyJob, error)
	FindJobByBusinessKey(ctx context.Context, familyID uint64, key string) (*NotifyJob, error)
	ListDueJobs(ctx context.Context, status string, before time.Time) ([]NotifyJob, error)
	ListReddotJobs(ctx context.Context, familyID, targetUserID uint64) ([]NotifyJob, error)
	UpdateJob(ctx context.Context, jobID uint64, updates map[string]any) error
}

// ScheduleVisibility 由 notify 定义，仅通过接口读取日程当前可见性。
type ScheduleVisibility interface {
	NotificationVisibility(ctx context.Context, userID, familyID, instanceID uint64) (bool, error)
}

// Service 路由触达、升级状态机、通道适配与回执。
type Service struct {
	schedules ScheduleVisibility
	store     notifyStore
	id        Identity
	channel   Channel
}

type TemplateConfig struct {
	TemplateIDs []string `json:"template_ids"`
}

type ReddotJobView struct {
	JobID   uint64         `json:"job_id"`
	Level   string         `json:"level"`
	Status  string         `json:"status"`
	Payload map[string]any `json:"payload"`
	Created string         `json:"created_at"`
}

func NewService(store *Store, id Identity, ch Channel) *Service {
	return &Service{store: store, id: id, channel: ch}
}

func (s *Service) SetScheduleVisibility(scope ScheduleVisibility) { s.schedules = scope }

func (s *Service) TemplateConfig() TemplateConfig {
	provider, ok := s.channel.(TemplateProvider)
	if !ok {
		return TemplateConfig{TemplateIDs: []string{}}
	}
	return TemplateConfig{TemplateIDs: provider.SubscribeTemplateIDs()}
}

func (s *Service) ListReddotJobs(ctx context.Context, userID, familyID uint64) ([]ReddotJobView, error) {
	if _, _, err := s.id.RequireActive(ctx, userID, familyID); err != nil {
		return nil, err
	}
	jobs, err := s.store.ListReddotJobs(ctx, familyID, userID)
	if err != nil {
		return nil, err
	}
	out := make([]ReddotJobView, 0, len(jobs))
	for i := range jobs {
		visible, err := s.scheduleJobVisibility(ctx, &jobs[i])
		if err != nil {
			return nil, err
		}
		if !visible {
			continue
		}
		out = append(out, ReddotJobView{
			JobID:   jobs[i].JobID,
			Level:   jobs[i].Level,
			Status:  jobs[i].Status,
			Payload: jobs[i].Payload,
			Created: jobs[i].CreatedAt.Format(time.RFC3339),
		})
	}
	return out, nil
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

// CreateJobForUser 是 HTTP 入口专用的安全包装：调用者与目标用户都必须是该家庭 active 成员。
// 业务模块内部已在各自写路径做 membership 校验，仍走 CreateJob 避免循环鉴权。
func (s *Service) CreateJobForUser(ctx context.Context, actorUserID uint64, in *CreateJobInput) (uint64, error) {
	if in.FamilyID == 0 || in.TargetUserID == 0 || !validLevel(in.Level) {
		return 0, ErrBadLevel
	}
	if _, _, err := s.id.RequireActive(ctx, actorUserID, in.FamilyID); err != nil {
		return 0, err
	}
	if _, _, err := s.id.RequireActive(ctx, in.TargetUserID, in.FamilyID); err != nil {
		return 0, err
	}
	return s.CreateJob(ctx, in)
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
			logx.Errorf("[notify] process pending job=%d err=%v", pending[i].JobID, err)
		}
	}

	sent, err := s.store.ListDueJobs(ctx, StatusSent, now)
	if err != nil {
		return err
	}
	for i := range sent {
		if err := s.escalateReddot(ctx, &sent[i], now); err != nil {
			logx.Errorf("[notify] escalate reddot job=%d err=%v", sent[i].JobID, err)
		}
	}

	reddot, err := s.store.ListDueJobs(ctx, StatusReddot, now)
	if err != nil {
		return err
	}
	for i := range reddot {
		if err := s.escalateBackup(ctx, &reddot[i], now); err != nil {
			logx.Errorf("[notify] escalate backup job=%d err=%v", reddot[i].JobID, err)
		}
	}
	return nil
}

// processPending 处理待发送任务：weak 直接完成；medium 发送后完成；strong 发送后进升级计时。
func (s *Service) processPending(ctx context.Context, job *NotifyJob, now time.Time) error {
	visible, err := s.scheduleJobVisibility(ctx, job)
	if err != nil {
		return err
	}
	if !visible {
		return s.store.UpdateJob(ctx, job.JobID, map[string]any{"status": StatusDone})
	}
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
	visible, err := s.scheduleJobVisibility(ctx, job)
	if err != nil {
		return err
	}
	if !visible {
		return s.store.UpdateJob(ctx, job.JobID, map[string]any{"status": StatusDone})
	}
	if job.Level != LevelStrong {
		return s.store.UpdateJob(ctx, job.JobID, map[string]any{"status": StatusDone})
	}

	cs := cloneState(job.ChannelState)
	cs["reddot_at"] = now.Format(time.RFC3339)
	next := now.Add(escalateBackupAfter - escalateReddotAfter) // 再过 15min 达 30min
	return s.store.UpdateJob(ctx, job.JobID, map[string]any{
		"status":        StatusReddot,
		"next_check_at": &next,
		"channel_state": cs,
	})
}

// escalateBackup 强触达 30min 无回执 → 备份联系人（Phase 0：同家庭其他管理员/家长订阅消息），终端。
func (s *Service) escalateBackup(ctx context.Context, job *NotifyJob, now time.Time) error {
	visible, err := s.scheduleJobVisibility(ctx, job)
	if err != nil {
		return err
	}
	if !visible {
		return s.store.UpdateJob(ctx, job.JobID, map[string]any{"status": StatusDone})
	}
	if job.Level != LevelStrong {
		return s.store.UpdateJob(ctx, job.JobID, map[string]any{"status": StatusDone})
	}

	userIDs, err := s.id.BackupUserIDs(ctx, job.FamilyID)
	if err != nil {
		logx.Warnf("[notify] 备份触达解析联系人失败 job=%d err=%v", job.JobID, err)
		return err
	}
	for _, userID := range userIDs {
		if userID == job.TargetUserID {
			continue
		}
		payload := cloneState(job.Payload)
		payload["source_type"] = job.Payload["type"]
		payload["type"] = "backup_notification"
		payload["title"] = "备份提醒：" + payloadStringForBackup(job.Payload, "title")
		if _, err := s.CreateJob(ctx, &CreateJobInput{
			FamilyID:     job.FamilyID,
			TargetUserID: userID,
			Level:        LevelMedium,
			Payload:      payload,
			BusinessKey:  "backup:" + instKey(job.JobID) + ":" + instKey(userID),
		}); err != nil {
			logx.Errorf("[notify] 创建备份触达失败 job=%d target=%d err=%v", job.JobID, userID, err)
		}
	}
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

func payloadStringForBackup(m map[string]any, key string) string {
	if m == nil || m[key] == nil {
		return ""
	}
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

func instKey(id uint64) string { return strconv.FormatUint(id, 10) }

// cloneState 浅拷贝 channel_state，避免原地修改共享 map。
func cloneState(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

// schedule visibility is rechecked before each read, send and escalation.
func (s *Service) scheduleJobVisibility(ctx context.Context, job *NotifyJob) (bool, error) {
	typ, _ := job.Payload["type"].(string)
	source, _ := job.Payload["source_type"].(string)
	scheduleJob := strings.HasPrefix(typ, "schedule_") || strings.HasPrefix(source, "schedule_") || payloadID(job.Payload["instance_id"]) > 0 || payloadID(job.Payload["schedule_id"]) > 0
	// Historical schedule owner jobs had no type or ID; task jobs have their own type.
	if (typ == "" || (typ == "backup_notification" && payloadID(job.Payload["task_id"]) == 0)) && (job.Payload["start_time"] != nil || job.Payload["location"] != nil || job.Payload["note"] != nil) {
		scheduleJob = true
	}
	if !scheduleJob {
		return true, nil
	}
	_, role, err := s.id.RequireActive(ctx, job.TargetUserID, job.FamilyID)
	if err != nil {
		var denial *httpx.Err
		if errors.As(err, &denial) && denial.HTTP == 403 && (denial.Code == 1005 || denial.Code == 1014) {
			return false, nil
		}
		return false, err
	}
	if typ == "schedule_unassigned" {
		job.Payload = genericSchedulePayload("schedule_unassigned", role)
		job.Level = LevelMedium
		return true, nil
	}
	iid := payloadID(job.Payload["instance_id"])
	if iid == 0 {
		if role != "admin" && role != "parent" {
			job.Payload = genericSchedulePayload("schedule_updated", role)
			job.Level = LevelMedium
		}
		return true, nil
	}
	if s.schedules == nil {
		return false, nil
	}
	return s.schedules.NotificationVisibility(ctx, job.TargetUserID, job.FamilyID, iid)
}
func genericSchedulePayload(typ, role string) map[string]any {
	page := "pages/today/index"
	if role != "admin" && role != "parent" {
		page = "pages/gp-today/index"
	}
	return map[string]any{"type": typ, "title": "分工已调整", "page": page}
}
func payloadID(v any) uint64 {
	switch n := v.(type) {
	case uint64:
		return n
	case uint:
		return uint64(n)
	case int:
		if n > 0 {
			return uint64(n)
		}
	case float64:
		if n > 0 && n == float64(uint64(n)) {
			return uint64(n)
		}
	case json.Number:
		val, _ := strconv.ParseUint(string(n), 10, 64)
		return val
	case string:
		val, _ := strconv.ParseUint(n, 10, 64)
		return val
	}
	return 0
}
