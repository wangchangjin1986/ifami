// Package db 提供 MySQL 连接（GORM）。
package db

import (
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// Open 建立 GORM 连接。DSN 需带 parseTime=true&loc=UTC，
// 保证 DATETIME 以 UTC 读写（见 wiki/conventions/database.md）。
func Open(dsn string) (*gorm.DB, error) {
	return gorm.Open(mysql.Open(dsn), &gorm.Config{})
}
