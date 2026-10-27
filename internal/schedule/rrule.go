package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// RRule 是 RFC 5545 重复规则的最小子集（V1 覆盖"每周固定课"即可）。
// 起点日期在 schedules.start_date，不塞进 RRULE 字符串。
type RRule struct {
	ByDay []time.Weekday
	Until *time.Time
	Count *int
}

// ParseRRule 解析 RRULE；空串/纯空白返回 (nil, nil) 表示单次日程。
func ParseRRule(s string) (*RRule, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	r := &RRule{}
	hasFreq := false
	for _, part := range strings.Split(s, ";") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch strings.ToUpper(strings.TrimSpace(k)) {
		case "FREQ":
			if strings.ToUpper(strings.TrimSpace(v)) != "WEEKLY" {
				return nil, fmt.Errorf("unsupported FREQ: %s", v)
			}
			hasFreq = true
		case "BYDAY":
			days, err := parseByDay(v)
			if err != nil {
				return nil, err
			}
			r.ByDay = days
		case "UNTIL":
			t, err := time.Parse("20060102", strings.TrimSpace(v))
			if err != nil {
				return nil, fmt.Errorf("bad UNTIL: %w", err)
			}
			r.Until = &t
		case "COUNT":
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("bad COUNT: %s", v)
			}
			r.Count = &n
		}
	}
	if !hasFreq {
		return nil, fmt.Errorf("FREQ required")
	}
	if len(r.ByDay) == 0 {
		return nil, fmt.Errorf("BYDAY required")
	}
	return r, nil
}

var weekdayNames = map[string]time.Weekday{
	"MO": time.Monday, "TU": time.Tuesday, "WE": time.Wednesday,
	"TH": time.Thursday, "FR": time.Friday, "SA": time.Saturday, "SU": time.Sunday,
}

func parseByDay(v string) ([]time.Weekday, error) {
	parts := strings.Split(v, ",")
	days := make([]time.Weekday, 0, len(parts))
	for _, p := range parts {
		wd, ok := weekdayNames[strings.ToUpper(strings.TrimSpace(p))]
		if !ok {
			return nil, fmt.Errorf("bad BYDAY: %s", p)
		}
		days = append(days, wd)
	}
	return days, nil
}

// occurrences 物化 [begin, begin+windowDays] 窗口内（起点不早于 today）的发生日期。
// r 为 nil 表示单次：仅在 start 不早于 today 时返回 [start]。
func occurrences(r *RRule, start, today time.Time, windowDays int) []time.Time {
	if r == nil {
		if start.Before(today) {
			return nil
		}
		return []time.Time{start}
	}
	begin := start
	if begin.Before(today) {
		begin = today
	}
	end := today.AddDate(0, 0, windowDays)
	if r.Until != nil && r.Until.Before(end) {
		end = *r.Until
	}
	var out []time.Time
	for d := begin; !d.After(end); d = d.AddDate(0, 0, 1) {
		if !containsWeekday(r.ByDay, d.Weekday()) {
			continue
		}
		out = append(out, d)
		if r.Count != nil && len(out) >= *r.Count {
			break
		}
	}
	return out
}

func containsWeekday(days []time.Weekday, d time.Weekday) bool {
	for _, wd := range days {
		if wd == d {
			return true
		}
	}
	return false
}
