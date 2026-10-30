package identity

import (
	"context"
	"errors"
	"time"

	"ifami/internal/pkg/auth"
	"ifami/internal/pkg/crypto"
	"ifami/internal/pkg/invite"
	"ifami/internal/pkg/logx"
	"ifami/internal/pkg/wechat"
)

// Service 是 identity 模块的业务层，也是其他模块调用本模块的唯一入口
// （跨模块只走 service 接口，见 wiki/conventions/go.md）。
type Service struct {
	store        *Store
	wx           *wechat.MiniClient
	appid        string
	sessions     auth.SessionStore
	field        *crypto.FieldCipher
	inviteSecret string
}

func NewService(store *Store, wx *wechat.MiniClient, appid string, sessions auth.SessionStore, field *crypto.FieldCipher, inviteSecret string) *Service {
	return &Service{
		store: store, wx: wx, appid: appid,
		sessions: sessions, field: field, inviteSecret: inviteSecret,
	}
}

// ---- 对外 DTO ----

type UserView struct {
	UserID uint64 `json:"user_id"`
	Nick   string `json:"nick"`
	Avatar string `json:"avatar"`
}

type Membership struct {
	FamilyID uint64 `json:"family_id"`
	Role     string `json:"role"`
	IsAdmin  bool   `json:"is_admin"`
}

type LoginResult struct {
	Token       string       `json:"token"`
	User        *UserView    `json:"user"`
	Memberships []Membership `json:"memberships"`
}

type JoinResult struct {
	Token    string `json:"token"`
	FamilyID uint64 `json:"family_id"`
	Role     string `json:"role"`
}

type MemberView struct {
	MemberID      uint64  `json:"member_id"`
	UserID        *uint64 `json:"user_id"`
	Type          string  `json:"type"`
	Role          string  `json:"role"`
	IsAdmin       bool    `json:"is_admin"`
	RelationLabel string  `json:"relation_label"`
	Name          string  `json:"name"`
	Status        string  `json:"status"`
}

// ---- 业务方法 ----

// Login 小程序登录：code → openid → 查/建 user → 发会话。
func (s *Service) Login(ctx context.Context, code string) (*LoginResult, error) {
	sess, err := s.wx.Code2Session(ctx, code)
	if err != nil {
		// 记录微信原始 errcode/errmsg，否则「登录凭证无效」无法定位是 appid/secret 错误还是 code 失效。
		logx.Errorf("登录 code2session 失败: %v", err)
		return nil, ErrBadCode
	}
	userID, err := s.getOrCreateUser(ctx, sess)
	if err != nil {
		return nil, err
	}
	token, err := s.newSession(ctx, userID)
	if err != nil {
		return nil, err
	}
	u, err := s.store.FindUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	memberships, err := s.memberships(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &LoginResult{
		Token:       token,
		User:        &UserView{UserID: userID, Nick: u.Nick, Avatar: u.Avatar},
		Memberships: memberships,
	}, nil
}

// Me 当前用户信息（含所属家庭与角色）。
func (s *Service) Me(ctx context.Context, userID uint64) (*LoginResult, error) {
	u, err := s.store.FindUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, ErrNotMember
	}
	memberships, err := s.memberships(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &LoginResult{
		User:        &UserView{UserID: userID, Nick: u.Nick, Avatar: u.Avatar},
		Memberships: memberships,
	}, nil
}

// CreateFamily 创建家庭，创建者成为 admin。
func (s *Service) CreateFamily(ctx context.Context, userID uint64, name string) (uint64, uint64, error) {
	if name == "" {
		return 0, 0, ErrBadParam
	}
	f := &Family{Name: name, CreatorID: userID}
	uid := userID
	m := &Member{UserID: &uid, Type: TypeReal, Role: RoleParent, IsAdmin: true, Status: StatusActive}
	if err := s.store.CreateFamilyWithAdmin(ctx, f, m); err != nil {
		return 0, 0, err
	}
	return f.FamilyID, m.MemberID, nil
}

// ListFamilies 当前用户所属家庭列表。
func (s *Service) ListFamilies(ctx context.Context, userID uint64) ([]FamilyRole, error) {
	rows, err := s.store.ListFamiliesByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].IsAdmin = isAdminValue(rows[i].Role, rows[i].IsAdmin)
		rows[i].Role = normalizeMemberRole(rows[i].Role)
	}
	return rows, nil
}

