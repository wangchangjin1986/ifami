package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"strconv"

	"github.com/cloudwego/hertz/pkg/app"
	"ifami/internal/pkg/auth"
	"ifami/internal/pkg/httpx"
)

func bindJoin(c *app.RequestContext, out any) error {
	body := c.Request.Body()
	if len(body) == 0 || len(body) > 2048 {
		return ErrBadParam
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(out); e != nil {
		return ErrBadParam
	}
	var extra any
	if e := decoder.Decode(&extra); e != io.EOF {
		return ErrBadParam
	}
	return nil
}
func joinFamilyParam(c *app.RequestContext) (uint64, error) {
	fid, e := familyIDParam(c)
	if e != nil || fid == 0 {
		return 0, ErrBadParam
	}
	header, e := strconv.ParseUint(string(c.GetHeader("X-Family-Id")), 10, 64)
	if e != nil || header != fid {
		return 0, ErrBadParam
	}
	return fid, nil
}
func (h *Handler) GetJoinCode(ctx context.Context, c *app.RequestContext) {
	fid, e := joinFamilyParam(c)
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	out, e := h.svc.GetJoinCode(ctx, auth.UserID(ctx), fid)
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	httpx.OK(c, out)
}
func (h *Handler) GenerateJoinCode(ctx context.Context, c *app.RequestContext) {
	fid, e := joinFamilyParam(c)
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	out, e := h.svc.GenerateJoinCode(ctx, auth.UserID(ctx), fid, string(c.GetHeader("Idempotency-Key")))
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	httpx.OK(c, out)
}
func (h *Handler) RevokeJoinCode(ctx context.Context, c *app.RequestContext) {
	fid, e := joinFamilyParam(c)
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	var in struct {
		GenerationID string `json:"generation_id"`
	}
	if e = bindJoin(c, &in); e != nil {
		httpx.Fail(c, e)
		return
	}
	out, e := h.svc.RevokeJoinCode(ctx, auth.UserID(ctx), fid, in.GenerationID)
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	httpx.OK(c, out)
}
func (h *Handler) PreviewJoin(ctx context.Context, c *app.RequestContext) {
	var in JoinPreviewInput
	if e := bindJoin(c, &in); e != nil {
		httpx.Fail(c, e)
		return
	}
	c.SetClientIPFunc(joinClientIP)
	in.ClientIP = c.ClientIP()
	out, e := h.svc.PreviewJoin(ctx, auth.UserID(ctx), in)
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	httpx.OK(c, out)
}
func (h *Handler) ConfirmJoin(ctx context.Context, c *app.RequestContext) {
	var in JoinConfirmInput
	if e := bindJoin(c, &in); e != nil {
		httpx.Fail(c, e)
		return
	}
	c.SetClientIPFunc(joinClientIP)
	in.ClientIP = c.ClientIP()
	out, e := h.svc.ConfirmJoin(ctx, auth.UserID(ctx), in)
	if e != nil {
		httpx.Fail(c, e)
		return
	}
	httpx.OK(c, out)
}

// Only the deployed loopback Caddy proxy may supply forwarded IP headers.
var joinClientIP = app.ClientIPWithOption(app.ClientIPOptions{
	RemoteIPHeaders: []string{"X-Forwarded-For"},
	TrustedCIDRs: []*net.IPNet{
		{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(32, 32)},
		{IP: net.ParseIP("::1"), Mask: net.CIDRMask(128, 128)},
	},
})
