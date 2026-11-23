package schedule

import (
	"context"
	"encoding/json"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"strings"
	"testing"
)

type scheduleTestSessions map[string]uint64

func (s scheduleTestSessions) Create(_ context.Context, token string, user uint64) error {
	s[token] = user
	return nil
}
func (s scheduleTestSessions) UserID(_ context.Context, token string) (uint64, bool, error) {
	v, ok := s[token]
	return v, ok, nil
}
func TestMySQLJoinScheduleHTTPContract(t *testing.T) {
	store, svc := scopeFixture(t)
	h := server.New()
	NewHandler(svc, scheduleTestSessions{"self": 1020, "other": 1021, "parent": 1030, "admin": 1040}).Register(h.Group("/v1"))
	request := func(method, path, token, family, body string) map[string]any {
		t.Helper()
		headers := []ut.Header{{Key: "Authorization", Value: "Bearer " + token}, {Key: "X-Family-Id", Value: family}, {Key: "Content-Type", Value: "application/json"}}
		var b *ut.Body
		if body != "" {
			b = &ut.Body{Body: strings.NewReader(body), Len: len(body)}
		}
		result := ut.PerformRequest(h.Engine, method, path, b, headers...)
		var parsed map[string]any
		if err := json.Unmarshal(result.Body.Bytes(), &parsed); err != nil {
			t.Fatal(result.Body.String(), err)
		}
		return parsed
	}
	expect := func(r map[string]any, code int) {
		t.Helper()
		if r["code"] != float64(code) {
			t.Fatalf("want %d got%+v", code, r)
		}
	}
	expect(request("GET", "/v1/schedules/instances/7", "", "100", ""), 4001)
	for _, token := range []string{"self", "parent", "admin"} {
		r := request("GET", "/v1/schedules/instances/7?title=forged&location=forged", token, "100", "")
		expect(r, 0)
		data := r["data"].(map[string]any)
		if data["family_id"] != float64(100) || data["location"] != "东门" || data["title"] != "接孩子" {
			t.Fatal(data)
		}
	}
	expect(request("GET", "/v1/schedules/instances/7", "other", "100", ""), 2004)
	expect(request("GET", "/v1/schedules/instances/7", "self", "200", ""), 2004)
	expect(request("GET", "/v1/schedules/instances/7", "self", "", ""), 2001)
	expect(request("GET", "/v1/schedules/instances/0", "self", "100", ""), 2001)
	r := request("GET", "/v1/schedules/instances?start=2026-11-20&end=2026-11-21&owner_member_id=21", "self", "100", "")
	expect(r, 0)
	rows := r["data"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["owner_member_id"] != float64(20) {
		t.Fatal(rows)
	}
	expect(request("GET", "/v1/schedules", "self", "100", ""), 2004)
	expect(request("POST", "/v1/schedules/instances/7/complete", "other", "100", ""), 2004)
	expect(request("POST", "/v1/schedules/instances/7/feedback", "other", "100", `{"reason":"今天去不了"}`), 2004)
	expect(request("PATCH", "/v1/schedules/instances/7", "parent", "100", `{"owner_member_id":21}`), 2004)
	assertScopeCounts(t, store, 0, 0)
	expect(request("POST", "/v1/schedules/instances/7/complete", "self", "100", ""), 0)
	expect(request("PATCH", "/v1/schedules/instances/7", "admin", "100", `{"owner_member_id":21}`), 0)
	expect(request("POST", "/v1/schedules/instances/7/complete", "self", "100", ""), 2004)
	expect(request("GET", "/v1/schedules/instances/7", "self", "100", ""), 2004)
	expect(request("GET", "/v1/schedules/instances/7", "other", "100", ""), 0)
	assertScopeCounts(t, store, 1, 0)
}
