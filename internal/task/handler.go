package task

import (
	"context"
	"strconv"
	"time"

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
	authed.GET("/checkins", h.ListCheckinsByFamily)
	authed.DELETE("/checkins/:cid", h.DeleteCheckin)
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

func (h *Handler) ListCheckinsByFamily(ctx context.Context, c *app.RequestContext) {
	fid, err := familyID(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	start, end, err := parseDateRange(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	childID := uint64(0)
	if s := c.Query("child_id"); s != "" {
		childID, err = strconv.ParseUint(s, 10, 64)
		if err != nil {
			httpx.Fail(c, ErrBadParam)
			return
		}
	}
	views, err := h.svc.ListCheckinsByFamily(ctx, auth.UserID(ctx), fid, start, end, childID)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, views)
}

func (h *Handler) DeleteCheckin(ctx context.Context, c *app.RequestContext) {
	fid, err := familyID(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	cid, err := checkinIDParam(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	if err := h.svc.DeleteCheckin(ctx, auth.UserID(ctx), fid, cid); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, nil)
}

func familyID(c *app.RequestContext) (uint64, error) {
	return strconv.ParseUint(string(c.GetHeader("X-Family-Id")), 10, 64)
}

func taskIDParam(c *app.RequestContext) (uint64, error) {
	return strconv.ParseUint(c.Param("tid"), 10, 64)
}

func checkinIDParam(c *app.RequestContext) (uint64, error) {
	return strconv.ParseUint(c.Param("cid"), 10, 64)
}

func parseDateRange(c *app.RequestContext) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 1)
	if s := c.Query("start"); s != "" {
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return start, end, err
		}
		start = t
	}
	if s := c.Query("end"); s != "" {
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return start, end, err
		}
		end = t.AddDate(0, 0, 1)
	}
	return start, end, nil
}
