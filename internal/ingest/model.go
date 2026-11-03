// Package ingest 负责拍照导入日程：识别任务、调用方舟识别、草稿归一化、确认入库。
// 设计见 docs/技术方案设计.md §5.5 与 wiki/architecture/adr-005-ai-draft-confirm.md。
// 原图只在识别阶段存在于本地临时目录，任务终态即删（ADR-006 修订记录 2026-11-03）。
package ingest

import "time"

// 任务状态（术语表 wiki/product/glossary.md：拍照导入 ingest_job）。
const (
	StatusUploading = "uploading" // 已创建，等待上传图片
	StatusPending   = "pending"   // 已开始，等待执行
	StatusRunning   = "running"   // 识别中（heartbeat_at 心跳）
	StatusDone      = "done"      // 识别完成，草稿待确认
	StatusFailed    = "failed"    // 识别失败
	StatusConfirmed = "confirmed" // 已确认入库

	ImageWaiting = "waiting"
	ImageDone    = "done"
	ImageFailed  = "failed"
)

// 业务限制。
const (
	MaxImagesPerJob     = 9
	MaxImageBytes       = 5 << 20
	DailyImageQuota     = 20 // 每家庭每天识别张数上限
	MaxDraftsPerJob     = 50 // 与 schedule.MaxBatchSize 一致
	maxAttempts         = 2  // 首次执行 + Worker 接管重跑一次
	runConcurrency      = 2  // 单进程同时执行的任务数
	heartbeatInterval   = 10 * time.Second
	stalePendingAfter   = time.Minute
	staleHeartbeatAfter = 3 * time.Minute
	orphanDirAfter      = 24 * time.Hour
)

// JobImage 任务内单张图片的元数据（不存路径，路径由 ImageStore 按 job/seq 推导）。
type JobImage struct {
	Seq           int    `json:"seq"`
	ChildMemberID uint64 `json:"child_member_id"` // 0 = 自动识别
	Status        string `json:"status"`
}

// JobStats 埋点统计（线上准确率基线）。
type JobStats struct {
	DurationMs    int64 `json:"duration_ms,omitempty"`
	Drafts        int   `json:"drafts,omitempty"`
	LowConfidence int   `json:"low_confidence,omitempty"`
	FailedImages  int   `json:"failed_images,omitempty"`
	Confirmed     int   `json:"confirmed,omitempty"`
}

// Job 拍照导入任务。
type Job struct {
	JobID         uint64     `gorm:"column:job_id;primaryKey"`
	FamilyID      uint64     `gorm:"column:family_id"`
	CreatorUserID uint64     `gorm:"column:creator_user_id"`
	Status        string     `gorm:"column:status"`
	Images        []JobImage `gorm:"column:images;type:json;serializer:json"`
	ResultEnc     []byte     `gorm:"column:result_enc"`
	Stats         JobStats   `gorm:"column:stats;type:json;serializer:json"`
	ErrorCode     int        `gorm:"column:error_code"`
	Attempts      int        `gorm:"column:attempts"`
	HeartbeatAt   *time.Time `gorm:"column:heartbeat_at"`
	CreatedAt     time.Time  `gorm:"column:created_at;autoCreateTime"`
	FinishedAt    *time.Time `gorm:"column:finished_at"`
}

func (Job) TableName() string { return "ingest_jobs" }

// Draft 归一化后的日程草稿（前端确认屏数据结构）。
type Draft struct {
	ImageSeq      int      `json:"image_seq"`
	Title         string   `json:"title"`
	Type          string   `json:"type"`            // course|pickup|activity|school
	ChildMemberID uint64   `json:"child_member_id"` // 0 = 待选
	Weekdays      []int    `json:"weekdays"`        // 1=周一 … 7=周日；单次为空
	Date          string   `json:"date,omitempty"`  // 单次日期 YYYY-MM-DD
	StartTime     string   `json:"start_time"`
	EndTime       string   `json:"end_time"`
	Until         string   `json:"until,omitempty"`
	Location      string   `json:"location"`
	Note          string   `json:"note"`
	LowConfidence bool     `json:"low_confidence"`
	Uncertain     []string `json:"uncertain,omitempty"`
	SourceText    string   `json:"source_text"`
	Unreadable    bool     `json:"unreadable"`
	Generated     bool     `json:"generated,omitempty"` // 系统自动生成（如接放学）
	MergedItems   []Draft  `json:"merged_items,omitempty"`
}

// ImageResult 单张图片的识别结论。
type ImageResult struct {
	Seq       int    `json:"seq"`
	ImageKind string `json:"image_kind"`
	OK        bool   `json:"ok"`
}

// Result 任务识别结果（加密存 result_enc）。
type Result struct {
	Images []ImageResult `json:"images"`
	Drafts []Draft       `json:"drafts"`
}
