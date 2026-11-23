package identity

import (
	"context"
	"errors"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
		if m.UserID == nil {
			return ErrBadParam
		}
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, *m.UserID).Error; err != nil {
			return err
		}

		// The first consistent read occurs AFTER the users lock. Do not lock this
		// user_id scan: members has no user_id index, so gap locks would block unrelated users.
		var active []Member
		if err := tx.Where("user_id = ? AND status = ?", *m.UserID, StatusActive).Find(&active).Error; err != nil {
			return err
		}
		if len(active) > 0 {
			return ErrOtherFamily
		}
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
		Select("families.family_id, families.name, members.role, members.is_admin").
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

// UpdateMemberFields 更新成员可变字段（role / is_admin / relation_label / name_enc）。
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
		Where("family_id = ? AND (is_admin = ? OR role = ?) AND status = ? AND member_id <> ?",
			familyID, true, RoleAdmin, StatusActive, excludeMemberID).
		Count(&n).Error
	return n, err
}

// ListAdminUserIDs 返回家庭内 active 管理员的 user_id（执行端反馈强触达用）。
func (s *Store) ListAdminUserIDs(ctx context.Context, familyID uint64) ([]uint64, error) {
	var rows []uint64
	err := s.db.WithContext(ctx).
		Model(&Member{}).
		Where("family_id = ? AND status = ? AND type = ? AND user_id IS NOT NULL AND (is_admin = ? OR role = ?)",
			familyID, StatusActive, TypeReal, true, RoleAdmin).
		Pluck("user_id", &rows).Error
	return rows, err
}

// ListBackupUserIDs 返回家庭内可作为备份联系人的真实成员 user_id：管理员优先，父母兜底。
func (s *Store) ListBackupUserIDs(ctx context.Context, familyID uint64) ([]uint64, error) {
	var rows []struct {
		UserID  uint64 `gorm:"column:user_id"`
		IsAdmin bool   `gorm:"column:is_admin"`
		Role    string `gorm:"column:role"`
	}
	err := s.db.WithContext(ctx).
		Model(&Member{}).
		Select("user_id, is_admin, role").
		Where("family_id = ? AND status = ? AND type = ? AND user_id IS NOT NULL AND (is_admin = ? OR role IN ?)",
			familyID, StatusActive, TypeReal, true, []string{RoleAdmin, RoleParent}).
		Order("is_admin DESC, member_id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	ids := make([]uint64, 0, len(rows))
	seen := map[uint64]bool{}
	for _, row := range rows {
		if row.UserID == 0 || seen[row.UserID] {
			continue
		}
		seen[row.UserID] = true
		ids = append(ids, row.UserID)
	}
	return ids, nil
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

// JoinSingleFamily serializes every real membership creation on the same users row.
func (s *Store) JoinSingleFamily(ctx context.Context, uid, fid uint64, role, label string) (*Member, bool, error) {
	var result *Member
	already := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, uid).Error; err != nil {
			return err
		}

		// The first consistent read occurs AFTER the users lock. Do not lock this
		// user_id scan: members has no user_id index, so gap locks would block unrelated users.
		var active []Member
		if err := tx.Where("user_id = ? AND status = ?", uid, StatusActive).Find(&active).Error; err != nil {
			return err
		}
		for i := range active {
			if active[i].FamilyID != fid {
				return ErrOtherFamily
			}
		}
		if len(active) > 0 {
			result = &active[0]
			already = true
			return nil
		}
		var previous Member
		err := tx.Where("user_id = ? AND family_id = ?", uid, fid).First(&previous).Error
		if err == nil {
			return ErrRemovedMember
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var family Family
		if err := tx.First(&family, fid).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrFamilyNotFound
			}
			return err
		}
		result = &Member{FamilyID: fid, UserID: &uid, Type: TypeReal, Role: role, RelationLabel: label, Status: StatusActive}
		if err := tx.Create(result).Error; err != nil {
			var duplicate *mysqlDriver.MySQLError
			if !errors.As(err, &duplicate) || duplicate.Number != 1062 {
				return err
			}
			var existing Member
			if lookup := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ? AND family_id = ?", uid, fid).First(&existing).Error; lookup != nil {
				return lookup
			}
			if existing.Status != StatusActive {
				return ErrRemovedMember
			}
			result = &existing
			already = true
		}
		return nil
	})
	return result, already, err
}
