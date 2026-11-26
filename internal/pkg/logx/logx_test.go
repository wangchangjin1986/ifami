package logx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
)

func TestDiagnosticsDoNotLeakErrorData(t *testing.T) {
	var b bytes.Buffer
	Configure(&b, "api", "debug")
	t.Cleanup(func() { Configure(os.Stdout, "unknown", "info") })
	secret := "secret-token-child-phone-image"
	errList := []error{errors.New(secret), &mysql.MySQLError{Number: 1062, Message: secret}, CodeError("wechat", 40029, fmt.Errorf("errmsg=%s", secret)), &net.DNSError{Name: secret, Err: secret}, redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1}).Ping(context.Background()).Err()}
	for _, err := range errList {
		Failure(With(context.Background(), "request_id", "test-request"), "test.safe", err)
		Errorf("failure %v", err)
	}
	if strings.Contains(b.String(), secret) {
		t.Fatal("sensitive cause leaked")
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	for _, line := range lines {
		var row map[string]any
		if json.Unmarshal([]byte(line), &row) != nil {
			t.Fatal("invalid JSON")
		}
		if row["service"] != "api" || row["version"] == nil {
			t.Fatal(row)
		}
	}
	if !strings.Contains(b.String(), "1062") || !strings.Contains(b.String(), "40029") || !strings.Contains(b.String(), "name_resolution_failed") {
		t.Fatal("safe codes lost")
	}
}
func TestImmutableScopesDetachCancellation(t *testing.T) {
	var b bytes.Buffer
	Configure(&b, "worker", "info")
	t.Cleanup(func() { Configure(os.Stdout, "unknown", "info") })
	base, cancel := context.WithCancel(With(context.Background(), "request_id", "source", "user_id", uint64(1)))
	background := Detach(base)
	cancel()
	if background.Err() != nil {
		t.Fatal("background inherits cancellation")
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			Info(With(background, "run_id", fmt.Sprint(i), "user_id", uint64(i+2)), "execution")
		}(i)
	}
	wg.Wait()
	rows := strings.Split(strings.TrimSpace(b.String()), "\n")
	seen := map[string]bool{}
	for _, line := range rows {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if row["request_id"] != "source" {
			t.Fatal("scope lost")
		}
		id := row["run_id"].(string)
		if seen[id] {
			t.Fatal("duplicate execution")
		}
		seen[id] = true
		if strings.Count(line, "\"user_id\"") != 1 {
			t.Fatal("duplicate JSON key")
		}
	}
	if len(seen) != 32 {
		t.Fatal(len(seen))
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }
func TestLogWriterFailureDoesNotPanic(t *testing.T) {
	Configure(failingWriter{}, "api", "info")
	t.Cleanup(func() { Configure(os.Stdout, "unknown", "info") })
	Info(context.Background(), "business_succeeded")
}
func TestRequestIdentifierValidation(t *testing.T) {
	for _, s := range []string{"", "invalid\n", strings.Repeat("a", 65), "中文"} {
		if ValidID(s) {
			t.Fatal(s)
		}
	}
	if !ValidID(ID()) || !ValidID("client-id.12_34") {
		t.Fatal("valid ID rejected")
	}
}
func TestFrameworkFailureRetainsSafeCauseAndCodeLocation(t *testing.T) {
	var b bytes.Buffer
	Configure(&b, "api", "info")
	t.Cleanup(func() { Configure(os.Stdout, "unknown", "info") })
	f := FrameworkLogger{}
	f.Errorf("ignored", "[Hertz] bind: address already in use private-token-child")
	f.Errorf("ignored", &net.OpError{Op: "listen", Err: syscall.EADDRINUSE})
	f.Errorf("ignored", &net.OpError{Op: "listen", Err: syscall.EACCES})
	f.CtxErrorf(With(context.Background(), "request_id", "panic-request"), "ignored", "panic: private-body-child")
	if strings.Contains(b.String(), "private-") {
		t.Fatal("framework argument leaked")
	}
	if !strings.Contains(b.String(), "address_in_use") || !strings.Contains(b.String(), "panic") || !strings.Contains(b.String(), "logx_test.go") {
		t.Fatal("safe diagnostic lost", b.String())
	}
}
