// Package invite 生成与校验家庭邀请 token（无状态 HMAC 签名，7 天有效）。
// v2 token 格式：<family_id>.<invite_type>.<role>.<is_admin>.<expires_unix>.<sig>。
package invite

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalid = errors.New("邀请链接无效")
	ErrExpired = errors.New("邀请链接已过期")
)

// TTL 邀请链接有效期（对应需求 R1.2「邀请链接 7 天有效」）。
const TTL = 7 * 24 * time.Hour

const (
	TypeParent      = "parent"
	TypeGrandparent = "grandparent"
	TypeCaregiver   = "caregiver"

	RoleParent      = "parent"
	RoleGrandparent = "grandparent"
)

type Claims struct {
	FamilyID   uint64
	InviteType string
	Role       string
	IsAdmin    bool
	ExpiresAt  int64
}

func sign(secret, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Sign 生成兼容旧调用的祖辈邀请 token。
func Sign(secret string, familyID uint64, now time.Time) string {
	token, _ := SignClaims(secret, Claims{FamilyID: familyID, InviteType: TypeGrandparent, Role: RoleGrandparent}, now)
	return token
}

// SignClaims 生成包含邀请类型、角色与管理员权限的邀请 token。
func SignClaims(secret string, claims Claims, now time.Time) (string, error) {
	if !ValidClaims(claims.InviteType, claims.Role, claims.IsAdmin) || claims.FamilyID == 0 {
		return "", ErrInvalid
	}
	expires := now.Add(TTL)
	isAdmin := "0"
	if claims.IsAdmin {
		isAdmin = "1"
	}
	payload := strings.Join([]string{
		strconv.FormatUint(claims.FamilyID, 10), claims.InviteType, claims.Role, isAdmin, strconv.FormatInt(expires.Unix(), 10),
	}, ".")
	return payload + "." + sign(secret, payload), nil
}

func ValidClaims(inviteType, role string, isAdmin bool) bool {
	switch inviteType {
	case TypeParent:
		return role == RoleParent
	case TypeGrandparent, TypeCaregiver:
		return role == RoleGrandparent && !isAdmin
	default:
		return false
	}
}

// Verify 校验 token，返回 family_id。
func Verify(secret, token string) (uint64, error) {
	claims, err := VerifyClaims(secret, token)
	if err != nil {
		return 0, err
	}
	return claims.FamilyID, nil
}

// VerifyClaims 校验 token 并返回签名声明；兼容旧 3 段 token，旧 token 视作 grandparent 邀请。
func VerifyClaims(secret, token string) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 && len(parts) != 6 {
		return Claims{}, ErrInvalid
	}
	if len(parts) == 3 {
		payload := parts[0] + "." + parts[1]
		if !hmac.Equal([]byte(sign(secret, payload)), []byte(parts[2])) {
			return Claims{}, ErrInvalid
		}
		expires, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return Claims{}, ErrInvalid
		}
		if time.Now().Unix() > expires {
			return Claims{}, ErrExpired
		}
		familyID, err := strconv.ParseUint(parts[0], 10, 64)
		if err != nil {
			return Claims{}, ErrInvalid
		}
		return Claims{FamilyID: familyID, InviteType: TypeGrandparent, Role: RoleGrandparent, ExpiresAt: expires}, nil
	}
	payload := strings.Join(parts[:5], ".")
	if !hmac.Equal([]byte(sign(secret, payload)), []byte(parts[5])) {
		return Claims{}, ErrInvalid
	}
	familyID, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return Claims{}, ErrInvalid
	}
	expires, err := strconv.ParseInt(parts[4], 10, 64)
	if err != nil {
		return Claims{}, ErrInvalid
	}
	if time.Now().Unix() > expires {
		return Claims{}, ErrExpired
	}
	isAdmin := parts[3] == "1"
	if parts[3] != "0" && parts[3] != "1" {
		return Claims{}, ErrInvalid
	}
	claims := Claims{FamilyID: familyID, InviteType: parts[1], Role: parts[2], IsAdmin: isAdmin, ExpiresAt: expires}
	if !ValidClaims(claims.InviteType, claims.Role, claims.IsAdmin) {
		return Claims{}, ErrInvalid
	}
	return claims, nil
}
