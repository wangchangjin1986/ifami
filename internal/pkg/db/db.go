// Package db 提供 MySQL 连接（GORM）。
package db

import (
	"context"
	"errors"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"ifami/internal/pkg/logx"
)

const slowSQL = 200 * time.Millisecond

// Open 建立 GORM 连接。DSN 需带 parseTime=true&loc=UTC，
// 保证 DATETIME 以 UTC 读写（见 wiki/conventions/database.md）。
// SQL 日志走 logx 带级别输出；「查无记录」是正常业务路径（如打卡幂等预查），不打 ERROR。
func Open(dsn string) (*gorm.DB, error) {
	return gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: gormLog{}})
}

// gormLog 实现 gorm logger.Interface，统一日志格式：级别 + [gorm] + SQL + 行数 + 耗时。
type gormLog struct{}

func (gormLog) LogMode(gormlogger.LogLevel) gormlogger.Interface { return gormLog{} }

func (gormLog) Info(_ context.Context, msg string, args ...any) {
	logx.Infof("[gorm] "+msg, args...)
}

func (gormLog) Warn(_ context.Context, msg string, args ...any) {
	logx.Warnf("[gorm] "+msg, args...)
}

func (gormLog) Error(_ context.Context, msg string, args ...any) {
	logx.Errorf("[gorm] "+msg, args...)
}

func (gormLog) Trace(_ context.Context, begin time.Time, fc func() (string, int64), err error) {
	sql, rows := fc()
	elapsed := time.Since(begin)
	switch {
	case err != nil && !errors.Is(err, gorm.ErrRecordNotFound):
		logx.Errorf("[gorm] %s | rows=%d | %.1fms | err=%v", sql, rows, ms(elapsed), err)
	case elapsed > slowSQL:
		logx.Warnf("[gorm] slow %s | rows=%d | %.1fms", sql, rows, ms(elapsed))
	default:
		logx.Infof("[gorm] %s | rows=%d | %.1fms", sql, rows, ms(elapsed))
	}
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000.0 }
