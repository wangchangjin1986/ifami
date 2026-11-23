package identity

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"ifami/internal/pkg/invite"
)

func redisJoinTest(t *testing.T) *RedisJoinCodeRegistry {
	t.Helper()
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR required")
	}
	client := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: 0})
	t.Cleanup(func() { _ = client.Close() })
	prefix := fmt.Sprintf("test_%d:", time.Now().UnixNano())
	r := NewRedisJoinCodeRegistry(client, newTestFieldCipher(t), "test-secret", prefix)
	t.Cleanup(func() {
		ctx := context.Background()
		keys, e := client.Keys(ctx, prefix+"*").Result()
		if e == nil && len(keys) > 0 {
			_ = client.Del(ctx, keys...).Err()
		}
	})
	if e := client.Ping(context.Background()).Err(); e != nil {
		t.Fatal(e)
	}
	return r
}
func registryInput(t *testing.T, fid uint64, code, gen string, now time.Time) *JoinRegistration {
	t.Helper()
	expires := now.Add(invite.JoinTTL)
	token, e := invite.SignJoin("test-secret", fid, expires)
	if e != nil {
		t.Fatal(e)
	}
	return &JoinRegistration{fid, 1, code, strings.Repeat(gen, 32), token, expires}
}
func TestRedisJoinLifecycle(t *testing.T) {
	r := redisJoinTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	a := registryInput(t, 1, "000001", "a", now)
	got, e := r.Rotate(ctx, a, "op1", now)
	if e != nil || got.JoinCode != a.JoinCode {
		t.Fatalf("generate: %+v %v", got, e)
	}
	if ttl := r.client.TTL(ctx, r.family(1)).Val(); ttl > 24*time.Hour || ttl < 24*time.Hour-time.Second*3 {
		t.Fatal("TTL", ttl)
	}
	stored := r.client.HGet(ctx, r.family(1), "data").Val()
	if strings.Contains(stored, a.Credential) || strings.Contains(stored, a.JoinCode) {
		t.Fatal("plaintext registry")
	}
	got, e = r.Current(ctx, 1, now)
	if e != nil || got.GenerationID != a.GenerationID {
		t.Fatal("readback", e)
	}
	if _, e = r.Rotate(ctx, registryInput(t, 2, "000001", "b", now), "other", now); e != errJoinCollision {
		t.Fatal("collision", e)
	}
	b := registryInput(t, 1, "000002", "b", now)
	got, e = r.Rotate(ctx, b, "op2", now)
	if e != nil || got.GenerationID != b.GenerationID {
		t.Fatal("rotate", e)
	}
	if got, e = r.Resolve(ctx, a.JoinCode, now); e != nil || got != nil {
		t.Fatal("old digit", e)
	}
	got, e = r.Rotate(ctx, a, "op1", now)
	if e != nil || got.GenerationID != b.GenerationID {
		t.Fatal("stale operation restored generation", e)
	}
	if e = r.Revoke(ctx, 1, a.GenerationID, now); e != nil {
		t.Fatal(e)
	}
	got, _ = r.Current(ctx, 1, now)
	if got == nil || got.GenerationID != b.GenerationID {
		t.Fatal("stale revoke removed new")
	}
	got, e = r.Current(ctx, 1, b.ExpiresAt)
	if e != nil || got != nil {
		t.Fatal("expiry boundary", e)
	}
	got, e = r.Resolve(ctx, b.JoinCode, b.ExpiresAt)
	if e != nil || got != nil {
		t.Fatal("index expiry", e)
	}
	for range 2 {
		if e = r.Revoke(ctx, 1, b.GenerationID, now); e != nil {
			t.Fatal(e)
		}
	}
	got, e = r.Rotate(ctx, b, "op2", now)
	if e != nil || got != nil {
		t.Fatal("replayed operation after revoke regenerated", e)
	}
	got, e = r.Resolve(ctx, b.JoinCode, now)
	if e != nil || got != nil {
		t.Fatal("revoked digit", e)
	}
	// Redis record loss cannot fall back to a still-valid signature.
	c := registryInput(t, 3, "000003", "c", now)
	_, e = r.Rotate(ctx, c, "op3", now)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.client.Del(ctx, r.family(3)).Err(); e != nil {
		t.Fatal(e)
	}
	got, e = r.Resolve(ctx, c.JoinCode, now)
	if e != nil || got != nil {
		t.Fatal("lost registration", e)
	}
}
func TestRedisJoinConcurrentOperations(t *testing.T) {
	r := redisJoinTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	var wg sync.WaitGroup
	out := make([]*JoinRegistration, 12)
	errs := make([]error, 12)
	for i := range out {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out[i], errs[i] = r.Rotate(ctx, registryInput(t, 1, fmt.Sprintf("%06d", i), "a", now), "same-op", now)
		}(i)
	}
	wg.Wait()
	for i := range out {
		if errs[i] != nil || out[i] == nil || out[i].JoinCode != out[0].JoinCode {
			t.Fatalf("replay %d %v", i, errs[i])
		}
	}
	// Different family attempts for one candidate have exactly one winner.
	wins := 0
	wg = sync.WaitGroup{}
	errs = make([]error, 12)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = r.Rotate(ctx, registryInput(t, uint64(i+100), "999999", "b", now), "new", now)
		}(i)
	}
	wg.Wait()
	for _, e := range errs {
		if e == nil {
			wins++
		} else if e != errJoinCollision {
			t.Fatal(e)
		}
	}
	if wins != 1 {
		t.Fatal("collision winners", wins)
	}
}
func TestRedisJoinSharedFailureGuardAndOutage(t *testing.T) {
	r := redisJoinTest(t)
	ctx := context.Background()
	for range 10 {
		reservation, e := r.Reserve(ctx, 9, "ip")
		if e != nil {
			t.Fatal(e)
		}
		if e := r.Finish(ctx, reservation, true); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := r.Reserve(ctx, 9, "another-ip"); e != ErrJoinLimited {
		t.Fatal("user limit", e)
	}
	for i := 0; i < 20; i++ {
		reservation, e := r.Reserve(ctx, uint64(100+i), "ip")
		if e != nil {
			t.Fatal(e)
		}
		if e := r.Finish(ctx, reservation, true); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := r.Reserve(ctx, 999, "ip"); e != ErrJoinLimited {
		t.Fatal("ip limit", e)
	}
	if ttl := r.client.TTL(ctx, r.guardKeys(9, "ip")[0]).Val(); ttl > 15*time.Minute || ttl < 14*time.Minute {
		t.Fatal("guard TTL", ttl)
	}
	_ = r.client.Close()
	if _, e := r.Current(ctx, 1, time.Now()); e != ErrJoinUnavailable {
		t.Fatal("offline read", e)
	}
	if _, e := r.Resolve(ctx, "000001", time.Now()); e != ErrJoinUnavailable {
		t.Fatal("offline lookup", e)
	}
	if e := r.Revoke(ctx, 1, strings.Repeat("a", 32), time.Now()); e != ErrJoinUnavailable {
		t.Fatal("offline revoke", e)
	}
	if _, e := r.Reserve(ctx, 1, "ip"); e != ErrJoinUnavailable {
		t.Fatal("offline guard", e)
	}
}
func TestRedisJoinProtectionOptions(t *testing.T) {
	r := redisJoinTest(t)
	configured := NewRedisJoinCodeRegistry(r.client, r.field, r.secret, r.prefix+"configured:", JoinProtectionOptions{UserLimit: 2, IPLimit: 3, Window: time.Minute})
	ctx := context.Background()
	for range 2 {
		reservation, e := configured.Reserve(ctx, 1, "ip")
		if e != nil {
			t.Fatal(e)
		}
		if e := configured.Finish(ctx, reservation, true); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := configured.Reserve(ctx, 1, "fresh-ip"); e != ErrJoinLimited {
		t.Fatal("configured user", e)
	}
	reservation, e := configured.Reserve(ctx, 2, "ip")
	if e != nil {
		t.Fatal(e)
	}
	if e := configured.Finish(ctx, reservation, true); e != nil {
		t.Fatal(e)
	}
	if _, e := configured.Reserve(ctx, 3, "ip"); e != ErrJoinLimited {
		t.Fatal("configured ip", e)
	}
	if ttl := r.client.TTL(ctx, configured.guardKeys(1, "ip")[0]).Val(); ttl > time.Minute || ttl < 50*time.Second {
		t.Fatal("configured window", ttl)
	}
}
func TestRedisJoinLostIndexIsUnavailableCode(t *testing.T) {
	r := redisJoinTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	input := registryInput(t, 1, "123456", "a", now)
	if _, e := r.Rotate(ctx, input, "original", now); e != nil {
		t.Fatal(e)
	}
	index := r.client.HGet(ctx, r.family(1), "index").Val()
	if e := r.client.Del(ctx, index).Err(); e != nil {
		t.Fatal(e)
	}
	if out, e := r.Current(ctx, 1, now); e != nil || out != nil {
		t.Fatalf("lost index advertised active code; err=%v", e)
	}
	if out, e := r.Rotate(ctx, registryInput(t, 1, "654321", "b", now), "original", now); e != nil || out != nil {
		t.Fatalf("retry after index loss; err=%v", e)
	}
}
