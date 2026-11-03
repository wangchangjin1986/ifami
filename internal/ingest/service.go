package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"ifami/internal/pkg/logx"
)

// Membership 对 identity 的最小依赖（消费方定义接口，跨模块只走接口）。
type Membership interface {
	RequireActive(ctx context.Context, userID, familyID uint64) (memberID uint64, role string, err error)
	ChildNames(ctx context.Context, familyID uint64) (map[uint64]string, error)
	MemberUserID(ctx context.Context, familyID, memberID uint64) (uint64, error)
}

// ScheduleCreator 对 schedule 的最小依赖：确认后批量入库（单事务、冲突逐条返回）。
type ScheduleCreator interface {
	CreateBatch(ctx context.Context, userID, familyID uint64, items []ScheduleItem, force bool) ([]uint64, error)
}

// ScheduleItem 入库条目（对齐 schedule.ScheduleInput，不 import schedule 保持模块边界）。
type ScheduleItem struct {
	Title         string `json:"title"`
	Type          string `json:"type"`
	ChildMemberID uint64 `json:"child_member_id"`
	StartDate     string `json:"start_date"`
	RRule         string `json:"rrule"`
	StartTime     string `json:"start_time"`
	EndTime       string `json:"end_time"`
	Location      string `json:"location"`
	Note          string `json:"note"`
	OwnerMemberID uint64 `json:"owner_member_id"`
}

// Notifier 对 notify 的最小依赖：识别完成提醒（「先去忙」场景）。
type Notifier interface {
	CreateJob(ctx context.Context, in NotifyInput) (uint64, error)
}

// NotifyInput 对齐 notify.CreateJobInput。
type NotifyInput struct {
	FamilyID     uint64
	TargetUserID uint64
	Level        string
	Payload      map[string]any
	BusinessKey  string
}

// Cipher 字段级加密（识别结果含孩子姓名/原文片段）。
type Cipher interface {
	Encrypt([]byte) ([]byte, error)
	Decrypt([]byte) ([]byte, error)
}

const roleAdmin = "admin"

// Service 是 ingest 模块业务层。
type Service struct {
	store     *Store
	images    ImageStore
	rec       Recognizer
	id        Membership
	schedules ScheduleCreator
	notify    Notifier
	cipher    Cipher
	sem       chan struct{}
	running   sync.Map // job_id → struct{}：本进程正在执行的任务
	now       func() time.Time
}

func NewService(store *Store, images ImageStore, rec Recognizer, id Membership, schedules ScheduleCreator, notify Notifier, cipher Cipher) *Service {
	return &Service{
		store: store, images: images, rec: rec, id: id, schedules: schedules, notify: notify, cipher: cipher,
		sem: make(chan struct{}, runConcurrency),
		now: func() time.Time { return time.Now().UTC() },
	}
}

// JobView 任务视图（进度 + 完成后草稿）。
type JobView struct {
	JobID     uint64        `json:"job_id"`
	Status    string        `json:"status"`
	Total     int           `json:"total"`
	Done      int           `json:"done"`
	ErrorCode int           `json:"error_code,omitempty"`
	Images    []ImageResult `json:"images,omitempty"`
	Drafts    []Draft       `json:"drafts,omitempty"`
	CreatedAt string        `json:"created_at"`
}

// CreateJob 创建上传中的任务。
func (s *Service) CreateJob(ctx context.Context, userID, familyID uint64) (uint64, error) {
	if err := s.requireAdmin(ctx, userID, familyID); err != nil {
		return 0, err
	}
	j := &Job{FamilyID: familyID, CreatorUserID: userID, Status: StatusUploading, Images: []JobImage{}, CreatedAt: s.now()}
	if err := s.store.CreateJob(ctx, j); err != nil {
		return 0, err
	}
	return j.JobID, nil
}

