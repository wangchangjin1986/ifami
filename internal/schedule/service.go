package schedule

import (
	"context"
	"fmt"
	"ifami/internal/pkg/logx"
	"strconv"
	"time"

	"ifami/internal/pkg/httpx"
)

// materializeWindowDays 是物化滚动窗口：始终物化未来 90 天实例（ADR-003）。
const materializeWindowDays = 90
const scheduleReminderLead = 15 * time.Minute

var appLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

// Membership 是 schedule 对 identity 模块的最小依赖（消费方定义接口，跨模块只走接口）。
type Membership interface {
	// RequireActive 校验 user 是 family 的 active 成员，返回 member_id 与 role。
	RequireActive(ctx context.Context, userID, familyID uint64) (memberID uint64, role string, err error)
	// RequireMember 校验 member_id 属于 family 且 active，返回成员 type 与 role。
	RequireMember(ctx context.Context, familyID, memberID uint64) (mtype, role string, err error)
	// MemberUserID 返回 real 成员的 user_id（触达需 openid，openid 需 user_id）。
	MemberUserID(ctx context.Context, familyID, memberID uint64) (uint64, error)
	// AdminUserIDs 返回家庭管理员 user_id 列表（执行端反馈通知家长处理用）。
	AdminUserIDs(ctx context.Context, familyID uint64) ([]uint64, error)
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

// BatchCreateResult 批量创建结果（拍照导入确认后一次性入库）。
type BatchCreateResult struct {
	ScheduleIDs []uint64        `json:"schedule_ids"`
	Conflicts   []BatchConflict `json:"conflicts"`
}

// BatchConflict 批量创建的冲突项：Index 为入参下标；
// 与库内实例冲突时 Instance 非空，与同批另一条冲突时 OtherIndex 非空。
type BatchConflict struct {
	Index      int           `json:"index"`
	Instance   *InstanceView `json:"instance,omitempty"`
	OtherIndex *int          `json:"other_index,omitempty"`
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
	FamilyID      uint64  `json:"family_id"`
	InstanceID    uint64  `json:"instance_id"`
	ScheduleID    uint64  `json:"schedule_id"`
	ChildMemberID uint64  `json:"child_member_id"`
	Title         string  `json:"title"`
	Type          string  `json:"type"`
	RRule         string  `json:"rrule"`
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

// FeedbackInput 祖辈/执行人对日程实例的异常反馈。
type FeedbackInput struct {
	Reason string `json:"reason"`
}

type FeedbackResolveInput struct {
	Status string `json:"status"`
}

type FeedbackView struct {
	FeedbackID     uint64 `json:"feedback_id"`
	InstanceID     uint64 `json:"instance_id"`
	ScheduleID     uint64 `json:"schedule_id"`
	ReporterID     uint64 `json:"reporter_member_id"`
	Reason         string `json:"reason"`
	Status         string `json:"status"`
	LastReportedAt string `json:"last_reported_at"`
	UpdatedAt      string `json:"updated_at"`
}

// CreateSchedule 创建日程：物化未来 90 天 + 冲突检测（force 跳过冲突拦截）。
func (s *Service) CreateSchedule(ctx context.Context, userID, familyID uint64, in *ScheduleInput, force bool, idempotencyKey ...string) (*CreateResult, error) {
	if err := s.requireWriter(ctx, userID, familyID); err != nil {
		return nil, err
	}
	idem := ""
	if len(idempotencyKey) > 0 {
		idem = idempotencyKey[0]
	}
	if idem != "" {
		if existing, err := s.store.FindScheduleByIdempotency(ctx, familyID, idem); err != nil {
			return nil, err
		} else if existing != nil {
			return &CreateResult{ScheduleID: existing.ScheduleID}, nil
		}
	}
	sch, err := s.buildSchedule(ctx, familyID, in)
	if err != nil {
		return nil, err
	}
	if idem != "" {
		sch.IdempotencyKey = &idem
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
		if isDuplicateKey(err) && idem != "" {
			if existing, e2 := s.store.FindScheduleByIdempotency(ctx, familyID, idem); e2 == nil && existing != nil {
				return &CreateResult{ScheduleID: existing.ScheduleID, Conflicts: conflicts}, nil
			}
		}
		return nil, err
	}
	s.notifyOwner(ctx, familyID, sch)
	return &CreateResult{ScheduleID: sch.ScheduleID, Conflicts: conflicts}, nil
}

// CreateSchedulesBatch 批量创建日程模板（拍照导入确认后一次入库，单事务）。
// 冲突检测含两部分：与库内已有实例、同批条目两两之间；冲突项带入参下标便于前端定位。
func (s *Service) CreateSchedulesBatch(ctx context.Context, userID, familyID uint64, inputs []ScheduleInput, force bool, batchKey ...string) (*BatchCreateResult, error) {
	if err := s.requireWriter(ctx, userID, familyID); err != nil {
		return nil, err
	}
	key := ""
	if len(batchKey) > 0 {
		key = batchKey[0]
	}
	if len(key) > 64 {
		return nil, ErrBadParam
	}
	if key != "" {
		if ids, found, err := s.FindBatchResult(ctx, familyID, key); err != nil {
			return nil, err
		} else if found {
			return &BatchCreateResult{ScheduleIDs: ids}, nil
		}
	}
	if len(inputs) == 0 || len(inputs) > MaxBatchSize {
		return nil, ErrBadParam
	}
	today := dateOnly(time.Now().UTC())
	schedules := make([]*Schedule, 0, len(inputs))
	instances := make([][]ScheduleInstance, 0, len(inputs))
	conflicts := make([]BatchConflict, 0)
	specs := make([]conflictSpec, 0, len(inputs))
	for i := range inputs {
		sch, err := s.buildSchedule(ctx, familyID, &inputs[i])
		if err != nil {
			return nil, err
		}
		insts, err := s.materialize(sch, today)
		if err != nil {
			return nil, err
		}
		schedules = append(schedules, sch)
		instances = append(instances, insts)
		specs = append(specs, conflictSpec{ChildID: sch.ChildMemberID, OwnerID: sch.OwnerMemberID, Instances: insts})
	}
	storedConflicts, err := s.batchConflicts(ctx, familyID, specs, 0)
	if err != nil {
		return nil, err
	}
	conflicts = append(conflicts, storedConflicts...)
	conflicts = append(conflicts, intraBatchConflicts(schedules, instances)...)
	if len(conflicts) > 0 && !force {
		// Another confirmation may have committed after our initial lookup.
		// Its instances must not turn a retry into a conflict with itself.
		if key != "" {
			if ids, found, lookupErr := s.FindBatchResult(ctx, familyID, key); lookupErr != nil {
				return nil, lookupErr
			} else if found {
				return &BatchCreateResult{ScheduleIDs: ids}, nil
			}
		}
		return nil, &httpx.Err{Code: 2005, Message: "存在时间冲突", HTTP: 400, Data: map[string]any{"conflicts": conflicts}}
	}
	if err := s.store.CreateSchedules(ctx, schedules, instances, key); err != nil {
		if key != "" && isDuplicateKey(err) {
			if ids, found, lookupErr := s.FindBatchResult(ctx, familyID, key); lookupErr == nil && found {
				return &BatchCreateResult{ScheduleIDs: ids}, nil
			}
		}
		return nil, err
	}
	ids := make([]uint64, 0, len(schedules))
	for _, sch := range schedules {
		ids = append(ids, sch.ScheduleID)
		s.notifyOwner(ctx, familyID, sch)
	}
	return &BatchCreateResult{ScheduleIDs: ids, Conflicts: conflicts}, nil
}

// FindBatchResult is an internal module contract for recovering committed imports.
// Public callers must perform membership checks before using it.
func (s *Service) FindBatchResult(ctx context.Context, familyID uint64, key string) ([]uint64, bool, error) {
	return s.store.FindBatchResult(ctx, familyID, key)
}

// MaxBatchSize 批量创建上限（拍照导入单任务草稿上限与之一致）。
const MaxBatchSize = 50

// intraBatchConflicts 同批条目两两检测：同孩子、或同一非空责任人，且任一实例时间重叠。
// 每对条目最多报一次（i<j，记在 j 上，OtherIndex=i）。
func intraBatchConflicts(schedules []*Schedule, instances [][]ScheduleInstance) []BatchConflict {
	var out []BatchConflict
	for i := 0; i < len(schedules); i++ {
		for j := i + 1; j < len(schedules); j++ {
			a, b := schedules[i], schedules[j]
			sameChild := a.ChildMemberID == b.ChildMemberID
			sameOwner := a.OwnerMemberID != 0 && a.OwnerMemberID == b.OwnerMemberID
			if !sameChild && !sameOwner {
				continue
			}
			if instancesOverlap(instances[i], instances[j]) {
				other := i
				out = append(out, BatchConflict{Index: j, OtherIndex: &other})
			}
		}
	}
	return out
}

func instancesOverlap(xs, ys []ScheduleInstance) bool {
	for _, x := range xs {
		xe := x.StartAt
		if x.EndAt != nil {
			xe = *x.EndAt
		}
		for _, y := range ys {
			if !x.OccurDate.Equal(y.OccurDate) {
				continue
			}
			ye := y.StartAt
			if y.EndAt != nil {
				ye = *y.EndAt
			}
			// 与 Store.FindConflicts 同一判定：区间相交（零时长实例按点处理）。
			if y.StartAt.Before(xe) && ye.After(x.StartAt) || x.StartAt.Equal(y.StartAt) {
				return true
			}
		}
	}
	return false
}

// ExtendInstances 滚动续展：为所有 active 重复日程补齐到 today+90 天的实例（ADR-003，Worker 每日调用）。
// 返回新增实例数。只补"最后一个已物化日期之后"的发生，不重建、不覆盖已取消的单实例。
func (s *Service) ExtendInstances(ctx context.Context, today time.Time) (int, error) {
	today = dateOnly(today)
	rows, err := s.store.ListRecurringSchedules(ctx)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	ids := make([]uint64, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ScheduleID)
	}
	last, err := s.store.MaxOccurDates(ctx, ids)
	if err != nil {
		return 0, err
	}
	total := 0
	for i := range rows {
		insts, err := extensionInstances(&rows[i], last[rows[i].ScheduleID], today)
		if err != nil {
			logx.Warn(ctx, "legacy_operation", "message", logx.Format("[schedule] 续展跳过 schedule_id=%d: %v", rows[i].ScheduleID, err))
			continue
		}
		if len(insts) == 0 {
			continue
		}
		if err := s.store.InsertInstances(ctx, insts); err != nil {
			return total, err
		}
		total += len(insts)
	}
	return total, nil
}

// extensionInstances 计算单个重复日程需要续展的实例：从起点完整展开（保证 COUNT 语义正确），
// 只保留 > last 且 >= today 的日期，窗口上限 today+90。
func extensionInstances(sch *Schedule, last *time.Time, today time.Time) ([]ScheduleInstance, error) {
	r, err := ParseRRule(sch.RRule)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, nil
	}
	end := today.AddDate(0, 0, materializeWindowDays)
	var out []ScheduleInstance
	for _, d := range occurrencesBetween(r, sch.StartDate, sch.StartDate, end) {
		if d.Before(today) || (last != nil && !d.After(dateOnly(*last))) {
			continue
		}
		out = append(out, newInstance(sch, d))
	}
	return out, nil
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
	sch.IdempotencyKey = existing.IdempotencyKey

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
	if err := s.store.ReplaceFutureInstances(ctx, sch, instances, today, existing.OwnerMemberID); err != nil {
		return nil, err
	}
	s.notifyOwner(ctx, familyID, sch)
	return &CreateResult{ScheduleID: scheduleID, Conflicts: conflicts}, nil
}

