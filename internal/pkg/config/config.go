// Package config 从环境变量加载 Phase 0 运行配置。
// 配置项清单与含义见 deploy/.env.example。
package config

import "os"

type Config struct {
	APIAddr string

	// 微信小程序
	WXMiniAppID  string
	WXMiniSecret string

	// 数据库 / 缓存
	RDSDSN    string
	RedisAddr string

	// 敏感字段加密密钥（base64 编码的 32 字节，AES-256-GCM）
	FieldEncKey string

	// 邀请 token 签名密钥（HMAC-SHA256）
	InviteSecret string
}

// Load 读取环境变量，未设置的回落到默认值。
func Load() *Config {
	return &Config{
		APIAddr:      getenv("API_ADDR", ":8080"),
		WXMiniAppID:  getenv("WX_MINIAPP_APPID", ""),
		WXMiniSecret: getenv("WX_MINIAPP_SECRET", ""),
		RDSDSN:       getenv("RDS_DSN", ""),
		RedisAddr:    getenv("REDIS_ADDR", "localhost:6379"),
		FieldEncKey:  getenv("FIELD_ENC_KEY", ""),
		InviteSecret: getenv("INVITE_SECRET", ""),
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
