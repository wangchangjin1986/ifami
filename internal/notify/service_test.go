package notify

import (
	"context"
	"errors"
	"testing"
	"time"

	"ifami/internal/pkg/httpx"
)

type fakeIdentity struct {
	roles     map[uint64]string
	active    map[uint64]map[uint64]bool
	backup    map[uint64][]uint64
	backupErr error
}

var testNow = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

type fakeChannel struct {
	sent     []uint64
	payloads []map[string]any
}

func (f *fakeChannel) Name() string { return ChannelSubscribe }

func (f *fakeChannel) Send(_ context.Context, _ string, job *NotifyJob) error {
	f.sent = append(f.sent, job.JobID)
	f.payloads = append(f.payloads, cloneState(job.Payload))
	return nil
}

func (f *fakeChannel) SubscribeTemplateIDs() []string { return []string{"tpl_schedule", "tpl_task"} }

func (f fakeIdentity) OpenID(_ context.Context, userID uint64) (string, error) {
	return "openid", nil
}

func (f fakeIdentity) RequireActive(_ context.Context, userID, familyID uint64) (uint64, string, error) {
	if f.active[familyID] != nil && f.active[familyID][userID] {
		role := "parent"
		if f.roles[userID] != "" {
			role = f.roles[userID]
		}
		return userID, role, nil
	}
	return 0, "", &httpx.Err{Code: 1005, Message: "不是该家庭成员", HTTP: 403}
}

func (f fakeIdentity) AdminUserIDs(_ context.Context, familyID uint64) ([]uint64, error) {
	ids := make([]uint64, 0, len(f.active[familyID]))
	for userID := range f.active[familyID] {
		ids = append(ids, userID)
	}
	return ids, nil
}

func (f fakeIdentity) BackupUserIDs(ctx context.Context, familyID uint64) ([]uint64, error) {
	if f.backupErr != nil {
		return nil, f.backupErr
	}
	if f.backup != nil {
		return f.backup[familyID], nil
	}
	return f.AdminUserIDs(ctx, familyID)
}

type fakeNotifyStore struct {
	created       []*NotifyJob
	updatedStatus string
	reddots       []NotifyJob
}

func (f *fakeNotifyStore) CreateJob(_ context.Context, j *NotifyJob) error {
	f.created = append(f.created, j)
	return nil
}

func (f *fakeNotifyStore) FindJob(context.Context, uint64) (*NotifyJob, error) { return nil, nil }

func (f *fakeNotifyStore) FindJobByBusinessKey(context.Context, uint64, string) (*NotifyJob, error) {
	return nil, nil
}

func (f *fakeNotifyStore) ListDueJobs(context.Context, string, time.Time) ([]NotifyJob, error) {
	return nil, nil
}

func (f *fakeNotifyStore) ListReddotJobs(context.Context, uint64, uint64) ([]NotifyJob, error) {
	return f.reddots, nil
}

func (f *fakeNotifyStore) UpdateJob(_ context.Context, _ uint64, updates map[string]any) error {
	if s, ok := updates["status"].(string); ok {
		f.updatedStatus = s
	}
	return nil
}

func TestCreateJobForUserRejectsCrossFamilyActor(t *testing.T) {
	svc := NewService(nil, fakeIdentity{active: map[uint64]map[uint64]bool{
		200: {1: true, 2: true},
	}}, MockSubscribe{})

	_, err := svc.CreateJobForUser(context.Background(), 1, &CreateJobInput{
		FamilyID: 100, TargetUserID: 2, Level: LevelWeak,
	})
	var he *httpx.Err
	if !errors.As(err, &he) || he.HTTP != 403 {
		t.Fatalf("want forbidden membership error, got %v", err)
	}
}

func TestCreateJobForUserRejectsTargetOutsideFamily(t *testing.T) {
	svc := NewService(nil, fakeIdentity{active: map[uint64]map[uint64]bool{
		100: {1: true},
		200: {2: true},
	}}, MockSubscribe{})

	_, err := svc.CreateJobForUser(context.Background(), 1, &CreateJobInput{
		FamilyID: 100, TargetUserID: 2, Level: LevelWeak,
	})
	var he *httpx.Err
	if !errors.As(err, &he) || he.HTTP != 403 {
		t.Fatalf("want forbidden target membership error, got %v", err)
	}
}

