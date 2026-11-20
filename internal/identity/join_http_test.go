package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

type joinTestSessions map[string]uint64

func (s joinTestSessions) Create(_ context.Context, token string, uid uint64) error {
	s[token] = uid
	return nil
}
func (s joinTestSessions) UserID(_ context.Context, token string) (uint64, bool, error) {
	uid, ok := s[token]
	return uid, ok, nil
}

func TestMySQLJoinHTTPContract(t *testing.T) {
	store := mysqlJoinTest(t)
	registry := redisJoinTest(t)
	for uid := uint64(1); uid <= 4; uid++ {
		addJoinUser(t, store, uid)
	}
	sessions := joinTestSessions{"admin": 1, "joiner": 2, "probe": 3, "second": 4}
	svc := NewService(store, nil, "", sessions, newTestFieldCipher(t), "test-secret")
	svc.SetJoinCodeRegistry(registry)
	fid, _, e := svc.CreateFamily(context.Background(), 1, "HTTP真实家庭")
	if e != nil {
		t.Fatal(e)
	}
	h := server.New()
	NewHandler(svc).Register(h.Group("/v1"))
	base := fmt.Sprintf("/v1/families/%d/join-code", fid)
	request := func(method, path, token string, body any, extra ...ut.Header) map[string]any {
		t.Helper()
		var b *ut.Body
		if body != nil {
			raw, e := json.Marshal(body)
			if e != nil {
				t.Fatal(e)
			}
			b = &ut.Body{Body: strings.NewReader(string(raw)), Len: len(raw)}
		}
		headers := []ut.Header{{Key: "Content-Type", Value: "application/json"}}
		if token != "" {
			headers = append(headers, ut.Header{Key: "Authorization", Value: "Bearer " + token})
		}
		headers = append(headers, extra...)
		response := ut.PerformRequest(h.Engine, method, path, b, headers...)
		var result map[string]any
		if e := json.Unmarshal(response.Body.Bytes(), &result); e != nil {
			t.Fatalf("response %s: %v", response.Body.String(), e)
		}
		return result
	}
	expect := func(result map[string]any, code int) {
		t.Helper()
		if result["code"] != float64(code) {
			t.Fatalf("want code %d got %+v", code, result)
		}
	}
	expect(request("POST", "/v1/family-join/preview", "", map[string]any{"join_code": "000001"}), 4001)
	expect(request("POST", "/v1/families/join", "", map[string]any{}), 1015)
	expect(request("POST", fmt.Sprintf("/v1/families/%d/invitations", fid), "admin", map[string]any{}), 1015)
	expect(request("GET", base, "admin", nil), 1008)
	familyHeader := ut.Header{Key: "X-Family-Id", Value: fmt.Sprint(fid)}
	expect(request("GET", base, "admin", nil, ut.Header{Key: "X-Family-Id", Value: fmt.Sprint(fid + 1)}), 1008)
	expect(request("POST", base, "admin", nil, familyHeader), 1008)
	generated := request("POST", base, "admin", nil, familyHeader, ut.Header{Key: "Idempotency-Key", Value: "http-generate"})
	expect(generated, 0)
	code := generated["data"].(map[string]any)
	preview := request("POST", "/v1/family-join/preview", "joiner", map[string]any{"join_code": code["join_code"]})
	expect(preview, 0)
	data := preview["data"].(map[string]any)
	if data["family_name"] != "HTTP真实家庭" {
		t.Fatal(data)
	}
	input := map[string]any{"join_code": code["join_code"], "generation_id": data["generation_id"], "credential": data["credential"], "identity": "caregiver", "relation_label": "接送阿姨"}
	input["is_admin"] = true
	expect(request("POST", "/v1/family-join/confirm", "joiner", input), 1008)
	delete(input, "is_admin")
	input["family_id"] = fid + 1
	expect(request("POST", "/v1/family-join/confirm", "joiner", input), 1008)
	delete(input, "family_id")
	joined := request("POST", "/v1/family-join/confirm", "joiner", input)
	expect(joined, 0)
	member := joined["data"].(map[string]any)
	if member["is_admin"] != false || member["role"] != RoleGrandparent || member["already_member"] != false || member["token"] != nil {
		t.Fatal(member)
	}
	replay := request("POST", "/v1/family-join/confirm", "joiner", input)
	expect(replay, 0)
	if replay["data"].(map[string]any)["already_member"] != true {
		t.Fatal(replay)
	}
	expect(request("GET", base, "joiner", nil, familyHeader), 1006)
	// A valid code remains reusable by another person; identity selection never grants admin.
	input["identity"] = "parent"
	input["relation_label"] = "妈妈"
	expect(request("POST", "/v1/family-join/confirm", "second", input), 0)
	expect(request("POST", fmt.Sprintf("/v1/families/%d/children", fid), "second", map[string]any{"name": "孩子", "relation_label": "儿子"}), 1006)
	// preview and confirm share the user guard, including when the code is otherwise valid.
	for range 10 {
		expect(request("POST", "/v1/family-join/preview", "probe", map[string]any{"join_code": "bad"}), 1008)
	}
	expect(request("POST", "/v1/family-join/confirm", "probe", input), 1016)
	next := request("POST", base, "admin", nil, familyHeader, ut.Header{Key: "Idempotency-Key", Value: "http-rotate"})
	expect(next, 0)
	expect(request("POST", "/v1/family-join/confirm", "second", input), 1011)
	expect(request("DELETE", base, "admin", map[string]any{"generation_id": code["generation_id"]}, familyHeader), 0)
	current := request("GET", base, "admin", nil, familyHeader)
	expect(current, 0)
	if current["data"].(map[string]any)["generation_id"] != next["data"].(map[string]any)["generation_id"] {
		t.Fatal("stale revoke lost current generation")
	}
	expect(request("DELETE", base, "admin", map[string]any{"generation_id": next["data"].(map[string]any)["generation_id"]}, familyHeader), 0)
	current = request("GET", base, "admin", nil, familyHeader)
	if current["data"].(map[string]any)["has_code"] != false {
		t.Fatal("revoked has code")
	}
}

