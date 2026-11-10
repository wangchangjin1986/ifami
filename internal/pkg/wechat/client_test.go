package wechat

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCode2SessionRequestErrorRedactsSecretAndCode(t *testing.T) {
	t.Setenv("WX_CODE2SESSION_URL", "http://127.0.0.1:1/sns/jscode2session")
	t.Setenv("WX_HTTP_TIMEOUT_SECONDS", "1")

	client := NewMiniClient("appid-for-test", "super-secret-for-test")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := client.Code2Session(ctx, "login-code-for-test")
	if err == nil {
		t.Fatal("expected request error")
	}
	msg := err.Error()
	for _, sensitive := range []string{"super-secret-for-test", "login-code-for-test", "appid-for-test"} {
		if strings.Contains(msg, sensitive) {
			t.Fatalf("error leaked sensitive value %q: %s", sensitive, msg)
		}
	}
}