func TestTemplateConfigReturnsChannelTemplateIDs(t *testing.T) {
	svc := NewService(nil, fakeIdentity{}, &fakeChannel{})
	cfg := svc.TemplateConfig()
	if len(cfg.TemplateIDs) != 2 || cfg.TemplateIDs[0] != "tpl_schedule" || cfg.TemplateIDs[1] != "tpl_task" {
		t.Fatalf("TemplateIDs = %#v", cfg.TemplateIDs)
	}
}

func TestEscalateBackupUsesBackupContacts(t *testing.T) {
	store := &fakeNotifyStore{}
	svc := &Service{store: store, id: fakeIdentity{backup: map[uint64][]uint64{100: {1, 2, 3}}}, channel: &fakeChannel{}}
	job := &NotifyJob{JobID: 10, FamilyID: 100, TargetUserID: 2, Level: LevelStrong, Status: StatusReddot, Payload: map[string]any{"title": "接送"}, ChannelState: map[string]any{}}
	if err := svc.escalateBackup(context.Background(), job, testNow); err != nil {
		t.Fatalf("escalateBackup err = %v", err)
	}
	if len(store.created) != 2 {
		t.Fatalf("created backup jobs = %d, want 2", len(store.created))
	}
	for _, j := range store.created {
		if j.TargetUserID == 2 {
			t.Fatalf("backup should skip original target user")
		}
		if j.Payload["type"] != "backup_notification" || j.Level != LevelMedium {
			t.Fatalf("backup job = %#v", j)
		}
	}
	if store.updatedStatus != StatusEscalated {
		t.Fatalf("updatedStatus = %s, want %s", store.updatedStatus, StatusEscalated)
	}
}

func TestEscalateBackupKeepsReddotWhenBackupLookupFails(t *testing.T) {
	store := &fakeNotifyStore{}
	svc := &Service{store: store, id: fakeIdentity{backupErr: errors.New("identity unavailable")}, channel: &fakeChannel{}}
	job := &NotifyJob{JobID: 10, FamilyID: 100, TargetUserID: 2, Level: LevelStrong, Status: StatusReddot, Payload: map[string]any{"title": "接送"}, ChannelState: map[string]any{}}
	if err := svc.escalateBackup(context.Background(), job, testNow); err == nil {
		t.Fatalf("escalateBackup error = nil, want lookup error")
	}
	if len(store.created) != 0 {
		t.Fatalf("created backup jobs = %d, want 0", len(store.created))
	}
	if store.updatedStatus != "" {
		t.Fatalf("updatedStatus = %s, want unchanged", store.updatedStatus)
	}
}

type deniedScheduleScope struct{}

func (deniedScheduleScope) NotificationVisibility(context.Context, uint64, uint64, uint64) (bool, error) {
	return false, nil
}
func TestStaleScheduleCannotSendOrEscalate(t *testing.T) {
	for _, phase := range []string{"pending", "sent", "reddot"} {
		t.Run(phase, func(t *testing.T) {
			store := &fakeNotifyStore{}
			ch := &fakeChannel{}
			svc := &Service{store: store, id: fakeIdentity{active: map[uint64]map[uint64]bool{100: {20: true}}, roles: map[uint64]string{20: "grandparent"}}, channel: ch, schedules: deniedScheduleScope{}}
			job := &NotifyJob{JobID: 7, FamilyID: 100, TargetUserID: 20, Level: LevelStrong, Payload: map[string]any{"type": "schedule_reminder", "instance_id": uint64(7), "title": "private"}}
			switch phase {
			case "pending":
				svc.processPending(context.Background(), job, testNow)
			case "sent":
				svc.escalateReddot(context.Background(), job, testNow)
			case "reddot":
				svc.escalateBackup(context.Background(), job, testNow)
			}
			if len(ch.sent) > 0 || len(store.created) > 0 || store.updatedStatus != StatusDone {
				t.Fatalf("stale: sends=%v creates=%d status=%s", ch.sent, len(store.created), store.updatedStatus)
			}
		})
	}
}

