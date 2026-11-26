// Package wechat 封装微信小程序服务端接口。
package wechat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"ifami/internal/pkg/logx"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"
)

const code2SessionURL = "https://api.weixin.qq.com/sns/jscode2session"
const accessTokenURL = "https://api.weixin.qq.com/cgi-bin/token"
const subscribeSendURL = "https://api.weixin.qq.com/cgi-bin/message/subscribe/send"
const imgSecCheckURL = "https://api.weixin.qq.com/wxa/img_sec_check"
const defaultTimeout = 5 * time.Second

// MiniClient 小程序服务端客户端。
type MiniClient struct {
	appid   string
	secret  string
	http    *http.Client
	baseURL string

	accessTokenURL   string
	subscribeSendURL string
	imgSecCheckURL   string
	mu               sync.Mutex
	accessToken      string
	accessTokenExp   time.Time
}

func NewMiniClient(appid, secret string) *MiniClient {
	// 默认走微信正式端点；本地联调/测试可用 WX_CODE2SESSION_URL 覆盖到 mock。
	base := code2SessionURL
	if v := os.Getenv("WX_CODE2SESSION_URL"); v != "" {
		base = v
	}
	tokenURL := accessTokenURL
	if v := os.Getenv("WX_ACCESS_TOKEN_URL"); v != "" {
		tokenURL = v
	}
	sendURL := subscribeSendURL
	if v := os.Getenv("WX_SUBSCRIBE_SEND_URL"); v != "" {
		sendURL = v
	}
	secURL := imgSecCheckURL
	if v := os.Getenv("WX_IMG_SEC_CHECK_URL"); v != "" {
		secURL = v
	}
	return &MiniClient{appid: appid, secret: secret, http: &http.Client{Timeout: envTimeout()}, baseURL: base, accessTokenURL: tokenURL, subscribeSendURL: sendURL, imgSecCheckURL: secURL}
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
func (c *MiniClient) Code2Session(ctx context.Context, code string) (result *Session, err error) {
	done := logx.Operation(ctx, "wechat.code2session", time.Second)
	defer func() { done(err) }()
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
		return nil, c.safeRequestError("code2session", c.baseURL, err)
	}
	defer resp.Body.Close()
	logx.RemoteResponse(ctx, "wechat.code2session", resp.StatusCode)

	var s Session
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return nil, err
	}
	if s.ErrCode != 0 {
		return nil, logx.CodeError("wechat", s.ErrCode, fmt.Errorf("code2session 失败: errcode=%d errmsg=%s", s.ErrCode, s.ErrMsg))
	}
	return &s, nil
}

type AccessToken struct {
	Token     string `json:"access_token"`
	ExpiresIn int    `json:"expires_in"`
	ErrCode   int    `json:"errcode"`
	ErrMsg    string `json:"errmsg"`
}

type SubscribeMessage struct {
	ToUser     string                       `json:"touser"`
	TemplateID string                       `json:"template_id"`
	Page       string                       `json:"page,omitempty"`
	Data       map[string]SubscribeDataItem `json:"data"`
	MiniState  string                       `json:"miniprogram_state,omitempty"`
}

type SubscribeDataItem struct {
	Value string `json:"value"`
}

// SendSubscribeMessage 发送小程序一次性订阅消息。
func (c *MiniClient) SendSubscribeMessage(ctx context.Context, msg SubscribeMessage) (err error) {
	done := logx.Operation(ctx, "wechat.subscribe_send", time.Second)
	defer func() { done(err) }()
	if c.appid == "" || c.secret == "" {
		return errors.New("微信小程序 appid/secret 未配置")
	}
	token, err := c.getAccessToken(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	u := c.subscribeSendURL + "?access_token=" + url.QueryEscape(token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return c.safeRequestError("subscribe.send", c.subscribeSendURL, err)
	}
	defer resp.Body.Close()
	logx.RemoteResponse(ctx, "wechat.subscribe_send", resp.StatusCode)
	b, _ := io.ReadAll(resp.Body)
	var out struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return err
	}
	if out.ErrCode != 0 {
		return logx.CodeError("wechat", out.ErrCode, fmt.Errorf("subscribe.send 失败: errcode=%d errmsg=%s", out.ErrCode, out.ErrMsg))
	}
	return nil
}

// CheckImageSafety 调用微信图片内容安全检测。失败或命中违规均返回错误，调用方不得入库存储原图。
func (c *MiniClient) CheckImageSafety(ctx context.Context, data []byte) (err error) {
	done := logx.Operation(ctx, "wechat.image_check", time.Second)
	defer func() { done(err) }()
	if c.appid == "" || c.secret == "" {
		return errors.New("微信小程序 appid/secret 未配置")
	}
	token, err := c.getAccessToken(ctx)
	if err != nil {
		return err
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("media", "upload.img")
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}
	u := c.imgSecCheckURL + "?access_token=" + url.QueryEscape(token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.http.Do(req)
	if err != nil {
		return c.safeRequestError("img_sec_check", c.imgSecCheckURL, err)
	}
	defer resp.Body.Close()
	logx.RemoteResponse(ctx, "wechat.image_check", resp.StatusCode)
	b, _ := io.ReadAll(resp.Body)
	var out struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return err
	}
	if out.ErrCode != 0 {
		return logx.CodeError("wechat", out.ErrCode, fmt.Errorf("img_sec_check 失败: errcode=%d errmsg=%s", out.ErrCode, out.ErrMsg))
	}
	return nil
}

func (c *MiniClient) getAccessToken(ctx context.Context) (result string, err error) {
	done := logx.Operation(ctx, "wechat.access_token", time.Second)
	defer func() { done(err) }()
	c.mu.Lock()
	if c.accessToken != "" && time.Now().Before(c.accessTokenExp.Add(-5*time.Minute)) {
		token := c.accessToken
		c.mu.Unlock()
		return token, nil
	}
	c.mu.Unlock()

	u := fmt.Sprintf("%s?grant_type=client_credential&appid=%s&secret=%s", c.accessTokenURL, url.QueryEscape(c.appid), url.QueryEscape(c.secret))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", c.safeRequestError("access_token", c.accessTokenURL, err)
	}
	defer resp.Body.Close()
	logx.RemoteResponse(ctx, "wechat.access_token", resp.StatusCode)
	var out AccessToken
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.ErrCode != 0 || out.Token == "" {
		return "", logx.CodeError("wechat", out.ErrCode, fmt.Errorf("access_token 获取失败: errcode=%d errmsg=%s", out.ErrCode, out.ErrMsg))
	}
	if out.ExpiresIn <= 0 {
		out.ExpiresIn = 7200
	}
	c.mu.Lock()
	c.accessToken = out.Token
	c.accessTokenExp = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	c.mu.Unlock()
	return out.Token, nil
}

func (c *MiniClient) safeRequestError(op, endpoint string, err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		// net/url.Error 默认会把完整请求 URL 放进 Error()，而 code2session URL
		// 包含 app secret 和一次性 js_code；这里只保留端点与底层网络错误。
		return fmt.Errorf("%s 请求失败: %s %s: %w", op, uerr.Op, endpoint, uerr.Err)
	}
	return fmt.Errorf("%s 请求失败: %w", op, err)
}
