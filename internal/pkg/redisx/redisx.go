// Package redisx 提供 go-redis 客户端。
package redisx

import "github.com/redis/go-redis/v9"

// New 建立 Redis 连接（Phase 0 为容器 Redis，纯缓存/会话，可丢）。
func New(addr string) *redis.Client {
	return redis.NewClient(&redis.Options{Addr: addr})
}
