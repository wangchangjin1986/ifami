package schedule

import (
	"context"
	"strconv"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"

	"ifami/internal/pkg/auth"
	"ifami/internal/pkg/httpx"
)

// Handler 是 schedule 模块的 HTTP 层。家庭上下文走 Header X-Family-Id（api.md）。
type Handler struct {
	svc      *Service
	sessions auth.SessionStore
}

func NewHandler(svc *Service, sessions auth.SessionStore) *Handler {
	return &Handler{svc: svc, sessions: sessions}
}

// Register 注册路由。实例挂在 schedules 之下，静态路由优先于 :sid 参数匹配。
func (h *Handler) Register(g *route.RouterGroup) {
	authed := g.Group("", auth.BearerAuth(h.sessions))
	authed.POST("/schedules", h.CreateSchedule)
	authed.GET("/schedules", h.ListSchedules)
	authed.PATCH("/schedules/:sid", h.UpdateSchedule)
	authed.DELETE("/schedules/:sid", h.DeleteSchedule)
	authed.GET("/schedules/instances", h.ListInstances)
	authed.PATCH("/schedules/instances/:iid", h.UpdateInstance)
}

func (h *Handler) CreateSchedule(ctx context.Context, c *app.RequestContext) {
	fid, err := familyID(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	var req struct {
		ScheduleInput
		Force bool `json:"force"`
	}
	if err := c.BindJSON(&req); err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	res, err := h.svc.CreateSchedule(ctx, auth.UserID(ctx), fid, &req.ScheduleInput, req.Force)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, res)
}

func (h *Handler) ListSchedules(ctx context.Context, c *app.RequestContext) {
	fid, err := familyID(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	views, err := h.svc.ListSchedules(ctx, auth.UserID(ctx), fid)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, views)
}

func (h *Handler) UpdateSchedule(ctx context.Context, c *app.RequestContext) {
	fid, err := familyID(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	sid, err := scheduleIDParam(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	var req struct {
		ScheduleInput
		Force bool `json:"force"`
	}
	if err := c.BindJSON(&req); err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	res, err := h.svc.UpdateSchedule(ctx, auth.UserID(ctx), fid, sid, &req.ScheduleInput, req.Force)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, res)
}

func (h *Handler) DeleteSchedule(ctx context.Context, c *app.RequestContext) {
	fid, err := familyID(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	sid, err := scheduleIDParam(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	if err := h.svc.DeleteSchedule(ctx, auth.UserID(ctx), fid, sid); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, nil)
}

func (h *Handler) ListInstances(ctx context.Context, c *app.RequestContext) {
	fid, err := familyID(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	start, end, err := parseRange(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	views, err := h.svc.ListInstances(ctx, auth.UserID(ctx), fid, start, end)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, views)
}

func (h *Handler) UpdateInstance(ctx context.Context, c *app.RequestContext) {
	fid, err := familyID(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	iid, err := instanceIDParam(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	var req InstanceInput
	if err := c.BindJSON(&req); err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	if err := h.svc.UpdateInstance(ctx, auth.UserID(ctx), fid, iid, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, nil)
}

func familyID(c *app.RequestContext) (uint64, error) {
	return strconv.ParseUint(string(c.GetHeader("X-Family-Id")), 10, 64)
}

func scheduleIDParam(c *app.RequestContext) (uint64, error) {
	return strconv.ParseUint(c.Param("sid"), 10, 64)
}

func instanceIDParam(c *app.RequestContext) (uint64, error) {
	return strconv.ParseUint(c.Param("iid"), 10, 64)
}

// parseRange 解析 ?start=&end=（YYYY-MM-DD）；缺省回落到今天（今日视图）。
func parseRange(c *app.RequestContext) (time.Time, time.Time, error) {
	today := dateOnly(time.Now().UTC())
	start, end := today, today
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
		end = t
	}
	return start, end, nil
}
