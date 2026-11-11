// Package auth 提供会话存储与鉴权中间件。
// 会话 token 采用进程内缓存 + Redis + MySQL 兜底（7 天过期），
// 鉴权规则见 wiki/conventions/api.md。
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"ifami/internal/pkg/httpx"
	"ifami/internal/pkg/logx"
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

type cacheEntry struct {
	UserID    uint64
	ExpiresAt time.Time
}

type localSessionCache struct {
	mu   sync.RWMutex
	data map[string]cacheEntry
}

func newLocalSessionCache() *localSessionCache {
	return &localSessionCache{data: map[string]cacheEntry{}}
}

func (c *localSessionCache) Set(token string, userID uint64, expiresAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[token] = cacheEntry{UserID: userID, ExpiresAt: expiresAt}
}

func (c *localSessionCache) Get(token string, now time.Time) (uint64, bool) {
	c.mu.RLock()
	entry, ok := c.data[token]
	c.mu.RUnlock()
	if !ok {
		return 0, false
	}
	if !entry.ExpiresAt.After(now) {
		c.mu.Lock()
		delete(c.data, token)
		c.mu.Unlock()
		return 0, false
	}
	return entry.UserID, true
}

// Session 是 MySQL 兜底会话表。Redis 不可用时登录与鉴权仍可工作。
type Session struct {
	Token     string    `gorm:"primaryKey;size:128"`
	UserID    uint64    `gorm:"not null;index:idx_user_expires"`
	ExpiresAt time.Time `gorm:"not null;index:idx_expires_at;index:idx_user_expires"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

func (Session) TableName() string { return "auth_sessions" }

// HybridStore 基于「本地缓存 → Redis → MySQL」的多级会话存储。
// Redis 是弱依赖：写 Redis 失败不影响登录；读 Redis 失败自动降级查 MySQL。
type HybridStore struct {
	local *localSessionCache
	rdb   *redis.Client
	db    *gorm.DB
	ttl   time.Duration
}

func NewHybridStore(db *gorm.DB, rdb *redis.Client) *HybridStore {
	return &HybridStore{local: newLocalSessionCache(), rdb: rdb, db: db, ttl: sessionTTL}
}

// NewRedisStore 保留旧构造函数兼容测试/本地用法；没有 DB 时只使用本地缓存 + Redis。
func NewRedisStore(rdb *redis.Client) *HybridStore { return NewHybridStore(nil, rdb) }

func (s *HybridStore) Create(ctx context.Context, token string, userID uint64) error {
	expiresAt := time.Now().UTC().Add(s.ttl)
	s.local.Set(token, userID, expiresAt)
	s.setRedisBestEffort(ctx, token, userID, expiresAt)
	if s.db == nil {
		return nil
	}
	row := &Session{Token: token, UserID: userID, ExpiresAt: expiresAt}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "token"}},
		DoUpdates: clause.Assignments(map[string]any{
			"user_id":    userID,
			"expires_at": expiresAt,
			"updated_at": time.Now().UTC(),
		}),
	}).Create(row).Error
}

func (s *HybridStore) UserID(ctx context.Context, token string) (uint64, bool, error) {
	now := time.Now().UTC()
	if uid, ok := s.local.Get(token, now); ok {
		return uid, true, nil
	}
	if s.rdb != nil {
		v, err := s.rdb.Get(ctx, keyPrefix+token).Uint64()
		if err == nil {
			s.local.Set(token, v, now.Add(s.ttl))
			return v, true, nil
		}
		if err != redis.Nil {
			logx.Warnf("读取 Redis 会话失败，降级查询数据库: %v", err)
		}
	}
	if s.db == nil {
		return 0, false, nil
	}
	var row Session
	err := s.db.WithContext(ctx).
		Where("token = ? AND expires_at > ?", token, now).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	s.local.Set(token, row.UserID, row.ExpiresAt)
	s.setRedisBestEffort(ctx, token, row.UserID, row.ExpiresAt)
	return row.UserID, true, nil
}

func (s *HybridStore) setRedisBestEffort(ctx context.Context, token string, userID uint64, expiresAt time.Time) {
	if s.rdb == nil {
		return
	}
	ttl := time.Until(expiresAt)
	if ttl <= 0 {
		return
	}
	if err := s.rdb.Set(ctx, keyPrefix+token, strconv.FormatUint(userID, 10), ttl).Err(); err != nil {
		logx.Warnf("写入 Redis 会话失败，已使用本地缓存和数据库兜底: %v", err)
	}
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