// CreateInvitation 生成邀请 token（admin/parent 可邀请）。
func (s *Service) CreateInvitation(ctx context.Context, userID, familyID uint64) (string, error) {
	if _, err := s.requireRole(ctx, userID, familyID, RoleAdmin, RoleParent); err != nil {
		return "", err
	}
	return invite.Sign(s.inviteSecret, familyID, time.Now()), nil
}

// JoinFamily 接受邀请：验证 token + code → 建/查 user → 加 grandparent 成员 → 自动登录。
func (s *Service) JoinFamily(ctx context.Context, code, token, relationLabel string) (*JoinResult, error) {
	familyID, err := invite.Verify(s.inviteSecret, token)
	if err != nil {
		if errors.Is(err, invite.ErrExpired) {
			return nil, ErrInviteExpired
		}
		return nil, ErrBadInvite
	}
	f, err := s.store.FindFamily(ctx, familyID)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, ErrFamilyNotFound
	}
	sess, err := s.wx.Code2Session(ctx, code)
	if err != nil {
		logx.Errorf("加入家庭 code2session 失败: %v", err)
		return nil, ErrBadCode
	}
	userID, err := s.getOrCreateUser(ctx, sess)
	if err != nil {
		return nil, err
	}
	if existing, _ := s.store.FindMember(ctx, userID, familyID); existing != nil {
		return nil, ErrAlreadyMember
	}
	uid := userID
	m := &Member{
		FamilyID: familyID, UserID: &uid, Type: TypeReal,
		Role: RoleGrandparent, RelationLabel: relationLabel, Status: StatusActive,
	}
	if err := s.store.CreateMember(ctx, m); err != nil {
		return nil, err
	}
	sessionToken, err := s.newSession(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &JoinResult{Token: sessionToken, FamilyID: familyID, Role: RoleGrandparent}, nil
}

// ListMembers 家庭成员列表（含虚拟孩子，孩子姓名解密返回）。
func (s *Service) ListMembers(ctx context.Context, userID, familyID uint64) ([]MemberView, error) {
	if _, err := s.requireRole(ctx, userID, familyID); err != nil {
		return nil, err
	}
	ms, err := s.store.ListMembers(ctx, familyID)
	if err != nil {
		return nil, err
	}
	views := make([]MemberView, 0, len(ms))
	for i := range ms {
		views = append(views, s.toView(&ms[i]))
	}
	return views, nil
}

// AddChild 添加虚拟成员（孩子），姓名字段级加密。
func (s *Service) AddChild(ctx context.Context, userID, familyID uint64, name, relationLabel string) (uint64, error) {
	if _, err := s.requireRole(ctx, userID, familyID, RoleAdmin, RoleParent); err != nil {
		return 0, err
	}
	if name == "" {
		return 0, ErrBadParam
	}
	var nameEnc []byte
	if s.field != nil {
		var err error
		nameEnc, err = s.field.Encrypt([]byte(name))
		if err != nil {
			return 0, err
		}
	}
	m := &Member{
		FamilyID: familyID, UserID: nil, Type: TypeVirtual,
		Role: RoleChild, NameEnc: nameEnc, RelationLabel: relationLabel, Status: StatusActive,
	}
	if err := s.store.CreateMember(ctx, m); err != nil {
		return 0, err
	}
	return m.MemberID, nil
}

