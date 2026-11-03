package ingest

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	fieldcrypto "ifami/internal/pkg/crypto"
)

type fakeMembership struct {
	role     string
	children map[uint64]string
}

func (f fakeMembership) RequireActive(context.Context, uint64, uint64) (uint64, string, error) {
	return 1, f.role, nil
}
func (f fakeMembership) ChildNames(context.Context, uint64) (map[uint64]string, error) {
	return f.children, nil
}
func (f fakeMembership) MemberUserID(context.Context, uint64, uint64) (uint64, error) { return 1, nil }

type fakeRecognizer struct {
	out   map[int]*ModelOutput // 按调用顺序返回
	calls int
	err   error
}

func (f *fakeRecognizer) Recognize(context.Context, string, []byte) (*ModelOutput, error) {
	i := f.calls
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.out[i], nil
}

type fakeNotifier struct{ got []NotifyInput }

func (f *fakeNotifier) CreateJob(_ context.Context, in NotifyInput) (uint64, error) {
	f.got = append(f.got, in)
	return 1, nil
}

var jpeg = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0}

func newSvc(t *testing.T, rec Recognizer, role string) (*Service, sqlmock.Sqlmock, *LocalImageStore, *fakeNotifier) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	gdb, err := gorm.Open(mysql.New(mysql.Config{Conn: db, SkipInitializeWithVersion: true}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	imgs, err := NewLocalImageStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := fieldcrypto.NewFieldCipher(base64.StdEncoding.EncodeToString([]byte("12345678901234567890123456789012")))
	if err != nil {
		t.Fatal(err)
	}
	n := &fakeNotifier{}
	svc := NewService(NewStore(gdb), imgs, rec, fakeMembership{role: role, children: map[uint64]string{10: "乐乐"}}, nil, n, c)
	return svc, mock, imgs, n
}

func TestCreateJob_AdminOnly(t *testing.T) {
	svc, _, _, _ := newSvc(t, &fakeRecognizer{}, "parent")
	if _, err := svc.CreateJob(context.Background(), 1, 100); !errors.Is(err, ErrForbidden) {
		t.Fatalf("parent CreateJob err = %v, want ErrForbidden", err)
	}
}

func TestUploadImage_RejectsNonImage(t *testing.T) {
	svc, _, _, _ := newSvc(t, &fakeRecognizer{}, roleAdmin)
	if err := svc.UploadImage(context.Background(), 1, 100, 5, 0, 0, []byte("%PDF-1.4")); !errors.Is(err, ErrBadImage) {
		t.Fatalf("pdf upload err = %v, want ErrBadImage", err)
	}
	if err := svc.UploadImage(context.Background(), 1, 100, 5, MaxImagesPerJob, 0, jpeg); !errors.Is(err, ErrBadParam) {
		t.Fatalf("seq 越界 err = %v, want ErrBadParam", err)
	}
}

// execute：一张成功一张失败 → done，结果加密，原图目录删除，发出完成提醒。
func TestExecute_PartialSuccessDeletesImages(t *testing.T) {
	rec := &fakeRecognizer{out: map[int]*ModelOutput{
		0: {ImageKind: kindCourseTable, Items: []ModelItem{{Title: "钢琴", Weekdays: []int{3}, StartTime: "17:30", EndTime: "18:30", Confidence: "high"}}},
	}}
	svc, mock, imgs, notif := newSvc(t, &seqFailRecognizer{inner: rec, failFrom: 1}, roleAdmin)
	_ = imgs.Put(100, 7, 0, jpeg)
	_ = imgs.Put(100, 7, 1, jpeg)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `ingest_jobs` SET")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	j := &Job{JobID: 7, FamilyID: 100, CreatorUserID: 1, Status: StatusRunning,
		Images: []JobImage{{Seq: 0, ChildMemberID: 10, Status: ImageWaiting}, {Seq: 1, Status: ImageWaiting}}}
	svc.execute(context.Background(), j, time.Now())

	if j.Status != StatusDone || j.Stats.Drafts != 1 || j.Stats.FailedImages != 1 {
		t.Fatalf("job = status %s stats %+v", j.Status, j.Stats)
	}
	r, err := svc.decryptResult(j.ResultEnc)
	if err != nil || len(r.Drafts) != 1 || r.Drafts[0].ChildMemberID != 10 {
		t.Fatalf("result = %+v err=%v", r, err)
	}
	if _, err := os.Stat(filepath.Join(imgs.root, "100", "7")); !os.IsNotExist(err) {
		t.Fatalf("终态后原图目录应删除")
	}
	if len(notif.got) != 1 || notif.got[0].BusinessKey != "ingest_done:7" {
		t.Fatalf("notify = %+v", notif.got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// execute：全部失败 → failed，同样删除原图。
func TestExecute_AllFailed(t *testing.T) {
	svc, mock, imgs, _ := newSvc(t, &fakeRecognizer{err: errors.New("timeout")}, roleAdmin)
	_ = imgs.Put(100, 8, 0, jpeg)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `ingest_jobs` SET")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	j := &Job{JobID: 8, FamilyID: 100, Status: StatusRunning, Images: []JobImage{{Seq: 0, Status: ImageWaiting}}}
	svc.execute(context.Background(), j, time.Now())
	if j.Status != StatusFailed || j.ErrorCode != ErrRecognize.Code {
		t.Fatalf("job status = %s code=%d", j.Status, j.ErrorCode)
	}
	if _, err := os.Stat(filepath.Join(imgs.root, "100", "8")); !os.IsNotExist(err) {
		t.Fatalf("失败后原图目录应删除")
	}
}

// seqFailRecognizer：第 failFrom 次及以后的图片（按图计，每图最多重试 1 次）失败。
type seqFailRecognizer struct {
	inner    *fakeRecognizer
	failFrom int
	images   int
}

func (s *seqFailRecognizer) Recognize(ctx context.Context, mime string, b []byte) (*ModelOutput, error) {
	if s.images >= s.failFrom {
		return nil, errors.New("bad output")
	}
	s.images++
	return s.inner.Recognize(ctx, mime, b)
}