// UploadImage 上传单张图（按文件头校验格式），落本地临时目录。
func (s *Service) UploadImage(ctx context.Context, userID, familyID, jobID uint64, seq int, childMemberID uint64, data []byte) error {
	if err := s.requireAdmin(ctx, userID, familyID); err != nil {
		return err
	}
	if seq < 0 || seq >= MaxImagesPerJob {
		return ErrBadParam
	}
	if len(data) == 0 || len(data) > MaxImageBytes {
		return ErrBadImage
	}
	if _, ok := detectImageMIME(data); !ok {
		return ErrBadImage
	}
	if childMemberID != 0 {
		children, err := s.id.ChildNames(ctx, familyID)
		if err != nil {
			return err
		}
		if _, ok := children[childMemberID]; !ok {
			return ErrBadParam
		}
	}
	j, err := s.store.FindJob(ctx, familyID, jobID)
	if err != nil {
		return err
	}
	if j == nil {
		return ErrJobNotFound
	}
	if j.Status != StatusUploading {
		return ErrBadState
	}
	if err := s.images.Put(familyID, jobID, seq, data); err != nil {
		return err
	}
	replaced := false
	for i := range j.Images {
		if j.Images[i].Seq == seq {
			j.Images[i] = JobImage{Seq: seq, ChildMemberID: childMemberID, Status: ImageWaiting}
			replaced = true
		}
	}
	if !replaced {
		j.Images = append(j.Images, JobImage{Seq: seq, ChildMemberID: childMemberID, Status: ImageWaiting})
	}
	return s.store.SaveImages(ctx, j)
}

// StartJob 校验额度后置 pending，并在本进程异步执行。
func (s *Service) StartJob(ctx context.Context, userID, familyID, jobID uint64) error {
	if err := s.requireAdmin(ctx, userID, familyID); err != nil {
		return err
	}
	j, err := s.store.FindJob(ctx, familyID, jobID)
	if err != nil {
		return err
	}
	if j == nil {
		return ErrJobNotFound
	}
	if j.Status != StatusUploading {
		return ErrBadState
	}
	if len(j.Images) == 0 {
		return ErrBadParam
	}
	now := s.now()
	used, err := s.store.CountImagesToday(ctx, familyID, time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC))
	if err != nil {
		return err
	}
	if used+len(j.Images) > DailyImageQuota {
		return ErrQuota
	}
	ok, err := s.store.Transition(ctx, jobID, []string{StatusUploading}, map[string]any{"status": StatusPending, "heartbeat_at": now})
	if err != nil {
		return err
	}
	if !ok {
		return ErrBadState
	}
	go s.Run(context.Background(), jobID, nil)
	return nil
}

// GetJob 查任务进度与草稿。
func (s *Service) GetJob(ctx context.Context, userID, familyID, jobID uint64) (*JobView, error) {
	if err := s.requireAdmin(ctx, userID, familyID); err != nil {
		return nil, err
	}
	j, err := s.store.FindJob(ctx, familyID, jobID)
	if err != nil {
		return nil, err
	}
	if j == nil {
		return nil, ErrJobNotFound
	}
	return s.toView(j, true)
}

// ListJobs 列近 7 天任务（status 可选），不含草稿明细。
func (s *Service) ListJobs(ctx context.Context, userID, familyID uint64, status string) ([]JobView, error) {
	if err := s.requireAdmin(ctx, userID, familyID); err != nil {
		return nil, err
	}
	rows, err := s.store.ListJobs(ctx, familyID, status)
	if err != nil {
		return nil, err
	}
	out := make([]JobView, 0, len(rows))
	for i := range rows {
		v, err := s.toView(&rows[i], false)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, nil
}

// Confirm 提交最终日程列表：经 schedule 模块单事务批量入库，成功后任务置 confirmed。
func (s *Service) Confirm(ctx context.Context, userID, familyID, jobID uint64, items []ScheduleItem, force bool) ([]uint64, error) {
	if err := s.requireAdmin(ctx, userID, familyID); err != nil {
		return nil, err
	}
	if len(items) == 0 || len(items) > MaxDraftsPerJob {
		return nil, ErrBadParam
	}
	j, err := s.store.FindJob(ctx, familyID, jobID)
	if err != nil {
		return nil, err
	}
	if j == nil {
		return nil, ErrJobNotFound
	}
	if j.Status != StatusDone {
		return nil, ErrBadState
	}
	ids, err := s.schedules.CreateBatch(ctx, userID, familyID, items, force)
	if err != nil {
		return nil, err // 冲突（2005）/ 校验错误透传给前端
	}
	stats := j.Stats
	stats.Confirmed = len(ids)
	if _, err := s.store.Transition(ctx, jobID, []string{StatusDone}, map[string]any{
		"status": StatusConfirmed, "stats": gormJSON(stats),
	}); err != nil {
		logx.Errorf("[ingest] 任务 %d 置 confirmed 失败: %v", jobID, err)
	}
	return ids, nil
}

// Run 执行识别。staleBefore=nil：API 刚 start 的任务（pending → running）；
// 非 nil：Worker 接管心跳早于该时间的任务。执行权由 Store.Claim 的 CAS 保证唯一。
func (s *Service) Run(ctx context.Context, jobID uint64, staleBefore *time.Time) {
	if _, loaded := s.running.LoadOrStore(jobID, struct{}{}); loaded {
		return
	}
	defer s.running.Delete(jobID)
	s.sem <- struct{}{}
	defer func() { <-s.sem }()

	now := s.now()
	ok, err := s.store.Claim(ctx, jobID, now, staleBefore)
	if err != nil || !ok {
		return
	}
	j, err := s.store.FindJobByID(ctx, jobID)
	if err != nil || j == nil {
		return
	}
	stop := s.startHeartbeat(ctx, jobID)
	s.execute(ctx, j, now)
	stop()
}

func (s *Service) startHeartbeat(ctx context.Context, jobID uint64) func() {
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(heartbeatInterval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				_ = s.store.Heartbeat(ctx, jobID, s.now())
			}
		}
	}()
	return func() { close(done) }
}

