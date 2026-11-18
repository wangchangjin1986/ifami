package task

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

type fakeTaskMembership struct{ role string }

func (f fakeTaskMembership) RequireActive(context.Context, uint64, uint64) (uint64, string, error) {
	return 1, f.role, nil
}

func (f fakeTaskMembership) RequireMember(context.Context, uint64, uint64) (string, string, error) {
	return memberTypeReal, f.role, nil
}

func (f fakeTaskMembership) MemberUserID(context.Context, uint64, uint64) (uint64, error) {
	return 1, nil
}

type fakeTaskNotifier struct{ jobs []NotifyInput }

func (f *fakeTaskNotifier) CreateJob(_ context.Context, in NotifyInput) (uint64, error) {
	f.jobs = append(f.jobs, in)
	return uint64(len(f.jobs)), nil
}

type fakeImageSafety struct{ err error }

func (f fakeImageSafety) CheckImageSafety(context.Context, []byte) error { return f.err }

func newTaskTestStore(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	gdb, err := gorm.Open(mysql.New(mysql.Config{Conn: db, SkipInitializeWithVersion: true}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	return NewStore(gdb), mock
}

func remindableTaskRows(freq string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"task_id", "family_id", "child_member_id", "name", "goal_type", "goal_value", "freq", "weekly_target", "owner_member_id", "remind_time", "status", "created_at"}).
		AddRow(1, 100, 10, "阅读", GoalCount, 1, freq, 0, 20, "20:00", StatusActive, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
}

func taskRows(goalType string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"task_id", "family_id", "child_member_id", "name", "goal_type", "goal_value", "freq", "weekly_target", "owner_member_id", "remind_time", "status", "created_at"}).
		AddRow(1, 100, 10, "拍照记录", goalType, 0, FreqDaily, 0, 20, "", StatusActive, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
}

func pngBytes() []byte {
	return []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R'}
}

func TestRequireWriter_AdminOnly(t *testing.T) {
	ctx := context.Background()
	if err := (&Service{id: fakeTaskMembership{role: roleAdmin}}).requireWriter(ctx, 1, 100); err != nil {
		t.Fatalf("admin requireWriter error = %v", err)
	}
	if err := (&Service{id: fakeTaskMembership{role: roleParent}}).requireWriter(ctx, 1, 100); err != ErrForbidden {
		t.Fatalf("parent requireWriter error = %v，want ErrForbidden", err)
	}
}

func TestWeekStart(t *testing.T) {
	// 锚点：2026-10-26 是周一（自然周起点）。
	mon := time.Date(2026, 10, 26, 15, 4, 0, 0, time.UTC)
	if mon.Weekday() != time.Monday {
		t.Fatalf("测试锚点假设错误：%s 不是周一", mon.Weekday())
	}
	cases := []struct {
		in   time.Time
		want time.Time
	}{
		{time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)},  // 周一本身
		{time.Date(2026, 10, 29, 8, 30, 0, 0, time.UTC), time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)}, // 周四
		{time.Date(2026, 11, 1, 23, 59, 0, 0, time.UTC), time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)}, // 周日（同周尾）
	}
	for _, c := range cases {
		if got := weekStart(c.in); !got.Equal(c.want) {
			t.Errorf("weekStart(%s) = %s，want %s", c.in, got, c.want)
		}
	}
}

func TestTaskOccursOnSkipsWeekdaysAtWeekend(t *testing.T) {
	sat := time.Date(2026, 11, 21, 20, 30, 0, 0, appLocation)
	if taskOccursOn(FreqWeekdays, sat) {
		t.Fatalf("weekdays task should not occur on Saturday")
	}
	mon := time.Date(2026, 11, 23, 20, 30, 0, 0, appLocation)
	if !taskOccursOn(FreqWeekdays, mon) {
		t.Fatalf("weekdays task should occur on Monday")
	}
}

