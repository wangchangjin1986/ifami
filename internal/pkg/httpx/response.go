// Package httpx 定义统一响应格式与业务错误（见 wiki/conventions/api.md）。
package httpx

import (
	"context"
	"errors"
	"ifami/internal/pkg/logx"

	"github.com/cloudwego/hertz/pkg/app"
)

// Body 统一响应体：{code, data, message}。
// code=0 成功；业务错误码 4 位按模块分段（1xxx identity）。
type Body struct {
	Code    int    `json:"code"`
	Data    any    `json:"data,omitempty"`
	Message string `json:"message"`
}

// OK 输出成功响应。
func OK(c *app.RequestContext, data any) {
	if data == nil {
		data = struct{}{}
	}
	c.Set("logx.code", 0)
	c.JSON(200, Body{Code: 0, Data: data})
}

// Err 业务错误。HTTP 状态码只表达传输层，默认 400。
// Data 可选：携带业务结果（如日程冲突列表），由 Fail 一并输出到响应 data 字段。
type Err struct {
	Code    int
	Message string
	HTTP    int
	Data    any
}

func (e *Err) Error() string       { return e.Message }
func (e *Err) DiagnosticCode() int { return e.Code }

// Fail 输出业务错误；普通 error 回落到 ErrInternal。
func Fail(c *app.RequestContext, err error) {
	var e *Err
	if !errors.As(err, &e) {
		e = ErrInternal
		ctx := context.Background()
		if v, ok := c.Get("logx.context"); ok {
			if requestCtx, ok := v.(context.Context); ok {
				ctx = requestCtx
			}
		}
		logx.Failure(ctx, "http.internal", err)
	}
	status := e.HTTP
	if status == 0 {
		status = 400
	}
	c.Set("logx.code", e.Code)
	c.JSON(status, Body{Code: e.Code, Message: e.Message, Data: e.Data})
}

// 通用错误（不属于某个业务模块）。
var (
	ErrUnauthorized = &Err{Code: 4001, Message: "未登录或登录已过期", HTTP: 401}
	ErrInternal     = &Err{Code: 5000, Message: "服务内部错误", HTTP: 500}
)