// ListSchedules 仅管理员与普通家长可读取全家庭模板。
func (s *Service) ListSchedules(ctx context.Context, userID, familyID uint64) ([]ScheduleView, error) {
	_, role, err := s.id.RequireActive(ctx, userID, familyID)
	if err != nil {
		return nil, err
	}
	if role != "admin" && role != "parent" {
		return nil, ErrForbidden
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

// DeleteSchedule 软删模板并清空 from 当天及之后的已物化实例，历史实例保留用于时间线/历史回看。
func (s *Service) DeleteSchedule(ctx context.Context, userID, familyID, scheduleID uint64, from time.Time) error {
	if err := s.requireWriter(ctx, userID, familyID); err != nil {
		return err
	}
	return s.store.RemoveSchedule(ctx, familyID, scheduleID, dateOnly(from))
}

// ListInstances 按 membership 生成本人或全家庭范围，再查询日期内实例。
func (s *Service) ListInstances(ctx context.Context, userID, familyID uint64, start, end time.Time) ([]InstanceView, error) {
	owner, err := s.executionScope(ctx, userID, familyID)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListInstances(ctx, familyID, start, end, owner...)
	if err != nil {
		return nil, err
	}
	views := make([]InstanceView, 0, len(rows))
	rrules, err := s.rrulesForInstances(ctx, familyID, rows)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		view := toInstanceView(&rows[i])
		view.RRule = rrules[rows[i].ScheduleID]
		views = append(views, view)
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
		ownerType, _, err := s.id.RequireMember(ctx, familyID, in.OwnerMemberID)
		if err != nil {
			return err
		}
		if ownerType != memberTypeReal {
			return ErrOwnerRequired
		}
		if in.OwnerMemberID != inst.OwnerMemberID {
			updates["owner_member_id"] = in.OwnerMemberID
		}
	}
	if len(updates) == 0 {
		return nil // 幂等：无实际变化（如重指派到同一责任人）视为成功
	}
	if err := s.store.UpdateInstance(ctx, inst, updates); err != nil {
		return err
	}
	inst.ChangeVersion++
	if status, ok := updates["status"].(string); ok && status == StatusCancelled {
		s.notifyInstanceOwner(ctx, familyID, inst, "schedule_cancelled")
	}
	if ownerID, ok := updates["owner_member_id"].(uint64); ok {
		oldOwnerID := inst.OwnerMemberID
		if oldOwnerID != 0 {
			s.notifyInstanceOwner(ctx, familyID, inst, "schedule_unassigned")
		}
		inst.OwnerMemberID = ownerID
		s.notifyInstanceOwner(ctx, familyID, inst, "schedule_reassigned")
	}
	return nil
}

// CompleteInstance 在实例锁事务内校验当前 owner、状态并写入完成事件。
func (s *Service) CompleteInstance(ctx context.Context, userID, familyID, instanceID uint64) error {
	owner, err := s.executionScope(ctx, userID, familyID)
	if err != nil {
		return err
	}
	return s.store.CompleteInstanceWithEvent(ctx, familyID, instanceID, owner...)
}

// RemindDue 扫描即将开始的日程实例并创建触达任务（Worker 每 30s 调用）。
func (s *Service) RemindDue(ctx context.Context, now time.Time) error {
	if s.notify == nil {
		return nil
	}
	rows, err := s.store.ListReminderDueInstances(ctx, now.UTC(), now.UTC().Add(scheduleReminderLead))
	if err != nil {
		return err
	}
	for i := range rows {
		inst := &rows[i]
		userID, err := s.id.MemberUserID(ctx, inst.FamilyID, inst.OwnerMemberID)
		if err != nil {
			logx.Warn(ctx, "legacy_operation", "message", logx.Format("[schedule] 触达解析 owner user_id 失败 instance=%d err=%v", inst.InstanceID, err))
			continue
		}
		page := instancePage(inst)
		if _, err := s.notify.CreateJob(ctx, NotifyInput{
			FamilyID:     inst.FamilyID,
			TargetUserID: userID,
			Level:        levelStrong,
			Payload: map[string]any{
				"type":        "schedule_reminder",
				"title":       inst.Title,
				"time_text":   inst.StartAt.In(appLocation).Format("15:04"),
				"start_at":    inst.StartAt.Format(time.RFC3339),
				"location":    inst.Location,
				"instance_id": inst.InstanceID,
				"schedule_id": inst.ScheduleID,
				"page":        page,
			},
			BusinessKey: fmt.Sprintf("schedule_remind:%d:%d", inst.InstanceID, userID),
		}); err != nil {
			logx.Error(ctx, "legacy_operation", "message", logx.Format("[schedule] 创建日程到点提醒失败 instance=%d err=%v", inst.InstanceID, err))
		}
	}
	return nil
}

// SubmitFeedback 执行端提交「要调整」反馈：待处理状态下重复提交只更新原因和时间，并强触达管理员。
func (s *Service) SubmitFeedback(ctx context.Context, userID, familyID, instanceID uint64, in *FeedbackInput) error {
	reporterID, role, err := s.id.RequireActive(ctx, userID, familyID)
	if err != nil {
		return err
	}
	if !validFeedbackReason(in.Reason) {
		return ErrBadParam
	}
	inst, err := s.store.FindInstance(ctx, familyID, instanceID)
	if err != nil {
		return err
	}
	if inst == nil {
		return ErrInstanceNotFound
	}
	owner := []uint64{}
	if role != "admin" && role != "parent" {
		owner = append(owner, reporterID)
	}
	if err := s.store.UpsertPendingFeedbackWithEvent(ctx, &ScheduleFeedback{
		FamilyID:   familyID,
		InstanceID: instanceID,
		ScheduleID: inst.ScheduleID,
		ReporterID: reporterID,
		Reason:     in.Reason,
		Status:     StatusPending,
	}, owner...); err != nil {
		return err
	}
	s.notifyAdminsFeedback(ctx, familyID, inst, in.Reason)
	return nil
}

func (s *Service) ListFeedbacks(ctx context.Context, userID, familyID uint64, status string) ([]FeedbackView, error) {
	if err := s.requireWriter(ctx, userID, familyID); err != nil {
		return nil, err
	}
	if status == "" {
		status = StatusPending
	}
	if !validFeedbackStatus(status) {
		return nil, ErrBadParam
	}
	rows, err := s.store.ListFeedbacks(ctx, familyID, status)
	if err != nil {
		return nil, err
	}
	out := make([]FeedbackView, 0, len(rows))
	for i := range rows {
		out = append(out, toFeedbackView(&rows[i]))
	}
	return out, nil
}

func (s *Service) ResolveFeedback(ctx context.Context, userID, familyID, feedbackID uint64, in *FeedbackResolveInput) error {
	if err := s.requireWriter(ctx, userID, familyID); err != nil {
		return err
	}
	if in.Status != StatusHandled && in.Status != StatusIgnored {
		return ErrBadParam
	}
	return s.store.UpdateFeedbackStatus(ctx, familyID, feedbackID, in.Status)
}

// ---- 内部辅助 ----

func (s *Service) rrulesForInstances(ctx context.Context, familyID uint64, rows []ScheduleInstance) (map[uint64]string, error) {
	ids := map[uint64]bool{}
	for i := range rows {
		ids[rows[i].ScheduleID] = true
	}
	if len(ids) == 0 {
		return map[uint64]string{}, nil
	}
	schedules, err := s.store.ListSchedules(ctx, familyID)
	if err != nil {
		return nil, err
	}
	out := map[uint64]string{}
	for i := range schedules {
		if ids[schedules[i].ScheduleID] {
			out[schedules[i].ScheduleID] = schedules[i].RRule
		}
	}
	return out, nil
}

// notifyOwner 接送类日程创建/变更后立即强触达执行端 owner（R4.3：创建→收到<1min）。
// 触达是尽力而为，失败不阻塞日程主流程（log 后忽略）。到点提醒（开始前 X 分钟）需 Worker 扫实例表，留 TODO。
func (s *Service) notifyOwner(ctx context.Context, familyID uint64, sch *Schedule) {
	if s.notify == nil || sch.Type != TypePickup || sch.OwnerMemberID == 0 {
		return
	}
	inst, err := s.store.FirstOwnedInstance(ctx, familyID, sch.ScheduleID, sch.OwnerMemberID)
	if err != nil {
		logx.Warn(ctx, "legacy_operation", "message", logx.Format("[schedule] instance notification lookup: %v", err))
		return
	}
	if inst != nil {
		s.notifyInstanceOwner(ctx, familyID, inst, "schedule_updated")
	}

}

func (s *Service) notifyInstanceOwner(ctx context.Context, familyID uint64, inst *ScheduleInstance, eventType string) {
	if s.notify == nil || inst.OwnerMemberID == 0 {
		return
	}
	userID, err := s.id.MemberUserID(ctx, familyID, inst.OwnerMemberID)
	if err != nil {
		logx.Warn(ctx, "legacy_operation", "message", logx.Format("[schedule] 改分工触达解析 owner user_id 失败 instance=%d err=%v", inst.InstanceID, err))
		return
	}
	page := instancePage(inst)
	payload := map[string]any{
		"type":        eventType,
		"title":       inst.Title,
		"time_text":   inst.StartAt.In(appLocation).Format("15:04"),
		"start_at":    inst.StartAt.Format(time.RFC3339),
		"location":    inst.Location,
		"instance_id": inst.InstanceID,
		"schedule_id": inst.ScheduleID,
		"page":        page,
	}
	if eventType == "schedule_unassigned" {
		payload = map[string]any{"type": eventType, "title": "分工已调整", "page": "pages/gp-today/index"}
	}

	if _, err := s.notify.CreateJob(ctx, NotifyInput{
		FamilyID:     familyID,
		TargetUserID: userID,
		Level:        levelStrong,
		Payload:      payload,
		BusinessKey:  "schedule_instance:" + eventType + ":" + instKey(inst.InstanceID) + ":" + instKey(inst.ChangeVersion) + ":" + instKey(userID),
	}); err != nil {
		logx.Error(ctx, "legacy_operation", "message", logx.Format("[schedule] 创建改分工触达任务失败 instance=%d err=%v", inst.InstanceID, err))
	}
}

func (s *Service) notifyAdminsFeedback(ctx context.Context, familyID uint64, inst *ScheduleInstance, reason string) {
	if s.notify == nil {
		return
	}
	userIDs, err := s.id.AdminUserIDs(ctx, familyID)
	if err != nil {
		logx.Warn(ctx, "legacy_operation", "message", logx.Format("[schedule] 反馈触达解析管理员失败: %v", err))
		return
	}
	for _, userID := range userIDs {
		if _, err := s.notify.CreateJob(ctx, NotifyInput{
			FamilyID:     familyID,
			TargetUserID: userID,
			Level:        levelStrong,
			BusinessKey:  "schedule_feedback:" + instKey(inst.InstanceID) + ":" + instKey(userID),
			Payload: map[string]any{
				"type":        "schedule_feedback",
				"title":       inst.Title,
				"reason":      reason,
				"time_text":   inst.StartAt.Format("15:04"),
				"start_at":    inst.StartAt.Format(time.RFC3339),
				"location":    inst.Location,
				"instance_id": inst.InstanceID,
				"schedule_id": inst.ScheduleID,
				"page":        "pages/today/index",
			},
		}); err != nil {
			logx.Error(ctx, "legacy_operation", "message", logx.Format("[schedule] 创建反馈触达任务失败: %v", err))
		}
	}
}

func instKey(id uint64) string { return strconv.FormatUint(id, 10) }

func validFeedbackReason(reason string) bool {
	switch reason {
	case "今天去不了", "时间不对", "地点/携带物不清楚", "其他，电话说":
		return true
	default:
		return false
	}
}

func validFeedbackStatus(status string) bool {
	return status == StatusPending || status == StatusHandled || status == StatusIgnored
}

func toFeedbackView(f *ScheduleFeedback) FeedbackView {
	return FeedbackView{
		FeedbackID:     f.FeedbackID,
		InstanceID:     f.InstanceID,
		ScheduleID:     f.ScheduleID,
		ReporterID:     f.ReporterID,
		Reason:         f.Reason,
		Status:         f.Status,
		LastReportedAt: f.LastReportedAt.Format(time.RFC3339),
		UpdatedAt:      f.UpdatedAt.Format(time.RFC3339),
	}
}

// requireWriter 校验用户是该家庭的 admin（写日程权限，见 roles.md）。
func (s *Service) requireWriter(ctx context.Context, userID, familyID uint64) error {
	_, role, err := s.id.RequireActive(ctx, userID, familyID)
	if err != nil {
		return err
	}
	if role != roleAdmin {
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
		et, err := parseClock(in.EndTime)
		if err != nil {
			return nil, ErrBadParam
		}
		st, _ := parseClock(in.StartTime)
		if !et.After(st) {
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
	// 在校块无责任人；其余类型责任人必须是真实成员。
	if in.Type == TypeSchool {
		in.OwnerMemberID = 0
	} else {
		ownerType, _, err := s.id.RequireMember(ctx, familyID, in.OwnerMemberID)
		if err != nil {
			return nil, err
		}
		if ownerType != memberTypeReal {
			return nil, ErrOwnerRequired
		}
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
		instances = append(instances, newInstance(sch, d))
	}
	return instances, nil
}

// newInstance 由模板与发生日期构造实例（冗余展示字段）。
func newInstance(sch *Schedule, d time.Time) ScheduleInstance {
	startAt := combineDateTime(d, sch.StartTime)
	var endAt *time.Time
	if sch.EndTime != nil {
		t := combineDateTime(d, *sch.EndTime)
		endAt = &t
	}
	return ScheduleInstance{
		ScheduleID:    sch.ScheduleID,
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
	}
}

// conflicts 批量拉取候选后在内存中检测时间重叠，按 instance_id 去重返回冲突视图。
func (s *Service) conflicts(ctx context.Context, familyID, childID, ownerID uint64, instances []ScheduleInstance, excludeScheduleID uint64) ([]InstanceView, error) {
	return s.conflictsForSpecs(ctx, familyID, []conflictSpec{{ChildID: childID, OwnerID: ownerID, Instances: instances}}, excludeScheduleID)
}

type conflictSpec struct {
	ChildID   uint64
	OwnerID   uint64
	Instances []ScheduleInstance
}

// conflictsForSpecs 批量冲突检测：先用一次 SQL 拉出本批实例时间窗内的候选，再在内存中做精确匹配。
// 这样拍照导入确认的 50 条×90 天实例不会退化成数千次 SQL。
func (s *Service) conflictsForSpecs(ctx context.Context, familyID uint64, specs []conflictSpec, excludeScheduleID uint64) ([]InstanceView, error) {
	minStart, maxEnd, ok := conflictWindow(specs)
	if !ok {
		return nil, nil
	}
	children := uniqueChildren(specs)
	owners := uniqueOwners(specs)
	candidates, err := s.store.FindConflictCandidates(ctx, familyID, children, owners, minStart, maxEnd, excludeScheduleID)
	if err != nil {
		return nil, err
	}
	seen := map[uint64]bool{}
	var out []InstanceView
	for _, spec := range specs {
		for _, inst := range spec.Instances {
			for i := range candidates {
				cand := &candidates[i]
				if seen[cand.InstanceID] || !sameConflictScope(spec.ChildID, spec.OwnerID, cand) || !timeOverlap(inst, *cand) {
					continue
				}
				seen[cand.InstanceID] = true
				out = append(out, toInstanceView(cand))
			}
		}
	}
	return out, nil
}

func (s *Service) batchConflicts(ctx context.Context, familyID uint64, specs []conflictSpec, excludeScheduleID uint64) ([]BatchConflict, error) {
	minStart, maxEnd, ok := conflictWindow(specs)
	if !ok {
		return nil, nil
	}
	candidates, err := s.store.FindConflictCandidates(ctx, familyID, uniqueChildren(specs), uniqueOwners(specs), minStart, maxEnd, excludeScheduleID)
	if err != nil {
		return nil, err
	}
	out := make([]BatchConflict, 0)
	for idx, spec := range specs {
		seen := map[uint64]bool{}
		for _, inst := range spec.Instances {
			for i := range candidates {
				cand := &candidates[i]
				if seen[cand.InstanceID] || !sameConflictScope(spec.ChildID, spec.OwnerID, cand) || !timeOverlap(inst, *cand) {
					continue
				}
				seen[cand.InstanceID] = true
				view := toInstanceView(cand)
				out = append(out, BatchConflict{Index: idx, Instance: &view})
			}
		}
	}
	return out, nil
}

func conflictWindow(specs []conflictSpec) (time.Time, time.Time, bool) {
	var minStart, maxEnd time.Time
	ok := false
	for _, spec := range specs {
		for _, inst := range spec.Instances {
			endAt := effectiveEnd(inst)
			if !ok || inst.StartAt.Before(minStart) {
				minStart = inst.StartAt
			}
			if !ok || endAt.After(maxEnd) {
				maxEnd = endAt
			}
			ok = true
		}
	}
	return minStart, maxEnd, ok
}

func uniqueChildren(specs []conflictSpec) []uint64 {
	seen := map[uint64]bool{}
	out := make([]uint64, 0, len(specs))
	for _, spec := range specs {
		if spec.ChildID == 0 || seen[spec.ChildID] {
			continue
		}
		seen[spec.ChildID] = true
		out = append(out, spec.ChildID)
	}
	return out
}

func uniqueOwners(specs []conflictSpec) []uint64 {
	seen := map[uint64]bool{}
	out := make([]uint64, 0, len(specs))
	for _, spec := range specs {
		if spec.OwnerID == 0 || seen[spec.OwnerID] {
			continue
		}
		seen[spec.OwnerID] = true
		out = append(out, spec.OwnerID)
	}
	return out
}

func sameConflictScope(childID, ownerID uint64, cand *ScheduleInstance) bool {
	if cand.ChildMemberID == childID {
		return true
	}
	return ownerID != 0 && cand.OwnerMemberID == ownerID
}

func timeOverlap(a, b ScheduleInstance) bool {
	ae, be := effectiveEnd(a), effectiveEnd(b)
	return b.StartAt.Before(ae) && be.After(a.StartAt) || a.StartAt.Equal(b.StartAt)
}

func effectiveEnd(inst ScheduleInstance) time.Time {
	if inst.EndAt != nil {
		return *inst.EndAt
	}
	return inst.StartAt
}

// legacyConflicts 保留逐实例冲突检测实现，便于必要时回退/对照测试。
func (s *Service) legacyConflicts(ctx context.Context, familyID, childID, ownerID uint64, instances []ScheduleInstance, excludeScheduleID uint64) ([]InstanceView, error) {
	seen := map[uint64]bool{}
	var out []InstanceView
	for _, inst := range instances {
		hits, err := s.store.FindConflicts(ctx, familyID, childID, ownerID, inst.StartAt, effectiveEnd(inst), excludeScheduleID)
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
	case TypeCourse, TypePickup, TypeActivity, TypeReminder, TypeSchool:
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
		FamilyID:      inst.FamilyID,
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

// combineDateTime 把上海本地日期与时钟合并后转为 UTC 存储。
func combineDateTime(d time.Time, clock string) time.Time {
	t, err := parseClock(clock)
	if err != nil {
		return d
	}
	return time.Date(d.Year(), d.Month(), d.Day(), t.Hour(), t.Minute(), t.Second(), 0, appLocation).UTC()
}

// dateOnly 归一化到 UTC 当天 00:00（物化/删除窗口的边界比较用）。
func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func (s *Service) executionScope(ctx context.Context, userID, familyID uint64) ([]uint64, error) {
	member, role, err := s.id.RequireActive(ctx, userID, familyID)
	if err != nil {
		return nil, err
	}
	if role == "admin" || role == "parent" {
		return nil, nil
	}
	return []uint64{member}, nil
}
func (s *Service) GetInstance(ctx context.Context, userID, familyID, instanceID uint64) (*InstanceView, error) {
	owner, err := s.executionScope(ctx, userID, familyID)
	if err != nil {
		return nil, err
	}
	inst, err := s.store.FindInstance(ctx, familyID, instanceID)
	if err != nil {
		return nil, err
	}
	if inst == nil {
		return nil, ErrInstanceNotFound
	}
	if len(owner) > 0 && owner[0] != inst.OwnerMemberID {
		return nil, ErrForbidden
	}
	view := toInstanceView(inst)
	rules, err := s.rrulesForInstances(ctx, familyID, []ScheduleInstance{*inst})
	if err != nil {
		return nil, err
	}
	view.RRule = rules[inst.ScheduleID]
	return &view, nil
}
func (s *Service) NotificationVisibility(ctx context.Context, userID, familyID, instanceID uint64) (bool, error) {
	owner, err := s.executionScope(ctx, userID, familyID)
	if err != nil {
		return false, err
	}
	inst, err := s.store.FindInstance(ctx, familyID, instanceID)
	if err != nil {
		return false, err
	}
	return inst != nil && (len(owner) == 0 || owner[0] == inst.OwnerMemberID), nil
}
func instancePage(inst *ScheduleInstance) string {
	return fmt.Sprintf("pages/event-detail/index?instance_id=%d&family_id=%d", inst.InstanceID, inst.FamilyID)
}
