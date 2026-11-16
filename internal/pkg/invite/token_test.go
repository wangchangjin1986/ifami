package invite

import (
	"errors"
	"strconv"
	"testing"
	"time"
)

func TestSignVerifyClaims(t *testing.T) {
	now := time.Now()
	cases := []Claims{
		{FamilyID: 123, InviteType: TypeParent, Role: RoleParent, IsAdmin: false},
		{FamilyID: 123, InviteType: TypeParent, Role: RoleParent, IsAdmin: true},
		{FamilyID: 123, InviteType: TypeGrandparent, Role: RoleGrandparent, IsAdmin: false},
		{FamilyID: 123, InviteType: TypeCaregiver, Role: RoleGrandparent, IsAdmin: false},
	}
	for _, in := range cases {
		t.Run(in.InviteType, func(t *testing.T) {
			token, err := SignClaims("secret", in, now)
			if err != nil {
				t.Fatalf("SignClaims error = %v", err)
			}
			got, err := VerifyClaims("secret", token)
			if err != nil {
				t.Fatalf("VerifyClaims error = %v", err)
			}
			if got.FamilyID != in.FamilyID || got.InviteType != in.InviteType || got.Role != in.Role || got.IsAdmin != in.IsAdmin {
				t.Fatalf("claims = %+v, want %+v", got, in)
			}
		})
	}
}

func TestSignClaimsRejectsPrivilegeEscalation(t *testing.T) {
	bad := []Claims{
		{FamilyID: 123, InviteType: TypeCaregiver, Role: RoleParent},
		{FamilyID: 123, InviteType: TypeCaregiver, Role: RoleGrandparent, IsAdmin: true},
		{FamilyID: 123, InviteType: TypeGrandparent, Role: RoleGrandparent, IsAdmin: true},
		{FamilyID: 123, InviteType: "child", Role: "child"},
	}
	for _, in := range bad {
		if _, err := SignClaims("secret", in, time.Now()); !errors.Is(err, ErrInvalid) {
			t.Fatalf("SignClaims(%+v) error = %v, want ErrInvalid", in, err)
		}
	}
}

func TestVerifyClaimsCompatOldToken(t *testing.T) {
	expires := time.Now().Add(TTL).Unix()
	payload := "456." + strconv.FormatInt(expires, 10)
	token := payload + "." + sign("secret", payload)
	got, err := VerifyClaims("secret", token)
	if err != nil {
		t.Fatalf("VerifyClaims old token error = %v", err)
	}
	if got.FamilyID != 456 || got.InviteType != TypeGrandparent || got.Role != RoleGrandparent || got.IsAdmin {
		t.Fatalf("old claims = %+v", got)
	}
}
