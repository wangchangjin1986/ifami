package invite

import (
	"crypto/hmac"
	"strconv"
	"strings"
	"time"
)

// JoinTTL is shared by signed credentials and the active Redis registration.
const JoinTTL = 24 * time.Hour

type JoinClaims struct {
	FamilyID  uint64
	ExpiresAt int64
}

// SignJoin signs only the family and absolute expiry. A registry match is still mandatory.
func SignJoin(secret string, familyID uint64, expires time.Time) (string, error) {
	if secret == "" || familyID == 0 || expires.Unix() <= 0 {
		return "", ErrInvalid
	}
	payload := strconv.FormatUint(familyID, 10) + "." + strconv.FormatInt(expires.Unix(), 10)
	return payload + "." + sign(secret, "IFAMI_JOIN_V1:"+payload), nil
}
func VerifyJoin(secret, credential string, now time.Time) (JoinClaims, error) {
	if secret == "" || len(credential) > 160 {
		return JoinClaims{}, ErrInvalid
	}
	parts := strings.Split(credential, ".")
	if len(parts) != 3 {
		return JoinClaims{}, ErrInvalid
	}
	payload := parts[0] + "." + parts[1]
	if !hmac.Equal([]byte(sign(secret, "IFAMI_JOIN_V1:"+payload)), []byte(parts[2])) {
		return JoinClaims{}, ErrInvalid
	}
	fid, e := strconv.ParseUint(parts[0], 10, 64)
	if e != nil || fid == 0 {
		return JoinClaims{}, ErrInvalid
	}
	expires, e := strconv.ParseInt(parts[1], 10, 64)
	if e != nil {
		return JoinClaims{}, ErrInvalid
	}
	if now.Unix() >= expires {
		return JoinClaims{}, ErrExpired
	}
	return JoinClaims{fid, expires}, nil
}
