// Package logx provides structured, context-aware application diagnostics.
package logx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
)

type contextKey struct{}

var logger atomic.Pointer[slog.Logger]

func init() { Configure(os.Stdout, "unknown", os.Getenv("LOG_LEVEL")) }

func Configure(out io.Writer, service, level string) {
	l := slog.LevelInfo
	switch strings.ToLower(level) {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	}
	logger.Store(slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: l, ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
		if a.Key == slog.TimeKey {
			a.Value = slog.StringValue(a.Value.Time().UTC().Format(time.RFC3339Nano))
		}
		return a
	}})).With("service", service, "version", Version()))
}
func Version() string {
	if b, ok := debug.ReadBuildInfo(); ok {
		rev := "unknown"
		dirty := false
		for _, s := range b.Settings {
			if s.Key == "vcs.revision" {
				rev = s.Value
			}
			if s.Key == "vcs.modified" {
				dirty = s.Value == "true"
			}
		}
		if dirty {
			return rev + "-modified"
		}
		return rev
	}
	return "unknown"
}
func ID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
func ValidID(s string) bool {
	if len(s) < 1 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// With copies fields so no background task shares a mutable map with a request.
func With(ctx context.Context, args ...any) context.Context {
	prev, _ := ctx.Value(contextKey{}).([]any)
	fields := mergeFields(prev, args)
	return context.WithValue(ctx, contextKey{}, fields)
}

// Detach carries only diagnostic fields, never request objects or authentication tokens.
func Detach(ctx context.Context) context.Context {
	fields, _ := ctx.Value(contextKey{}).([]any)
	return With(context.Background(), fields...)
}
func Write(ctx context.Context, level slog.Level, event string, args ...any) {
	if !logger.Load().Enabled(ctx, level) {
		return
	}
	fields, _ := ctx.Value(contextKey{}).([]any)
	all := mergeFields(fields, append([]any{"event", event}, args...))
	logger.Load().Log(ctx, level, event, all...)
}
func Debug(ctx context.Context, event string, args ...any) {
	Write(ctx, slog.LevelDebug, event, args...)
}
func Info(ctx context.Context, event string, args ...any) { Write(ctx, slog.LevelInfo, event, args...) }
func Warn(ctx context.Context, event string, args ...any) { Write(ctx, slog.LevelWarn, event, args...) }
func Error(ctx context.Context, event string, args ...any) {
	Write(ctx, slog.LevelError, event, args...)
}

// CodedError preserves public error behavior while providing a safe numeric cause.
type CodedError struct {
	Kind string
	Code int
	Err  error
}

func (e *CodedError) Error() string                    { return e.Err.Error() }
func (e *CodedError) Unwrap() error                    { return e.Err }
func CodeError(kind string, code int, err error) error { return &CodedError{kind, code, err} }
func ErrorFields(err error) []any {
	if err == nil {
		return nil
	}
	kind, code, cause := "unknown", 0, "unclassified_error"
	var m *mysql.MySQLError
	var re redis.Error
	var n net.Error
	var remote *CodedError
	var dns *net.DNSError
	switch {
	case errors.As(err, &remote):
		kind = remote.Kind
		code = remote.Code
		cause = "remote_error"
	case errors.Is(err, context.Canceled):
		kind = "canceled"
		cause = "operation_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		kind = "timeout"
		cause = "deadline_exceeded"
	case errors.As(err, &m):
		kind = "mysql"
		code = int(m.Number)
		cause = "mysql_error"
	case errors.As(err, &re):
		kind = "redis"
		cause = "redis_error"
		s := re.Error()
		switch {
		case strings.Contains(s, "ERR syntax error"):
			cause = "syntax_error"
		case strings.HasPrefix(s, "NOAUTH"):
			cause = "authentication_required"
		case strings.HasPrefix(s, "NOPERM"):
			cause = "permission_denied"
		case strings.HasPrefix(s, "WRONGTYPE"):
			cause = "wrong_type"
		case strings.HasPrefix(s, "NOSCRIPT"):
			cause = "script_missing"
		case strings.HasPrefix(s, "OOM"):
			cause = "out_of_memory"
		}
	case errors.As(err, &dns):
		kind = "dns"
		cause = "name_resolution_failed"
	case errors.As(err, &n) && n.Timeout():
		kind = "timeout"
		cause = "network_timeout"
	case errors.Is(err, syscall.EADDRINUSE):
		kind = "listen"
		cause = "address_in_use"
	case errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM):
		kind = "permission"
		cause = "permission_denied"
	case errors.Is(err, syscall.ECONNREFUSED):
		kind = "connection"
		cause = "connection_refused"
	case errors.Is(err, syscall.ECONNRESET):
		kind = "connection"
		cause = "connection_reset"
	case errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF):
		kind = "connection"
		cause = "unexpected_eof"
	}
	return []any{"error_kind", kind, "error_code", code, "cause", cause}
}
func Failure(ctx context.Context, operation string, err error, args ...any) {
	fields := append([]any{"operation", operation, "outcome", "failed"}, args...)
	fields = append(fields, ErrorFields(err)...)
	Error(ctx, "operation_failed", fields...)
}

