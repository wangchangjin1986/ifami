package identity

import (
	"context"
	"encoding/base64"
	"errors"
	"regexp"
	"testing"

	fieldcrypto "ifami/internal/pkg/crypto"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// 用 sqlmock 驱动真实 Store + Service，验证 UpdateMember 的权限与字段规则。
func newTestService(t *testing.T) (*Service, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	gdb, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      db,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	// 非孩子姓名更新路径不触碰 wx/sessions/field，传 nil 即可。
	return NewService(NewStore(gdb), nil, "", nil, nil, "test-secret"), mock
}

func strptr(s string) *string { return &s }

func boolptr(b bool) *bool { return &b }

func updateMemberForTest(svc *Service, ctx context.Context, userID, familyID, memberID uint64, role, relationLabel *string) error {
	return svc.UpdateMember(ctx, userID, familyID, memberID, role, nil, relationLabel, nil)
}

func newTestFieldCipher(t *testing.T) *fieldcrypto.FieldCipher {
	t.Helper()
	c, err := fieldcrypto.NewFieldCipher(base64.StdEncoding.EncodeToString([]byte("12345678901234567890123456789012")))
	if err != nil {
		t.Fatalf("NewFieldCipher: %v", err)
	}
	return c
}

// expectFindMember 预置「操作者成员关系」的查询（First 会自动追加 LIMIT 1 参数）。
func expectFindMember(mock sqlmock.Sqlmock, userID, familyID uint64, role string) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `members` WHERE user_id = ? AND family_id = ? AND status = ?")).
		WithArgs(userID, familyID, StatusActive, 1).
		WillReturnRows(sqlmock.NewRows([]string{"member_id", "family_id", "user_id", "type", "role", "is_admin", "status"}).
			AddRow(1, familyID, userID, TypeReal, normalizeMemberRole(role), isAdminValue(role, false), StatusActive))
}

// expectFindTarget 预置「目标成员」查询。
func expectFindTarget(mock sqlmock.Sqlmock, memberID, familyID uint64, mtype, role string) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `members` WHERE member_id = ? AND family_id = ? AND status = ?")).
		WithArgs(memberID, familyID, StatusActive, 1).
		WillReturnRows(sqlmock.NewRows([]string{"member_id", "family_id", "type", "role", "is_admin", "status"}).
			AddRow(memberID, familyID, mtype, normalizeMemberRole(role), isAdminValue(role, false), StatusActive))
}

