package identity

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"sync/atomic"
	"testing"

	"github.com/redis/go-redis/v9"
)

// Discard real Redis replies after reading them, so go-redis's production
// default retry loop reissues the command without a second application call.
type lostBudgetReply struct {
	script    string
	limit     int32
	armed     atomic.Bool
	dropped   atomic.Int32
	writes    atomic.Int32
	afterDrop func()
}
type lostBudgetReplyConn struct {
	net.Conn
	fault    *lostBudgetReply
	dropNext atomic.Bool
}

func (c *lostBudgetReplyConn) Write(body []byte) (int, error) {
	if c.fault.armed.Load() && bytes.Contains(body, []byte(c.fault.script)) {
		c.fault.writes.Add(1)
		if c.fault.dropped.Load() < c.fault.limit {
			c.dropNext.Store(true)
		}
	}
	return c.Conn.Write(body)
}
func (c *lostBudgetReplyConn) Read(body []byte) (int, error) {
	n, err := c.Conn.Read(body)
	if n > 0 && c.dropNext.CompareAndSwap(true, false) {
		if c.fault.dropped.Add(1) == 1 && c.fault.afterDrop != nil {
			c.fault.afterDrop()
		}
		return 0, io.EOF
	}
	return n, err
}
func retryingBudgetRegistry(t *testing.T, script string, drops int32) (*RedisJoinCodeRegistry, *lostBudgetReply) {
	t.Helper()
	r := redisJoinTest(t)
	original := r.client
	fault := &lostBudgetReply{script: script, limit: drops}
	client := redis.NewClient(&redis.Options{Addr: os.Getenv("TEST_REDIS_ADDR"), Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &lostBudgetReplyConn{Conn: conn, fault: fault}, nil
	}})
	t.Cleanup(func() { r.client = original; _ = client.Close() })
	r.client = client
	r.protection.UserLimit = 2
	r.protection.IPLimit = 2
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	return r, fault
}
func requireOneBudgetSlot(t *testing.T, r *RedisJoinCodeRegistry) {
	t.Helper()
	ctx := context.Background()
	for _, key := range r.guardKeys(1, "ip") {
		value, err := r.client.Get(ctx, key).Result()
		if err != nil || value != "1" {
			t.Errorf("budget after retry=%q err=%v, want one occupied slot", value, err)
		}
	}
}
func TestJoinProtectionFinishLostReplies(t *testing.T) {
	for _, drops := range []int32{1, 4} {
		name := "one"
		if drops == 4 {
			name = "all"
		}
		t.Run(name, func(t *testing.T) {
			r, fault := retryingBudgetRegistry(t, finishJoinLua, drops)
			ctx := context.Background()
			success, err := r.Reserve(ctx, 1, "ip")
			if err != nil {
				t.Fatal(err)
			}
			failed, err := r.Reserve(ctx, 1, "ip")
			if err != nil {
				t.Fatal(err)
			}
			if err = r.Finish(ctx, failed, true); err != nil {
				t.Fatal(err)
			}
			fault.armed.Store(true)
			err = r.Finish(ctx, success, false)
			if drops == 1 && err != nil {
				t.Fatal(err)
			}
			if drops == 4 && err != ErrJoinUnavailable {
				t.Fatalf("unknown completion must fail closed: %v", err)
			}
			if fault.dropped.Load() != drops {
				t.Fatalf("injected %d replies, want %d", fault.dropped.Load(), drops)
			}
			t.Logf("one Finish call, %d actual Eval writes, %d real replies discarded", fault.writes.Load(), fault.dropped.Load())
			requireOneBudgetSlot(t, r)
			fault.armed.Store(false)
			next, err := r.Reserve(ctx, 1, "ip")
			if err != nil {
				t.Fatal(err)
			}
			if err = r.Finish(ctx, next, true); err != nil {
				t.Fatal(err)
			}
			if _, err = r.Reserve(ctx, 1, "ip"); err != ErrJoinLimited {
				t.Fatal("retry erased another failure and reopened budget", err)
			}
		})
	}
}
func TestJoinProtectionReserveLostReplies(t *testing.T) {
	for _, drops := range []int32{1, 4} {
		name := "one"
		if drops == 4 {
			name = "all"
		}
		t.Run(name, func(t *testing.T) {
			r, fault := retryingBudgetRegistry(t, reserveJoinLua, drops)
			ctx := context.Background()
			fault.armed.Store(true)
			reservation, err := r.Reserve(ctx, 1, "ip")
			if drops == 1 && (err != nil || reservation == nil) {
				t.Fatalf("recovering one reservation: %v", err)
			}
			if drops == 4 && (err != ErrJoinUnavailable || reservation != nil) {
				t.Fatalf("unknown reservation must fail closed: %v", err)
			}
			if fault.dropped.Load() != drops {
				t.Fatalf("injected %d replies, want %d", fault.dropped.Load(), drops)
			}
			t.Logf("one Reserve call, %d actual Eval writes, %d real replies discarded", fault.writes.Load(), fault.dropped.Load())
			requireOneBudgetSlot(t, r)
			fault.armed.Store(false)
			if reservation != nil {
				if err = r.Finish(ctx, reservation, true); err != nil {
					t.Fatal(err)
				}
			}
			next, err := r.Reserve(ctx, 1, "ip")
			if err != nil {
				t.Fatal("duplicate reserve spent another budget slot", err)
			}
			if err = r.Finish(ctx, next, true); err != nil {
				t.Fatal(err)
			}
			if _, err = r.Reserve(ctx, 1, "ip"); err != ErrJoinLimited {
				t.Fatal(err)
			}
		})
	}
}

