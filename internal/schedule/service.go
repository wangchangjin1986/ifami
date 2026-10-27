package schedule

import (
	"context"
	"log"
	"time"

	"ifami/internal/pkg/httpx"
)

// materializeWindowDays 是物化滚动窗口：始终物化未来 90 天实例（ADR-003）。
const materializeWindowDays = 90

// Membership 是 schedule 对 identity 模块的最小依赖（消费方定义接口，跨模块只走接口）。
type Membership interface {
	// RequireActive 校验 user 是 family 的 active 成员，返回 member_id 与 role。
	RequireActive(ctx context.Context, userID, familyID uint64) (memberID uint64, role string, err error)
	// RequireMember 校验 member_id 属于 family 且 active，返回成员 type 与 role。
	RequireMember(ctx context.Context, familyID, memberID uint64) (mtype, role string, err error)
	// MemberUserID 返回 real 成员的 user_id（触达需 openid，openid 需 user_id）。
	MemberUserID(ctx context.Context, familyID, memberID uint64) (uint64, error)
}

// Notifier 是 schedule 对 notify 模块的最小依赖（消费方定义接口，跨模块只走接口，硬性规则 6）。
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

// Service 是 schedule 模块业务层，也是其他模块调用本模块的唯一入口。
type Service struct {
	store  *Store
	id     Membership
	notify Notifier
}

func NewService(store *Store, id Membership, notify Notifier) *Service {
	return &Service{store: store, id: id, notify: notify}
}

// ScheduleInput 创建/更新日程的入参。
type ScheduleInput struct {
	Title         string `json:"title"`
	Type          string `json:"type"`
	ChildMemberID uint64 `json:"child_member_id"`
	StartDate     string `json:"start_date"` // YYYY-MM-DD
	RRule         string `json:"rrule"`      // 重复规则；空=单次
	StartTime     string `json:"start_time"` // HH:MM 或 HH:MM:SS
	EndTime       string `json:"end_time"`   // 可空
	Location      string `json:"location"`
	Note          string `json:"note"`
	OwnerMemberID uint64 `json:"owner_member_id"`
}

// CreateResult 创建/更新结果，含冲突列表（冲突允许强制保存）。
type CreateResult struct {
	ScheduleID uint64         `json:"schedule_id"`
	Conflicts  []InstanceView `json:"conflicts"`
}

// ScheduleView 模板视图。
type ScheduleView struct {
	ScheduleID    uint64  `json:"schedule_id"`
	ChildMemberID uint64  `json:"child_member_id"`
	Title         string  `json:"title"`
	Type          string  `json:"type"`
	StartDate     string  `json:"start_date"`
	RRule         string  `json:"rrule"`
	StartTime     string  `json:"start_time"`
	EndTime       *string `json:"end_time"`
	Location      string  `json:"location"`
	Note          string  `json:"note"`
	OwnerMemberID uint64  `json:"owner_member_id"`
}

// InstanceView 实例视图（今日/周/月视图与冲突列表共用）。
type InstanceView struct {
	InstanceID    uint64  `json:"instance_id"`
	ScheduleID    uint64  `json:"schedule_id"`
	ChildMemberID uint64  `json:"child_member_id"`
	Title         string  `json:"title"`
	Type          string  `json:"type"`
	OccurDate     string  `json:"occur_date"`
	StartAt       string  `json:"start_at"`
	EndAt         *string `json:"end_at"`
	Location      string  `json:"location"`
	Note          string  `json:"note"`
	OwnerMemberID uint64  `json:"owner_member_id"`
	Status        string  `json:"status"`
}

// InstanceInput 更新单实例的入参（部分更新）。
type InstanceInput struct {
	Status        string `json:"status"`
	OwnerMemberID uint64 `json:"owner_member_id"`
}