// execute 逐张识别 → 归一化 → 加密存结果 → 删除原图 → 必要时通知。
func (s *Service) execute(ctx context.Context, j *Job, started time.Time) {
	children, err := s.id.ChildNames(ctx, j.FamilyID)
	if err != nil {
		logx.Errorf("[ingest] 任务 %d 读取孩子失败: %v", j.JobID, err)
		children = map[uint64]string{}
	}
	var inputs []imageInput
	var results []ImageResult
	failed := 0
	for i := range j.Images {
		img := &j.Images[i]
		out, err := s.recognizeOne(ctx, j, img.Seq)
		if err != nil {
			logx.Warnf("[ingest] 任务 %d 第 %d 张识别失败: %v", j.JobID, img.Seq, err)
			img.Status = ImageFailed
			failed++
			results = append(results, ImageResult{Seq: img.Seq})
			continue
		}
		img.Status = ImageDone
		inputs = append(inputs, imageInput{Seq: img.Seq, ChildMemberID: img.ChildMemberID, Output: out})
		results = append(results, ImageResult{Seq: img.Seq, ImageKind: out.ImageKind, OK: true})
	}
	drafts := normalize(inputs, children)

	finished := s.now()
	j.FinishedAt = &finished
	j.Stats = JobStats{DurationMs: finished.Sub(started).Milliseconds(), Drafts: len(drafts), FailedImages: failed}
	for _, d := range drafts {
		if d.LowConfidence || d.Unreadable {
			j.Stats.LowConfidence++
		}
	}
	if len(drafts) == 0 {
		j.Status = StatusFailed
		j.ErrorCode = ErrRecognize.Code
	} else {
		j.Status = StatusDone
		enc, err := s.encryptResult(&Result{Images: results, Drafts: drafts})
		if err != nil {
			logx.Errorf("[ingest] 任务 %d 结果加密失败: %v", j.JobID, err)
			j.Status = StatusFailed
			j.ErrorCode = 5000
		}
		j.ResultEnc = enc
	}
	if _, err := s.store.SaveFinal(ctx, j); err != nil {
		logx.Errorf("[ingest] 任务 %d 保存结果失败: %v", j.JobID, err)
		return
	}
	// 终态即删原图（ADR-006 修订记录 2026-11-03）。
	if err := s.images.DeleteJob(j.FamilyID, j.JobID); err != nil {
		logx.Warnf("[ingest] 任务 %d 删除原图失败（Worker 将兜底清理）: %v", j.JobID, err)
	}
	s.notifyDone(ctx, j)
}

