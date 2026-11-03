package ingest

import "ifami/internal/pkg/httpx"

// 业务错误码分段：6xxx = ingest（wiki/conventions/api.md）。
var (
	ErrBadParam    = &httpx.Err{Code: 6001, Message: "参数错误", HTTP: 400}
	ErrJobNotFound = &httpx.Err{Code: 6002, Message: "导入任务不存在", HTTP: 404}
	ErrForbidden   = &httpx.Err{Code: 6003, Message: "只有管理员才能录入", HTTP: 403}
	ErrBadImage    = &httpx.Err{Code: 6004, Message: "图片格式或大小不符（JPG/PNG/WebP，≤5MB）", HTTP: 400}
	ErrRecognize   = &httpx.Err{Code: 6005, Message: "没识别出来，可以手动安排", HTTP: 400}
	ErrQuota       = &httpx.Err{Code: 6006, Message: "今天识别次数已用完，明天再试", HTTP: 429}
	ErrBadState    = &httpx.Err{Code: 6007, Message: "当前状态不允许此操作", HTTP: 409}
)
