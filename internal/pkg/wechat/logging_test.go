package wechat

import (
	"bytes"
	"context"
	"fmt"
	"ifami/internal/pkg/logx"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestHTTP200BusinessFailureLogsVendorCodeNotPayload(t *testing.T) {
	var logs bytes.Buffer
	logx.Configure(&logs, "api", "debug")
	t.Cleanup(func() { logx.Configure(os.Stdout, "unknown", "info") })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"errcode":40029,"errmsg":"private-token-child-phone"}`)
	}))
	defer srv.Close()
	t.Setenv("WX_CODE2SESSION_URL", srv.URL)
	_, err := NewMiniClient("private-appid", "private-secret").Code2Session(logx.With(context.Background(), "request_id", "vendor-test"), "private-login-code")
	if err == nil {
		t.Fatal("business error lost")
	}
	if !strings.Contains(logs.String(), "40029") || !strings.Contains(logs.String(), "vendor-test") {
		t.Fatal(logs.String())
	}
	if strings.Contains(logs.String(), "private-") {
		t.Fatal("vendor payload leaked")
	}
}
