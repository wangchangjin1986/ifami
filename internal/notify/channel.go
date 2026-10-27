package notify

import (
	"context"
	"log"
)

// Channel 是触达通道抽象（ADR-002）。Phase 0 纯个人主体只实现 wechat_subscribe，
// wechat_template / sms / app_push 留空实现，升个体户后补齐即恢复、结构零改动。
type Channel interface {
	Name() string
	Send(ctx context.Context, openid string, job *NotifyJob) error
}

// MockSubscribe 订阅消息通道的 Phase 0 mock 实现。
// 真实发送待 ICP 备案 + 订阅模板申请后接入微信 subscribe.send
// （需 access_token + openid + template_id + 一次性订阅额度）。
type MockSubscribe struct{}

func (MockSubscribe) Name() string { return ChannelSubscribe }

func (MockSubscribe) Send(_ context.Context, openid string, job *NotifyJob) error {
	log.Printf("[notify] mock send channel=%s openid=%s job=%d level=%s payload=%v",
		ChannelSubscribe, openid, job.JobID, job.Level, job.Payload)
	return nil
}
