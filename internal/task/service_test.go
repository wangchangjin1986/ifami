package task

import (
	"context"
	"testing"
	"time"
)

type fakeTaskMembership struct{ role string }

func (f fakeTaskMembership) RequireActive(context.Context, uint64, uint64) (uint64, string, error) {
	return 1, f.role, nil
}

func (f fakeTaskMembership) RequireMember(context.Context, uint64, uint64) (string, string, error) {
	return memberTypeReal, f.role, nil
}

func (f fakeTaskMembership) MemberUserID(context.Context, uint64, uint64) (uint64, error) {
	return 1, nil
}

func TestRequireWriter_AdminOnly(t *testing.T) {
	ctx := context.Background()
	if err := (&Service{id: fakeTaskMembership{role: roleAdmin}}).requireWriter(ctx, 1, 100); err != nil {
		t.Fatalf("admin requireWriter error = %v", err)
	}
	if err := (&Service{id: fakeTaskMembership{role: roleParent}}).requireWriter(ctx, 1, 100); err != ErrForbidden {
		t.Fatalf("parent requireWriter error = %v，want ErrForbidden", err)
	}
}

func TestWeekStart(t *testing.T) {
	// 锚点：2026-10-26 是周一（自然周起点）。
	mon := time.Date(2026, 10, 26, 15, 4, 0, 0, time.UTC)
	if mon.Weekday() != time.Monday {
		t.Fatalf("测试锚点假设错误：%s 不是周一", mon.Weekday())
	}
	cases := []struct {
		in   time.Time
		want time.Time
	}{
		{time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)},  // 周一本身
		{time.Date(2026, 10, 29, 8, 30, 0, 0, time.UTC), time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)}, // 周四
		{time.Date(2026, 11, 1, 23, 59, 0, 0, time.UTC), time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)}, // 周日（同周尾）
	}
	for _, c := range cases {
		if got := weekStart(c.in); !got.Equal(c.want) {
			t.Errorf("weekStart(%s) = %s，want %s", c.in, got, c.want)
		}
	}
}

func TestCalcStreakTodayAndYesterday(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		dates map[string]bool
		want  int
	}{
		{
			name: "今天已打卡从今天连续计算",
			dates: map[string]bool{
				"2026-11-05": true,
				"2026-11-04": true,
				"2026-11-03": true,
			},
			want: 3,
		},
		{
			name: "今天未打卡从昨天连续计算",
			dates: map[string]bool{
				"2026-11-04": true,
				"2026-11-03": true,
			},
			want: 2,
		},
		{
			name: "断档后停止",
			dates: map[string]bool{
				"2026-11-05": true,
				"2026-11-03": true,
			},
			want: 1,
		},
	}
	for _, c := range cases {
		if got := calcStreak(c.dates, now); got != c.want {
			t.Errorf("%s: calcStreak() = %d，want %d", c.name, got, c.want)
		}
	}
}
