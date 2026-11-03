package ingest

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// 草稿日程类型（对齐 schedule 模块类型值，跨模块只传字符串）。
const (
	typeCourse   = "course"
	typePickup   = "pickup"
	typeActivity = "activity"
	typeSchool   = "school"
)

// imageInput 归一化的单图输入。
type imageInput struct {
	Seq           int
	ChildMemberID uint64 // 用户为该图指定的孩子；0 = 自动识别
	Output        *ModelOutput
}

// normalize 把模型输出转成确定性草稿（技术方案 §5.5.6，规则须有单测）：
//  1. 时间校验与补全；2. 学校作息合并为「在校」块；3. 在校块生成接放学；
//  4. 孩子匹配；5. 单次/重复判定；6. 不可辨认行原样保留；7. 总数上限。
func normalize(images []imageInput, children map[uint64]string) []Draft {
	var out []Draft
	for _, img := range images {
		if img.Output == nil {
			continue
		}
		var drafts []Draft
		for _, it := range img.Output.Items {
			drafts = append(drafts, normalizeItem(img.Seq, it))
		}
		for i := range drafts {
			drafts[i].ChildMemberID = matchChild(img.ChildMemberID, img.Output.Items[i].ChildName, children)
		}
		if img.Output.ImageKind == kindSchool {
			drafts = mergeSchool(drafts, children)
		}
		out = append(out, drafts...)
	}
	if len(out) > MaxDraftsPerJob {
		out = out[:MaxDraftsPerJob]
	}
	return out
}

// normalizeItem 单行校验：时间格式、结束晚于开始、星期/日期/截止日期合法性。
func normalizeItem(seq int, it ModelItem) Draft {
	d := Draft{
		ImageSeq:   seq,
		Title:      strings.TrimSpace(it.Title),
		Location:   strings.TrimSpace(it.Location),
		Note:       strings.TrimSpace(it.Note),
		SourceText: strings.TrimSpace(it.SourceText),
		Unreadable: it.Unreadable,
		Uncertain:  append([]string(nil), it.Uncertain...),
	}
	d.LowConfidence = it.Confidence != "high" || len(d.Uncertain) > 0
	if d.Title == "" {
		d.Unreadable = true
	}
	start, okStart := normClock(it.StartTime)
	if !okStart {
		d.Unreadable = true
	}
	d.StartTime = start
	if okStart {
		end, okEnd := normClock(it.EndTime)
		if !okEnd || end <= start {
			// 缺结束时间或结束不晚于开始：默认 +60 分钟并标低置信。
			end = addMinutes(start, 60)
			d.LowConfidence = true
			d.Uncertain = appendUnique(d.Uncertain, "end_time")
		}
		d.EndTime = end
	}
	if it.Date != nil && validDate(*it.Date) {
		d.Date = *it.Date
	} else {
		d.Weekdays = normWeekdays(it.Weekdays)
		if len(d.Weekdays) == 0 {
			d.LowConfidence = true
			d.Uncertain = appendUnique(d.Uncertain, "weekdays")
		}
		if it.Until != nil && validDate(*it.Until) {
			d.Until = *it.Until
		}
	}
	if d.Date != "" {
		d.Type = typeActivity
	} else {
		d.Type = typeCourse
	}
	return d
}

