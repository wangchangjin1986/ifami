package identity

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// Store 是 identity 模块的 DB 访问层，只读写本模块的表
// （users / identities / families / members）。
type Store struct {
	db *gorm.DB
}

func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// FindIdentityByOpenID 按 (channel, appid, openid) 查身份，未找到返回 (nil, nil)。
func (s *Store) FindIdentityByOpenID(ctx context.Context, channel, appid, openid string) (*Identity, error) {
	var id Identity
	err := s.db.WithContext(ctx).
		Where("channel = ? AND appid = ? AND openid = ?", channel, appid, openid).
		First(&id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// FindIdentityByUser 查用户某通道的身份（notify 发订阅消息需 openid），未找到返回 (nil, nil)。
func (s *Store) FindIdentityByUser(ctx context.Context, userID uint64, channel string) (*Identity, error) {
	var id Identity
	err := s.db.WithContext(ctx).
		Where("user_id = ? AND channel = ?", userID, channel).
		First(&id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// CreateUserWithIdentity 事务创建 user + 关联 identity，返回 user_id。
func (s *Store) CreateUserWithIdentity(ctx context.Context, u *User, id *Identity) (uint64, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(u).Error; err != nil {
			return err
		}
		id.UserID = u.UserID
		return tx.Create(id).Error
	})
	return u.UserID, err
}

// FindUser 查用户，未找到返回 (nil, nil)。
func (s *Store) FindUser(ctx context.Context, userID uint64) (*User, error) {
	var u User
	err := s.db.WithContext(ctx).First(&u, userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// FindFamily 查家庭，未找到返回 (nil, nil)。
func (s *Store) FindFamily(ctx context.Context, familyID uint64) (*Family, error) {
	var f Family
	err := s.db.WithContext(ctx).First(&f, familyID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// CreateFamilyWithAdmin 事务创建家庭 + 管理员成员。
func (s *Store) CreateFamilyWithAdmin(ctx context.Context, f *Family, m *Member) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(f).Error; err != nil {
			return err
		}
		m.FamilyID = f.FamilyID
		return tx.Create(m).Error
	})
}

// ListFamiliesByUser 查用户所属的全部家庭及角色。
func (s *Store) ListFamiliesByUser(ctx context.Context, userID uint64) ([]FamilyRole, error) {
	var rows []FamilyRole
	err := s.db.WithContext(ctx).
		Table("members").
		Select("families.family_id, families.name, members.role").
		Joins("JOIN families ON families.family_id = members.family_id").
		Where("members.user_id = ? AND members.status = ?", userID, StatusActive).
		Scan(&rows).Error
	return rows, err
}

// FindMember 查 (user_id, family_id) 的 active 成员关系，未找到返回 (nil, nil)。
func (s *Store) FindMember(ctx context.Context, userID, familyID uint64) (*Member, error) {
	var m Member
	err := s.db.WithContext(ctx).
		Where("user_id = ? AND family_id = ? AND status = ?", userID, familyID, StatusActive).
		First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// CreateMember 创建成员。
func (s *Store) CreateMember(ctx context.Context, m *Member) error {
	return s.db.WithContext(ctx).Create(m).Error
}

// ListMembers 查家庭全部 active 成员（含虚拟成员）。
func (s *Store) ListMembers(ctx context.Context, familyID uint64) ([]Member, error) {
	var ms []Member
	err := s.db.WithContext(ctx).
		Where("family_id = ? AND status = ?", familyID, StatusActive).
		Order("member_id ASC").
		Find(&ms).Error
	return ms, err
}

// UpdateMemberFields 更新成员可变字段（role / relation_label）。
func (s *Store) UpdateMemberFields(ctx context.Context, memberID, familyID uint64, fields map[string]any) error {
	res := s.db.WithContext(ctx).
		Model(&Member{}).
		Where("member_id = ? AND family_id = ? AND status = ?", memberID, familyID, StatusActive).
		Updates(fields)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrMemberNotFound
	}
	return nil
}

// FindMemberByID 按 member_id + family_id 查 active 成员，未找到返回 (nil, nil)。
func (s *Store) FindMemberByID(ctx context.Context, memberID, familyID uint64) (*Member, error) {
	var m Member
	err := s.db.WithContext(ctx).
		Where("member_id = ? AND family_id = ? AND status = ?", memberID, familyID, StatusActive).
		First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// CountAdmins 统计家庭中排除指定成员后的管理员数（末位管理员保护用）。
func (s *Store) CountAdmins(ctx context.Context, familyID, excludeMemberID uint64) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).
		Model(&Member{}).
		Where("family_id = ? AND role = ? AND status = ? AND member_id <> ?",
			familyID, RoleAdmin, StatusActive, excludeMemberID).
		Count(&n).Error
	return n, err
}

// RemoveMember 软删除成员（status=removed）。孩子档案不受影响（见 ADR-001）。
func (s *Store) RemoveMember(ctx context.Context, memberID, familyID uint64) error {
	res := s.db.WithContext(ctx).
		Model(&Member{}).
		Where("member_id = ? AND family_id = ? AND status = ?", memberID, familyID, StatusActive).
		Update("status", StatusRemoved)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrMemberNotFound
	}
	return nil
}
