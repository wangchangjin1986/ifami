package logx

import (
	"context"
	"fmt"
	"github.com/cloudwego/hertz/pkg/common/hlog"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// FrameworkLogger intentionally suppresses arbitrary framework arguments (URLs/panic bodies).
// The embedded control API remains available; application LOG_LEVEL controls output.
type FrameworkLogger struct{ hlog.FullLogger }

func (FrameworkLogger) Trace(args ...interface{}) {
	framework(context.Background(), slog.LevelDebug, "trace", args...)
}
func (FrameworkLogger) Tracef(_ string, args ...interface{}) {
	framework(context.Background(), slog.LevelDebug, "trace", args...)
}
func (FrameworkLogger) CtxTracef(ctx context.Context, _ string, args ...interface{}) {
	framework(ctx, slog.LevelDebug, "trace", args...)
}
func (FrameworkLogger) Debug(args ...interface{}) {
	framework(context.Background(), slog.LevelDebug, "debug", args...)
}
func (FrameworkLogger) Debugf(_ string, args ...interface{}) {
	framework(context.Background(), slog.LevelDebug, "debug", args...)
}
func (FrameworkLogger) CtxDebugf(ctx context.Context, _ string, args ...interface{}) {
	framework(ctx, slog.LevelDebug, "debug", args...)
}
func (FrameworkLogger) Info(args ...interface{}) {
	framework(context.Background(), slog.LevelInfo, "info", args...)
}
func (FrameworkLogger) Infof(_ string, args ...interface{}) {
	framework(context.Background(), slog.LevelInfo, "info", args...)
}
func (FrameworkLogger) CtxInfof(ctx context.Context, _ string, args ...interface{}) {
	framework(ctx, slog.LevelInfo, "info", args...)
}
func (FrameworkLogger) Notice(args ...interface{}) {
	framework(context.Background(), slog.LevelInfo, "notice", args...)
}
func (FrameworkLogger) Noticef(_ string, args ...interface{}) {
	framework(context.Background(), slog.LevelInfo, "notice", args...)
}
func (FrameworkLogger) CtxNoticef(ctx context.Context, _ string, args ...interface{}) {
	framework(ctx, slog.LevelInfo, "notice", args...)
}
func (FrameworkLogger) Warn(args ...interface{}) {
	framework(context.Background(), slog.LevelWarn, "warn", args...)
}
func (FrameworkLogger) Warnf(_ string, args ...interface{}) {
	framework(context.Background(), slog.LevelWarn, "warn", args...)
}
func (FrameworkLogger) CtxWarnf(ctx context.Context, _ string, args ...interface{}) {
	framework(ctx, slog.LevelWarn, "warn", args...)
}
func (FrameworkLogger) Error(args ...interface{}) {
	framework(context.Background(), slog.LevelError, "error", args...)
}
func (FrameworkLogger) Errorf(_ string, args ...interface{}) {
	framework(context.Background(), slog.LevelError, "error", args...)
}
func (FrameworkLogger) CtxErrorf(ctx context.Context, _ string, args ...interface{}) {
	framework(ctx, slog.LevelError, "error", args...)
}
func (FrameworkLogger) Fatal(args ...interface{}) {
	framework(context.Background(), slog.LevelError, "fatal", args...)
	os.Exit(1)
}
func (FrameworkLogger) Fatalf(_ string, args ...interface{}) {
	framework(context.Background(), slog.LevelError, "fatal", args...)
	os.Exit(1)
}
func (FrameworkLogger) CtxFatalf(ctx context.Context, _ string, args ...interface{}) {
	framework(ctx, slog.LevelError, "fatal", args...)
	os.Exit(1)
}

func CodeLocations() []string {
	pc := make([]uintptr, 16)
	n := runtime.Callers(2, pc)
	frames := runtime.CallersFrames(pc[:n])
	result := make([]string, 0, n)
	for {
		f, more := frames.Next()
		if !strings.HasSuffix(f.Function, "logx.CodeLocations") && !strings.HasSuffix(f.Function, "logx.framework") && !strings.Contains(f.Function, "logx.FrameworkLogger.") {
			result = append(result, fmt.Sprintf("%s:%d %s", filepath.Base(f.File), f.Line, f.Function))
		}
		if !more {
			break
		}
	}
	return result
}
func framework(ctx context.Context, level slog.Level, name string, args ...interface{}) {
	cause := "framework_error"
	for _, arg := range args {
		if e, ok := arg.(error); ok {
			Write(ctx, level, "framework_diagnostic", append([]any{"source", "hertz", "diagnostic_level", name, "code_locations", CodeLocations()}, ErrorFields(e)...)...)
			return
		}
		if text, ok := arg.(string); ok {
			switch {
			case strings.Contains(text, "address already in use"):
				cause = "address_in_use"
			case strings.Contains(text, "permission denied"):
				cause = "permission_denied"
			case strings.Contains(text, "panic"):
				cause = "panic"
			case strings.Contains(text, "invalid route"):
				cause = "invalid_route"
			}
		}
	}
	fields := []any{"source", "hertz", "diagnostic_level", name, "cause", cause}
	if level >= slog.LevelWarn {
		fields = append(fields, "code_locations", CodeLocations())
	}
	Write(ctx, level, "framework_diagnostic", fields...)
}
