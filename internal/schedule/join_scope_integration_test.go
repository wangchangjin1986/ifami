package schedule

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"strings"
	"sync"
	"testing"
	"time"
)

type scopedMembership struct{}

func (scopedMembership) RequireActive(_ context.Context, user, family uint64) (uint64, string, error) {
	if family != 100 {
		return 0, "", ErrForbidden
	}
	switch user {
	case 1020:
		return 20, "grandparent", nil
	case 1021:
		return 21, "grandparent", nil
	case 1030:
		return 30, "parent", nil
	case 1040:
		return 40, "admin", nil
	}
	return 0, "", ErrForbidden
}
func (scopedMembership) RequireMember(_ context.Context, _ uint64, memberID uint64) (string, string, error) {
	if memberID == 10 {
		return memberTypeVirtual, "child", nil
	}
	return memberTypeReal, "grandparent", nil
}
func (scopedMembership) MemberUserID(_ context.Context, _ uint64, id uint64) (uint64, error) {
	return id + 1000, nil
}
func (scopedMembership) AdminUserIDs(context.Context, uint64) ([]uint64, error) {
	return []uint64{1040}, nil
}

func scopeFixture(t *testing.T) (*Store, *Service) {
	t.Helper()
	store := mysqlTestStore(t)
	day := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	start := day.Add(8 * time.Hour)
	sch := &Schedule{ScheduleID: 3, FamilyID: 100, ChildMemberID: 10, Title: "接孩子", Type: TypePickup, StartDate: day, StartTime: "16:00", Location: "东门", Note: "带水", OwnerMemberID: 20, RRule: "FREQ=DAILY", Status: StatusActive}
	inst := []ScheduleInstance{{InstanceID: 7, FamilyID: 100, ScheduleID: 3, ChildMemberID: 10, Title: sch.Title, Type: TypePickup, OccurDate: day, StartAt: start, Location: sch.Location, Note: sch.Note, OwnerMemberID: 20, Status: StatusActive}, {InstanceID: 8, FamilyID: 100, ScheduleID: 3, ChildMemberID: 10, Title: "其他分工", Type: TypePickup, OccurDate: day.AddDate(0, 0, 1), StartAt: start.AddDate(0, 0, 1), OwnerMemberID: 21, Status: StatusActive}, {InstanceID: 9, FamilyID: 200, ScheduleID: 3, OccurDate: day, StartAt: start, OwnerMemberID: 20, Status: StatusActive}}
	if err := store.CreateSchedule(context.Background(), sch, inst); err != nil {
		t.Fatal(err)
	}
	return store, &Service{store: store, id: scopedMembership{}, notify: &fakeScheduleNotifier{}}
}
func TestMySQLJoinInstanceScopeAndActions(t *testing.T) {
	store, svc := scopeFixture(t)
	ctx := context.Background()
	day := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		user uint64
		want int
	}{{1020, 1}, {1021, 1}, {1030, 2}, {1040, 2}} {
		rows, err := svc.ListInstances(ctx, tc.user, 100, day, day.AddDate(0, 0, 1))
		if err != nil || len(rows) != tc.want {
			t.Fatalf("user%d list=%+v err=%v", tc.user, rows, err)
		}
	}
	rows, err := svc.ListInstances(ctx, 1020, 100, day.AddDate(0, 0, 1), day.AddDate(0, 0, 1))
	if err != nil || len(rows) != 0 {
		t.Fatalf("forged date list=%+v %v", rows, err)
	}
	if _, err := svc.ListInstances(ctx, 1020, 200, day, day); !errors.Is(err, ErrForbidden) {
		t.Fatalf("family=%v", err)
	}
	for _, user := range []uint64{1020, 1030, 1040} {
		v, err := svc.GetInstance(ctx, user, 100, 7)
		if err != nil || v.FamilyID != 100 || v.RRule != "FREQ=DAILY" || v.Location != "东门" || v.Note != "带水" {
			t.Fatalf("detail=%+v %v", v, err)
		}
	}
	if _, err := svc.GetInstance(ctx, 1021, 100, 7); !errors.Is(err, ErrForbidden) {
		t.Fatalf("foreign detail=%v", err)
	}
	if _, err := svc.GetInstance(ctx, 1030, 100, 9); !errors.Is(err, ErrInstanceNotFound) {
		t.Fatalf("cross-family instance=%v", err)
	}
	if _, err := svc.ListSchedules(ctx, 1020, 100); !errors.Is(err, ErrForbidden) {
		t.Fatalf("templates=%v", err)
	}
	for _, user := range []uint64{1030, 1040} {
		if rows, err := svc.ListSchedules(ctx, user, 100); err != nil || len(rows) != 1 {
			t.Fatalf("parent/admin templates=%v %v", rows, err)
		}
	}
	// Rejected feedback leaves neither event, pending feedback, nor notification.
	if err := svc.SubmitFeedback(ctx, 1021, 100, 7, &FeedbackInput{Reason: "今天去不了"}); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	assertScopeCounts(t, store, 0, 0)
	if len(svc.notify.(*fakeScheduleNotifier).jobs) != 0 {
		t.Fatal("denied feedback notified")
	}
	if err := svc.SubmitFeedback(ctx, 1020, 100, 7, &FeedbackInput{Reason: "今天去不了"}); err != nil {
		t.Fatal(err)
	}
	assertScopeCounts(t, store, 1, 1)
	if err := svc.CompleteInstance(ctx, 1020, 100, 7); err != nil {
		t.Fatal(err)
	}
	if err := svc.CompleteInstance(ctx, 1020, 100, 7); err != nil {
		t.Fatal(err)
	}
	assertScopeCounts(t, store, 2, 1)
	inst, _ := store.FindInstance(ctx, 100, 7)
	if err := store.UpdateInstance(ctx, inst, map[string]any{"owner_member_id": uint64(21)}); err != nil {
		t.Fatal(err)
	}
	if err := svc.CompleteInstance(ctx, 1020, 100, 7); !errors.Is(err, ErrForbidden) {
		t.Fatalf("done replay old owner=%v", err)
	}
	if err := svc.SubmitFeedback(ctx, 1020, 100, 7, &FeedbackInput{Reason: "今天去不了"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("done feedback old owner=%v", err)
	}
	if _, err := svc.GetInstance(ctx, 1020, 100, 7); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if ok, err := svc.NotificationVisibility(ctx, 1020, 100, 7); err != nil || ok {
		t.Fatalf("stale notify=%v %v", ok, err)
	}
	if ok, err := svc.NotificationVisibility(ctx, 1021, 100, 7); err != nil || !ok {
		t.Fatalf("new notify=%v %v", ok, err)
	}
	if err := svc.CompleteInstance(ctx, 1021, 100, 7); err != nil {
		t.Fatal(err)
	}
	assertScopeCounts(t, store, 2, 1)
}
func assertScopeCounts(t *testing.T, store *Store, events, feedbacks int64) {
	t.Helper()
	var n int64
	if err := store.db.Model(&Event{}).Count(&n).Error; err != nil || n != events {
		t.Fatalf("events=%d want=%d err=%v", n, events, err)
	}
	if err := store.db.Model(&ScheduleFeedback{}).Count(&n).Error; err != nil || n != feedbacks {
		t.Fatalf("feedbacks=%d want=%d err=%v", n, feedbacks, err)
	}
}
func TestMySQLJoinReassignmentBlocksExecutionAndDenies(t *testing.T) {
	for _, action := range []string{"complete", "feedback"} {
		t.Run(action, func(t *testing.T) {
			store, svc := scopeFixture(t)
			ctx := context.Background()
			tx := store.db.Begin()
			defer tx.Rollback()
			if err := tx.Model(&ScheduleInstance{}).Where("family_id=100 AND instance_id=7").Updates(map[string]any{"owner_member_id": 21, "change_version": gorm.Expr("change_version+1")}).Error; err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() {
				if action == "complete" {
					result <- svc.CompleteInstance(ctx, 1020, 100, 7)
				} else {
					result <- svc.SubmitFeedback(ctx, 1020, 100, 7, &FeedbackInput{Reason: "今天去不了"})
				}
			}()
			select {
			case err := <-result:
				t.Fatalf("execution bypassed held reassignment lock: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			if err := tx.Commit().Error; err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				if !errors.Is(err, ErrForbidden) {
					t.Fatalf("old owner=%v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("execution remained blocked")
			}
			assertScopeCounts(t, store, 0, 0)
			if len(svc.notify.(*fakeScheduleNotifier).jobs) != 0 {
				t.Fatal("denied write notified")
			}
		})
	}
}

func TestMySQLJoinExecutionLockSerializesReassignment(t *testing.T) {
	for _, action := range []string{"complete", "feedback"} {
		t.Run(action, func(t *testing.T) {
			store, svc := scopeFixture(t)
			ctx := context.Background()
			locked := make(chan struct{})
			release := make(chan struct{})
			var once sync.Once
			callback := "test_join_execution_lock"
			if err := store.db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
				if strings.Contains(tx.Statement.SQL.String(), "FOR UPDATE") {
					once.Do(func() { close(locked); <-release })
				}
			}); err != nil {
				t.Fatal(err)
			}
			defer store.db.Callback().Query().Remove(callback)
			executed := make(chan error, 1)
			go func() {
				if action == "complete" {
					executed <- svc.CompleteInstance(ctx, 1020, 100, 7)
				} else {
					executed <- svc.SubmitFeedback(ctx, 1020, 100, 7, &FeedbackInput{Reason: "今天去不了"})
				}
			}()
			select {
			case <-locked:
			case <-time.After(5 * time.Second):
				t.Fatal("execution lock missing")
			}
			reassigned := make(chan error, 1)
			go func() { reassigned <- svc.UpdateInstance(ctx, 1040, 100, 7, &InstanceInput{OwnerMemberID: 21}) }()
			select {
			case err := <-reassigned:
				close(release)
				t.Fatalf("reassignment bypassed execution lock: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			close(release)
			select {
			case err := <-executed:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("execution blocked")
			}
			select {
			case err := <-reassigned:
				if action == "complete" && !errors.Is(err, ErrBadParam) {
					t.Fatalf("stale version update=%v", err)
				}
				if action == "feedback" && err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("reassignment blocked")
			}
			if action == "complete" {
				assertScopeCounts(t, store, 1, 0)
			} else {
				assertScopeCounts(t, store, 1, 1)
			}
		})
	}
}

func TestMySQLJoinNewOwnerNotificationsHaveStableLanding(t *testing.T) {
	store := mysqlTestStore(t)
	notifier := &fakeScheduleNotifier{}
	svc := &Service{store: store, id: scopedMembership{}, notify: notifier}
	in := futureScheduleInput()
	in.Type = TypePickup
	ctx := context.Background()
	result, err := svc.CreateSchedule(ctx, 1040, 100, &in, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(notifier.jobs) != 1 {
		t.Fatalf("new schedule notifications=%d", len(notifier.jobs))
	}
	payload := notifier.jobs[0].Payload
	iid, ok := payload["instance_id"].(uint64)
	if !ok || iid == 0 || payload["schedule_id"] != result.ScheduleID || payload["type"] != "schedule_updated" {
		t.Fatal(payload)
	}
	inst, err := store.FindInstance(ctx, 100, iid)
	if err != nil || inst == nil || payload["page"] != instancePage(inst) {
		t.Fatalf("landing=%+v err=%v", payload, err)
	}
	if err := svc.UpdateInstance(ctx, 1040, 100, iid, &InstanceInput{OwnerMemberID: 21}); err != nil {
		t.Fatal(err)
	}
	if len(notifier.jobs) != 3 || notifier.jobs[1].TargetUserID != 1020 || len(notifier.jobs[1].Payload) != 3 || notifier.jobs[1].Payload["title"] != "分工已调整" || notifier.jobs[2].TargetUserID != 1021 {
		t.Fatal(notifier.jobs)
	}
}
