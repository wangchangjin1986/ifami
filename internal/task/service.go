package task

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"
)

// streakWindowDays 连续天数最多向前追溯的窗口（种子期够用；更长留给 V2 重放）。
const streakWindowDays = 90

// Membership 是 task 对 identity 模块的最小依赖（消费方定义接口，跨模块只走接口）。
type Membership interface {
	// RequireActive 校验 user 是 family 的 active 成员，返回 member_id 与 role。
	RequireActive(ctx context.Context, userID, familyID uint64) (memberID uint64, role string, err error)
	// RequireMember 校验 member_id 属于 family 且 active，返回成员 type 与 role。
	RequireMember(ctx context.Context, familyID, memberID uint64) (mtype, role string, err error)
	// MemberUserID 返回 real 成员的 user_id（触达需 openid，openid 需 user_id）。
	MemberUserID(ctx context.Context, familyID, memberID uint64) (uint64, error)
}

// Notifier 是 task 对 notify 模块的最小依赖（消费方定义接口，跨模块只走接口，硬性规则 6）。
type Notifier interface {
	CreateJob(ctx context.Context, in NotifyInput) (uint64, error)
}

// NotifyInput 触达任务入参（对齐 notify.CreateJobInput，不 import notify 保持模块边界）。
type NotifyInput struct {
	FamilyID     uint64         `json:"family_id"`
	TargetUserID uint64         `json:"target_user_id"`
	Level        string         `json:"level"`
	Payload      map[string]any `json:"payload"`
	BusinessKey  string         `json:"business_key"`
}

// 触达级别（对齐 notify 模块常量，术语表 wiki/product/glossary.md）。
const (
	levelStrong = "strong"
	levelMedium = "medium"
	levelWeak   = "weak"
)

// Service 是 task 模块业务层，也是其他模块调用本模块的唯一入口。
type Service struct {
	store  *Store
	id     Membership
	notify Notifier
}

func NewService(store *Store, id Membership, notify Notifier) *Service {
	return &Service{store: store, id: id, notify: notify}
}

// ---- 对外 DTO ----

// TaskInput 创建/更新打卡任务的入参。
type TaskInput struct {
	Name          string `json:"name"`
	GoalType      string `json:"goal_type"`
	GoalValue     int    `json:"goal_value"`
	Freq          string `json:"freq"`
	ChildMemberID uint64 `json:"child_member_id"`
	OwnerMemberID uint64 `json:"owner_member_id"`
	RemindTime    string `json:"remind_time"` // HH:MM，空=不提醒
}

// TaskView 任务视图（列表带连续天数与今日状态，供打卡页直接渲染）。
type TaskView struct {
	TaskID        uint64 `json:"task_id"`
	ChildMemberID uint64 `json:"child_member_id"`
	Name          string `json:"name"`
	GoalType      string `json:"goal_type"`
	GoalValue     int    `json:"goal_value"`
	Freq          string `json:"freq"`
	OwnerMemberID uint64 `json:"owner_member_id"`
	RemindTime    string `json:"remind_time"`
	StreakDays    int    `json:"streak_days"`
	TodayChecked  bool   `json:"today_checked"`
}

// CheckinInput 打卡提交入参。
type CheckinInput struct {
	Payload        map[string]any `json:"payload"`
	MediaTosKey    string         `json:"media_tos_key"`
	SubmitMemberID uint64         `json:"submit_member_id"`
}

// SubmitResult 打卡提交结果。
type SubmitResult struct {
	CheckinID  uint64 `json:"checkin_id"`
	StreakDays int    `json:"streak_days"`
	GoalHit    bool   `json:"goal_hit"`
}

// CheckinView 打卡记录视图（进步曲线数据源）。
type CheckinView struct {
	CheckinID      uint64         `json:"checkin_id"`
	TaskID         uint64         `json:"task_id"`
	ChildMemberID  uint64         `json:"child_member_id"`
	SubmitMemberID uint64         `json:"submit_member_id"`
	Payload        map[string]any `json:"payload"`
	MediaTosKey    string         `json:"media_tos_key"`
	CreatedAt      string         `json:"created_at"`
}

// ---- 任务 CRUD ----

// CreateTask 创建打卡任务（admin/parent）。
func (s *Service) CreateTask(ctx context.Context, userID, familyID uint64, in *TaskInput) (uint64, error) {
	if err := s.requireWriter(ctx, userID, familyID); err != nil {
		return 0, err
	}
	t, err := s.buildTask(ctx, familyID, in)
	if err != nil {
		return 0, err
	}
	if err := s.store.CreateTask(ctx, t); err != nil {
		return 0, err
	}
	return t.TaskID, nil
}

