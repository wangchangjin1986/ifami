package ingest

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func sp(s string) *string { return &s }

func TestParseModelOutput(t *testing.T) {
	ok := "```json\n{\"image_kind\":\"course_table\",\"items\":[{\"title\":\"钢琴\",\"weekdays\":[3],\"date\":null,\"start_time\":\"17:30\",\"end_time\":\"18:30\",\"until\":null,\"location\":\"\",\"note\":\"\",\"child_name\":null,\"confidence\":\"high\",\"uncertain\":[],\"source_text\":\"周三 钢琴\",\"unreadable\":false}]}\n```"
	mo, err := parseModelOutput(ok)
	if err != nil || len(mo.Items) != 1 || mo.Items[0].Title != "钢琴" {
		t.Fatalf("parse fenced json: %+v, %v", mo, err)
	}
	bad := []string{
		"我看不清这张图",
		`{"image_kind":"menu","items":[]}`,
		`{"image_kind":"other","items":[],"extra":1}`,
	}
	for _, b := range bad {
		if _, err := parseModelOutput(b); err == nil {
			t.Fatalf("parseModelOutput(%q) want error", b)
		}
	}
}

func TestNormalizeItem_TimeRules(t *testing.T) {
	d := normalizeItem(0, ModelItem{Title: "钢琴", Weekdays: []int{3, 3, 9}, StartTime: "17:30", EndTime: "", Confidence: "high"})
	if d.EndTime != "18:30" || !d.LowConfidence {
		t.Fatalf("缺结束时间应补 +60 并标低置信: %+v", d)
	}
	if len(d.Weekdays) != 1 || d.Weekdays[0] != 3 {
		t.Fatalf("weekdays 去重去非法: %v", d.Weekdays)
	}
	d = normalizeItem(0, ModelItem{Title: "英语", Weekdays: []int{2}, StartTime: "19:00", EndTime: "18:00", Confidence: "high"})
	if d.EndTime != "20:00" || !d.LowConfidence {
		t.Fatalf("结束早于开始应修正: %+v", d)
	}
	d = normalizeItem(0, ModelItem{Title: "书法", StartTime: "18:□0", Confidence: "low"})
	if !d.Unreadable {
		t.Fatalf("非法开始时间应标不可辨认: %+v", d)
	}
	d = normalizeItem(0, ModelItem{Title: "考级", Date: sp("2026-12-20"), StartTime: "9:00", EndTime: "10:00", Confidence: "high"})
	if d.Date != "2026-12-20" || d.Type != typeActivity || d.StartTime != "09:00" || d.LowConfidence {
		t.Fatalf("单次事项: %+v", d)
	}
	d = normalizeItem(0, ModelItem{Title: "思维课", Weekdays: []int{7}, StartTime: "14:00", EndTime: "15:00", Confidence: "low", Uncertain: []string{"start_time"}})
	if !d.LowConfidence || d.Type != typeCourse {
		t.Fatalf("低置信: %+v", d)
	}
}

func TestMatchChild(t *testing.T) {
	kids := map[uint64]string{10: "乐乐", 11: "果果"}
	if got := matchChild(11, sp("乐乐"), kids); got != 11 {
		t.Fatalf("指定优先: %d", got)
	}
	if got := matchChild(0, sp("张乐乐"), kids); got != 10 {
		t.Fatalf("姓名包含匹配: %d", got)
	}
	if got := matchChild(0, nil, kids); got != 0 {
		t.Fatalf("多孩无姓名应待选: %d", got)
	}
	if got := matchChild(0, nil, map[uint64]string{10: "乐乐"}); got != 10 {
		t.Fatalf("单孩默认: %d", got)
	}
	if got := matchChild(99, nil, kids); got != 0 {
		t.Fatalf("指定的孩子不属于家庭应忽略: %d", got)
	}
}