// Operation logs once when a synchronous operation finishes; caller passes its actual result.
func Operation(ctx context.Context, operation string, slow time.Duration) func(error) {
	start := time.Now()
	return func(err error) {
		elapsed := time.Since(start)
		args := []any{"operation", operation, "duration_ms", float64(elapsed.Microseconds()) / 1000}
		if err != nil {
			Failure(ctx, operation, err, args...)
			return
		}
		args = append(args, "outcome", "succeeded")
		if elapsed > slow {
			Warn(ctx, "operation_finished", args...)
		} else {
			Debug(ctx, "operation_finished", args...)
		}
	}
}
func safeFormat(format string, args []any) string {
	safe := append([]any(nil), args...)
	for i, a := range safe {
		if e, ok := a.(error); ok {
			safe[i] = fmt.Sprint(ErrorFields(e))
		}
	}
	return fmt.Sprintf(format, safe...)
}
func Infof(f string, a ...any)  { Info(context.Background(), "legacy", "message", safeFormat(f, a)) }
func Warnf(f string, a ...any)  { Warn(context.Background(), "legacy", "message", safeFormat(f, a)) }
func Errorf(f string, a ...any) { Error(context.Background(), "legacy", "message", safeFormat(f, a)) }
func Fatalf(f string, a ...any) {
	Error(context.Background(), "fatal", "message", safeFormat(f, a))
	os.Exit(1)
}

// Business records outcomes without printing public messages or sensitive inputs.
func Business(ctx context.Context, operation string) func(error) {
	start := time.Now()
	return func(err error) {
		args := []any{"operation", operation, "duration_ms", float64(time.Since(start).Microseconds()) / 1000, "outcome", "succeeded"}
		if err != nil {
			args[len(args)-1] = "rejected"
			var coded interface{ DiagnosticCode() int }
			if errors.As(err, &coded) {
				args = append(args, "code", coded.DiagnosticCode())
			} else {
				args[len(args)-1] = "failed"
			}
		}
		Info(ctx, "business_finished", args...)
	}
}

// Format is for existing static format strings. Raw errors are never interpolated.
func Format(format string, args ...any) string { return safeFormat(format, args) }

// Last value wins when a scope enriches an existing field, matching context semantics.
func mergeFields(base, extra []any) []any {
	out := make([]any, 0, len(base)+len(extra))
	for _, fields := range [][]any{base, extra} {
		for i := 0; i+1 < len(fields); i += 2 {
			key, ok := fields[i].(string)
			if !ok {
				continue
			}
			found := false
			for j := 0; j < len(out); j += 2 {
				if out[j] == key {
					out[j+1] = fields[i+1]
					found = true
					break
				}
			}
			if !found {
				out = append(out, key, fields[i+1])
			}
		}
	}
	return out
}

func RemoteResponse(ctx context.Context, operation string, status int) {
	args := []any{"operation", operation, "http_status", status}
	if status >= 400 {
		Warn(ctx, "remote_http_failed", args...)
	} else {
		Debug(ctx, "remote_response", args...)
	}
}

// Throttle only limits repetitive diagnostics; it never controls business admission.
type Throttle struct{ last atomic.Int64 }

func (t *Throttle) Warn(ctx context.Context, event string, interval time.Duration, args ...any) {
	now := time.Now().UnixNano()
	last := t.last.Load()
	if now-last < int64(interval) || !t.last.CompareAndSwap(last, now) {
		return
	}
	Warn(ctx, event, args...)
}
