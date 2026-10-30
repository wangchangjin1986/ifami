package task

import (
	"testing"
	"time"
)

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
		{time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)}, // 周一本身
		{time.Date(2026, 10, 29, 8, 30, 0, 0, time.UTC), time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)}, // 周四
		{time.Date(2026, 11, 1, 23, 59, 0, 0, time.UTC), time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)},  // 周日（同周尾）
	}
	for _, c := range cases {
		if got := weekStart(c.in); !got.Equal(c.want) {
			t.Errorf("weekStart(%s) = %s，want %s", c.in, got, c.want)
		}
	}
}