// ListTasks 列家庭任务，附带连续天数与今日状态（任意 active 成员可读）。
func (s *Service) ListTasks(ctx context.Context, userID, familyID uint64) ([]TaskView, error) {
	if _, _, err := s.id.RequireActive(ctx, userID, familyID); err != nil {
		return nil, err
	}
	rows, err := s.store.ListTasks(ctx, familyID)
	if err != nil {
		return nil, err
	}
	// 一次拉取窗口内全部打卡，按任务分组算 streak / 今日状态（避免 N+1）。
	now := time.Now().UTC()
	start := dateOnly(now).AddDate(0, 0, -streakWindowDays)
	end := dateOnly(now).AddDate(0, 0, 1)
	checkins, err := s.store.ListCheckinsByFamily(ctx, familyID, start, end, 0)
	if err != nil {
		return nil, err
	}
	byTask := map[uint64][]Checkin{}
	for i := range checkins {
		byTask[checkins[i].TaskID] = append(byTask[checkins[i].TaskID], checkins[i])
	}
	today := dateOnly(now).Format("2006-01-02")
	views := make([]TaskView, 0, len(rows))
	for i := range rows {
		dates := map[string]bool{}
		for _, c := range byTask[rows[i].TaskID] {
			dates[c.CreatedAt.UTC().Format("2006-01-02")] = true
		}
		views = append(views, TaskView{
			TaskID:        rows[i].TaskID,
			ChildMemberID: rows[i].ChildMemberID,
			Name:          rows[i].Name,
			GoalType:      rows[i].GoalType,
			GoalValue:     rows[i].GoalValue,
			Freq:          rows[i].Freq,
			OwnerMemberID: rows[i].OwnerMemberID,
			RemindTime:    rows[i].RemindTime,
			StreakDays:    calcStreak(dates, now),
			TodayChecked:  dates[today],
		})
	}
	return views, nil
}

// UpdateTask 更新任务（admin/parent）。
func (s *Service) UpdateTask(ctx context.Context, userID, familyID, taskID uint64, in *TaskInput) error {
	if err := s.requireWriter(ctx, userID, familyID); err != nil {
		return err
	}
	t, err := s.buildTask(ctx, familyID, in)
	if err != nil {
		return err
	}
	updates := map[string]any{
		"name":            t.Name,
		"goal_type":       t.GoalType,
		"goal_value":      t.GoalValue,
		"freq":            t.Freq,
		"child_member_id": t.ChildMemberID,
		"owner_member_id": t.OwnerMemberID,
		"remind_time":     t.RemindTime,
	}
	return s.store.UpdateTask(ctx, taskID, familyID, updates)
}

// DeleteTask 软删任务（admin/parent）。
func (s *Service) DeleteTask(ctx context.Context, userID, familyID, taskID uint64) error {
	if err := s.requireWriter(ctx, userID, familyID); err != nil {
		return err
	}
	return s.store.RemoveTask(ctx, familyID, taskID)
}

// ---- 打卡提交 ----