// CreateSchedule 创建日程：物化未来 90 天 + 冲突检测（force 跳过冲突拦截）。
func (s *Service) CreateSchedule(ctx context.Context, userID, familyID uint64, in *ScheduleInput, force bool) (*CreateResult, error) {
	if err := s.requireWriter(ctx, userID, familyID); err != nil {
		return nil, err
	}
	sch, err := s.buildSchedule(ctx, familyID, in)
	if err != nil {
		return nil, err
	}
	instances, err := s.materialize(sch, dateOnly(time.Now().UTC()))
	if err != nil {
		return nil, err
	}
	conflicts, err := s.conflicts(ctx, familyID, sch.ChildMemberID, sch.OwnerMemberID, instances, 0)
	if err != nil {
		return nil, err
	}
	if len(conflicts) > 0 && !force {
		return nil, NewConflictError(conflicts)
	}
	if err := s.store.CreateSchedule(ctx, sch, instances); err != nil {
		return nil, err
	}
	s.notifyOwner(ctx, familyID, sch)
	return &CreateResult{ScheduleID: sch.ScheduleID, Conflicts: conflicts}, nil
}

// UpdateSchedule 全量更新模板：重建未来实例 + 冲突检测（force 跳过冲突拦截）。
func (s *Service) UpdateSchedule(ctx context.Context, userID, familyID, scheduleID uint64, in *ScheduleInput, force bool) (*CreateResult, error) {
	if err := s.requireWriter(ctx, userID, familyID); err != nil {
		return nil, err
	}
	existing, err := s.store.FindSchedule(ctx, familyID, scheduleID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, ErrScheduleNotFound
	}
	sch, err := s.buildSchedule(ctx, familyID, in)
	if err != nil {
		return nil, err
	}
	sch.ScheduleID = scheduleID
	sch.CreatedAt = existing.CreatedAt

	today := dateOnly(time.Now().UTC())
	instances, err := s.materialize(sch, today)
	if err != nil {
		return nil, err
	}
	conflicts, err := s.conflicts(ctx, familyID, sch.ChildMemberID, sch.OwnerMemberID, instances, scheduleID)
	if err != nil {
		return nil, err
	}
	if len(conflicts) > 0 && !force {
		return nil, NewConflictError(conflicts)
	}
	if err := s.store.ReplaceFutureInstances(ctx, sch, instances, today); err != nil {
		return nil, err
	}
	s.notifyOwner(ctx, familyID, sch)
	return &CreateResult{ScheduleID: scheduleID, Conflicts: conflicts}, nil
}

// ListSchedules 列家庭日程模板（任意 active 成员可读）。
func (s *Service) ListSchedules(ctx context.Context, userID, familyID uint64) ([]ScheduleView, error) {
	if _, _, err := s.id.RequireActive(ctx, userID, familyID); err != nil {
		return nil, err
	}
	rows, err := s.store.ListSchedules(ctx, familyID)
	if err != nil {
		return nil, err
	}
	views := make([]ScheduleView, 0, len(rows))
	for i := range rows {
		views = append(views, toScheduleView(&rows[i]))
	}
	return views, nil
}

// DeleteSchedule 软删模板并清空未来实例。
func (s *Service) DeleteSchedule(ctx context.Context, userID, familyID, scheduleID uint64) error {
	if err := s.requireWriter(ctx, userID, familyID); err != nil {
		return err
	}
	return s.store.RemoveSchedule(ctx, familyID, scheduleID, dateOnly(time.Now().UTC()))
}

// ListInstances 按日期范围列实例（今日/周/月视图，任意 active 成员可读）。
func (s *Service) ListInstances(ctx context.Context, userID, familyID uint64, start, end time.Time) ([]InstanceView, error) {
	if _, _, err := s.id.RequireActive(ctx, userID, familyID); err != nil {
		return nil, err
	}
	rows, err := s.store.ListInstances(ctx, familyID, start, end)
	if err != nil {
		return nil, err
	}
	views := make([]InstanceView, 0, len(rows))
	for i := range rows {
		views = append(views, toInstanceView(&rows[i]))
	}
	return views, nil
}

