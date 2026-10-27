// Package notify 负责触达路由、升级状态机、通道适配与回执（ADR-002）。
// 数据模型对齐 migrations notify_jobs。
package notify

import "time"

// 触达级别与状态常量（术语表 wiki/product/glossary.md + ADR-002）。
const (
	LevelStrong = "strong"
	LevelMedium = "medium"
	LevelWeak   = "weak"

	// 状态机：pending → sent/reddot → receipted | escalated | done。
	StatusPending   = "pending"   // 待发送
	StatusSent      = "sent"      // 已发通道1（订阅消息），等回执/升级红点
	StatusReddot    = "reddot"    // 已升级通道2（应用内待办红点），等升级备份
	StatusReceipted = "receipted" // 已回执（终端，取消后续升级）
	StatusEscalated = "escalated" // 已升级通道3（备份联系人，终端）
	StatusDone      = "done"      // medium/weak 完成（无回执/升级）

	ChannelSubscribe = "wechat_subscribe"
)

// 升级计时（ADR-002）：强触达 15min 无回执→红点；30min 无回执→备份联系人。
const (
	escalateReddotAfter = 15 * time.Minute
	escalateBackupAfter = 30 * time.Minute
)

// NotifyJob 触达任务。channel_state 记录各通道发送/回执状态（看板数据源）。
type NotifyJob struct {
	JobID        uint64         `gorm:"column:job_id;primaryKey"`
	FamilyID     uint64         `gorm:"column:family_id"`
	TargetUserID uint64         `gorm:"column:target_user_id"`
	Level        string         `gorm:"column:level"`
	ChannelState map[string]any `gorm:"column:channel_state;type:json;serializer:json"`
	Payload      map[string]any `gorm:"column:payload;type:json;serializer:json"`
	BusinessKey  string         `gorm:"column:business_key"`
	Status       string         `gorm:"column:status"`
	NextCheckAt  *time.Time     `gorm:"column:next_check_at"`
	CreatedAt    time.Time      `gorm:"column:created_at"`
}

func (NotifyJob) TableName() string { return "notify_jobs" }