// recognizeOne 单图识别：读图 → 校验格式 → 调模型，失败重试 1 次。
func (s *Service) recognizeOne(ctx context.Context, j *Job, seq int) (*ModelOutput, error) {
	data, err := s.images.Get(j.FamilyID, j.JobID, seq)
	if err != nil {
		return nil, err
	}
	mime, ok := detectImageMIME(data)
	if !ok {
		return nil, errors.New("图片格式不支持")
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		out, err := s.rec.Recognize(ctx, mime, data)
		if err == nil {
			return out, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// notifyDone 识别完成后给发起人发 medium 级提醒（用户可能已「先去忙」离开）。
func (s *Service) notifyDone(ctx context.Context, j *Job) {
	if s.notify == nil {
		return
	}
	msg := "课表识别好了，点开核对后加入日历"
	if j.Status == StatusFailed {
		msg = "课表没识别出来，可以换张清晰的图或手动安排"
	}
	if _, err := s.notify.CreateJob(ctx, NotifyInput{
		FamilyID:     j.FamilyID,
		TargetUserID: j.CreatorUserID,
		Level:        "medium",
		Payload:      map[string]any{"title": msg, "page": "pages/import-confirm/index?job_id=" + uitoa(j.JobID)},
		BusinessKey:  "ingest_done:" + uitoa(j.JobID),
	}); err != nil {
		logx.Warnf("[ingest] 任务 %d 完成提醒创建失败: %v", j.JobID, err)
	}
}

// RecoverStale Worker 每轮调用：接管 pending 超时 / running 心跳中断的任务；超过次数置 failed。
func (s *Service) RecoverStale(ctx context.Context) error {
	now := s.now()
	pendingBefore, runningBefore := now.Add(-stalePendingAfter), now.Add(-staleHeartbeatAfter)
	rows, err := s.store.ListStale(ctx, pendingBefore, runningBefore)
	if err != nil {
		return err
	}
	for i := range rows {
		j := rows[i]
		if j.Attempts >= maxAttempts {
			fin := now
			if ok, err := s.store.Transition(ctx, j.JobID, []string{StatusPending, StatusRunning}, map[string]any{
				"status": StatusFailed, "error_code": ErrRecognize.Code, "finished_at": fin,
			}); err == nil && ok {
				_ = s.images.DeleteJob(j.FamilyID, j.JobID)
				j.Status = StatusFailed
				s.notifyDone(ctx, &j)
			}
			continue
		}
		cutoff := runningBefore
		if j.Status == StatusPending {
			cutoff = pendingBefore
		}
		logx.Infof("[ingest] Worker 接管任务 %d（第 %d 次执行）", j.JobID, j.Attempts+1)
		// 异步执行，避免阻塞 Worker 的触达扫描（接送提醒不能被识别耗时拖慢）。
		go s.Run(context.Background(), j.JobID, &cutoff)
	}
	return nil
}

// CleanupOrphans Worker 每轮调用：删除超过 24h 的残留原图目录（进程异常中断兜底）。
func (s *Service) CleanupOrphans() {
	n, err := s.images.CleanupOlderThan(s.now().Add(-orphanDirAfter))
	if err != nil {
		logx.Warnf("[ingest] 清理残留原图失败: %v", err)
		return
	}
	if n > 0 {
		logx.Infof("[ingest] 清理残留原图目录 %d 个", n)
	}
}

func uitoa(v uint64) string { return strconv.FormatUint(v, 10) }

func (s *Service) requireAdmin(ctx context.Context, userID, familyID uint64) error {
	_, role, err := s.id.RequireActive(ctx, userID, familyID)
	if err != nil {
		return err
	}
	if role != roleAdmin {
		return ErrForbidden
	}
	return nil
}

func (s *Service) toView(j *Job, withDrafts bool) (*JobView, error) {
	v := &JobView{
		JobID: j.JobID, Status: j.Status, Total: len(j.Images), ErrorCode: j.ErrorCode,
		CreatedAt: j.CreatedAt.Format(time.RFC3339),
	}
	for _, img := range j.Images {
		if img.Status != ImageWaiting {
			v.Done++
		}
	}
	if withDrafts && (j.Status == StatusDone || j.Status == StatusConfirmed) && len(j.ResultEnc) > 0 {
		r, err := s.decryptResult(j.ResultEnc)
		if err != nil {
			return nil, err
		}
		v.Images, v.Drafts = r.Images, r.Drafts
	}
	return v, nil
}

func (s *Service) encryptResult(r *Result) ([]byte, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return s.cipher.Encrypt(b)
}

func (s *Service) decryptResult(enc []byte) (*Result, error) {
	b, err := s.cipher.Decrypt(enc)
	if err != nil {
		return nil, err
	}
	var r Result
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
