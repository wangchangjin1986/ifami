// Package schedule 负责日程、RRULE 物化与冲突检测。
// 数据模型对齐 wiki/architecture/adr-003-schedule-materialization.md。
package schedule

import "time"

// 日程类型与状态常量（术语表 wiki/product/glossary.md）。
const (
	TypeCourse   = "course"
	TypePickup   = "pickup"
	TypeActivity = "activity"
	TypeReminder = "reminder"

	StatusActive    = "active"
	StatusCancelled = "cancelled" // 单实例：取消本次
	StatusDone      = "done"      // 单实例：已完成
	StatusRemoved   = "removed"   // 模板：软删除
)

// 成员类型/角色枚举值（对齐 identity 模块，见 wiki/product/roles.md）。
// 跨模块只走接口传字符串值，不 import identity，保持模块边界。
const (
	memberTypeReal    = "real"
	memberTypeVirtual = "virtual"
	roleAdmin         = "admin"
	roleParent        = "parent"
)

// Schedule 日程模板。重复规则存 rrule，实例物化到 ScheduleInstance。
type Schedule struct {
	ScheduleID    uint64    `gorm:"column:schedule_id;primaryKey"`
	FamilyID      uint64    `gorm:"column:family_id"`
	ChildMemberID uint64    `gorm:"column:child_member_id"`
	Title         string    `gorm:"column:title"`
	Type          string    `gorm:"column:type"`
	StartDate     time.Time `gorm:"column:start_date"`
	RRule         string    `gorm:"column:rrule"`
	StartTime     string    `gorm:"column:start_time"`
	EndTime       *string   `gorm:"column:end_time"`
	Location      string    `gorm:"column:location"`
	Note          string    `gorm:"column:note"`
	OwnerMemberID uint64    `gorm:"column:owner_member_id"`
	Status        string    `gorm:"column:status"`
	CreatedAt     time.Time `gorm:"column:created_at"`
}

func (Schedule) TableName() string { return "schedules" }

// ScheduleInstance 日程实例（RRULE 物化出的单次发生）。冗余展示字段，视图只读本表。
type ScheduleInstance struct {
	InstanceID    uint64     `gorm:"column:instance_id;primaryKey"`
	ScheduleID    uint64     `gorm:"column:schedule_id"`
	FamilyID      uint64     `gorm:"column:family_id"`
	ChildMemberID uint64     `gorm:"column:child_member_id"`
	Title         string     `gorm:"column:title"`
	Type          string     `gorm:"column:type"`
	OccurDate     time.Time  `gorm:"column:occur_date"`
	StartAt       time.Time  `gorm:"column:start_at"`
	EndAt         *time.Time `gorm:"column:end_at"`
	Location      string     `gorm:"column:location"`
	Note          string     `gorm:"column:note"`
	OwnerMemberID uint64     `gorm:"column:owner_member_id"`
	Status        string     `gorm:"column:status"`
}

func (ScheduleInstance) TableName() string { return "schedule_instances" }
