package identity

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// Hold completed guard commands until every caller has checked Redis. This
// exposes the check/use gap while preserving real command results and lookups.
type lookupGuardBarrier struct {
	arrived atomic.Int32
	lookups atomic.Int32
	release chan struct{}
	count   int32
}

func (b *lookupGuardBarrier) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}
func (b *lookupGuardBarrier) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (b *lookupGuardBarrier) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		args := cmd.Args()
		if cmd.Name() != "eval" || len(args) < 4 {
			return err
		}
		key, _ := args[3].(string)
		if strings.Contains(key, ":digit:") {
			b.lookups.Add(1)
		}
		if strings.Contains(key, ":failed:user:") {
			if b.arrived.Add(1) == b.count {
				close(b.release)
			}
			select {
			case <-b.release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return err
	}
}
func TestJoinProtectionConcurrentLookups(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sameUser bool
		want     int
	}{{"user", true, 10}, {"ip", false, 30}} {
		t.Run(tc.name, func(t *testing.T) {
			r := redisJoinTest(t)
			const count = 64
			barrier := &lookupGuardBarrier{count: count, release: make(chan struct{})}
			r.client.AddHook(barrier)
			svc := NewService(nil, nil, "", nil, nil, "test-secret")
			svc.SetJoinCodeRegistry(r)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			var wg sync.WaitGroup
			errs := make([]error, count)
			for i := range errs {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					uid := uint64(919191)
					if !tc.sameUser {
						uid += uint64(i)
					}
					code := fmt.Sprintf("%06d", 800000+i)
					if i%2 == 0 {
						_, errs[i] = svc.PreviewJoin(ctx, uid, JoinPreviewInput{JoinCode: code, ClientIP: "198.51.100.99"})
					} else {
						_, errs[i] = svc.ConfirmJoin(ctx, uid, JoinConfirmInput{JoinCode: code, GenerationID: strings.Repeat("a", 32), Credential: "invalid", Identity: "parent", RelationLabel: "妈妈", ClientIP: "198.51.100.99"})
					}
				}(i)
			}
			wg.Wait()
			admitted, limited := 0, 0
			for _, err := range errs {
				switch err {
				case ErrJoinInvalid:
					admitted++
				case ErrJoinLimited:
					limited++
				default:
					t.Fatalf("unexpected: %v", err)
				}
			}
			t.Logf("admitted=%d limited=%d actual Redis lookups=%d", admitted, limited, barrier.lookups.Load())
			if admitted != tc.want || limited != count-tc.want || int(barrier.lookups.Load()) != tc.want {
				t.Fatalf("admitted=%d limited=%d real lookups=%d; want %d/%d/%d", admitted, limited, barrier.lookups.Load(), tc.want, count-tc.want, tc.want)
			}
		})
	}
}

func TestJoinProtectionSuccessfulLookupsDoNotSpendFailureBudget(t *testing.T) {
	r := redisJoinTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	in := registryInput(t, 42, "004219", "a", now)
	if _, err := r.Rotate(ctx, in, "valid", now); err != nil {
		t.Fatal(err)
	}
	svc := NewService(nil, nil, "", nil, nil, "test-secret")
	svc.SetJoinCodeRegistry(r)
	for range 40 {
		if _, err := svc.validateJoin(ctx, 919191, JoinPreviewInput{JoinCode: in.JoinCode, ClientIP: "198.51.100.99"}, false); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range r.guardKeys(919191, "198.51.100.99") {
		if value := r.client.Get(ctx, key).Val(); value != "" && value != "0" {
			t.Fatalf("successful lookup spent failure budget: %q", value)
		}
	}
	for range 10 {
		if _, err := svc.validateJoin(ctx, 919191, JoinPreviewInput{JoinCode: "999999", ClientIP: "198.51.100.99"}, false); err != ErrJoinInvalid {
			t.Fatal(err)
		}
	}
	if _, err := svc.validateJoin(ctx, 919191, JoinPreviewInput{JoinCode: in.JoinCode, ClientIP: "198.51.100.99"}, false); err != ErrJoinLimited {
		t.Fatal("failed lookup budget must block later valid guesses", err)
	}
}

func TestJoinProtectionLateRefundDoesNotEraseNextWindow(t *testing.T) {
	r := redisJoinTest(t)
	r.protection.UserLimit = 1
	r.protection.IPLimit = 1
	ctx := context.Background()
	old, err := r.Reserve(ctx, 1, "ip")
	if err != nil {
		t.Fatal(err)
	}
	// Remove only this test's window as if its TTL had expired.
	if err = r.client.Del(ctx, old.keys[:4]...).Err(); err != nil {
		t.Fatal(err)
	}
	current, err := r.Reserve(ctx, 1, "ip")
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Finish(ctx, current, true); err != nil {
		t.Fatal(err)
	}
	if err = r.Finish(ctx, old, false); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Reserve(ctx, 1, "ip"); err != ErrJoinLimited {
		t.Fatal("late refund erased current failure", err)
	}
}

func TestJoinProtectionPreservesExistingWindow(t *testing.T) {
	r := redisJoinTest(t)
	ctx := context.Background()
	keys := r.guardKeys(1, "ip")
	for _, key := range keys {
		if err := r.client.Set(ctx, key, 5, time.Minute).Err(); err != nil {
			t.Fatal(err)
		}
	}
	before := r.client.PTTL(ctx, keys[0]).Val()
	reservation, err := r.Reserve(ctx, 1, "ip")
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Finish(ctx, reservation, false); err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if got := r.client.Get(ctx, key).Val(); got != "5" {
			t.Fatal("existing failures changed", got)
		}
	}
	if after := r.client.PTTL(ctx, keys[0]).Val(); after > before || after <= 0 {
		t.Fatal("window renewed", before, after)
	}
}

type cancelAfterReservation struct {
	JoinCodeRegistry
	cancel context.CancelFunc
}

func (r *cancelAfterReservation) Reserve(ctx context.Context, uid uint64, ip string) (*JoinLookupReservation, error) {
	reservation, err := r.JoinCodeRegistry.Reserve(ctx, uid, ip)
	r.cancel()
	return reservation, err
}
func TestJoinProtectionCancellationReturnsReservation(t *testing.T) {
	r := redisJoinTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc := NewService(nil, nil, "", nil, nil, "test-secret")
	svc.SetJoinCodeRegistry(&cancelAfterReservation{JoinCodeRegistry: r, cancel: cancel})
	if _, err := svc.validateJoin(ctx, 1, JoinPreviewInput{JoinCode: "123456", ClientIP: "ip"}, false); err != ErrJoinUnavailable {
		t.Fatal(err)
	}
	for _, key := range r.guardKeys(1, "ip") {
		if got := r.client.Get(context.Background(), key).Val(); got != "0" && got != "" {
			t.Fatal("cancellation stranded failure budget", got)
		}
	}
}

func TestJoinProtectionRefundOnlyOnce(t *testing.T) {
	r := redisJoinTest(t)
	r.protection.UserLimit = 2
	ctx := context.Background()
	successful, err := r.Reserve(ctx, 1, "ip")
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
	for range 2 {
		if err = r.Finish(ctx, successful, false); err != nil {
			t.Fatal(err)
		}
	}
	next, err := r.Reserve(ctx, 1, "ip")
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Finish(ctx, next, true); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Reserve(ctx, 1, "ip"); err != ErrJoinLimited {
		t.Fatal("duplicate refund erased another failure", err)
	}
}
