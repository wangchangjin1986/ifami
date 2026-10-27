// Package auth 提供会话存储与鉴权中间件。
// 会话 token 存 Redis（7 天滑动过期，可丢，丢失仅需重新登录），
// 鉴权规则见 wiki/conventions/api.md。
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/redis/go-redis/v9"

	"ifami/internal/pkg/httpx"
)

const (
	sessionTTL  = 7 * 24 * time.Hour
	keyPrefix   = "session:"
	channelMini = "miniapp"
)

// SessionStore 会话存储接口。
type SessionStore interface {
	Create(ctx context.Context, token string, userID uint64) error
	UserID(ctx context.Context, token string) (uint64, bool, error)
}

// RedisStore 基于 Redis 的会话存储。
type RedisStore struct {
	rdb *redis.Client
}

func NewRedisStore(rdb *redis.Client) *RedisStore { return &RedisStore{rdb: rdb} }

func (s *RedisStore) Create(ctx context.Context, token string, userID uint64) error {
	return s.rdb.Set(ctx, keyPrefix+token, userID, sessionTTL).Err()
}

func (s *RedisStore) UserID(ctx context.Context, token string) (uint64, bool, error) {
	v, err := s.rdb.Get(ctx, keyPrefix+token).Uint64()
	if err == redis.Nil {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return v, true, nil
}

// NewToken 生成随机会话 token。
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

type ctxKey struct{ name string }

var userIDKey = ctxKey{"auth.user_id"}

// BearerAuth 校验 Authorization: Bearer <token>，把 user_id 放入 ctx。
func BearerAuth(store SessionStore) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		token, ok := strings.CutPrefix(string(c.GetHeader("Authorization")), "Bearer ")
		if !ok || token == "" {
			httpx.Fail(c, httpx.ErrUnauthorized)
			c.Abort()
			return
		}
		uid, found, err := store.UserID(ctx, token)
		if err != nil {
			httpx.Fail(c, httpx.ErrInternal)
			c.Abort()
			return
		}
		if !found {
			httpx.Fail(c, httpx.ErrUnauthorized)
			c.Abort()
			return
		}
		c.Next(context.WithValue(ctx, userIDKey, uid))
	}
}

// UserID 从 ctx 取当前登录用户（需在 BearerAuth 之后）。
func UserID(ctx context.Context) uint64 {
	v, _ := ctx.Value(userIDKey).(uint64)
	return v
}
