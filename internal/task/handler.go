package task

import (
	"context"
	"strconv"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"

	"ifami/internal/pkg/auth"
	"ifami/internal/pkg/httpx"
)

// Handler 是 task 模块的 HTTP 层。家庭上下文走 Header X-Family-Id（api.md）。
type Handler struct {
	svc      *Service
	sessions auth.SessionStore
}

func NewHandler(svc *Service, sessions auth.SessionStore) *Handler {
	return &Handler{svc: svc, sessions: sessions}
}

// Register 注册路由。全部走 Bearer 鉴权，家庭上下文用 X-Family-Id。
func (h *Handler) Register(g *route.RouterGroup) {
	authed := g.Group("", auth.BearerAuth(h.sessions))
	authed.POST("/tasks", h.CreateTask)
	authed.GET("/tasks", h.ListTasks)
	authed.PATCH("/tasks/:tid", h.UpdateTask)
	authed.DELETE("/tasks/:tid", h.DeleteTask)
	authed.POST("/tasks/:tid/checkins", h.SubmitCheckin)
	authed.GET("/tasks/:tid/checkins", h.ListCheckins)
}

func (h *Handler) CreateTask(ctx context.Context, c *app.RequestContext) {
	fid, err := familyID(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	var req TaskInput
	if err := c.BindJSON(&req); err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	taskID, err := h.svc.CreateTask(ctx, auth.UserID(ctx), fid, &req)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, map[string]any{"task_id": taskID})
}

func (h *Handler) ListTasks(ctx context.Context, c *app.RequestContext) {
	fid, err := familyID(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	views, err := h.svc.ListTasks(ctx, auth.UserID(ctx), fid)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, views)
}

func (h *Handler) UpdateTask(ctx context.Context, c *app.RequestContext) {
	fid, err := familyID(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	tid, err := taskIDParam(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	var req TaskInput
	if err := c.BindJSON(&req); err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	if err := h.svc.UpdateTask(ctx, auth.UserID(ctx), fid, tid, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, nil)
}

func (h *Handler) DeleteTask(ctx context.Context, c *app.RequestContext) {
	fid, err := familyID(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	tid, err := taskIDParam(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	if err := h.svc.DeleteTask(ctx, auth.UserID(ctx), fid, tid); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, nil)
}

func (h *Handler) SubmitCheckin(ctx context.Context, c *app.RequestContext) {
	fid, err := familyID(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	tid, err := taskIDParam(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	var req CheckinInput
	if err := c.BindJSON(&req); err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	idemKey := string(c.GetHeader("Idempotency-Key"))
	res, err := h.svc.SubmitCheckin(ctx, auth.UserID(ctx), fid, tid, &req, idemKey)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, res)
}

func (h *Handler) ListCheckins(ctx context.Context, c *app.RequestContext) {
	fid, err := familyID(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	tid, err := taskIDParam(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	views, err := h.svc.ListCheckinsByTask(ctx, auth.UserID(ctx), fid, tid)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, views)
}

func familyID(c *app.RequestContext) (uint64, error) {
	return strconv.ParseUint(string(c.GetHeader("X-Family-Id")), 10, 64)
}

func taskIDParam(c *app.RequestContext) (uint64, error) {
	return strconv.ParseUint(c.Param("tid"), 10, 64)
}