func TestUpdateMember_PermissionDenied(t *testing.T) {
	svc, mock := newTestService(t)
	expectFindMember(mock, 9, 100, RoleParent) // 操作者只是 parent

	err := svc.UpdateMember(context.Background(), 9, 100, 2, nil, boolptr(true), nil, nil)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateMember_NoFields(t *testing.T) {
	svc, mock := newTestService(t)
	expectFindMember(mock, 1, 100, RoleAdmin)

	err := updateMemberForTest(svc, context.Background(), 1, 100, 2, nil, nil)
	if !errors.Is(err, ErrBadParam) {
		t.Fatalf("want ErrBadParam, got %v", err)
	}
}

func TestUpdateMember_RelationLabelOnly(t *testing.T) {
	svc, mock := newTestService(t)
	expectFindMember(mock, 1, 100, RoleAdmin)
	expectFindTarget(mock, 2, 100, TypeReal, RoleGrandparent)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `members` SET `relation_label`=? WHERE member_id = ? AND family_id = ? AND status = ?")).
		WithArgs("奶奶", 2, 100, StatusActive).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := updateMemberForTest(svc, context.Background(), 1, 100, 2, nil, strptr("奶奶")); err != nil {
		t.Fatalf("update relation_label: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateMember_VirtualNameOnly(t *testing.T) {
	svc, mock := newTestService(t)
	svc.field = newTestFieldCipher(t)
	expectFindMember(mock, 1, 100, RoleAdmin)
	expectFindTarget(mock, 3, 100, TypeVirtual, RoleChild)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `members` SET `name_enc`=? WHERE member_id = ? AND family_id = ? AND status = ?")).
		WithArgs(sqlmock.AnyArg(), 3, 100, StatusActive).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := svc.UpdateMember(context.Background(), 1, 100, 3, nil, nil, nil, strptr("乐乐")); err != nil {
		t.Fatalf("update virtual name: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateMember_RealNameBlocked(t *testing.T) {
	svc, mock := newTestService(t)
	svc.field = newTestFieldCipher(t)
	expectFindMember(mock, 1, 100, RoleAdmin)
	expectFindTarget(mock, 2, 100, TypeReal, RoleParent)

	err := svc.UpdateMember(context.Background(), 1, 100, 2, nil, nil, nil, strptr("爸爸"))
	if !errors.Is(err, ErrBadParam) {
		t.Fatalf("real member name change: want ErrBadParam, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateMember_GrantAdminFlag(t *testing.T) {
	svc, mock := newTestService(t)
	expectFindMember(mock, 1, 100, RoleAdmin)
	expectFindTarget(mock, 2, 100, TypeReal, RoleParent)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `members` SET `is_admin`=? WHERE member_id = ? AND family_id = ? AND status = ?")).
		WithArgs(true, 2, 100, StatusActive).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := svc.UpdateMember(context.Background(), 1, 100, 2, nil, boolptr(true), nil, nil); err != nil {
		t.Fatalf("grant admin: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateMember_RevokeLastAdminBlocked(t *testing.T) {
	svc, mock := newTestService(t)
	expectFindMember(mock, 1, 100, RoleAdmin)
	expectFindTarget(mock, 1, 100, TypeReal, RoleAdmin) // 目标也是 admin（自己降级自己）
	// 除目标外已无其他管理员 → CountAdmins 返回 0
	mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*) FROM `members`")).
		WithArgs(100, true, RoleAdmin, StatusActive, 1).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	err := svc.UpdateMember(context.Background(), 1, 100, 1, nil, boolptr(false), nil, nil)
	if !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("want ErrLastAdmin, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateMember_RevokeAdminWithBackup(t *testing.T) {
	svc, mock := newTestService(t)
	expectFindMember(mock, 1, 100, RoleAdmin)
	expectFindTarget(mock, 2, 100, TypeReal, RoleAdmin)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*) FROM `members`")).
		WithArgs(100, true, RoleAdmin, StatusActive, 2).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1)) // 还有别的管理员
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `members` SET `is_admin`=?")).
		WithArgs(false, 2, 100, StatusActive).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := svc.UpdateMember(context.Background(), 1, 100, 2, nil, boolptr(false), nil, nil); err != nil {
		t.Fatalf("demote with backup admin: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateMember_VirtualRoleLocked(t *testing.T) {
	svc, mock := newTestService(t)
	expectFindMember(mock, 1, 100, RoleAdmin)
	expectFindTarget(mock, 3, 100, TypeVirtual, RoleChild)

	err := svc.UpdateMember(context.Background(), 1, 100, 3, nil, boolptr(true), nil, nil)
	if !errors.Is(err, ErrBadParam) {
		t.Fatalf("virtual member admin flag: want ErrBadParam, got %v", err)
	}
}

func TestUpdateMember_RealMemberCannotBeChild(t *testing.T) {
	svc, mock := newTestService(t)
	expectFindMember(mock, 1, 100, RoleAdmin)
	expectFindTarget(mock, 2, 100, TypeReal, RoleParent)

	err := updateMemberForTest(svc, context.Background(), 1, 100, 2, strptr(RoleChild), nil)
	if !errors.Is(err, ErrBadParam) {
		t.Fatalf("real member to child: want ErrBadParam, got %v", err)
	}
}

func TestUpdateMember_RealMemberRoleOnlyKeepsAdmin(t *testing.T) {
	svc, mock := newTestService(t)
	expectFindMember(mock, 1, 100, RoleAdmin)
	expectFindTarget(mock, 2, 100, TypeReal, RoleAdmin)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `members` SET `role`=? WHERE member_id = ? AND family_id = ? AND status = ?")).
		WithArgs(RoleGrandparent, 2, 100, StatusActive).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := updateMemberForTest(svc, context.Background(), 1, 100, 2, strptr(RoleGrandparent), nil); err != nil {
		t.Fatalf("update role only: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateMember_InvalidRole(t *testing.T) {
	svc, mock := newTestService(t)
	expectFindMember(mock, 1, 100, RoleAdmin)
	expectFindTarget(mock, 2, 100, TypeReal, RoleParent)

	err := updateMemberForTest(svc, context.Background(), 1, 100, 2, strptr("superadmin"), nil)
	if !errors.Is(err, ErrBadParam) {
		t.Fatalf("invalid role: want ErrBadParam, got %v", err)
	}
}

func TestUpdateMember_TargetNotFound(t *testing.T) {
	svc, mock := newTestService(t)
	expectFindMember(mock, 1, 100, RoleAdmin)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `members` WHERE member_id = ? AND family_id = ? AND status = ?")).
		WithArgs(99, 100, StatusActive, 1).
		WillReturnRows(sqlmock.NewRows([]string{"member_id", "family_id", "type", "role", "is_admin", "status"}))

	err := svc.UpdateMember(context.Background(), 1, 100, 99, nil, boolptr(true), nil, nil)
	if !errors.Is(err, ErrMemberNotFound) {
		t.Fatalf("want ErrMemberNotFound, got %v", err)
	}
}