// UpdateMember 改成员角色、管理员权限、身份/名称与孩子档案名（仅 admin；末位管理员不可降级）。
// role / isAdmin / relationLabel / name 为 nil 表示不修改该字段；至少传一个。
func (s *Service) UpdateMember(ctx context.Context, userID, familyID, memberID uint64, role *string, isAdmin *bool, relationLabel, name *string) error {
	if _, err := s.requireRole(ctx, userID, familyID, RoleAdmin); err != nil {
		return err
	}
	if role == nil && isAdmin == nil && relationLabel == nil && name == nil {
		return ErrBadParam
	}
	target, err := s.store.FindMemberByID(ctx, memberID, familyID)
	if err != nil {
		return err
	}
	if target == nil {
		return ErrMemberNotFound
	}
	fields := map[string]any{}
	if role != nil {
		if !validRole(*role) {
			return ErrBadParam
		}
		if *role == RoleAdmin {
			return ErrBadParam
		}
		// 角色与成员类型绑定：虚拟成员（孩子）角色固定为 child，真实成员不可改为 child。
		if target.Type == TypeVirtual && *role != RoleChild {
			return ErrBadParam
		}
		if target.Type == TypeReal && *role == RoleChild {
			return ErrBadParam
		}
		fields["role"] = *role
	}
	if isAdmin != nil {
		if target.Type == TypeVirtual {
			return ErrBadParam
		}
		if isMemberAdmin(target) && !*isAdmin {
			if err := s.ensureNotLastAdmin(ctx, familyID, target.MemberID); err != nil {
				return err
			}
		}
		fields["is_admin"] = *isAdmin
	}
	if relationLabel != nil {
		fields["relation_label"] = *relationLabel
	}
	if name != nil {
		if target.Type != TypeVirtual || *name == "" {
			return ErrBadParam
		}
		if s.field == nil {
			return ErrBadParam
		}
		nameEnc, err := s.field.Encrypt([]byte(*name))
		if err != nil {
			return err
		}
		fields["name_enc"] = nameEnc
	}
	return s.store.UpdateMemberFields(ctx, memberID, familyID, fields)
}

// RemoveMember 移除成员：admin 可移除他人，普通成员仅可退出自己。
func (s *Service) RemoveMember(ctx context.Context, userID, familyID, memberID uint64) error {
	actor, err := s.store.FindMember(ctx, userID, familyID)
	if err != nil {
		return err
	}
	if actor == nil {
		return ErrNotMember
	}
	target, err := s.store.FindMemberByID(ctx, memberID, familyID)
	if err != nil {
		return err
	}
	if target == nil {
		return ErrMemberNotFound
	}
	isSelf := target.UserID != nil && *target.UserID == userID
	if !isMemberAdmin(actor) && !isSelf {
		return ErrForbidden
	}
	if isMemberAdmin(target) {
		if err := s.ensureNotLastAdmin(ctx, familyID, target.MemberID); err != nil {
			return err
		}
	}
	return s.store.RemoveMember(ctx, memberID, familyID)
}

// ---- 供其他模块调用的鉴权能力（跨模块只走 service 接口，见 wiki/architecture/overview.md）----

// RequireActive 校验用户是家庭的 active 成员，返回 member_id 与 role（schedule/task 依赖）。
func (s *Service) RequireActive(ctx context.Context, userID, familyID uint64) (uint64, string, error) {
	m, err := s.requireRole(ctx, userID, familyID)
	if err != nil {
		return 0, "", err
	}
	return m.MemberID, effectivePermissionRole(m), nil
}

// RequireMember 校验 member_id 属于家庭且 active，返回成员 type 与 role（schedule 校验孩子/责任人）。
func (s *Service) RequireMember(ctx context.Context, familyID, memberID uint64) (string, string, error) {
	m, err := s.store.FindMemberByID(ctx, memberID, familyID)
	if err != nil {
		return "", "", err
	}
	if m == nil {
		return "", "", ErrMemberNotFound
	}
	return m.Type, effectivePermissionRole(m), nil
}

// OpenID 返回用户小程序通道的 openid（notify 发订阅消息用）。
func (s *Service) OpenID(ctx context.Context, userID uint64) (string, error) {
	id, err := s.store.FindIdentityByUser(ctx, userID, ChannelMiniapp)
	if err != nil {
		return "", err
	}
	if id == nil {
		return "", ErrNotMember
	}
	return id.OpenID, nil
}

