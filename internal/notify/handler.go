package notify

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"

	"ifami/internal/pkg/auth"
	"ifami/internal/pkg/httpx"
)

// Handler 是 notify 模块的 HTTP 层。
type Handler struct {
	svc      *Service
	sessions auth.SessionStore
}

func NewHandler(svc *Service, sessions auth.SessionStore) *Handler {
	return &Handler{svc: svc, sessions: sessions}
}

// Register 注册路由。回执上报无鉴权门槛（祖辈点开即算，api.md）；
// 创建任务走 Bearer 鉴权（联调/跨进程调用入口；业务模块内部走 service.CreateJob）。
func (h *Handler) Register(g *route.RouterGroup) {
	g.POST("/notify/receipt", h.ReportReceipt)
	authed := g.Group("", auth.BearerAuth(h.sessions))
	authed.POST("/notify/jobs", h.CreateJob)
}

func (h *Handler) ReportReceipt(ctx context.Context, c *app.RequestContext) {
	var req struct {
		JobID uint64 `json:"job_id"`
	}
	if err := c.BindJSON(&req); err != nil || req.JobID == 0 {
		httpx.Fail(c, ErrBadParam)
		return
	}
	if err := h.svc.ReportReceipt(ctx, req.JobID); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, nil)
}

func (h *Handler) CreateJob(ctx context.Context, c *app.RequestContext) {
	var req CreateJobInput
	if err := c.BindJSON(&req); err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	jobID, err := h.svc.CreateJob(ctx, &req)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, map[string]any{"job_id": jobID})
}