// Exercise the public member-write route against synthetic, migrated MySQL rows.
func TestMySQLRemoveMemberHTTPPermissions(t *testing.T) {
	store := mysqlJoinTest(t)
	ctx := context.Background()
	for uid := uint64(1); uid <= 4; uid++ {
		addJoinUser(t, store, uid)
	}
	svc := NewService(store, nil, "", joinTestSessions{"admin": 1, "parent": 2}, newTestFieldCipher(t), "test-secret")
	fid, adminID, err := svc.CreateFamily(ctx, 1, "成员删除权限夹具")
	if err != nil {
		t.Fatal(err)
	}
	members := map[uint64]uint64{1: adminID}
	for uid := uint64(2); uid <= 4; uid++ {
		m, _, err := store.JoinSingleFamily(ctx, uid, fid, RoleParent, "其他家长")
		if err != nil {
			t.Fatal(err)
		}
		members[uid] = m.MemberID
	}
	// Admin status is independent of the persisted parent role.
	if err := store.db.Model(&Member{}).Where("member_id = ?", members[4]).Update("is_admin", true).Error; err != nil {
		t.Fatal(err)
	}
	h := server.New()
	NewHandler(svc).Register(h.Group("/v1"))
	remove := func(token string, target uint64, wantHTTP, wantCode int) {
		t.Helper()
		path := fmt.Sprintf("/v1/families/%d/members/%d", fid, target)
		response := ut.PerformRequest(h.Engine, "DELETE", path, nil,
			ut.Header{Key: "Authorization", Value: "Bearer " + token},
			ut.Header{Key: "X-Family-Id", Value: fmt.Sprint(fid)})
		var result struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if response.Code != wantHTTP || result.Code != wantCode {
			t.Fatalf("DELETE %s target%d: HTTP%d code%d want HTTP%d code%d", token, target, response.Code, result.Code, wantHTTP, wantCode)
		}
	}
	assertStatus := func(target uint64, want string) {
		t.Helper()
		var row Member
		if err := store.db.First(&row, target).Error; err != nil {
			t.Fatal(err)
		}
		if row.Status != want {
			t.Fatalf("member%d status%s want%s", target, row.Status, want)
		}
	}
	for _, target := range []uint64{members[2], members[3], adminID} {
		remove("parent", target, 403, 1006)
		assertStatus(target, StatusActive)
	}
	remove("admin", members[3], 200, 0)
	assertStatus(members[3], StatusRemoved)
	remove("admin", members[4], 200, 0)
	assertStatus(members[4], StatusRemoved)
	remove("admin", adminID, 400, 1010)
	assertStatus(adminID, StatusActive)
	assertStatus(members[2], StatusActive)
}