// MemberUserID 返回 real 成员的 user_id（触达需 openid，openid 需 user_id；虚拟成员无账号）。
func (s *Service) MemberUserID(ctx context.Context, familyID, memberID uint64) (uint64, error) {
	m, err := s.store.FindMemberByID(ctx, memberID, familyID)
	if err != nil {
		return 0, err
	}
	if m == nil {
		return 0, ErrMemberNotFound
	}
	if m.UserID == nil {
		return 0, ErrNotMember
	}
	return *m.UserID, nil
}

// ---- 内部辅助 ----

func (s *Service) getOrCreateUser(ctx context.Context, sess *wechat.Session) (uint64, error) {
	id, err := s.store.FindIdentityByOpenID(ctx, ChannelMiniapp, s.appid, sess.OpenID)
	if err != nil {
		return 0, err
	}
	if id != nil {
		return id.UserID, nil
	}
	return s.store.CreateUserWithIdentity(ctx, &User{}, &Identity{
		Channel: ChannelMiniapp, AppID: s.appid, OpenID: sess.OpenID, UnionID: sess.UnionID,
	})
}

func (s *Service) newSession(ctx context.Context, userID uint64) (string, error) {
	token, err := auth.NewToken()
	if err != nil {
		return "", err
	}
	if err := s.sessions.Create(ctx, token, userID); err != nil {
		return "", err
	}
	return token, nil
}

func (s *Service) memberships(ctx context.Context, userID uint64) ([]Membership, error) {
	frs, err := s.store.ListFamiliesByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	ms := make([]Membership, 0, len(frs))
	for _, fr := range frs {
		ms = append(ms, Membership{FamilyID: fr.FamilyID, Role: normalizeMemberRole(fr.Role), IsAdmin: isAdminValue(fr.Role, fr.IsAdmin)})
	}
	return ms, nil
}

// requireRole 校验用户在该家庭的成员关系与角色；roles 为空表示任意 active 成员。
func (s *Service) requireRole(ctx context.Context, userID, familyID uint64, roles ...string) (*Member, error) {
	m, err := s.store.FindMember(ctx, userID, familyID)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, ErrNotMember
	}
	if len(roles) == 0 {
		return m, nil
	}
	for _, r := range roles {
		if r == RoleAdmin && isMemberAdmin(m) {
			return m, nil
		}
		if normalizeMemberRole(m.Role) == r {
			return m, nil
		}
	}
	return nil, ErrForbidden
}

func (s *Service) ensureNotLastAdmin(ctx context.Context, familyID, memberID uint64) error {
	n, err := s.store.CountAdmins(ctx, familyID, memberID)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrLastAdmin
	}
	return nil
}

func (s *Service) toView(m *Member) MemberView {
	v := MemberView{
		MemberID: m.MemberID, UserID: m.UserID, Type: m.Type,
		Role: normalizeMemberRole(m.Role), IsAdmin: isAdminValue(m.Role, m.IsAdmin), RelationLabel: m.RelationLabel, Status: m.Status,
	}
	if m.NameEnc != nil && s.field != nil {
		if plain, err := s.field.Decrypt(m.NameEnc); err == nil {
			v.Name = string(plain)
		}
	}
	return v
}

func validRole(r string) bool {
	switch r {
	case RoleParent, RoleGrandparent, RoleChild:
		return true
	}
	return false
}

func isMemberAdmin(m *Member) bool { return isAdminValue(m.Role, m.IsAdmin) }

func isAdminValue(role string, isAdmin bool) bool { return isAdmin || role == RoleAdmin }

func normalizeMemberRole(role string) string {
	if role == RoleAdmin {
		return RoleParent
	}
	return role
}

func effectivePermissionRole(m *Member) string {
	if isMemberAdmin(m) {
		return RoleAdmin
	}
	return normalizeMemberRole(m.Role)
}
