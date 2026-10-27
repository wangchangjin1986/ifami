package notify

import "ifami/internal/pkg/httpx"

// notify 模块业务错误码：4xxx 段（api.md）。4001 已被通用未登录占用，从 4002 起。
var (
	ErrBadParam    = &httpx.Err{Code: 4002, Message: "参数错误"}
	ErrJobNotFound = &httpx.Err{Code: 4003, Message: "触达任务不存在"}
	ErrBadLevel    = &httpx.Err{Code: 4004, Message: "触达级别不合法"}
)