// UpdateInstance 更新单实例（取消本次 / 完成 / 换责任人），仅 admin/parent。
func (s *Service) UpdateInstance(ctx context.Context, userID, familyID, instanceID uint64, in *InstanceInput) error {
	if err := s.requireWriter(ctx, userID, familyID); err != nil {
		return err
	}
	inst, err := s.store.FindInstance(ctx, familyID, instanceID)
	if err != nil {
		return err
	}
	if inst == nil {
		return ErrInstanceNotFound
	}
	updates := map[string]any{}
	if in.Status != "" {
		if in.Status != StatusActive && in.Status != StatusCancelled && in.Status != StatusDone {
			return ErrBadParam
		}
		if in.Status != inst.Status {
			updates["status"] = in.Status
		}
	}
	if in.OwnerMemberID != 0 {
		if _, _, err := s.id.RequireMember(ctx, familyID, in.OwnerMemberID); err != nil {
			return err
		}
		if in.OwnerMemberID != inst.OwnerMemberID {
			updates["owner_member_id"] = in.OwnerMemberID
		}
	}
	if len(updates) == 0 {
		return nil // 幂等：无实际变化（如重指派到同一责任人）视为成功
	}
	return s.store.UpdateInstance(ctx, instanceID, familyID, updates)
}

// ---- 内部辅助 ----

// notifyOwner 接送类日程创建/变更后立即强触达执行端 owner（R4.3：创建→收到<1min）。
// 触达是尽力而为，失败不阻塞日程主流程（log 后忽略）。到点提醒（开始前 X 分钟）需 Worker 扫实例表，留 TODO。
func (s *Service) notifyOwner(ctx context.Context, familyID uint64, sch *Schedule) {
	if s.notify == nil || sch.Type != TypePickup {
		return
	}
	userID, err := s.id.MemberUserID(ctx, familyID, sch.OwnerMemberID)
	if err != nil {
		log.Printf("[schedule] 触达解析 owner user_id 失败: %v", err)
		return
	}
	if _, err := s.notify.CreateJob(ctx, NotifyInput{
		FamilyID:     familyID,
		TargetUserID: userID,
		Level:        levelStrong,
		Payload: map[string]any{
			"title":      sch.Title,
			"start_time": sch.StartTime,
			"location":   sch.Location,
			"note":       sch.Note,
		},
	}); err != nil {
		log.Printf("[schedule] 创建触达任务失败: %v", err)
	}
}

// requireWriter 校验用户是该家庭的 admin/parent（写日程权限，见 roles.md）。
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

