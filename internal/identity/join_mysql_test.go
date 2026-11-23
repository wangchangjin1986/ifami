package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func mysqlJoinTest(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN required")
	}
	cfg, e := mysqlDriver.ParseDSN(dsn)
	if e != nil {
		t.Fatal(e)
	}
	cfg.MultiStatements = true
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	admin, e := sql.Open("mysql", cfg.FormatDSN())
	if e != nil {
		t.Fatal(e)
	}
	name := fmt.Sprintf("ifami_test_identity_%d", time.Now().UnixNano())
	if _, e = admin.Exec("CREATE DATABASE " + name); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _, _ = admin.Exec("DROP DATABASE " + name); _ = admin.Close() })
	cfg.DBName = name
	db, e := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if e != nil {
		t.Fatal(e)
	}
	sqldb, _ := db.DB()
	t.Cleanup(func() { _ = sqldb.Close() })
	files, e := filepath.Glob("../../migrations/*.up.sql")
	if e != nil {
		t.Fatal(e)
	}
	for _, file := range files {
		body, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = sqldb.Exec(string(body)); e != nil {
			t.Fatalf("migration %s: %v", file, e)
		}
	}
	return NewStore(db)
}
func addJoinUser(t *testing.T, s *Store, uid uint64) {
	t.Helper()
	if e := s.db.Create(&User{UserID: uid}).Error; e != nil {
		t.Fatal(e)
	}
}
func addJoinFamily(t *testing.T, s *Store, fid uint64) {
	t.Helper()
	if e := s.db.Create(&Family{FamilyID: fid, Name: fmt.Sprintf("真实家庭%d", fid), CreatorID: 1}).Error; e != nil {
		t.Fatal(e)
	}
}
func TestMySQLJoinSingleFamilyAndReplay(t *testing.T) {
	s := mysqlJoinTest(t)
	ctx := context.Background()
	addJoinUser(t, s, 1)
	addJoinFamily(t, s, 100)
	addJoinFamily(t, s, 200)
	first, already, e := s.JoinSingleFamily(ctx, 1, 100, RoleParent, "妈妈")
	if e != nil || already {
		t.Fatalf("first %v %v", already, e)
	}
	if e = s.db.Model(first).Updates(map[string]any{"is_admin": true, "relation_label": "原称谓"}).Error; e != nil {
		t.Fatal(e)
	}
	again, already, e := s.JoinSingleFamily(ctx, 1, 100, RoleGrandparent, "阿姨")
	if e != nil || !already || again.MemberID != first.MemberID || !again.IsAdmin || again.RelationLabel != "原称谓" || again.Role != RoleParent {
		t.Fatalf("replay %+v %v %v", again, already, e)
	}
	if _, _, e = s.JoinSingleFamily(ctx, 1, 200, RoleParent, "爸爸"); e != ErrOtherFamily {
		t.Fatal("other family", e)
	}
	f := &Family{Name: "不得创建", CreatorID: 1}
	m := &Member{UserID: first.UserID, Type: TypeReal, Role: RoleParent, IsAdmin: true, Status: StatusActive}
	if e = s.CreateFamilyWithAdmin(ctx, f, m); e != ErrOtherFamily {
		t.Fatal("create while joined", e)
	}
	if e = s.RemoveMember(ctx, first.MemberID, 100); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.JoinSingleFamily(ctx, 1, 100, RoleParent, "妈妈"); e != ErrRemovedMember {
		t.Fatal("removed", e)
	}
	// Real MySQL1062 is recognized by its driver type even without TranslateError.
	e = s.db.Create(&Member{FamilyID: 100, UserID: first.UserID, Type: TypeReal, Role: RoleParent, Status: StatusActive}).Error
	var duplicate *mysqlDriver.MySQLError
	if !errors.As(e, &duplicate) || duplicate.Number != 1062 {
		t.Fatalf("unique error %T %v", e, e)
	}
}
func TestMySQLConcurrentJoinAndCreateRace(t *testing.T) {
	s := mysqlJoinTest(t)
	ctx := context.Background()
	addJoinFamily(t, s, 100)
	addJoinFamily(t, s, 200)
	for round := 0; round < 16; round++ {
		uid := uint64(round + 1)
		addJoinUser(t, s, uid)
		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				if round%2 == 0 {
					_, _, errs[i] = s.JoinSingleFamily(ctx, uid, uint64(100+i*100), RoleGrandparent, "奶奶")
				} else if i == 0 {
					f := &Family{Name: "竞争创建", CreatorID: uid}
					m := &Member{UserID: &uid, Type: TypeReal, Role: RoleParent, IsAdmin: true, Status: StatusActive}
					errs[i] = s.CreateFamilyWithAdmin(ctx, f, m)
				} else {
					_, _, errs[i] = s.JoinSingleFamily(ctx, uid, 100, RoleParent, "爸爸")
				}
			}(i)
		}
		close(start)
		wg.Wait()
		wins := 0
		for _, e := range errs {
			if e == nil {
				wins++
			} else if e != ErrOtherFamily {
				t.Fatalf("round %d: %v", round, e)
			}
		}
		var count int64
		if e := s.db.Model(&Member{}).Where("user_id = ? AND status = ?", uid, StatusActive).Count(&count).Error; e != nil {
			t.Fatal(e)
		}
		if wins != 1 || count != 1 {
			t.Fatalf("round %d wins=%d members=%d", round, wins, count)
		}
	}
	// Same-target concurrency returns one real member and never overwrites its privileges.
	uid := uint64(99)
	addJoinUser(t, s, uid)
	var wg sync.WaitGroup
	members := make([]*Member, 12)
	errs := make([]error, 12)
	for i := range members {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			members[i], _, errs[i] = s.JoinSingleFamily(ctx, uid, 100, RoleParent, "妈妈")
		}(i)
	}
	wg.Wait()
	for i := range members {
		if errs[i] != nil || members[i].MemberID != members[0].MemberID {
			t.Fatalf("same target %d %v", i, errs[i])
		}
	}
}
func TestMySQLServiceJoinDirectAndPermissions(t *testing.T) {
	store := mysqlJoinTest(t)
	registry := redisJoinTest(t)
	ctx := context.Background()
	addJoinUser(t, store, 1)
	addJoinUser(t, store, 2)
	addJoinUser(t, store, 3)
	svc := NewService(store, nil, "", nil, newTestFieldCipher(t), "test-secret")
	svc.SetJoinCodeRegistry(registry)
	fid, adminID, e := svc.CreateFamily(ctx, 1, "真实家庭名称")
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = svc.CreateFamily(ctx, 1, "第二家庭"); e != ErrOtherFamily {
		t.Fatal("create twice", e)
	}
	current, e := svc.GetJoinCode(ctx, 1, fid)
	if e != nil || current.HasCode {
		t.Fatal("empty read", e)
	}
	code, e := svc.GenerateJoinCode(ctx, 1, fid, "generate1")
	if e != nil || !code.HasCode {
		t.Fatal(e)
	}
	preview, e := svc.PreviewJoin(ctx, 2, JoinPreviewInput{JoinCode: code.JoinCode, ClientIP: "service-ip"})
	if e != nil || preview.FamilyName != "真实家庭名称" {
		t.Fatalf("preview %+v %v", preview, e)
	}
	var count int64
	if e = store.db.Model(&Member{}).Where("user_id = ?", 2).Count(&count).Error; e != nil {
		t.Fatal(e)
	}
	if count != 0 {
		t.Fatal("preview wrote member")
	}
	input := JoinConfirmInput{code.JoinCode, code.GenerationID, preview.Credential, "parent", "妈妈", "service-ip"}
	input.GenerationID = "ffffffffffffffffffffffffffffffff"
	if _, e = svc.ConfirmJoin(ctx, 2, input); e != ErrJoinInvalid {
		t.Fatal("bad generation", e)
	}
	input.GenerationID = code.GenerationID
	result, e := svc.ConfirmJoin(ctx, 2, input)
	if e != nil || result.IsAdmin || result.AlreadyMember || result.Role != RoleParent {
		t.Fatalf("joined %+v %v", result, e)
	}
	if _, e = svc.AddChild(ctx, 2, fid, "孩子", "儿子"); e != ErrForbidden {
		t.Fatal("ordinary parent add child", e)
	}
	if _, e = svc.GetJoinCode(ctx, 2, fid); e != ErrForbidden {
		t.Fatal("ordinary parent get code", e)
	}
	if _, e = svc.GenerateJoinCode(ctx, 2, fid, "forged"); e != ErrForbidden {
		t.Fatal("ordinary parent rotate", e)
	}
	if _, e = svc.RevokeJoinCode(ctx, 2, fid, code.GenerationID); e != ErrForbidden {
		t.Fatal("ordinary parent revoke", e)
	}
	input.Identity = "caregiver"
	input.RelationLabel = "阿姨"
	replay, e := svc.ConfirmJoin(ctx, 2, input)
	if e != nil || !replay.AlreadyMember || replay.Role != RoleParent || replay.RelationLabel != "妈妈" {
		t.Fatalf("replay %+v %v", replay, e)
	}
	input.Identity = "admin"
	if _, e = svc.ConfirmJoin(ctx, 3, input); e != ErrBadParam {
		t.Fatal("forged identity", e)
	}
	input.Identity = "caregiver"
	child, e := svc.AddChild(ctx, 1, fid, "孩子姓名", "儿子")
	if e != nil || child == adminID {
		t.Fatal("admin child", e)
	}
	if _, e = svc.RevokeJoinCode(ctx, 1, fid, code.GenerationID); e != nil {
		t.Fatal(e)
	}
	if _, e = svc.ConfirmJoin(ctx, 3, input); e != ErrJoinInvalid {
		t.Fatal("revoked confirm", e)
	}
	// Existing sessions/members remain independently readable after registry outage.
	_ = registry.client.Close()
	if _, e = svc.Me(ctx, 2); e != nil {
		t.Fatal("me during redis outage", e)
	}
	if _, e = svc.PreviewJoin(ctx, 3, JoinPreviewInput{JoinCode: code.JoinCode}); e != ErrJoinUnavailable {
		t.Fatal("preview outage", e)
	}
}
func TestMySQLDifferentUsersJoinConcurrently(t *testing.T) {
	store := mysqlJoinTest(t)
	addJoinFamily(t, store, 100)
	for uid := uint64(1); uid <= 24; uid++ {
		addJoinUser(t, store, uid)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 24)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, _, errs[i] = store.JoinSingleFamily(context.Background(), uint64(i+1), 100, RoleParent, "妈妈")
		}(i)
	}
	close(start)
	wg.Wait()
	for i, e := range errs {
		if e != nil {
			t.Fatalf("user %d: %v", i+1, e)
		}
	}
	var count int64
	if e := store.db.Model(&Member{}).Count(&count).Error; e != nil || count != 24 {
		t.Fatalf("count %d %v", count, e)
	}
}
