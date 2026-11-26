package notify

import (
	"bytes"
	"context"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"ifami/internal/pkg/logx"
	"os"
	"strings"
	"testing"
)

func TestJobStateLogRequiresSuccessfulPersistence(t *testing.T) {
	for _, name := range []string{"persisted", "database_failure", "missing_job"} {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			logx.Configure(&logs, "worker", "info")
			t.Cleanup(func() { logx.Configure(os.Stdout, "unknown", "info") })
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			gdb, err := gorm.Open(mysql.New(mysql.Config{Conn: db, SkipInitializeWithVersion: true}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			if err != nil {
				t.Fatal(err)
			}
			mock.ExpectBegin()
			query := mock.ExpectExec("UPDATE `notify_jobs`")
			if name == "database_failure" {
				query.WillReturnError(errors.New("private-driver-value"))
				mock.ExpectRollback()
			} else {
				rows := int64(1)
				if name == "missing_job" {
					rows = 0
				}
				query.WillReturnResult(sqlmock.NewResult(0, rows))
				mock.ExpectCommit()
			}
			err = NewStore(gdb).UpdateJob(logx.With(context.Background(), "run_id", "execution", "family_id", uint64(100)), 5, map[string]any{"status": StatusDone})
			if (err == nil) != (name == "persisted") {
				t.Fatal(name, err)
			}
			if strings.Contains(logs.String(), "job_state_changed") != (name == "persisted") {
				t.Fatal("false persistence outcome", logs.String())
			}
			if name == "persisted" && (!strings.Contains(logs.String(), "execution") || !strings.Contains(logs.String(), "\"job_id\":5")) {
				t.Fatal("state association lost")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