func TestNormalize_SchoolMergeAndPickup(t *testing.T) {
	kids := map[uint64]string{10: "乐乐", 11: "果果"}
	items := []ModelItem{
		{Title: "语文", Weekdays: []int{1, 2, 3, 4, 5}, StartTime: "08:00", EndTime: "08:40", Confidence: "high"},
		{Title: "数学", Weekdays: []int{1, 2, 3, 4, 5}, StartTime: "08:50", EndTime: "09:30", Confidence: "high"},
		{Title: "托管", Weekdays: []int{1, 2, 3, 4}, StartTime: "15:40", EndTime: "16:30", Confidence: "high"},
		{Title: "社团", Weekdays: []int{5}, StartTime: "14:30", EndTime: "15:30", Confidence: "high"},
		{Title: "家长会", Date: sp("2026-11-20"), StartTime: "18:00", EndTime: "19:00", Confidence: "high"},
		{Title: "", StartTime: "", Unreadable: true, SourceText: "周□ □"},
	}
	drafts := normalize([]imageInput{{Seq: 1, ChildMemberID: 10, Output: &ModelOutput{ImageKind: kindSchool, Items: items}}}, kids)

	var blocks, pickups, others []Draft
	for _, d := range drafts {
		switch d.Type {
		case typeSchool:
			blocks = append(blocks, d)
		case typePickup:
			pickups = append(pickups, d)
		default:
			others = append(others, d)
		}
	}
	if len(blocks) != 2 || len(pickups) != 2 {
		t.Fatalf("周一至四与周五放学时间不同应分两组: blocks=%d pickups=%d", len(blocks), len(pickups))
	}
	if b := blocks[0]; b.StartTime != "08:00" || b.EndTime != "16:30" || len(b.Weekdays) != 4 || b.ChildMemberID != 10 || len(b.MergedItems) != 3 {
		t.Fatalf("周一至四在校块: %+v", b)
	}
	if b := blocks[1]; b.EndTime != "15:30" || len(b.Weekdays) != 1 || b.Weekdays[0] != 5 {
		t.Fatalf("周五在校块: %+v", b)
	}
	if p := pickups[0]; p.Title != "接乐乐放学" || p.StartTime != "16:30" || !p.Generated {
		t.Fatalf("接放学草稿: %+v", p)
	}
	if len(others) != 2 {
		t.Fatalf("单次与不可辨认行应原样保留: %d", len(others))
	}
}

func TestNormalize_CapAtMax(t *testing.T) {
	var items []ModelItem
	for i := 0; i < MaxDraftsPerJob+10; i++ {
		items = append(items, ModelItem{Title: "课", Weekdays: []int{1}, StartTime: "10:00", EndTime: "11:00", Confidence: "high"})
	}
	got := normalize([]imageInput{{Output: &ModelOutput{ImageKind: kindCourseTable, Items: items}}}, map[uint64]string{1: "a"})
	if len(got) != MaxDraftsPerJob {
		t.Fatalf("drafts = %d, want %d", len(got), MaxDraftsPerJob)
	}
}

func TestBuildRRule(t *testing.T) {
	if got := buildRRule([]int{4, 2}, "2027-01-17"); got != "FREQ=WEEKLY;BYDAY=TU,TH;UNTIL=20270117" {
		t.Fatalf("rrule = %s", got)
	}
	if got := buildRRule(nil, ""); got != "" {
		t.Fatalf("单次 rrule = %q", got)
	}
}

func TestDetectImageMIME(t *testing.T) {
	cases := map[string][]byte{
		"image/jpeg": {0xFF, 0xD8, 0xFF, 0xE0},
		"image/png":  {0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'},
		"image/webp": []byte("RIFF\x00\x00\x00\x00WEBPVP8 "),
	}
	for want, b := range cases {
		if got, ok := detectImageMIME(b); !ok || got != want {
			t.Fatalf("detect %s = %s %v", want, got, ok)
		}
	}
	if _, ok := detectImageMIME([]byte("%PDF-1.4")); ok {
		t.Fatalf("PDF 不应通过")
	}
}

func TestLocalImageStore(t *testing.T) {
	root := t.TempDir()
	s, err := NewLocalImageStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(1, 2, 0, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if b, err := s.Get(1, 2, 0); err != nil || string(b) != "x" {
		t.Fatalf("get = %q %v", b, err)
	}
	info, _ := os.Stat(filepath.Join(root, "1", "2"))
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("job dir perm = %v", info.Mode().Perm())
	}
	if err := s.DeleteJob(1, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "1", "2")); !os.IsNotExist(err) {
		t.Fatalf("删除后目录仍存在")
	}

	_ = s.Put(1, 3, 0, []byte("old"))
	_ = s.Put(1, 4, 0, []byte("new"))
	old := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(filepath.Join(root, "1", "3"), old, old)
	n, err := s.CleanupOlderThan(time.Now().Add(-24 * time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("cleanup n=%d err=%v", n, err)
	}
	if _, err := os.Stat(filepath.Join(root, "1", "4")); err != nil {
		t.Fatalf("新任务目录不应被清理")
	}
}
