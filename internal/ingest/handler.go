package ingest

import (
	"context"
	"io"
	"strconv"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"

	"ifami/internal/pkg/auth"
	"ifami/internal/pkg/httpx"
)

// Handler 是 ingest 模块的 HTTP 层。家庭上下文走 Header X-Family-Id（api.md）。
type Handler struct {
	svc      *Service
	sessions auth.SessionStore
}

func NewHandler(svc *Service, sessions auth.SessionStore) *Handler {
	return &Handler{svc: svc, sessions: sessions}
}

// Register 注册路由（技术方案 §5.5.4）。
func (h *Handler) Register(g *route.RouterGroup) {
	authed := g.Group("", auth.BearerAuth(h.sessions))
	authed.POST("/ingest/jobs", h.CreateJob)
	authed.GET("/ingest/jobs", h.ListJobs)
	authed.GET("/ingest/jobs/:id", h.GetJob)
	authed.POST("/ingest/jobs/:id/images", h.UploadImage)
	authed.POST("/ingest/jobs/:id/start", h.StartJob)
	authed.POST("/ingest/jobs/:id/confirm", h.Confirm)
}

func (h *Handler) CreateJob(ctx context.Context, c *app.RequestContext) {
	fid, err := familyID(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	id, err := h.svc.CreateJob(ctx, auth.UserID(ctx), fid)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, map[string]any{"job_id": id})
}

func (h *Handler) ListJobs(ctx context.Context, c *app.RequestContext) {
	fid, err := familyID(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	views, err := h.svc.ListJobs(ctx, auth.UserID(ctx), fid, c.Query("status"))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, views)
}

func (h *Handler) GetJob(ctx context.Context, c *app.RequestContext) {
	fid, jid, ok := ids(c)
	if !ok {
		return
	}
	v, err := h.svc.GetJob(ctx, auth.UserID(ctx), fid, jid)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, v)
}

// UploadImage multipart：file（图片）、seq（0–8）、child_member_id（0=自动识别）。
func (h *Handler) UploadImage(ctx context.Context, c *app.RequestContext) {
	fid, jid, ok := ids(c)
	if !ok {
		return
	}
	seq, err := strconv.Atoi(string(c.FormValue("seq")))
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	var child uint64
	if v := string(c.FormValue("child_member_id")); v != "" {
		if child, err = strconv.ParseUint(v, 10, 64); err != nil {
			httpx.Fail(c, ErrBadParam)
			return
		}
	}
	fh, err := c.FormFile("file")
	if err != nil {
		httpx.Fail(c, ErrBadImage)
		return
	}
	if fh.Size > MaxImageBytes {
		httpx.Fail(c, ErrBadImage)
		return
	}
	f, err := fh.Open()
	if err != nil {
		httpx.Fail(c, ErrBadImage)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxImageBytes+1))
	if err != nil {
		httpx.Fail(c, ErrBadImage)
		return
	}
	if err := h.svc.UploadImage(ctx, auth.UserID(ctx), fid, jid, seq, child, data); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, nil)
}

func (h *Handler) StartJob(ctx context.Context, c *app.RequestContext) {
	fid, jid, ok := ids(c)
	if !ok {
		return
	}
	if err := h.svc.StartJob(ctx, auth.UserID(ctx), fid, jid); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, nil)
}

func (h *Handler) Confirm(ctx context.Context, c *app.RequestContext) {
	fid, jid, ok := ids(c)
	if !ok {
		return
	}
	var req struct {
		Schedules []ScheduleItem `json:"schedules"`
		Force     bool           `json:"force"`
	}
	if err := c.BindJSON(&req); err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	sids, err := h.svc.Confirm(ctx, auth.UserID(ctx), fid, jid, req.Schedules, req.Force)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, map[string]any{"schedule_ids": sids})
}

func familyID(c *app.RequestContext) (uint64, error) {
	return strconv.ParseUint(string(c.GetHeader("X-Family-Id")), 10, 64)
}

func ids(c *app.RequestContext) (uint64, uint64, bool) {
	fid, err := familyID(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return 0, 0, false
	}
	jid, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return 0, 0, false
	}
	return fid, jid, true
}