type scopeDecision struct {
	allowed bool
	calls   [][3]uint64
}

func (f *scopeDecision) NotificationVisibility(_ context.Context, user, family, instance uint64) (bool, error) {
	f.calls = append(f.calls, [3]uint64{user, family, instance})
	return f.allowed, nil
}
func TestScheduleVisibilityAllowedAndDeniedReddots(t *testing.T) {
	for _, allow := range []bool{true, false} {
		scope := &scopeDecision{allowed: allow}
		store := &fakeNotifyStore{reddots: []NotifyJob{{JobID: 7, FamilyID: 100, TargetUserID: 20, Level: LevelStrong, Status: StatusReddot, Payload: map[string]any{"type": "schedule_reminder", "instance_id": float64(7), "location": "private"}}}}
		svc := &Service{store: store, id: fakeIdentity{active: map[uint64]map[uint64]bool{100: {20: true}}, roles: map[uint64]string{20: "grandparent"}}, channel: &fakeChannel{}, schedules: scope}
		views, err := svc.ListReddotJobs(context.Background(), 20, 100)
		want := 0
		if allow {
			want = 1
		}
		if err != nil || len(views) != want {
			t.Fatalf("allow%v views%v err%v", allow, views, err)
		}
		if len(scope.calls) != 1 || scope.calls[0] != [3]uint64{20, 100, 7} {
			t.Fatalf("wrong authoritative lookup: %v", scope.calls)
		}
		ch := svc.channel.(*fakeChannel)
		job := store.reddots[0]
		if err := svc.processPending(context.Background(), &job, testNow); err != nil {
			t.Fatal(err)
		}
		if len(ch.sent) != want {
			t.Fatalf("allow%v sends%v", allow, ch.sent)
		}
	}
}
func TestScheduleGenericPayloadRemovesDetailsAndCannotEscalate(t *testing.T) {
	for _, typ := range []string{"", "backup_notification", "schedule_unassigned"} {
		store := &fakeNotifyStore{}
		ch := &fakeChannel{}
		scope := &scopeDecision{allowed: false}
		svc := &Service{store: store, id: fakeIdentity{active: map[uint64]map[uint64]bool{100: {20: true}}, roles: map[uint64]string{20: "grandparent"}}, channel: ch, schedules: scope}
		job := &NotifyJob{JobID: 7, FamilyID: 100, TargetUserID: 20, Level: LevelStrong, Payload: map[string]any{"type": typ, "title": "秘密课程", "location": "东门", "note": "private", "page": "pages/event-detail/index?note=private"}}
		if typ == "schedule_unassigned" {
			job.Payload["instance_id"] = uint64(7)
		}
		if err := svc.processPending(context.Background(), job, testNow); err != nil {
			t.Fatal(err)
		}
		if len(ch.sent) != 1 || len(ch.payloads[0]) != 3 || ch.payloads[0]["title"] != "分工已调整" || ch.payloads[0]["page"] != "pages/gp-today/index" || store.updatedStatus != StatusDone {
			t.Fatalf("generic: %+v status%v", ch.payloads, store.updatedStatus)
		}
		if err := svc.escalateBackup(context.Background(), job, testNow); err != nil {
			t.Fatal(err)
		}
		if len(store.created) != 0 {
			t.Fatal("generic escalated")
		}
	}
}
func TestTaskNotificationRetainsPayload(t *testing.T) {
	ch := &fakeChannel{}
	store := &fakeNotifyStore{}
	svc := &Service{store: store, id: fakeIdentity{}, channel: ch, schedules: deniedScheduleScope{}}
	job := &NotifyJob{JobID: 8, Level: LevelMedium, Payload: map[string]any{"type": "task_reminder", "task_id": uint64(7), "title": "阅读"}}
	if err := svc.processPending(context.Background(), job, testNow); err != nil {
		t.Fatal(err)
	}
	if len(ch.sent) != 1 || ch.payloads[0]["title"] != "阅读" {
		t.Fatal(ch.payloads)
	}
}
