// Package config 从环境变量加载 Phase 0 运行配置。
// 配置项清单与含义见 deploy/.env.example。
package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	APIAddr string

	// 微信小程序
	WXMiniAppID                 string
	WXMiniSecret                string
	NotifyChannel               string
	WXSubscribeScheduleTemplate string
	WXSubscribeTaskTemplate     string
	WXSubscribeFeedbackTemplate string

	// 数据库 / 缓存
	RDSDSN    string
	RedisAddr string

	// 敏感字段加密密钥（base64 编码的 32 字节，AES-256-GCM）
	FieldEncKey string

	// 邀请 token 签名密钥（HMAC-SHA256）
	InviteSecret        string
	JoinFailedUserLimit int
	JoinFailedIPLimit   int
	JoinFailedWindow    time.Duration

	// 火山方舟（拍照导入日程，豆包多模态）
	ArkAPIKey  string
	ArkBaseURL string
	ArkModel   string

	// 拍照导入识别原图临时目录（落盘识别、任务结束即删）
	IngestDir string
}

// Load 读取环境变量，未设置的回落到默认值。
func Load() *Config {
	return &Config{
		APIAddr:                     getenv("API_ADDR", ":8080"),
		WXMiniAppID:                 getenv("WX_MINIAPP_APPID", ""),
		WXMiniSecret:                getenv("WX_MINIAPP_SECRET", ""),
		NotifyChannel:               getenv("NOTIFY_CHANNEL", "wechat_subscribe"),
		WXSubscribeScheduleTemplate: getenv("WX_SUBSCRIBE_TEMPLATE_SCHEDULE", ""),
		WXSubscribeTaskTemplate:     getenv("WX_SUBSCRIBE_TEMPLATE_TASK", ""),
		WXSubscribeFeedbackTemplate: getenv("WX_SUBSCRIBE_TEMPLATE_FEEDBACK", ""),
		RDSDSN:                      getenv("RDS_DSN", ""),
		RedisAddr:                   getenv("REDIS_ADDR", "localhost:6379"),
		FieldEncKey:                 getenv("FIELD_ENC_KEY", ""),
		InviteSecret:                getenv("INVITE_SECRET", ""),
		JoinFailedUserLimit:         positiveInt("JOIN_FAILED_USER_LIMIT", 10),
		JoinFailedIPLimit:           positiveInt("JOIN_FAILED_IP_LIMIT", 30),
		JoinFailedWindow:            time.Duration(positiveInt("JOIN_FAILED_WINDOW_SECONDS", 900)) * time.Second,
		ArkAPIKey:                   getenv("ARK_API_KEY", ""),
		ArkBaseURL:                  getenv("ARK_BASE_URL", "https://ark.cn-beijing.volces.com/api/v3"),
		ArkModel:                    getenv("ARK_MODEL", ""),
		IngestDir:                   getenv("INGEST_DIR", "/var/lib/ifami/ingest"),
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func positiveInt(key string, def int) int {
	v, e := strconv.Atoi(os.Getenv(key))
	if e != nil || v <= 0 || v > 86400 {
		return def
	}
	return v
}
