package schedule

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeScheduleMembership struct {
	role    string
	members map[uint64]string
}

func (f fakeScheduleMembership) RequireActive(context.Context, uint64, uint64) (uint64, string, error) {
	return 1, f.role, nil
}

func (f fakeScheduleMembership) RequireMember(_ context.Context, _ uint64, memberID uint64) (string, string, error) {
	if f.members != nil {
		if mt, ok := f.members[memberID]; ok {
			return mt, f.role, nil
		}
	}
	return memberTypeReal, f.role, nil
}

func (f fakeScheduleMembership) MemberUserID(context.Context, uint64, uint64) (uint64, error) {
	return 1, nil
}

func TestRequireWriter_AdminOnly(t *testing.T) {
	ctx := context.Background()
	if err := (&Service{id: fakeScheduleMembership{role: roleAdmin}}).requireWriter(ctx, 1, 100); err != nil {
		t.Fatalf("admin requireWriter error = %v", err)
	}
	if err := (&Service{id: fakeScheduleMembership{role: "parent"}}).requireWriter(ctx, 1, 100); !errors.Is(err, ErrForbidden) {
		t.Fatalf("parent requireWriter error = %v，want ErrForbidden", err)
	}
}

func TestBuildSchedule_SingleOccurrence(t *testing.T) {
	svc := &Service{id: fakeScheduleMembership{role: roleAdmin, members: map[uint64]string{10: memberTypeVirtual, 20: memberTypeReal}}}
	sch, err := svc.buildSchedule(context.Background(), 100, &ScheduleInput{
		Title:         "钢琴课",
		Type:          TypeCourse,
		ChildMemberID: 10,
		OwnerMemberID: 20,
		StartDate:     "2026-11-04",
		RRule:         "",
		StartTime:     "17:30",
		EndTime:       "18:30",
		Location:      "音悦琴行",
		Note:          "带琴谱",
	})
	if err != nil {
		t.Fatalf("buildSchedule error = %v", err)
	}
	instances, err := svc.materialize(sch, sch.StartDate)
	if err != nil {
		t.Fatalf("materialize error = %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("single draft materialized %d instances, want 1", len(instances))
	}
	if got := instances[0].OccurDate.Format("2006-01-02"); got != "2026-11-04" {
		t.Fatalf("occur_date = %s, want 2026-11-04", got)
	}
}

func adminSvc() *Service {
	return &Service{id: fakeScheduleMembership{role: roleAdmin, members: map[uint64]string{10: memberTypeVirtual, 11: memberTypeVirtual, 20: memberTypeReal, 21: memberTypeReal}}}
}

func TestBuildSchedule_SchoolWithoutOwner(t *testing.T) {
	sch, err := adminSvc().buildSchedule(context.Background(), 100, &ScheduleInput{
		Title: "在校", Type: TypeSchool, ChildMemberID: 10, OwnerMemberID: 20,
		StartDate: "2026-11-02", RRule: "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR",
		StartTime: "08:00", EndTime: "16:30",
	})
	if err != nil {
		t.Fatalf("school buildSchedule error = %v", err)
	}
	if sch.OwnerMemberID != 0 {
		t.Fatalf("school owner = %d, want 0", sch.OwnerMemberID)
	}
}

func TestBuildSchedule_CourseRequiresOwner(t *testing.T) {
	svc := &Service{id: fakeScheduleMembership{role: roleAdmin, members: map[uint64]string{10: memberTypeVirtual, 0: memberTypeVirtual}}}
	_, err := svc.buildSchedule(context.Background(), 100, &ScheduleInput{
		Title: "钢琴", Type: TypeCourse, ChildMemberID: 10, OwnerMemberID: 0,
		StartDate: "2026-11-04", StartTime: "17:30",
	})
	if !errors.Is(err, ErrOwnerRequired) {
		t.Fatalf("course without real owner error = %v, want ErrOwnerRequired", err)
	}
}

func TestBuildSchedule_EndBeforeStartRejected(t *testing.T) {
	_, err := adminSvc().buildSchedule(context.Background(), 100, &ScheduleInput{
		Title: "钢琴", Type: TypeCourse, ChildMemberID: 10, OwnerMemberID: 20,
		StartDate: "2026-11-04", StartTime: "17:30", EndTime: "17:00",
	})
	if !errors.Is(err, ErrBadParam) {
		t.Fatalf("end<start error = %v, want ErrBadParam", err)
	}
}

func mustBuild(t *testing.T, in ScheduleInput, today time.Time) (*Schedule, []ScheduleInstance) {
	t.Helper()
	svc := adminSvc()
	sch, err := svc.buildSchedule(context.Background(), 100, &in)
	if err != nil {
		t.Fatalf("buildSchedule(%s) error = %v", in.Title, err)
	}
	insts, err := svc.materialize(sch, today)
	if err != nil {
		t.Fatalf("materialize error = %v", err)
	}
	return sch, insts
}

func TestIntraBatchConflicts(t *testing.T) {
	today := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC) // 周一
	base := ScheduleInput{Type: TypeCourse, StartDate: "2026-11-02", RRule: "FREQ=WEEKLY;BYDAY=SA"}
	theory := base
	theory.Title, theory.ChildMemberID, theory.OwnerMemberID, theory.StartTime, theory.EndTime = "乐理", 10, 20, "09:00", "10:00"
	swimSameOwner := base
	swimSameOwner.Title, swimSameOwner.ChildMemberID, swimSameOwner.OwnerMemberID, swimSameOwner.StartTime, swimSameOwner.EndTime = "游泳", 11, 20, "09:30", "10:30"
	swimOtherOwner := swimSameOwner
	swimOtherOwner.OwnerMemberID = 21
	adjacent := base
	adjacent.Title, adjacent.ChildMemberID, adjacent.OwnerMemberID, adjacent.StartTime, adjacent.EndTime = "书法", 10, 21, "10:00", "11:00"

	cases := []struct {
		name string
		ins  []ScheduleInput
		want int
	}{
		{"同责任人时间重叠", []ScheduleInput{theory, swimSameOwner}, 1},
		{"不同孩子不同责任人", []ScheduleInput{theory, swimOtherOwner}, 0},
		{"同孩子首尾相接不算冲突", []ScheduleInput{theory, adjacent}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var schs []*Schedule
			var insts [][]ScheduleInstance
			for _, in := range c.ins {
				s, is := mustBuild(t, in, today)
				schs = append(schs, s)
				insts = append(insts, is)
			}
			got := intraBatchConflicts(schs, insts)
			if len(got) != c.want {
				t.Fatalf("conflicts = %d, want %d", len(got), c.want)
			}
			if c.want == 1 && (got[0].Index != 1 || got[0].OtherIndex == nil || *got[0].OtherIndex != 0) {
				t.Fatalf("conflict index = %+v, want index=1 other=0", got[0])
			}
		})
	}
}

