package auth

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func newMockGorm(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	gdb, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return gdb, mock
}

func TestHybridStoreCreateFallsBackWhenRedisUnavailable(t *testing.T) {
	db, mock := newMockGorm(t)
	store := NewHybridStore(db, redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}))

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO `auth_sessions`")).
		WithArgs("token-a", uint64(42), anyTime{}, sqlmock.AnyArg(), sqlmock.AnyArg(), anyTime{}, sqlmock.AnyArg(), uint64(42)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	if err := store.Create(context.Background(), "token-a", 42); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet db expectations: %v", err)
	}
	uid, found, err := store.UserID(context.Background(), "token-a")
	if err != nil || !found || uid != 42 {
		t.Fatalf("local cache UserID = %d,%t,%v; want 42,true,nil", uid, found, err)
	}
}

func TestHybridStoreUserIDFallsBackToDatabase(t *testing.T) {
	db, mock := newMockGorm(t)
	store := NewHybridStore(db, redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}))
	expiresAt := time.Now().UTC().Add(time.Hour)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `auth_sessions` WHERE token = ? AND expires_at > ? ORDER BY `auth_sessions`.`token` LIMIT ?")).
		WithArgs("token-b", anyTime{}, 1).
		WillReturnRows(sqlmock.NewRows([]string{"token", "user_id", "expires_at", "created_at", "updated_at"}).
			AddRow("token-b", uint64(7), expiresAt, time.Now().UTC(), time.Now().UTC()))

	uid, found, err := store.UserID(context.Background(), "token-b")
	if err != nil || !found || uid != 7 {
		t.Fatalf("UserID = %d,%t,%v; want 7,true,nil", uid, found, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet db expectations: %v", err)
	}

	uid, found, err = store.UserID(context.Background(), "token-b")
	if err != nil || !found || uid != 7 {
		t.Fatalf("cached UserID = %d,%t,%v; want 7,true,nil", uid, found, err)
	}
}

func TestHybridStoreUserIDMissingWhenDatabaseMisses(t *testing.T) {
	db, mock := newMockGorm(t)
	store := NewHybridStore(db, nil)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `auth_sessions` WHERE token = ? AND expires_at > ? ORDER BY `auth_sessions`.`token` LIMIT ?")).
		WithArgs("missing", anyTime{}, 1).
		WillReturnRows(sqlmock.NewRows([]string{"token", "user_id", "expires_at", "created_at", "updated_at"}))

	uid, found, err := store.UserID(context.Background(), "missing")
	if err != nil || found || uid != 0 {
		t.Fatalf("UserID = %d,%t,%v; want 0,false,nil", uid, found, err)
	}
}

func TestHybridStoreDatabaseErrorPropagates(t *testing.T) {
	db, mock := newMockGorm(t)
	store := NewHybridStore(db, nil)
	want := errors.New("db down")

	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `auth_sessions` WHERE token = ? AND expires_at > ? ORDER BY `auth_sessions`.`token` LIMIT ?")).
		WithArgs("token-c", anyTime{}, 1).
		WillReturnError(want)

	_, _, err := store.UserID(context.Background(), "token-c")
	if !errors.Is(err, want) {
		t.Fatalf("err=%v, want %v", err, want)
	}
}

type anyTime struct{}

func (anyTime) Match(v driver.Value) bool {
	_, ok := v.(time.Time)
	return ok
}
