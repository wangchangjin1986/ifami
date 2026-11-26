package notify

import (
	"context"
	"fmt"
	"strings"
	"time"

	"ifami/internal/pkg/logx"
	"ifami/internal/pkg/wechat"
)

// Channel 是触达通道抽象（ADR-002）。Phase 0 纯个人主体只实现 wechat_subscribe，
// wechat_template / sms / app_push 留空实现，升个体户后补齐即恢复、结构零改动。
type Channel interface {
	Name() string
	Send(ctx context.Context, openid string, job *NotifyJob) error
}

type TemplateProvider interface {
	SubscribeTemplateIDs() []string
}

// MockSubscribe 订阅消息通道的 Phase 0 mock 实现。
// 真实发送待 ICP 备案 + 订阅模板申请后接入微信 subscribe.send
// （需 access_token + openid + template_id + 一次性订阅额度）。
type MockSubscribe struct{}

func (MockSubscribe) Name() string { return ChannelSubscribe }

func (MockSubscribe) SubscribeTemplateIDs() []string { return nil }

func (MockSubscribe) Send(ctx context.Context, _ string, job *NotifyJob) error {
	logx.Info(ctx, "notification_mocked", "channel", ChannelSubscribe, "job_type", "notify", "job_id", job.JobID)
	return nil
}

type SubscribeTemplates struct {
	Schedule string
	Task     string
	Feedback string
}

// WechatSubscribe 发送真实小程序一次性订阅消息。
type WechatSubscribe struct {
	mini      *wechat.MiniClient
	templates SubscribeTemplates
}

func NewWechatSubscribe(mini *wechat.MiniClient, templates SubscribeTemplates) *WechatSubscribe {
	return &WechatSubscribe{mini: mini, templates: templates}
}

func (WechatSubscribe) Name() string { return ChannelSubscribe }

func (c *WechatSubscribe) SubscribeTemplateIDs() []string {
	ids := make([]string, 0, 3)
	seen := map[string]bool{}
	for _, id := range []string{c.templates.Schedule, c.templates.Task, c.templates.Feedback} {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

func (c *WechatSubscribe) Send(ctx context.Context, openid string, job *NotifyJob) error {
	tpl := c.templateID(job)
	if tpl == "" {
		return fmt.Errorf("订阅消息模板未配置: type=%s", payloadString(job.Payload, "type"))
	}
	page := payloadString(job.Payload, "page")
	if page == "" {
		page = fallbackPage(job)
	}
	sep := "?"
	if strings.Contains(page, "?") {
		sep = "&"
	}
	page += sep + "job_id=" + fmt.Sprint(job.JobID)
	return c.mini.SendSubscribeMessage(ctx, wechat.SubscribeMessage{
		ToUser:     openid,
		TemplateID: tpl,
		Page:       page,
		Data:       subscribeData(job),
	})
}

func (c *WechatSubscribe) templateID(job *NotifyJob) string {
	switch payloadString(job.Payload, "type") {
	case "checkin_reminder":
		return c.templates.Task
	case "schedule_feedback", "backup_notification":
		return c.templates.Feedback
	default:
		return c.templates.Schedule
	}
}

func subscribeData(job *NotifyJob) map[string]wechat.SubscribeDataItem {
	title := firstNonEmpty(payloadString(job.Payload, "title"), payloadString(job.Payload, "task"), "家庭提醒")
	timeText := firstNonEmpty(payloadString(job.Payload, "time_text"), payloadString(job.Payload, "start_at"))
	if timeText == "" {
		// 泛化通知不带原日程时间；模板 TIME 字段用通知创建时间（零值用处理时间）。
		created := job.CreatedAt
		if created.IsZero() {
			created = time.Now()
		}
		timeText = created.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("15:04")
	}
	thing := firstNonEmpty(payloadString(job.Payload, "location"), payloadString(job.Payload, "reason"), "点击查看详情")
	return map[string]wechat.SubscribeDataItem{
		"thing1": {Value: truncate(title, 20)},
		"time2":  {Value: truncate(timeText, 20)},
		"thing3": {Value: truncate(thing, 20)},
	}
}

func fallbackPage(job *NotifyJob) string {
	if id := payloadUint(job.Payload, "instance_id"); id != 0 {
		return "pages/event-detail/index?instance_id=" + fmt.Sprint(id)
	}
	if id := payloadUint(job.Payload, "task_id"); id != 0 {
		return "pages/daka/index"
	}
	return "pages/today/index"
}

func payloadString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	switch v := m[key].(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		if v == nil {
			return ""
		}
		return fmt.Sprint(v)
	}
}

func payloadUint(m map[string]any, key string) uint64 {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case uint64:
		return v
	case uint:
		return uint64(v)
	case int:
		if v > 0 {
			return uint64(v)
		}
	case int64:
		if v > 0 {
			return uint64(v)
		}
	case float64:
		if v > 0 {
			return uint64(v)
		}
	}
	return 0
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
