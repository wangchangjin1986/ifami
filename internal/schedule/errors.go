package schedule

import "ifami/internal/pkg/httpx"

// 业务错误码分段：2xxx = schedule（wiki/conventions/api.md）。
var (
	ErrBadParam         = &httpx.Err{Code: 2001, Message: "参数错误", HTTP: 400}
	ErrScheduleNotFound = &httpx.Err{Code: 2002, Message: "日程不存在", HTTP: 404}
	ErrInstanceNotFound = &httpx.Err{Code: 2003, Message: "日程实例不存在", HTTP: 404}
	ErrForbidden        = &httpx.Err{Code: 2004, Message: "无权限编辑日程", HTTP: 403}
	ErrChildRequired    = &httpx.Err{Code: 2006, Message: "需绑定孩子（虚拟成员）", HTTP: 400}
	ErrOwnerRequired    = &httpx.Err{Code: 2007, Message: "责任人需为家庭成员", HTTP: 400}
	ErrBadRRule         = &httpx.Err{Code: 2008, Message: "重复规则无效", HTTP: 400}
)