// buildSchedule 校验入参并构造模板；孩子必须是虚拟成员，责任人必须是家庭成员。
func (s *Service) buildSchedule(ctx context.Context, familyID uint64, in *ScheduleInput) (*Schedule, error) {
	if in.Title == "" || in.StartDate == "" || in.StartTime == "" {
		return nil, ErrBadParam
	}
	if !validType(in.Type) {
		return nil, ErrBadParam
	}
	if _, err := time.Parse("2006-01-02", in.StartDate); err != nil {
		return nil, ErrBadParam
	}
	if _, err := ParseRRule(in.RRule); err != nil {
		return nil, ErrBadRRule
	}
	if _, err := parseClock(in.StartTime); err != nil {
		return nil, ErrBadParam
	}
	if in.EndTime != "" {
		if _, err := parseClock(in.EndTime); err != nil {
			return nil, ErrBadParam
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

	startDate, _ := time.Parse("2006-01-02", in.StartDate)
	var endTime *string
	if in.EndTime != "" {
		endTime = &in.EndTime
	}
	return &Schedule{
		FamilyID:      familyID,
		ChildMemberID: in.ChildMemberID,
		Title:         in.Title,
		Type:          in.Type,
		StartDate:     startDate,
		RRule:         in.RRule,
		StartTime:     in.StartTime,
		EndTime:       endTime,
		Location:      in.Location,
		Note:          in.Note,
		OwnerMemberID: in.OwnerMemberID,
		Status:        StatusActive,
	}, nil
}

// materialize 按 RRULE 展开未来窗口并生成实例（冗余展示字段）。
func (s *Service) materialize(sch *Schedule, today time.Time) ([]ScheduleInstance, error) {
	r, err := ParseRRule(sch.RRule)
	if err != nil {
		return nil, err
	}
	dates := occurrences(r, sch.StartDate, today, materializeWindowDays)
	instances := make([]ScheduleInstance, 0, len(dates))
	for _, d := range dates {
		startAt := combineDateTime(d, sch.StartTime)
		var endAt *time.Time
		if sch.EndTime != nil {
			t := combineDateTime(d, *sch.EndTime)
			endAt = &t
		}
		instances = append(instances, ScheduleInstance{
			FamilyID:      sch.FamilyID,
			ChildMemberID: sch.ChildMemberID,
			Title:         sch.Title,
			Type:          sch.Type,
			OccurDate:     d,
			StartAt:       startAt,
			EndAt:         endAt,
			Location:      sch.Location,
			Note:          sch.Note,
			OwnerMemberID: sch.OwnerMemberID,
			Status:        StatusActive,
		})
	}
	return instances, nil
}

// conflicts 逐个新实例查时间重叠，按 instance_id 去重返回冲突视图。
func (s *Service) conflicts(ctx context.Context, familyID, childID, ownerID uint64, instances []ScheduleInstance, excludeScheduleID uint64) ([]InstanceView, error) {
	seen := map[uint64]bool{}
	var out []InstanceView
	for _, inst := range instances {
		endAt := inst.StartAt
		if inst.EndAt != nil {
			endAt = *inst.EndAt
		}
		hits, err := s.store.FindConflicts(ctx, familyID, childID, ownerID, inst.StartAt, endAt, excludeScheduleID)
		if err != nil {
			return nil, err
		}
		for i := range hits {
			if seen[hits[i].InstanceID] {
				continue
			}
			seen[hits[i].InstanceID] = true
			out = append(out, toInstanceView(&hits[i]))
		}
	}
	return out, nil
}

// NewConflictError 构造带冲突列表的错误（force=false 时返回）。
func NewConflictError(conflicts []InstanceView) error {
	return &httpx.Err{Code: 2005, Message: "存在时间冲突", HTTP: 400, Data: map[string]any{"conflicts": conflicts}}
}

func validType(t string) bool {
	switch t {
	case TypeCourse, TypePickup, TypeActivity, TypeReminder:
		return true
	}
	return false
}

func toScheduleView(s *Schedule) ScheduleView {
	return ScheduleView{
		ScheduleID:    s.ScheduleID,
		ChildMemberID: s.ChildMemberID,
		Title:         s.Title,
		Type:          s.Type,
		StartDate:     s.StartDate.Format("2006-01-02"),
		RRule:         s.RRule,
		StartTime:     s.StartTime,
		EndTime:       s.EndTime,
		Location:      s.Location,
		Note:          s.Note,
		OwnerMemberID: s.OwnerMemberID,
	}
}

func toInstanceView(inst *ScheduleInstance) InstanceView {
	v := InstanceView{
		InstanceID:    inst.InstanceID,
		ScheduleID:    inst.ScheduleID,
		ChildMemberID: inst.ChildMemberID,
		Title:         inst.Title,
		Type:          inst.Type,
		OccurDate:     inst.OccurDate.Format("2006-01-02"),
		StartAt:       inst.StartAt.Format(time.RFC3339),
		Location:      inst.Location,
		Note:          inst.Note,
		OwnerMemberID: inst.OwnerMemberID,
		Status:        inst.Status,
	}
	if inst.EndAt != nil {
		s := inst.EndAt.Format(time.RFC3339)
		v.EndAt = &s
	}
	return v
}

// parseClock 解析 "HH:MM" 或 "HH:MM:SS"。
func parseClock(s string) (time.Time, error) {
	if t, err := time.Parse("15:04:05", s); err == nil {
		return t, nil
	}
	return time.Parse("15:04", s)
}

// combineDateTime 把日期与时钟合并为时间点（UTC）。
func combineDateTime(d time.Time, clock string) time.Time {
	t, err := parseClock(clock)
	if err != nil {
		return d
	}
	return time.Date(d.Year(), d.Month(), d.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
}

// dateOnly 归一化到 UTC 当天 00:00（物化/删除窗口的边界比较用）。
func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
