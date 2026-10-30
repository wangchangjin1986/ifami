// Package identity 负责微信登录、家庭/成员模型与权限。
// 数据模型对齐 wiki/architecture/adr-001-account-model.md。
package identity

import "time"

// 角色与成员类型常量（术语表 wiki/product/glossary.md）。
const (
	RoleParent      = "parent"
	RoleGrandparent = "grandparent"
	RoleChild       = "child"
	RoleAdmin       = "admin" // 兼容权限判断入参；持久化角色不再使用 admin。

	TypeReal    = "real"
	TypeVirtual = "virtual"

	StatusActive  = "active"
	StatusRemoved = "removed"

	ChannelMiniapp = "miniapp"
)

// User 自然人账号。openid/unionid 不在本表（见 Identity）。
type User struct {
	UserID    uint64    `gorm:"column:user_id;primaryKey"`
	PhoneEnc  []byte    `gorm:"column:phone_enc"`
	Nick      string    `gorm:"column:nick"`
	Avatar    string    `gorm:"column:avatar"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

func (User) TableName() string { return "users" }

// Identity 通道身份：openid 在 channel 内唯一；unionid 预留（升个体户后回填）。
type Identity struct {
	ID        uint64    `gorm:"column:id;primaryKey"`
	UserID    uint64    `gorm:"column:user_id"`
	Channel   string    `gorm:"column:channel"`
	AppID     string    `gorm:"column:appid"`
	OpenID    string    `gorm:"column:openid"`
	UnionID   string    `gorm:"column:unionid"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

func (Identity) TableName() string { return "identities" }

// Family 家庭（账户体系顶层组织单位）。
type Family struct {
	FamilyID  uint64    `gorm:"column:family_id;primaryKey"`
	Name      string    `gorm:"column:name"`
	CreatorID uint64    `gorm:"column:creator_id"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

func (Family) TableName() string { return "families" }

// Member 成员关系（用户与家庭的边）。UserID NULL = 虚拟成员（孩子）。
type Member struct {
	MemberID      uint64    `gorm:"column:member_id;primaryKey"`
	FamilyID      uint64    `gorm:"column:family_id"`
	UserID        *uint64   `gorm:"column:user_id"`
	Type          string    `gorm:"column:type"`
	Role          string    `gorm:"column:role"`
	IsAdmin       bool      `gorm:"column:is_admin"`
	RelationLabel string    `gorm:"column:relation_label"`
	NameEnc       []byte    `gorm:"column:name_enc"`
	PhoneEnc      []byte    `gorm:"column:phone_enc"`
	Status        string    `gorm:"column:status"`
	InvitedBy     *uint64   `gorm:"column:invited_by"`
	CreatedAt     time.Time `gorm:"column:created_at"`
}

func (Member) TableName() string { return "members" }

// FamilyRole 用户在家庭中的角色（列表查询结果）。
type FamilyRole struct {
	FamilyID uint64 `gorm:"column:family_id" json:"family_id"`
	Name     string `gorm:"column:name" json:"name"`
	Role     string `gorm:"column:role" json:"role"`
	IsAdmin  bool   `gorm:"column:is_admin" json:"is_admin"`
}
