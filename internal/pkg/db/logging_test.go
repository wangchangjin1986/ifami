package db

import (
	"bytes"
	"context"
	"github.com/go-sql-driver/mysql"
	"ifami/internal/pkg/logx"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSQLAndDriverValuesNeverReachLogs(t *testing.T) {
	var b bytes.Buffer
	logx.Configure(&b, "api", "debug")
	t.Cleanup(func() { logx.Configure(os.Stdout, "unknown", "info") })
	ctx := logx.With(context.Background(), "request_id", "sql-test")
	fc := func() (string, int64) { return "INSERT INTO children VALUES ('private-child-name-phone')", 1 }
	gormLog{}.Trace(ctx, time.Now(), fc, &mysql.MySQLError{Number: 1062, Message: "private-child-name-phone"})
	gormLog{}.Trace(ctx, time.Now().Add(-slowSQL-time.Millisecond), fc, nil)
	if strings.Contains(b.String(), "private-child") || strings.Contains(b.String(), "INSERT") {
		t.Fatal("SQL data leaked")
	}
	if !strings.Contains(b.String(), "1062") || !strings.Contains(b.String(), "sql-test") || !strings.Contains(b.String(), "database_slow") {
		t.Fatal(b.String())
	}
}
