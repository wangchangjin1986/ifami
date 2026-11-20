package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"ifami/internal/pkg/wechat"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestGenericSubscribeDataUsesNonSensitiveTimestamp(t *testing.T) {
	created := time.Date(2026, 11, 20, 3, 24, 0, 0, time.UTC)
	job := &NotifyJob{Payload: genericSchedulePayload("schedule_updated", "grandparent"), CreatedAt: created}
	if got := subscribeData(job)["time2"].Value; got != "11:24" {
		t.Fatalf("time2=%q want created time 11:24", got)
	}
	job.CreatedAt = time.Time{}
	if got := subscribeData(job)["time2"].Value; !regexp.MustCompile(`^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`).MatchString(got) {
		t.Fatalf("zero-created fallback is not valid TIME: %q", got)
	}
}

// The store is real MySQL; both WeChat requests terminate at a test-owned HTTP server.
func TestMySQLGenericScheduleSubscribeRequest(t *testing.T) {
	store := mysqlNotifyContractTest(t)
	var mu sync.Mutex
	var requests []wechat.SubscribeMessage
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			fmt.Fprint(w, `{"access_token":"fixture-token","expires_in":7200}`)
		case "/subscribe":
			var message wechat.SubscribeMessage
			if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
				http.Error(w, "bad JSON", 400)
				return
			}
			mu.Lock()
			requests = append(requests, message)
			mu.Unlock()
			fmt.Fprint(w, `{"errcode":0}`)
		default:
			http.Error(w, "unexpected endpoint", 404)
		}
	}))
	defer endpoint.Close()
	t.Setenv("WX_ACCESS_TOKEN_URL", endpoint.URL+"/token")
	t.Setenv("WX_SUBSCRIBE_SEND_URL", endpoint.URL+"/subscribe")
	channel := NewWechatSubscribe(wechat.NewMiniClient("fixture-app", "fixture-secret"), SubscribeTemplates{Schedule: "fixture-schedule", Feedback: "fixture-feedback"})
	svc := NewService(store, fakeIdentity{active: map[uint64]map[uint64]bool{100: {20: true}}, roles: map[uint64]string{20: "grandparent"}}, channel)
	svc.SetScheduleVisibility(&scopeDecision{allowed: false})
	for _, typ := range []string{"schedule_unassigned", "", "backup_notification"} {
		job := &NotifyJob{FamilyID: 100, TargetUserID: 20, Level: LevelStrong, Status: StatusPending, BusinessKey: "generic-contract:" + typ, CreatedAt: time.Date(2026, 11, 20, 3, 24, 0, 0, time.UTC),
			Payload: map[string]any{"type": typ, "title": "秘密课程", "location": "秘密地点", "note": "秘密备注", "start_at": "19:47", "time_text": "19:47", "page": "pages/event-detail/index?note=秘密备注"}}
		if typ == "schedule_unassigned" {
			job.Payload["instance_id"] = uint64(7)
		}
		if err := store.CreateJob(context.Background(), job); err != nil {
			t.Fatal(err)
		}
		if err := svc.processPending(context.Background(), job, job.CreatedAt.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		persisted, err := store.FindJob(context.Background(), job.JobID)
		if err != nil || persisted.Status != StatusDone {
			t.Fatalf("completion: %+v %v", persisted, err)
		}
		if len(job.Payload) != 3 || job.Level != LevelMedium {
			t.Fatalf("unsanitized job: %+v", job)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 3 {
		t.Fatalf("requests=%d want3", len(requests))
	}
	for _, message := range requests {
		if message.ToUser != "openid" || message.TemplateID != "fixture-schedule" || !strings.HasPrefix(message.Page, "pages/gp-today/index?job_id=") {
			t.Fatalf("request routing: %+v", message)
		}
		if len(message.Data) != 3 || message.Data["thing1"].Value != "分工已调整" || message.Data["time2"].Value != "11:24" {
			t.Fatalf("actual template data: %+v", message.Data)
		}
		raw, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"秘密课程", "秘密地点", "秘密备注", "19:47", "event-detail", "instance_id"} {
			if strings.Contains(string(raw), secret) {
				t.Fatalf("request leaks %s: %s", secret, raw)
			}
		}
	}
}

func mysqlNotifyContractTest(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN required")
	}
	cfg, err := mysqlDriver.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MultiStatements = true
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("ifami_test_notify_%d", time.Now().UnixNano())
	if _, err = admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec("DROP DATABASE " + name); _ = admin.Close() })
	cfg.DBName = name
	db, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqldb, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
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
		if _, err = sqldb.Exec(string(body)); err != nil {
			t.Fatalf("migration %s: %v", file, err)
		}
	}
	return NewStore(db)
}
