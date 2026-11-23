package invite

import (
	"strings"
	"testing"
	"time"
)

func TestJoinSigningFamilyExpiryOnly(t *testing.T) {
	now := time.Unix(1800000000, 0)
	expires := now.Add(JoinTTL)
	token, e := SignJoin("secret", 42, expires)
	if e != nil {
		t.Fatal(e)
	}
	if len(strings.Split(token, ".")) != 3 {
		t.Fatal("claims must be family/expiry/signature")
	}
	claims, e := VerifyJoin("secret", token, now)
	if e != nil || claims.FamilyID != 42 || claims.ExpiresAt != expires.Unix() {
		t.Fatalf("%+v %v", claims, e)
	}
	for _, bad := range []string{"43" + token[2:], token + ".admin", strings.Repeat("x", 161), Sign("secret", 42, now)} {
		if _, e := VerifyJoin("secret", bad, now); e == nil {
			t.Fatal("accepted invalid")
		}
	}
	if _, e := VerifyJoin("secret", token, expires); e != ErrExpired {
		t.Fatal("expiry boundary", e)
	}
	if _, e := VerifyJoin("", token, now); e != ErrInvalid {
		t.Fatal(e)
	}
}
