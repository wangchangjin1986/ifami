package identity

import (
	"context"
	"database/sql"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/network"
	mysqlDriver "github.com/go-sql-driver/mysql"
	"net"
	"strings"
	"testing"
)

func TestJoinNilRegistryFailsClosed(t *testing.T) {
	s := NewService(nil, nil, "", nil, nil, "secret")
	if _, err := s.PreviewJoin(context.Background(), 1, JoinPreviewInput{JoinCode: "000001"}); err != ErrJoinUnavailable {
		t.Fatalf("nil registry: %v", err)
	}
}
func TestJoinIdentityPresets(t *testing.T) {
	for _, tc := range []struct {
		identity, label, role string
		ok                    bool
	}{
		{"parent", "妈妈", RoleParent, true}, {"grandparent", "奶奶", RoleGrandparent, true}, {"caregiver", "接送阿姨", RoleGrandparent, true},
		{"admin", "妈妈", "", false}, {"parent", "接送阿姨", "", false}, {"caregiver", "管理员", "", false},
	} {
		role, ok := joinRole(tc.identity, tc.label)
		if role != tc.role || ok != tc.ok {
			t.Errorf("%+v: %q %v", tc, role, ok)
		}
	}
}
func TestOldInvitationsStopped(t *testing.T) {
	s := NewService(nil, nil, "", nil, nil, "secret")
	if _, err := s.CreateInvitation(context.Background(), 1, 1, InvitationInput{}); err != ErrOldClient {
		t.Fatal(err)
	}
	if _, err := s.JoinFamily(context.Background(), "wx", "old", "妈妈"); err != ErrOldClient {
		t.Fatal(err)
	}
}

func TestJoinInputBoundsAndPrivilegeFields(t *testing.T) {
	for _, body := range []string{
		`{"join_code":"000001","generation_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","credential":"signed","identity":"parent","relation_label":"妈妈","is_admin":true}`,
		`{"join_code":"000001","role":"admin"}`,
		`{"join_code":"000001","user_id":1}`,
		`{"join_code":"000001","family_id":1}`,
		`{"join_code":"000001","client_ip":"fake"}`,
		`{"join_code":"000001"} {}`,
	} {
		c := app.NewContext(0)
		c.Request.SetBodyString(body)
		var in JoinConfirmInput
		if e := bindJoin(c, &in); e != ErrBadParam {
			t.Fatalf("forgery accepted: %s", body)
		}
	}
	c := app.NewContext(0)
	c.Request.SetBodyString(strings.Repeat("x", 2049))
	var in JoinConfirmInput
	if e := bindJoin(c, &in); e != ErrBadParam {
		t.Fatal("body bounds")
	}
}

func TestJoinTransactionLocksUserFirst(t *testing.T) {
	for _, create := range []bool{false, true} {
		svc, mock := newTestService(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT .* FROM `users`.*FOR UPDATE").WithArgs(uint64(9), 1).WillReturnError(sql.ErrNoRows)
		mock.ExpectRollback()
		if create {
			uid := uint64(9)
			e := svc.store.CreateFamilyWithAdmin(context.Background(), &Family{Name: "x"}, &Member{UserID: &uid})
			if e == nil {
				t.Fatal("missing user accepted")
			}
		} else {
			if _, _, e := svc.store.JoinSingleFamily(context.Background(), 9, 10, RoleParent, "妈妈"); e == nil {
				t.Fatal("missing user accepted")
			}
		}
		if e := mock.ExpectationsWereMet(); e != nil {
			t.Fatal(e)
		}
	}
}
func TestJoinTypedMySQLDuplicateRecheck(t *testing.T) {
	for _, status := range []string{StatusActive, StatusRemoved} {
		svc, mock := newTestService(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT .* FROM `users`.*FOR UPDATE").WithArgs(uint64(9), 1).WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(9))
		mock.ExpectQuery("SELECT .* FROM `members`.*status").WithArgs(uint64(9), StatusActive).WillReturnRows(sqlmock.NewRows([]string{"member_id"}))
		mock.ExpectQuery("SELECT .* FROM `members`.*family_id").WithArgs(uint64(9), uint64(10), 1).WillReturnRows(sqlmock.NewRows([]string{"member_id"}))
		mock.ExpectQuery("SELECT .* FROM `families`").WithArgs(uint64(10), 1).WillReturnRows(sqlmock.NewRows([]string{"family_id"}).AddRow(10))
		mock.ExpectExec("INSERT INTO `members`").WillReturnError(&mysqlDriver.MySQLError{Number: 1062, Message: "duplicate"})
		mock.ExpectQuery("SELECT .* FROM `members`.*family_id").WithArgs(uint64(9), uint64(10), 1).WillReturnRows(sqlmock.NewRows([]string{"member_id", "family_id", "user_id", "role", "is_admin", "relation_label", "status"}).AddRow(88, 10, 9, RoleParent, true, "原称谓", status))
		if status == StatusActive {
			mock.ExpectCommit()
		} else {
			mock.ExpectRollback()
		}
		member, already, e := svc.store.JoinSingleFamily(context.Background(), 9, 10, RoleGrandparent, "奶奶")
		if status == StatusActive {
			if e != nil || !already || !member.IsAdmin || member.RelationLabel != "原称谓" {
				t.Fatalf("%+v %v %v", member, already, e)
			}
		} else if e != ErrRemovedMember {
			t.Fatal("removed duplicate", e)
		}
		if e := mock.ExpectationsWereMet(); e != nil {
			t.Fatal(e)
		}
	}
}

type joinRemoteConn struct {
	network.Conn
	address net.Addr
}

func (c joinRemoteConn) RemoteAddr() net.Addr { return c.address }
func TestJoinTrustedClientIP(t *testing.T) {
	for _, tc := range []struct{ remote, forwarded, want string }{
		{"203.0.113.10", "198.51.100.9", "203.0.113.10"},
		{"127.0.0.1", "198.51.100.9", "198.51.100.9"},
		{"127.0.0.1", "192.0.2.1, 198.51.100.9", "198.51.100.9"},
		{"127.0.0.1", "invalid", "127.0.0.1"},
	} {
		c := app.NewContext(0)
		c.SetConn(joinRemoteConn{address: &net.TCPAddr{IP: net.ParseIP(tc.remote), Port: 1234}})
		c.Request.Header.Set("X-Forwarded-For", tc.forwarded)
		c.SetClientIPFunc(joinClientIP)
		if got := c.ClientIP(); got != tc.want {
			t.Errorf("trusted source %s: %s want %s", tc.remote, got, tc.want)
		}
	}
}
