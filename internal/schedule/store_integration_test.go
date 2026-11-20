package schedule

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// Each run owns a disposable database. Never point this at production.
func mysqlTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is required for MySQL integration tests")
	}
	cfg, err := drivermysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MultiStatements, cfg.ParseTime, cfg.Loc = true, true, time.UTC
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("ifami_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec("DROP DATABASE " + name); _ = admin.Close() })
	cfg.DBName = name
	gdb, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqldb, _ := gdb.DB()
	t.Cleanup(func() { _ = sqldb.Close() })
	files, err := filepath.Glob("../../migrations/*.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sqldb.Exec(string(body)); err != nil {
			t.Fatalf("migration %s: %v", file, err)
		}
	}
	return NewStore(gdb)
}

func futureScheduleInput() ScheduleInput {
	return ScheduleInput{Title: "阅读", Type: TypeCourse, ChildMemberID: 10, OwnerMemberID: 20,
		StartDate: time.Now().In(appLocation).AddDate(0, 0, 1).Format("2006-01-02"), StartTime: "16:30", EndTime: "17:00"}
}

func TestMySQLScheduleIdempotency(t *testing.T) {
	store := mysqlTestStore(t)
	svc := &Service{store: store, id: fakeScheduleMembership{role: roleAdmin, members: map[uint64]string{10: memberTypeVirtual, 20: memberTypeReal}}}
	ctx, in := context.Background(), futureScheduleInput()
	for range 2 {
		if _, err := svc.CreateSchedule(ctx, 1, 100, &in, true); err != nil {
			t.Fatal(err)
		}
	}
	batch, err := svc.CreateSchedulesBatch(ctx, 1, 100, []ScheduleInput{in, in}, true)
	if err != nil || len(batch.ScheduleIDs) != 2 {
		t.Fatalf("unkeyed batch=%+v err=%v", batch, err)
	}
	created, err := svc.CreateSchedule(ctx, 1, 100, &in, true, "request-key")
	if err != nil {
		t.Fatal(err)
	}
	in.Title = "编辑后的阅读"
	if _, err := svc.UpdateSchedule(ctx, 1, 100, created.ScheduleID, &in, true); err != nil {
		t.Fatal(err)
	}
	replayed, err := svc.CreateSchedule(ctx, 1, 100, &in, true, "request-key")
	if err != nil || replayed.ScheduleID != created.ScheduleID {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}
	var count int64
	store.db.Model(&Schedule{}).Where("family_id = ?", 100).Count(&count)
	if count != 5 {
		t.Fatalf("schedule count=%d want 5", count)
	}
}

func TestMySQLCommittedBatch(t *testing.T) {
	store := mysqlTestStore(t)
	svc := &Service{store: store, id: fakeScheduleMembership{role: roleAdmin, members: map[uint64]string{10: memberTypeVirtual, 20: memberTypeReal}}}
	ctx, in := context.Background(), futureScheduleInput()
	var results [2]*BatchCreateResult
	var failures [2]error
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], failures[i] = svc.CreateSchedulesBatch(ctx, 1, 100, []ScheduleInput{in, in}, true, "ingest:7")
		}(i)
	}
	wg.Wait()
	for _, err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(results[0].ScheduleIDs, results[1].ScheduleIDs) {
		t.Fatalf("different results: %+v", results)
	}
	// Retry after commit must bypass conflicts with the batch's own instances.
	retry, err := svc.CreateSchedulesBatch(ctx, 1, 100, []ScheduleInput{in}, false, "ingest:7")
	if err != nil || !reflect.DeepEqual(retry.ScheduleIDs, results[0].ScheduleIDs) {
		t.Fatalf("retry=%+v err=%v", retry, err)
	}
	if _, found, err := svc.FindBatchResult(ctx, 200, "ingest:7"); err != nil || found {
		t.Fatalf("cross-family lookup found=%v err=%v", found, err)
	}
	var count int64
	store.db.Model(&Schedule{}).Count(&count)
	if count != 2 {
		t.Fatalf("duplicate batch schedules=%d", count)
	}
	// A transaction failure must roll back both the marker and earlier schedules.
	longTitle := strings.Repeat("x", 100)
	err = store.CreateSchedules(ctx, []*Schedule{{FamilyID: 100, Title: "valid", StartDate: time.Now(), StartTime: "10:00"}, {FamilyID: 100, Title: longTitle, StartDate: time.Now(), StartTime: "11:00"}}, [][]ScheduleInstance{{}, {}}, "ingest:8")
	if err == nil {
		t.Fatal("expected failed schedule INSERT")
	}
	if _, found, err := svc.FindBatchResult(ctx, 100, "ingest:8"); err != nil || found {
		t.Fatalf("failed batch found=%v err=%v", found, err)
	}
	store.db.Model(&Schedule{}).Count(&count)
	if count != 2 {
		t.Fatalf("partial batch persisted count=%d", count)
	}
}