// SubmitCheckin 提交打卡（admin/parent/grandparent 可提交/代打），写事件流，幂等。
func (s *Service) SubmitCheckin(ctx context.Context, userID, familyID, taskID uint64, in *CheckinInput, idempotencyKey string) (*SubmitResult, error) {
	actorMemberID, _, err := s.id.RequireActive(ctx, userID, familyID)
	if err != nil {
		return nil, err
	}
	task, err := s.store.FindTask(ctx, familyID, taskID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, ErrTaskNotFound
	}

	// 幂等：同 key 已存在直接返回（含连续天数）。
	if idempotencyKey != "" {
		if existing, err := s.store.FindCheckinByIdempotency(ctx, familyID, idempotencyKey); err != nil {
			return nil, err
		} else if existing != nil {
			streak, _ := s.streakDays(ctx, familyID, taskID)
			return &SubmitResult{CheckinID: existing.CheckinID, StreakDays: streak}, nil
		}
	}

	// 实际提交人：默认当前成员；显式传时必须是 real 成员（代打）。
	submitMemberID := in.SubmitMemberID
	if submitMemberID == 0 {
		submitMemberID = actorMemberID
	} else if mtype, _, err := s.id.RequireMember(ctx, familyID, submitMemberID); err != nil {
		return nil, err
	} else if mtype != memberTypeReal {
		return nil, ErrOwnerRequired
	}

	if err := validatePayload(task.GoalType, in.Payload, in.MediaTosKey); err != nil {
		return nil, err
	}
	hit := goalHit(task.GoalType, task.GoalValue, in.Payload)

	c := &Checkin{
		TaskID:         taskID,
		FamilyID:       familyID,
		ChildMemberID:  task.ChildMemberID,
		SubmitMemberID: submitMemberID,
		Payload:        in.Payload,
		MediaTosKey:    in.MediaTosKey,
		IdempotencyKey: idempotencyKey,
	}
	events := []Event{{Type: EventCheckinDone}}
	if hit {
		events = append(events, Event{Type: EventGoalHit})
	}
	if err := s.store.CreateCheckinWithEvents(ctx, c, events); err != nil {
		// 并发重复提交：唯一键冲突，回查已有记录返回。
		if errors.Is(err, gorm.ErrDuplicatedKey) && idempotencyKey != "" {
			if existing, e2 := s.store.FindCheckinByIdempotency(ctx, familyID, idempotencyKey); e2 == nil && existing != nil {
				streak, _ := s.streakDays(ctx, familyID, taskID)
				return &SubmitResult{CheckinID: existing.CheckinID, StreakDays: streak}, nil
			}
		}
		return nil, err
	}

	streak, err := s.streakDays(ctx, familyID, taskID)
	if err != nil {
		streak = 0
	}
	if streak > 0 && streak%7 == 0 {
		// 连续 7 天倍数里程碑：写 streak 事件（事实陈述，非激励）。
		ref := c.CheckinID
		_ = s.store.CreateEvents(ctx, []Event{{FamilyID: familyID, Type: EventStreak, RefID: &ref}})
	}
	return &SubmitResult{CheckinID: c.CheckinID, StreakDays: streak, GoalHit: hit}, nil
}

// ListCheckinsByTask 列任务打卡记录（进步曲线：只和自己比，ADR-004）。
func (s *Service) ListCheckinsByTask(ctx context.Context, userID, familyID, taskID uint64) ([]CheckinView, error) {
	if _, _, err := s.id.RequireActive(ctx, userID, familyID); err != nil {
		return nil, err
	}
	task, err := s.store.FindTask(ctx, familyID, taskID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, ErrTaskNotFound
	}
	rows, err := s.store.ListCheckinsByTask(ctx, familyID, taskID)
	if err != nil {
		return nil, err
	}
	views := make([]CheckinView, 0, len(rows))
	for i := range rows {
		views = append(views, toCheckinView(&rows[i]))
	}
	return views, nil
}

// ---- 内部辅助 ----

// RemindDue 打卡提醒扫描（R3.3，Worker 每 30s 调用）：到点未完成的任务给责任父母发中等触达。
// 幂等：business_key = checkin:<task_id>:<YYYY-MM-DD>，同一天只发一次（notify 按 key 去重）。
// 提醒时间按 UTC 解释（与打卡 dateOnly 同口径；中国时区偏移留 TODO，见 conventions 时区约定）。
func (s *Service) RemindDue(ctx context.Context, now time.Time) error {
	if s.notify == nil {
		return nil
	}
	rows, err := s.store.ListRemindableTasks(ctx)
	if err != nil {
		return err
	}
	dateKey := dateOnly(now).Format("2006-01-02")
	hhmm := now.UTC().Format("15:04")
	for i := range rows {
		t := &rows[i]
		if t.RemindTime > hhmm {
			continue // 未到提醒时间
		}
		checked, err := s.store.HasCheckinOn(ctx, t.FamilyID, t.TaskID, dateOnly(now))
		if err != nil {
			log.Printf("[task] 提醒扫描查打卡失败 task=%d err=%v", t.TaskID, err)
			continue
		}
		if checked {
			continue // 今天已打卡，不提醒
		}
		userID, err := s.id.MemberUserID(ctx, t.FamilyID, t.OwnerMemberID)
		if err != nil {
			log.Printf("[task] 触达解析 owner user_id 失败 task=%d err=%v", t.TaskID, err)
			continue
		}
		if _, err := s.notify.CreateJob(ctx, NotifyInput{
			FamilyID:     t.FamilyID,
			TargetUserID: userID,
			Level:        levelMedium,
			Payload:      map[string]any{"task": t.Name, "child_member_id": t.ChildMemberID},
			BusinessKey:  fmt.Sprintf("checkin:%d:%s", t.TaskID, dateKey),
		}); err != nil {
			log.Printf("[task] 创建提醒触达失败 task=%d err=%v", t.TaskID, err)
		}
	}
	return nil
}