func TestIntraBatchConflicts_SchoolOnlyByChild(t *testing.T) {
	today := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	school := ScheduleInput{Title: "在校", Type: TypeSchool, ChildMemberID: 10, StartDate: "2026-11-02",
		RRule: "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR", StartTime: "08:00", EndTime: "16:30"}
	school2 := school
	school2.ChildMemberID = 11
	s1, i1 := mustBuild(t, school, today)
	s2, i2 := mustBuild(t, school2, today)
	if got := intraBatchConflicts([]*Schedule{s1, s2}, [][]ScheduleInstance{i1, i2}); len(got) != 0 {
		t.Fatalf("two children's school blocks conflicts = %d, want 0", len(got))
	}
}

func TestExtensionInstances(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	sch := &Schedule{ScheduleID: 7, FamilyID: 100, Title: "钢琴", StartDate: start, RRule: "FREQ=WEEKLY;BYDAY=WE", StartTime: "17:30"}
	today := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	last := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)

	got, err := extensionInstances(sch, &last, today)
	if err != nil {
		t.Fatalf("extensionInstances error = %v", err)
	}
	// 窗口到 2027-01-31，11-25 之后的周三：12/2 … 1/27 共 9 个。
	if len(got) != 9 {
		t.Fatalf("extended %d instances, want 9", len(got))
	}
	if got[0].OccurDate.Format("2006-01-02") != "2026-12-02" || got[0].ScheduleID != 7 {
		t.Fatalf("first = %s sid=%d", got[0].OccurDate.Format("2006-01-02"), got[0].ScheduleID)
	}
	// 已物化到窗口末尾：不再新增（幂等）。
	end := today.AddDate(0, 0, materializeWindowDays)
	if again, _ := extensionInstances(sch, &end, today); len(again) != 0 {
		t.Fatalf("re-extend = %d, want 0", len(again))
	}
}

func TestExtensionInstances_RespectsUntilAndCount(t *testing.T) {
	today := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	until := &Schedule{StartDate: today, RRule: "FREQ=WEEKLY;BYDAY=MO;UNTIL=20261116", StartTime: "08:00"}
	if got, _ := extensionInstances(until, nil, today); len(got) != 3 {
		t.Fatalf("UNTIL instances = %d, want 3", len(got))
	}
	// COUNT 从起点计数：起点 9/7(周一)，COUNT=10 → 最后一次 11/9，today 之后只剩 11/2、11/9。
	count := &Schedule{StartDate: time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC), RRule: "FREQ=WEEKLY;BYDAY=MO;COUNT=10", StartTime: "08:00"}
	if got, _ := extensionInstances(count, nil, today); len(got) != 2 {
		t.Fatalf("COUNT instances = %d, want 2", len(got))
	}
}
