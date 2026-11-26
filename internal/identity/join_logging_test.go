package identity

import (
	"bytes"
	"context"
	"ifami/internal/pkg/logx"
	"os"
	"strings"
	"testing"
)

func TestRedisScriptErrorLogsSafeCause(t *testing.T) {
	r := redisJoinTest(t)
	var b bytes.Buffer
	logx.Configure(&b, "api", "debug")
	t.Cleanup(func() { logx.Configure(os.Stdout, "unknown", "info") })
	ctx := logx.With(context.Background(), "request_id", "redis-failure-test")
	err := r.eval(ctx, `return redis.call('SET',KEYS[1],ARGV[1],'INVALID_OPTION')`, []string{r.prefix + "secret-key"}, "private-join-code-image").Err()
	if err == nil {
		t.Fatal("fault script succeeded")
	}
	output := b.String()
	if !strings.Contains(output, "syntax_error") || !strings.Contains(output, "redis-failure-test") {
		t.Fatal(output)
	}
	for _, secret := range []string{"private-join-code-image", r.prefix + "secret-key", "INVALID_OPTION"} {
		if strings.Contains(output, secret) {
			t.Fatal("script data leaked")
		}
	}
}
