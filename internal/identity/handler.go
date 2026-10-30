package identity

import (
	"context"
	"strconv"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"

	"ifami/internal/pkg/auth"
	"ifami/internal/pkg/httpx"
)

// Handler 是 identity 模块的 HTTP 层。
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register 注册路由。/auth/login 与 /families/join 无需登录，其余走 Bearer 鉴权。
func (h *Handler) Register(g *route.RouterGroup) {
	g.POST("/auth/login", h.Login)
	g.POST("/families/join", h.JoinFamily)

	authed := g.Group("", auth.BearerAuth(h.svc.sessions))
	authed.POST("/families", h.CreateFamily)
	authed.GET("/families", h.ListFamilies)
	authed.GET("/me", h.Me)
	authed.POST("/families/:fid/invitations", h.CreateInvitation)
	authed.GET("/families/:fid/members", h.ListMembers)
	authed.POST("/families/:fid/children", h.AddChild)
	authed.PATCH("/families/:fid/members/:mid", h.UpdateMember)
	authed.DELETE("/families/:fid/members/:mid", h.RemoveMember)
}

func (h *Handler) Login(ctx context.Context, c *app.RequestContext) {
	var req struct {
		Code string `json:"code"`
	}
	if err := c.BindJSON(&req); err != nil || req.Code == "" {
		httpx.Fail(c, ErrBadParam)
		return
	}
	res, err := h.svc.Login(ctx, req.Code)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, res)
}

func (h *Handler) JoinFamily(ctx context.Context, c *app.RequestContext) {
	var req struct {
		Code          string `json:"code"`
		InviteToken   string `json:"invite_token"`
		RelationLabel string `json:"relation_label"`
	}
	if err := c.BindJSON(&req); err != nil || req.Code == "" || req.InviteToken == "" {
		httpx.Fail(c, ErrBadParam)
		return
	}
	res, err := h.svc.JoinFamily(ctx, req.Code, req.InviteToken, req.RelationLabel)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, res)
}

func (h *Handler) CreateFamily(ctx context.Context, c *app.RequestContext) {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.BindJSON(&req); err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	familyID, memberID, err := h.svc.CreateFamily(ctx, auth.UserID(ctx), req.Name)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, map[string]any{"family_id": familyID, "member_id": memberID})
}

func (h *Handler) ListFamilies(ctx context.Context, c *app.RequestContext) {
	frs, err := h.svc.ListFamilies(ctx, auth.UserID(ctx))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, frs)
}

func (h *Handler) Me(ctx context.Context, c *app.RequestContext) {
	res, err := h.svc.Me(ctx, auth.UserID(ctx))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, res)
}

func (h *Handler) CreateInvitation(ctx context.Context, c *app.RequestContext) {
	fid, err := familyIDParam(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	token, err := h.svc.CreateInvitation(ctx, auth.UserID(ctx), fid)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, map[string]any{"invite_token": token})
}

func (h *Handler) ListMembers(ctx context.Context, c *app.RequestContext) {
	fid, err := familyIDParam(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	views, err := h.svc.ListMembers(ctx, auth.UserID(ctx), fid)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, views)
}

func (h *Handler) AddChild(ctx context.Context, c *app.RequestContext) {
	var req struct {
		Name          string `json:"name"`
		RelationLabel string `json:"relation_label"`
	}
	if err := c.BindJSON(&req); err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	fid, err := familyIDParam(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	memberID, err := h.svc.AddChild(ctx, auth.UserID(ctx), fid, req.Name, req.RelationLabel)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, map[string]any{"member_id": memberID})
}

// UpdateMember 管理员改成员：role、is_admin、relation_label（身份/名称）与 name（孩子档案名）均为可选，至少传一个。
func (h *Handler) UpdateMember(ctx context.Context, c *app.RequestContext) {
	var req struct {
		Role          *string `json:"role"`
		IsAdmin       *bool   `json:"is_admin"`
		RelationLabel *string `json:"relation_label"`
		Name          *string `json:"name"`
	}
	if err := c.BindJSON(&req); err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	fid, err := familyIDParam(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	mid, err := memberIDParam(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	if err := h.svc.UpdateMember(ctx, auth.UserID(ctx), fid, mid, req.Role, req.IsAdmin, req.RelationLabel, req.Name); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, nil)
}

func (h *Handler) RemoveMember(ctx context.Context, c *app.RequestContext) {
	fid, err := familyIDParam(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	mid, err := memberIDParam(c)
	if err != nil {
		httpx.Fail(c, ErrBadParam)
		return
	}
	if err := h.svc.RemoveMember(ctx, auth.UserID(ctx), fid, mid); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, nil)
}

func familyIDParam(c *app.RequestContext) (uint64, error) {
	return strconv.ParseUint(c.Param("fid"), 10, 64)
}

func memberIDParam(c *app.RequestContext) (uint64, error) {
	return strconv.ParseUint(c.Param("mid"), 10, 64)
}