// requireWriter 校验用户是该家庭的 admin/parent（任务录入权限，见 roles.md）。
func (s *Service) requireWriter(ctx context.Context, userID, familyID uint64) error {
	_, role, err := s.id.RequireActive(ctx, userID, familyID)
	if err != nil {
		return err
	}
	if role != roleAdmin && role != roleParent {
		return ErrForbidden
	}
	return nil
}

// buildTask 校验入参并构造任务：孩子必须是虚拟成员，责任人必须是家庭成员。
func (s *Service) buildTask(ctx context.Context, familyID uint64, in *TaskInput) (*Task, error) {
	if in.Name == "" || !validGoalType(in.GoalType) {
		return nil, ErrBadGoal
	}
	if in.GoalType != GoalPhoto && in.GoalValue <= 0 {
		return nil, ErrBadGoal
	}
	if in.Freq == "" {
		in.Freq = "daily"
	}
	if in.RemindTime != "" {
		if _, err := time.Parse("15:04", in.RemindTime); err != nil {
			return nil, ErrBadGoal
		}
	}
	mtype, _, err := s.id.RequireMember(ctx, familyID, in.ChildMemberID)
	if err != nil {
		return nil, err
	}
	if mtype != memberTypeVirtual {
		return nil, ErrChildRequired
	}
	ownerType, _, err := s.id.RequireMember(ctx, familyID, in.OwnerMemberID)
	if err != nil {
		return nil, err
	}
	if ownerType != memberTypeReal {
		return nil, ErrOwnerRequired
	}
	return &Task{
		FamilyID:      familyID,
		ChildMemberID: in.ChildMemberID,
		Name:          in.Name,
		GoalType:      in.GoalType,
		GoalValue:     in.GoalValue,
		Freq:          in.Freq,
		OwnerMemberID: in.OwnerMemberID,
		RemindTime:    in.RemindTime,
		Status:        StatusActive,
	}, nil
}

// streakDays 计算任务当前连续打卡天数。
func (s *Service) streakDays(ctx context.Context, familyID, taskID uint64) (int, error) {
	rows, err := s.store.ListCheckinsByTask(ctx, familyID, taskID)
	if err != nil {
		return 0, err
	}
	dates := map[string]bool{}
	for i := range rows {
		dates[rows[i].CreatedAt.UTC().Format("2006-01-02")] = true
	}
	return calcStreak(dates, time.Now().UTC()), nil
}

// calcStreak 从今天起向前数连续有打卡的天数；今天未打卡不算断签。
func calcStreak(dates map[string]bool, now time.Time) int {
	d := dateOnly(now)
	if !dates[d.Format("2006-01-02")] {
		d = d.AddDate(0, 0, -1)
	}
	n := 0
	for dates[d.Format("2006-01-02")] {
		n++
		d = d.AddDate(0, 0, -1)
	}
	return n
}

func validGoalType(t string) bool {
	return t == GoalPhoto || t == GoalTimer || t == GoalCount
}

// validatePayload 校验打卡负载与任务目标类型匹配。
func validatePayload(goalType string, payload map[string]any, mediaTosKey string) error {
	switch goalType {
	case GoalPhoto:
		if mediaTosKey == "" {
			return ErrBadGoal
		}
	case GoalTimer:
		if v, _ := payload["seconds"].(float64); v <= 0 {
			return ErrBadGoal
		}
	case GoalCount:
		if v, _ := payload["count"].(float64); v <= 0 {
			return ErrBadGoal
		}
	}
	return nil
}

// goalHit 判断本次是否达成目标（count/timer 达到 goal_value；photo 无量化目标）。
func goalHit(goalType string, goalValue int, payload map[string]any) bool {
	switch goalType {
	case GoalCount:
		v, _ := payload["count"].(float64)
		return goalValue > 0 && int(v) >= goalValue
	case GoalTimer:
		v, _ := payload["seconds"].(float64)
		return goalValue > 0 && int(v) >= goalValue*60 // goal_value 单位=分钟，payload.seconds 单位=秒
	}
	return false
}

func toCheckinView(c *Checkin) CheckinView {
	return CheckinView{
		CheckinID:      c.CheckinID,
		TaskID:         c.TaskID,
		ChildMemberID:  c.ChildMemberID,
		SubmitMemberID: c.SubmitMemberID,
		Payload:        c.Payload,
		MediaTosKey:    c.MediaTosKey,
		CreatedAt:      c.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// dateOnly 归一化到 UTC 当天 00:00（streak/窗口边界用）。
func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
