package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"ifami/internal/pkg/auth"
	"ifami/internal/pkg/httpx"
	"ifami/internal/pkg/logx"
	"os"
	"strings"
	"testing"
)

type loggingSessions struct{}

func (loggingSessions) Create(context.Context, string, uint64) error         { return nil }
func (loggingSessions) UserID(context.Context, string) (uint64, bool, error) { return 7, true, nil }
func TestRequestLogCorrelatesIdentityAndFailure(t *testing.T) {
	var output bytes.Buffer
	logx.Configure(&output, "api", "debug")
	t.Cleanup(func() { logx.Configure(os.Stdout, "unknown", "info") })
	h := server.New()
	h.Use(accessLog())
	h.GET("/test/:id", auth.BearerAuth(loggingSessions{}), func(ctx context.Context, c *app.RequestContext) { httpx.Fail(c, errors.New("sensitive-private-body")) })
	r := ut.PerformRequest(h.Engine, "GET", "/test/123?token=sensitive-query", nil, ut.Header{Key: "X-Request-Id", Value: "known-request"}, ut.Header{Key: "Authorization", Value: "Bearer sensitive-session"}, ut.Header{Key: "X-Family-Id", Value: "999"})
	if r.Code != 500 || string(r.Header().Get("X-Request-Id")) != "known-request" {
		t.Fatal(r.Code, r.Header())
	}
	if strings.Contains(output.String(), "sensitive-") {
		t.Fatal("sensitive data leaked")
	}
	var end, root map[string]any
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if row["event"] == "request_out" {
			end = row
		}
		if row["event"] == "operation_failed" {
			root = row
		}
	}
	for _, row := range []map[string]any{end, root} {
		if row["request_id"] != "known-request" || row["user_id"] != float64(7) {
			t.Fatal("missing association", row)
		}
	}
	if end["code"] != float64(5000) || end["route"] != "/test/:id" || end["outcome"] != "failed" {
		t.Fatal(end)
	}
	if _, ok := end["family_id"]; ok {
		t.Fatal("request family promoted to authenticated identity")
	}
	if end["requested_family_id"] != float64(999) {
		t.Fatal(end)
	}
}
func TestInvalidRequestIDIsReplacedAndSuccessCodeRecorded(t *testing.T) {
	var b bytes.Buffer
	logx.Configure(&b, "api", "info")
	t.Cleanup(func() { logx.Configure(os.Stdout, "unknown", "info") })
	h := server.New()
	h.Use(accessLog())
	h.GET("/ok", func(ctx context.Context, c *app.RequestContext) { httpx.OK(c, nil) })
	r := ut.PerformRequest(h.Engine, "GET", "/ok", nil, ut.Header{Key: "X-Request-Id", Value: strings.Repeat("x", 65)})
	id := string(r.Header().Get("X-Request-Id"))
	if !logx.ValidID(id) || id == strings.Repeat("x", 65) {
		t.Fatal(id)
	}
	if !strings.Contains(b.String(), "\"code\":0") || !strings.Contains(b.String(), id) {
		t.Fatal(b.String())
	}
}
func TestPanicLogPreservesSafeLocationAndAuthenticatedIdentity(t *testing.T) {
	var output bytes.Buffer
	logx.Configure(&output, "api", "info")
	t.Cleanup(func() { logx.Configure(os.Stdout, "unknown", "info") })
	h := server.Default()
	h.Use(accessLog())
	h.GET("/panic", auth.BearerAuth(loggingSessions{}), func(ctx context.Context, c *app.RequestContext) { panic("sensitive-private-body") })
	r := ut.PerformRequest(h.Engine, "GET", "/panic", nil, ut.Header{Key: "Authorization", Value: "Bearer private-token"}, ut.Header{Key: "X-Request-Id", Value: "panic-request"})
	if r.Code != 500 {
		t.Fatal(r.Code)
	}
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var row map[string]any
		if json.Unmarshal([]byte(line), &row) != nil {
			t.Fatal("invalid json")
		}
		if row["event"] == "request_panicked" {
			if row["user_id"] != float64(7) || row["request_id"] != "panic-request" || row["route"] != "/panic" || row["status"] != float64(500) || row["code_locations"] == nil {
				t.Fatal(row)
			}
			if strings.Contains(line, "sensitive-private-body") {
				t.Fatal("panic body leaked")
			}
			return
		}
	}
	t.Fatal("panic diagnostic missing")
}
