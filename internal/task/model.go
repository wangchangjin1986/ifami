// Package task 负责打卡任务、提交与事件流写入（ADR-004：记录+反馈，激励后置）。
// 数据模型对齐 migrations tasks / checkins / events_stream。
package task

import "time"

// 目标类型与状态常量（术语表 wiki/product/glossary.md）。
// 单位约定：GoalCount 的 goal_value=目标次数；GoalTimer 的 goal_value=目标分钟数，
// 对应 checkin payload.seconds 存秒；GoalPhoto 无量化目标（照片走 media_tos_key）。
const (
	GoalPhoto = "photo"
	GoalTimer = "timer"
	GoalCount = "count"

	StatusActive  = "active"
	StatusRemoved = "removed"
)

// 成员类型/角色枚举值（对齐 identity 模块，见 wiki/product/roles.md）。
// 跨模块只走接口传字符串值，不 import identity，保持模块边界。
const (
	memberTypeReal    = "real"
	memberTypeVirtual = "virtual"
	roleAdmin         = "admin"
	roleParent        = "parent"
)

// 事件流类型（ADR-004：V1 只写不消费，千人千面激励预留钩子）。
const (
	EventCheckinDone = "checkin_done"
	EventGoalHit     = "goal_hit"
	EventStreak      = "streak"
)

// Task 打卡任务（重复性目标行为：阅读/跳绳…），进打卡。
type Task struct {
	TaskID        uint64    `gorm:"column:task_id;primaryKey"`
	FamilyID      uint64    `gorm:"column:family_id"`
	ChildMemberID uint64    `gorm:"column:child_member_id"`
	Name          string    `gorm:"column:name"`
	GoalType      string    `gorm:"column:goal_type"`
	GoalValue     int       `gorm:"column:goal_value"`
	Freq          string    `gorm:"column:freq"`
	OwnerMemberID uint64    `gorm:"column:owner_member_id"`
	RemindTime    string    `gorm:"column:remind_time"` // HH:MM，空=不提醒（R3.3 到点未完成提醒责任父母）
	Status        string    `gorm:"column:status"`
	CreatedAt     time.Time `gorm:"column:created_at"`
}

func (Task) TableName() string { return "tasks" }

// Checkin 打卡记录（任务的一次提交，可由父母/祖辈代打）。
// Payload 存计时/计数，照片走 media_tos_key（TOS 对象存储 key）。
type Checkin struct {
	CheckinID      uint64         `gorm:"column:checkin_id;primaryKey"`
	TaskID         uint64         `gorm:"column:task_id"`
	FamilyID       uint64         `gorm:"column:family_id"`
	ChildMemberID  uint64         `gorm:"column:child_member_id"`
	SubmitMemberID uint64         `gorm:"column:submit_member_id"`
	Payload        map[string]any `gorm:"column:payload;type:json;serializer:json"`
	MediaTosKey    string         `gorm:"column:media_tos_key"`
	IdempotencyKey string         `gorm:"column:idempotency_key"`
	CreatedAt      time.Time      `gorm:"column:created_at"`
}

func (Checkin) TableName() string { return "checkins" }

// Event 事件流记录（events_stream）。task 只写打卡类事件；schedule_done 由 schedule 写。
type Event struct {
	ID        uint64    `gorm:"column:id;primaryKey"`
	FamilyID  uint64    `gorm:"column:family_id"`
	Type      string    `gorm:"column:type"`
	RefID     *uint64   `gorm:"column:ref_id"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

func (Event) TableName() string { return "events_stream" }