// mergeSchool 学校作息：同一组上学日（按每天最早上课/最晚放学聚合后相同）合并为一条在校块，
// 逐节明细放 MergedItems；每个在校块追加一条接放学草稿（责任人待指派）。
// 单次事项与不可辨认行不参与合并，原样保留。
func mergeSchool(drafts []Draft, children map[uint64]string) []Draft {
	type span struct{ start, end string }
	type key struct {
		child uint64
		span  span
	}
	var keep []Draft
	perDay := map[uint64]map[int]span{} // child → weekday → span
	lessons := map[uint64][]Draft{}
	seq := 0
	for _, d := range drafts {
		seq = d.ImageSeq
		if d.Unreadable || d.Date != "" || len(d.Weekdays) == 0 {
			keep = append(keep, d)
			continue
		}
		days := perDay[d.ChildMemberID]
		if days == nil {
			days = map[int]span{}
			perDay[d.ChildMemberID] = days
		}
		for _, wd := range d.Weekdays {
			sp, ok := days[wd]
			if !ok || d.StartTime < sp.start {
				sp.start = d.StartTime
			}
			if !ok || d.EndTime > sp.end {
				sp.end = d.EndTime
			}
			days[wd] = sp
		}
		lessons[d.ChildMemberID] = append(lessons[d.ChildMemberID], d)
	}
	groups := map[key][]int{}
	var order []key
	childIDs := make([]uint64, 0, len(perDay))
	for c := range perDay {
		childIDs = append(childIDs, c)
	}
	sort.Slice(childIDs, func(i, j int) bool { return childIDs[i] < childIDs[j] })
	for _, c := range childIDs {
		for wd := 1; wd <= 7; wd++ {
			sp, ok := perDay[c][wd]
			if !ok {
				continue
			}
			k := key{c, sp}
			if _, seen := groups[k]; !seen {
				order = append(order, k)
			}
			groups[k] = append(groups[k], wd)
		}
	}
	var out []Draft
	for _, k := range order {
		wds := groups[k]
		block := Draft{
			ImageSeq:      seq,
			Title:         "在校",
			Type:          typeSchool,
			ChildMemberID: k.child,
			Weekdays:      wds,
			StartTime:     k.span.start,
			EndTime:       k.span.end,
			SourceText:    fmt.Sprintf("由 %d 节课合并", countLessons(lessons[k.child], wds)),
			MergedItems:   lessonsOn(lessons[k.child], wds),
		}
		for _, l := range block.MergedItems {
			if l.LowConfidence {
				block.LowConfidence = true
			}
		}
		name := children[k.child]
		if name == "" {
			name = "孩子"
		}
		pickup := Draft{
			ImageSeq:      seq,
			Title:         "接" + name + "放学",
			Type:          typePickup,
			ChildMemberID: k.child,
			Weekdays:      wds,
			StartTime:     k.span.end,
			EndTime:       addMinutes(k.span.end, 30),
			SourceText:    "由放学时间自动生成",
			Generated:     true,
		}
		out = append(out, block, pickup)
	}
	return append(out, keep...)
}

func lessonsOn(ls []Draft, wds []int) []Draft {
	var out []Draft
	for _, l := range ls {
		if overlapsWeekdays(l.Weekdays, wds) {
			out = append(out, l)
		}
	}
	return out
}

func countLessons(ls []Draft, wds []int) int { return len(lessonsOn(ls, wds)) }

func overlapsWeekdays(a, b []int) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// matchChild 孩子匹配：图片指定优先；否则按识别出的姓名唯一匹配（含包含关系）；家里只有一个孩子时默认该孩子；否则待选（0）。
func matchChild(specified uint64, childName *string, children map[uint64]string) uint64 {
	if specified != 0 {
		if _, ok := children[specified]; ok {
			return specified
		}
	}
	if childName != nil {
		n := strings.TrimSpace(*childName)
		if n != "" {
			var hit uint64
			hits := 0
			for id, name := range children {
				if name == "" {
					continue
				}
				if name == n || strings.Contains(n, name) || strings.Contains(name, n) {
					hit = id
					hits++
				}
			}
			if hits == 1 {
				return hit
			}
		}
	}
	if len(children) == 1 {
		for id := range children {
			return id
		}
	}
	return 0
}

// normClock 解析 H:MM / HH:MM / HH:MM:SS，统一输出 HH:MM。
func normClock(s string) (string, bool) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "：", ":"))
	for _, layout := range []string{"15:04", "15:04:05", "3:04"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("15:04"), true
		}
	}
	return "", false
}

func addMinutes(clock string, m int) string {
	t, err := time.Parse("15:04", clock)
	if err != nil {
		return clock
	}
	r := t.Add(time.Duration(m) * time.Minute)
	if r.Day() != t.Day() { // 跨天截断到 23:59
		return "23:59"
	}
	return r.Format("15:04")
}

func validDate(s string) bool {
	_, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	return err == nil
}

func normWeekdays(in []int) []int {
	seen := map[int]bool{}
	var out []int
	for _, d := range in {
		if d >= 1 && d <= 7 && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Ints(out)
	return out
}

func appendUnique(xs []string, v string) []string {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}

var bydayNames = [...]string{"", "MO", "TU", "WE", "TH", "FR", "SA", "SU"}

// buildRRule 由星期（1=周一）与截止日期生成 RRULE；星期为空返回空串（单次）。
func buildRRule(weekdays []int, until string) string {
	wds := normWeekdays(weekdays)
	if len(wds) == 0 {
		return ""
	}
	names := make([]string, 0, len(wds))
	for _, d := range wds {
		names = append(names, bydayNames[d])
	}
	r := "FREQ=WEEKLY;BYDAY=" + strings.Join(names, ",")
	if until != "" && validDate(until) {
		r += ";UNTIL=" + strings.ReplaceAll(until, "-", "")
	}
	return r
}