func TestJoinProtectionMissingReceiptFailsClosed(t *testing.T) {
	r := redisJoinTest(t)
	ctx := context.Background()
	success, err := r.Reserve(ctx, 1, "ip")
	if err != nil {
		t.Fatal(err)
	}
	failed, err := r.Reserve(ctx, 1, "ip")
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Finish(ctx, failed, true); err != nil {
		t.Fatal(err)
	}
	// Evict only the transport receipt, leaving both budget slots untouched.
	if err = r.client.Del(ctx, success.keys[4]).Err(); err != nil {
		t.Fatal(err)
	}
	if err = r.Finish(ctx, success, false); err != ErrJoinUnavailable {
		t.Fatal("missing receipt must not authorize a refund", err)
	}
	for _, key := range r.guardKeys(1, "ip") {
		if got := r.client.Get(ctx, key).Val(); got != "2" {
			t.Fatalf("receipt loss changed budget=%q, want 2", got)
		}
	}
}

func TestJoinProtectionReserveRetryCannotUseExpiredWindow(t *testing.T) {
	r, fault := retryingBudgetRegistry(t, reserveJoinLua, 1)
	ctx := context.Background()
	observer := redis.NewClient(&redis.Options{Addr: os.Getenv("TEST_REDIS_ADDR")})
	t.Cleanup(func() { _ = observer.Close() })
	var setupErr error
	fault.afterDrop = func() {
		// The original window expires and a new failed request occupies a slot
		// before the library retries the old Reserve. The original receipt lives.
		for _, key := range r.guardKeys(1, "ip") {
			if err := observer.Set(ctx, key, 1, r.protection.Window).Err(); err != nil {
				setupErr = err
				return
			}
			if err := observer.Set(ctx, key+":epoch", "later-window", r.protection.Window).Err(); err != nil {
				setupErr = err
				return
			}
		}
	}
	fault.armed.Store(true)
	reservation, err := r.Reserve(ctx, 1, "ip")
	if setupErr != nil {
		t.Fatal(setupErr)
	}
	if reservation != nil || err != ErrJoinUnavailable {
		t.Fatalf("stale retry granted a reservation: %v", err)
	}
	if fault.writes.Load() != 2 || fault.dropped.Load() != 1 {
		t.Fatal("default retry path not exercised", fault.writes.Load(), fault.dropped.Load())
	}
	requireOneBudgetSlot(t, r)
}
