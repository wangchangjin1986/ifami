// Package wechat 封装微信小程序服务端接口。
package wechat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

const code2SessionURL = "https://api.weixin.qq.com/sns/jscode2session"
const defaultTimeout = 5 * time.Second

// MiniClient 小程序服务端客户端。
type MiniClient struct {
	appid   string
	secret  string
	http    *http.Client
	baseURL string
}

func NewMiniClient(appid, secret string) *MiniClient {
	// 默认走微信正式端点；本地联调/测试可用 WX_CODE2SESSION_URL 覆盖到 mock。
	base := code2SessionURL
	if v := os.Getenv("WX_CODE2SESSION_URL"); v != "" {
		base = v
	}
	return &MiniClient{appid: appid, secret: secret, http: &http.Client{Timeout: envTimeout()}, baseURL: base}
}

func envTimeout() time.Duration {
	v, err := strconv.Atoi(os.Getenv("WX_HTTP_TIMEOUT_SECONDS"))
	if err != nil || v <= 0 {
		return defaultTimeout
	}
	return time.Duration(v) * time.Second
}

// Session 是 code2session 的返回。Phase 0 只依赖 OpenID；
// UnionID 在未绑定开放平台时为空，升个体户后才有。
type Session struct {
	OpenID     string `json:"openid"`
	SessionKey string `json:"session_key"`
	UnionID    string `json:"unionid"`
	ErrCode    int    `json:"errcode"`
	ErrMsg     string `json:"errmsg"`
}

// Code2Session 用小程序登录 code 换取 openid。
func (c *MiniClient) Code2Session(ctx context.Context, code string) (*Session, error) {
	u := fmt.Sprintf("%s?appid=%s&secret=%s&js_code=%s&grant_type=authorization_code",
		c.baseURL,
		url.QueryEscape(c.appid),
		url.QueryEscape(c.secret),
		url.QueryEscape(code),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, c.safeRequestError(err)
	}
	defer resp.Body.Close()

	var s Session
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return nil, err
	}
	if s.ErrCode != 0 {
		return nil, fmt.Errorf("code2session 失败: errcode=%d errmsg=%s", s.ErrCode, s.ErrMsg)
	}
	return &s, nil
}

func (c *MiniClient) safeRequestError(err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		// net/url.Error 默认会把完整请求 URL 放进 Error()，而 code2session URL
		// 包含 app secret 和一次性 js_code；这里只保留端点与底层网络错误。
		return fmt.Errorf("code2session 请求失败: %s %s: %w", uerr.Op, c.baseURL, uerr.Err)
	}
	return fmt.Errorf("code2session 请求失败: %w", err)
}
