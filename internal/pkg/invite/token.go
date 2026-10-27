// Package invite 生成与校验家庭邀请 token（无状态 HMAC 签名，7 天有效）。
// token 格式：<family_id>.<expires_unix>.<sig>，sig = HMAC-SHA256(secret, "<family_id>.<expires_unix>")。
package invite

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalid = errors.New("邀请链接无效")
	ErrExpired = errors.New("邀请链接已过期")
)

// TTL 邀请链接有效期（对应需求 R1.2「邀请链接 7 天有效」）。
const TTL = 7 * 24 * time.Hour

func sign(secret, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Sign 生成邀请 token。
func Sign(secret string, familyID uint64, now time.Time) string {
	expires := now.Add(TTL)
	payload := strconv.FormatUint(familyID, 10) + "." + strconv.FormatInt(expires.Unix(), 10)
	return payload + "." + sign(secret, payload)
}

// Verify 校验 token，返回 family_id。
func Verify(secret, token string) (uint64, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return 0, ErrInvalid
	}
	payload := parts[0] + "." + parts[1]
	if !hmac.Equal([]byte(sign(secret, payload)), []byte(parts[2])) {
		return 0, ErrInvalid
	}
	expires, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, ErrInvalid
	}
	if time.Now().Unix() > expires {
		return 0, ErrExpired
	}
	familyID, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, ErrInvalid
	}
	return familyID, nil
}