func TestReminderClockUsesShanghaiTime(t *testing.T) {
	// 12:30 UTC = 上海时间 20:30；20:00 的提醒应视为已到点。
	now := time.Date(2026, 11, 16, 12, 30, 0, 0, time.UTC)
	store, mock := newTaskTestStore(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `tasks`")).WillReturnRows(remindableTaskRows(FreqDaily))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*) FROM `checkins`")).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	notifier := &fakeTaskNotifier{}
	svc := &Service{store: store, id: fakeTaskMembership{role: roleAdmin}, notify: notifier}
	if err := svc.RemindDue(context.Background(), now); err != nil {
		t.Fatalf("RemindDue error = %v", err)
	}
	if len(notifier.jobs) != 1 {
		t.Fatalf("notify jobs = %d, want 1", len(notifier.jobs))
	}
	if notifier.jobs[0].BusinessKey != "checkin:1:2026-11-16" {
		t.Fatalf("business key = %s", notifier.jobs[0].BusinessKey)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestWeekdaysReminderSkippedOnSaturday(t *testing.T) {
	// 2026-11-21 20:30 Asia/Shanghai 是周六。
	now := time.Date(2026, 11, 21, 12, 30, 0, 0, time.UTC)
	store, mock := newTaskTestStore(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `tasks`")).WillReturnRows(remindableTaskRows(FreqWeekdays))
	notifier := &fakeTaskNotifier{}
	svc := &Service{store: store, id: fakeTaskMembership{role: roleAdmin}, notify: notifier}
	if err := svc.RemindDue(context.Background(), now); err != nil {
		t.Fatalf("RemindDue error = %v", err)
	}
	if len(notifier.jobs) != 0 {
		t.Fatalf("notify jobs = %d, want 0", len(notifier.jobs))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestCalcStreakTodayAndYesterday(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		dates map[string]bool
		want  int
	}{
		{
			name: "今天已打卡从今天连续计算",
			dates: map[string]bool{
				"2026-11-05": true,
				"2026-11-04": true,
				"2026-11-03": true,
			},
			want: 3,
		},
		{
			name: "今天未打卡从昨天连续计算",
			dates: map[string]bool{
				"2026-11-04": true,
				"2026-11-03": true,
			},
			want: 2,
		},
		{
			name: "断档后停止",
			dates: map[string]bool{
				"2026-11-05": true,
				"2026-11-03": true,
			},
			want: 1,
		},
	}
	for _, c := range cases {
		if got := calcStreak(c.dates, now); got != c.want {
			t.Errorf("%s: calcStreak() = %d，want %d", c.name, got, c.want)
		}
	}
}

func TestValidatePayloadAllowsAlternateManualMethod(t *testing.T) {
	if err := validatePayload(GoalTimer, map[string]any{"count": float64(30)}, ""); err != nil {
		t.Fatalf("timer task should allow count payload for manual checkin: %v", err)
	}
	if err := validatePayload(GoalCount, map[string]any{"seconds": float64(600)}, ""); err != nil {
		t.Fatalf("count task should allow timer payload for manual checkin: %v", err)
	}
	if err := validatePayload(GoalPhoto, map[string]any{"photo": true}, "wxfile://tmp.jpg"); err != nil {
		t.Fatalf("photo task should accept selected image key: %v", err)
	}
}

func TestSubmitCheckinRejectsJsonMediaTosKey(t *testing.T) {
	store, mock := newTaskTestStore(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `tasks`")).WillReturnRows(taskRows(GoalPhoto))
	svc := &Service{store: store, id: fakeTaskMembership{role: roleAdmin}}
	_, err := svc.SubmitCheckin(context.Background(), 1, 100, 1, &CheckinInput{
		Payload:     map[string]any{"photo": true},
		MediaTosKey: "wxfile://tmp.jpg",
	}, "")
	if err != ErrBadGoal {
		t.Fatalf("SubmitCheckin error = %v, want ErrBadGoal", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestSubmitPhotoCheckinStoresMysqlBlob(t *testing.T) {
	store, mock := newTaskTestStore(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `tasks`")).WillReturnRows(taskRows(GoalPhoto))
	mock.ExpectQuery("FROM `checkins`").WillReturnRows(sqlmock.NewRows([]string{"checkin_id"}))
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO `checkins`")).WillReturnResult(sqlmock.NewResult(55, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO `events_stream`")).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `created_at` FROM `checkins`")).WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(time.Now().UTC()))

	svc := &Service{store: store, id: fakeTaskMembership{role: roleAdmin}}
	got, err := svc.SubmitPhotoCheckin(context.Background(), 1, 100, 1, &PhotoInput{
		Data:           pngBytes(),
		ContentType:    "image/png",
		IdempotencyKey: "idem-photo-1",
	})
	if err != nil {
		t.Fatalf("SubmitPhotoCheckin error = %v", err)
	}
	if got.CheckinID != 55 || !got.GoalHit {
		t.Fatalf("SubmitPhotoCheckin result = %+v, want checkin_id=55 and goal_hit=true", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestSubmitPhotoCheckinRejectsNonPhotoTask(t *testing.T) {
	store, mock := newTaskTestStore(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `tasks`")).WillReturnRows(taskRows(GoalCount))
	svc := &Service{store: store, id: fakeTaskMembership{role: roleAdmin}}
	_, err := svc.SubmitPhotoCheckin(context.Background(), 1, 100, 1, &PhotoInput{Data: pngBytes(), ContentType: "image/png"})
	if err != ErrBadGoal {
		t.Fatalf("SubmitPhotoCheckin error = %v, want ErrBadGoal", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestSubmitPhotoCheckinRejectsSpoofedContentType(t *testing.T) {
	store, mock := newTaskTestStore(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `tasks`")).WillReturnRows(taskRows(GoalPhoto))
	svc := &Service{store: store, id: fakeTaskMembership{role: roleAdmin}}
	_, err := svc.SubmitPhotoCheckin(context.Background(), 1, 100, 1, &PhotoInput{Data: []byte("not an image"), ContentType: "image/png"})
	if err != ErrBadGoal {
		t.Fatalf("SubmitPhotoCheckin error = %v, want ErrBadGoal", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestSubmitPhotoCheckinRejectsUnsafeImage(t *testing.T) {
	store, mock := newTaskTestStore(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `tasks`")).WillReturnRows(taskRows(GoalPhoto))
	svc := &Service{store: store, id: fakeTaskMembership{role: roleAdmin}, safety: fakeImageSafety{err: errors.New("unsafe")}}
	_, err := svc.SubmitPhotoCheckin(context.Background(), 1, 100, 1, &PhotoInput{Data: pngBytes(), ContentType: "image/png"})
	if err != ErrImageUnsafe {
		t.Fatalf("SubmitPhotoCheckin error = %v, want ErrImageUnsafe", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestFindCheckinPhotoReturnsBlob(t *testing.T) {
	store, mock := newTaskTestStore(t)
	want := pngBytes()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `checkin_id`,`photo_data`,`photo_content_type` FROM `checkins`")).
		WillReturnRows(sqlmock.NewRows([]string{"checkin_id", "photo_data", "photo_content_type"}).AddRow(55, want, "image/png"))
	got, contentType, err := store.FindCheckinPhoto(context.Background(), 100, 55)
	if err != nil {
		t.Fatalf("FindCheckinPhoto error = %v", err)
	}
	if string(got) != string(want) || contentType != "image/png" {
		t.Fatalf("FindCheckinPhoto = (%v, %s), want (%v, image/png)", got, contentType, want)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}