func TestMySQLFeedbackRounds(t *testing.T) {
	store := mysqlTestStore(t)
	if err := store.db.Create(&ScheduleInstance{InstanceID: 7, FamilyID: 100, ScheduleID: 3, OwnerMemberID: 20, OccurDate: time.Now().UTC(), StartAt: time.Now().UTC(), Status: StatusActive}).Error; err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, status := range []string{StatusHandled, StatusHandled, StatusIgnored, StatusIgnored} {
		for range 2 {
			if err := store.UpsertPendingFeedbackWithEvent(ctx, &ScheduleFeedback{FamilyID: 100, InstanceID: 7, ScheduleID: 3, ReporterID: 20, Reason: "去不了"}); err != nil {
				t.Fatal(err)
			}
		}
		rows, err := store.ListFeedbacks(ctx, 100, StatusPending)
		if err != nil || len(rows) != 1 {
			t.Fatalf("pending=%v err=%v", rows, err)
		}
		if err := store.UpdateFeedbackStatus(ctx, 200, rows[0].FeedbackID, status); err == nil {
			t.Fatal("cross-family resolution accepted")
		}
		if err := store.UpdateFeedbackStatus(ctx, 100, rows[0].FeedbackID, status); err != nil {
			t.Fatal(err)
		}
	}
	for _, status := range []string{StatusHandled, StatusIgnored} {
		rows, err := store.ListFeedbacks(ctx, 100, status)
		if err != nil || len(rows) != 2 {
			t.Fatalf("history %s=%v err=%v", status, rows, err)
		}
	}
}

func TestMySQLInstanceAssignmentVersions(t *testing.T) {
	store := mysqlTestStore(t)
	notifier := &fakeScheduleNotifier{}
	svc := &Service{store: store, id: fakeScheduleMembership{role: roleAdmin, members: map[uint64]string{10: memberTypeVirtual, 20: memberTypeReal, 21: memberTypeReal}}, notify: notifier}
	ctx, in := context.Background(), futureScheduleInput()
	created, err := svc.CreateSchedule(ctx, 1, 100, &in, true)
	if err != nil {
		t.Fatal(err)
	}
	var inst ScheduleInstance
	if err := store.db.Where("schedule_id = ?", created.ScheduleID).First(&inst).Error; err != nil {
		t.Fatal(err)
	}
	notifier.jobs = nil
	for _, owner := range []uint64{21, 20, 21} {
		if err := svc.UpdateInstance(ctx, 1, 100, inst.InstanceID, &InstanceInput{OwnerMemberID: owner}); err != nil {
			t.Fatal(err)
		}
	}
	keys := map[string]bool{}
	for _, job := range notifier.jobs {
		if keys[job.BusinessKey] {
			t.Fatalf("reused key: %s", job.BusinessKey)
		}
		keys[job.BusinessKey] = true
	}
	if len(keys) != 6 {
		t.Fatalf("notification keys=%d want 6", len(keys))
	}
	// A repeated identical request must not increment the version or notify again.
	if err := svc.UpdateInstance(ctx, 1, 100, inst.InstanceID, &InstanceInput{OwnerMemberID: 21}); err != nil {
		t.Fatal(err)
	}
	if len(notifier.jobs) != 6 {
		t.Fatalf("no-op created extra notification")
	}
	// An outdated writer cannot apply an earlier assignment after newer writes.
	if err := store.UpdateInstance(ctx, &inst, map[string]any{"owner_member_id": uint64(20)}); err == nil {
		t.Fatal("stale version accepted")
	}
	current, err := store.FindInstance(ctx, 100, inst.InstanceID)
	if err != nil || current.ChangeVersion != 3 || current.OwnerMemberID != 21 {
		t.Fatalf("current=%+v err=%v", current, err)
	}
}
