package identity

import "ifami/internal/pkg/httpx"

// 业务错误码分段：1xxx = identity（wiki/conventions/api.md）。
var (
	ErrBadCode         = &httpx.Err{Code: 1001, Message: "登录凭证无效", HTTP: 401}
	ErrBadInvite       = &httpx.Err{Code: 1002, Message: "邀请链接无效", HTTP: 400}
	ErrInviteExpired   = &httpx.Err{Code: 1003, Message: "邀请链接已过期", HTTP: 400}
	ErrFamilyNotFound  = &httpx.Err{Code: 1004, Message: "家庭不存在", HTTP: 404}
	ErrNotMember       = &httpx.Err{Code: 1005, Message: "不是该家庭成员", HTTP: 403}
	ErrForbidden       = &httpx.Err{Code: 1006, Message: "无权限执行此操作", HTTP: 403}
	ErrAlreadyMember   = &httpx.Err{Code: 1007, Message: "已是该家庭成员", HTTP: 400}
	ErrBadParam        = &httpx.Err{Code: 1008, Message: "参数错误", HTTP: 400}
	ErrMemberNotFound  = &httpx.Err{Code: 1009, Message: "成员不存在", HTTP: 404}
	ErrLastAdmin       = &httpx.Err{Code: 1010, Message: "不能移除或降级最后一个管理员", HTTP: 400}
	ErrJoinInvalid     = &httpx.Err{Code: 1011, Message: "加入码不可用，请让家人重新展示", HTTP: 400}
	ErrJoinUnavailable = &httpx.Err{Code: 1012, Message: "加入暂不可用，请稍后重试", HTTP: 503}
	ErrOtherFamily     = &httpx.Err{Code: 1013, Message: "已归属其他家庭，请先处理原家庭关系", HTTP: 409}
	ErrRemovedMember   = &httpx.Err{Code: 1014, Message: "已被移出该家庭，请联系管理员处理", HTTP: 403}
	ErrOldClient       = &httpx.Err{Code: 1015, Message: "旧邀请方式已停止，请更新小程序并使用家庭加入码", HTTP: 400}
	ErrJoinLimited     = &httpx.Err{Code: 1016, Message: "尝试次数过多，请稍后重试", HTTP: 429}
	ErrJoinScan        = &httpx.Err{Code: 1017, Message: "扫码内容错误，请扫描家庭加入码或手输六位数字", HTTP: 400}
)
