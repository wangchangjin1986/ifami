package task

import "ifami/internal/pkg/httpx"

// task 模块业务错误码：3xxx 段（api.md）。
var (
	ErrBadParam        = &httpx.Err{Code: 3001, Message: "参数错误"}
	ErrTaskNotFound    = &httpx.Err{Code: 3002, Message: "任务不存在"}
	ErrCheckinNotFound = &httpx.Err{Code: 3003, Message: "打卡记录不存在"}
	ErrForbidden       = &httpx.Err{Code: 3004, Message: "无权限操作任务"}
	ErrChildRequired   = &httpx.Err{Code: 3006, Message: "任务必须绑定孩子"}
	ErrOwnerRequired   = &httpx.Err{Code: 3007, Message: "责任人必须是家庭成员"}
	ErrBadGoal         = &httpx.Err{Code: 3008, Message: "目标类型或目标量不合法"}
)
