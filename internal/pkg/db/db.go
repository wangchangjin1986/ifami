// Package db 提供 MySQL 连接（GORM）。
package db

import (
	"context"
	"errors"
	"os"
	"strconv"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"ifami/internal/pkg/logx"
)

var (
	slowSQL      = envDurationMS("SLOW_SQL_MS", 200)
	logAllSQL    = os.Getenv("SQL_LOG_LEVEL") == "debug"
	maxOpenConns = envInt("DB_MAX_OPEN_CONNS", 20)
	maxIdleConns = envInt("DB_MAX_IDLE_CONNS", 10)
	connMaxLife  = envDurationSeconds("DB_CONN_MAX_LIFETIME_SECONDS", 1800)
	connMaxIdle  = envDurationSeconds("DB_CONN_MAX_IDLE_SECONDS", 300)
)

// Open 建立 GORM 连接。DSN 需带 parseTime=true&loc=UTC，
// 保证 DATETIME 以 UTC 读写（见 wiki/conventions/database.md）。
// SQL 日志走 logx 带级别输出；「查无记录」是正常业务路径（如打卡幂等预查），不打 ERROR。
func Open(dsn string) (*gorm.DB, error) {
	gdb, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: gormLog{}})
	if err != nil {
		return nil, err
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(maxOpenConns)
	sqlDB.SetMaxIdleConns(maxIdleConns)
	sqlDB.SetConnMaxLifetime(connMaxLife)
	sqlDB.SetConnMaxIdleTime(connMaxIdle)
	return gdb, nil
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
	case logAllSQL:
		logx.Infof("[gorm] %s | rows=%d | %.1fms", sql, rows, ms(elapsed))
	}
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000.0 }

func envInt(key string, def int) int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil || v <= 0 {
		return def
	}
	return v
}

func envDurationMS(key string, def int) time.Duration {
	return time.Duration(envInt(key, def)) * time.Millisecond
}

func envDurationSeconds(key string, def int) time.Duration {
	return time.Duration(envInt(key, def)) * time.Second
}
