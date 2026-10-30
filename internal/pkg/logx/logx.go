// Package logx 提供带级别的最小日志封装（INFO/WARN/ERROR/FATAL）。
// 输出到 stdout，由 systemd `StandardOutput=append:` 落盘（见 deploy/*.service）。
// 格式：2026/10/30 10:00:00 INFO  [notify] ...
package logx

import (
	"log"
	"os"
)

var std = log.New(os.Stdout, "", log.LstdFlags)

// Infof 常规运行信息（启动、关键动作）。
func Infof(format string, args ...any) { std.Printf("INFO  "+format, args...) }

// Warnf 可降级的异常（主流程继续，但需要关注，如单次触达解析失败）。
func Warnf(format string, args ...any) { std.Printf("WARN  "+format, args...) }

// Errorf 明确失败（任务处理失败、DB 错误等）。
func Errorf(format string, args ...any) { std.Printf("ERROR "+format, args...) }

// Fatalf 致命错误，打日志后退出（等价 log.Fatalf）。
func Fatalf(format string, args ...any) { std.Printf("FATAL "+format, args...); os.Exit(1) }
