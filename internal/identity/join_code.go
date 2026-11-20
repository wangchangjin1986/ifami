package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"regexp"
	"time"

	"ifami/internal/pkg/invite"
)

type JoinCodeView struct {
	JoinCode     string    `json:"join_code"`
	GenerationID string    `json:"generation_id"`
	QrContent    string    `json:"qr_content"`
	ExpiresAt    time.Time `json:"expires_at"`
	HasCode      bool      `json:"has_code"`
}
type JoinPreviewInput struct {
	JoinCode     string `json:"join_code"`
	GenerationID string `json:"generation_id,omitempty"`
	Credential   string `json:"credential,omitempty"`
	ClientIP     string `json:"-"`
}
type JoinPreview struct {
	FamilyID     uint64    `json:"family_id"`
	FamilyName   string    `json:"family_name"`
	JoinCode     string    `json:"join_code"`
	GenerationID string    `json:"generation_id"`
	Credential   string    `json:"credential"`
	ExpiresAt    time.Time `json:"expires_at"`
}
type JoinConfirmInput struct {
	JoinCode      string `json:"join_code"`
	GenerationID  string `json:"generation_id"`
	Credential    string `json:"credential"`
	Identity      string `json:"identity"`
	RelationLabel string `json:"relation_label"`
	ClientIP      string `json:"-"`
}
type JoinConfirmResult struct {
	FamilyID      uint64 `json:"family_id"`
	MemberID      uint64 `json:"member_id"`
	Role          string `json:"role"`
	IsAdmin       bool   `json:"is_admin"`
	RelationLabel string `json:"relation_label"`
	AlreadyMember bool   `json:"already_member"`
}

var joinDigits = regexp.MustCompile(`^[0-9]{6}$`)
var joinGeneration = regexp.MustCompile(`^[0-9a-f]{32}$`)

// SetJoinCodeRegistry configures the required active-registration authority.
func (s *Service) SetJoinCodeRegistry(registry JoinCodeRegistry) { s.joinRegistry = registry }
func joinView(r *JoinRegistration) *JoinCodeView {
	if r == nil {
		return &JoinCodeView{}
	}
	return &JoinCodeView{r.JoinCode, r.GenerationID, "IFAMI_JOIN_V1:" + r.JoinCode + ":" + r.GenerationID + ":" + r.Credential, r.ExpiresAt, true}
}
func (s *Service) GetJoinCode(ctx context.Context, uid, fid uint64) (*JoinCodeView, error) {
	if _, e := s.requireRole(ctx, uid, fid, RoleAdmin); e != nil {
		return nil, e
	}
	if s.joinRegistry == nil {
		return nil, ErrJoinUnavailable
	}
	r, e := s.joinRegistry.Current(ctx, fid, time.Now())
	if e != nil {
		return nil, e
	}
	return joinView(r), nil
}
func (s *Service) GenerateJoinCode(ctx context.Context, uid, fid uint64, key string) (*JoinCodeView, error) {
	if len(key) == 0 || len(key) > 128 {
		return nil, ErrBadParam
	}
	if _, e := s.requireRole(ctx, uid, fid, RoleAdmin); e != nil {
		return nil, e
	}
	if s.joinRegistry == nil {
		return nil, ErrJoinUnavailable
	}
	now := time.Now().UTC().Truncate(time.Second)
	for attempt := 0; attempt < 32; attempt++ {
		n, e := rand.Int(rand.Reader, big.NewInt(1000000))
		if e != nil {
			return nil, ErrJoinUnavailable
		}
		gen := make([]byte, 16)
		if _, e = rand.Read(gen); e != nil {
			return nil, ErrJoinUnavailable
		}
		expires := now.Add(invite.JoinTTL)
		credential, e := invite.SignJoin(s.inviteSecret, fid, expires)
		if e != nil {
			return nil, ErrJoinUnavailable
		}
		r, e := s.joinRegistry.Rotate(ctx, &JoinRegistration{fid, uid, fmt.Sprintf("%06d", n.Int64()), hex.EncodeToString(gen), credential, expires}, key, now)
		if e == errJoinCollision {
			continue
		}
		if e != nil {
			return nil, e
		}
		return joinView(r), nil
	}
	return nil, ErrJoinUnavailable
}
func (s *Service) RevokeJoinCode(ctx context.Context, uid, fid uint64, generation string) (*JoinCodeView, error) {
	if !joinGeneration.MatchString(generation) {
		return nil, ErrBadParam
	}
	if _, e := s.requireRole(ctx, uid, fid, RoleAdmin); e != nil {
		return nil, e
	}
	if s.joinRegistry == nil {
		return nil, ErrJoinUnavailable
	}
	now := time.Now()
	if e := s.joinRegistry.Revoke(ctx, fid, generation, now); e != nil {
		return nil, e
	}
	r, e := s.joinRegistry.Current(ctx, fid, now)
	if e != nil {
		return nil, e
	}
	return joinView(r), nil
}
func (s *Service) validateJoin(ctx context.Context, uid uint64, in JoinPreviewInput, required bool) (registration *JoinRegistration, err error) {
	if s.joinRegistry == nil {
		return nil, ErrJoinUnavailable
	}
	reservation, e := s.joinRegistry.Reserve(ctx, uid, in.ClientIP)
	if e != nil {
		return nil, e
	}
	failed := true
	defer func() {
		// Cancellation must not strand a healthy lookup's reserved budget.
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if finishErr := s.joinRegistry.Finish(finishCtx, reservation, failed); finishErr != nil {
			registration, err = nil, finishErr
		}
	}()
	if uid == 0 || !joinDigits.MatchString(in.JoinCode) {
		return nil, ErrBadParam
	}
	hasQR := in.GenerationID != "" || in.Credential != ""
	if (required || hasQR) && (!joinGeneration.MatchString(in.GenerationID) || len(in.Credential) == 0 || len(in.Credential) > 160) {
		return nil, ErrJoinScan
	}
	now := time.Now()
	r, e := s.joinRegistry.Resolve(ctx, in.JoinCode, now)
	if e != nil {
		failed = false
		return nil, e
	}
	if r == nil {
		return nil, ErrJoinInvalid
	}
	// Verify even manual lookups: a corrupt/expired registry must never grant membership.
	claims, e := invite.VerifyJoin(s.inviteSecret, r.Credential, now)
	if e != nil || claims.FamilyID != r.FamilyID || claims.ExpiresAt != r.ExpiresAt.Unix() || !now.Before(r.ExpiresAt) {
		return nil, ErrJoinInvalid
	}
	if hasQR && (!hmac.Equal([]byte(in.GenerationID), []byte(r.GenerationID)) || !hmac.Equal([]byte(in.Credential), []byte(r.Credential))) {
		return nil, ErrJoinInvalid
	}
	failed = false
	return r, nil
}
func (s *Service) PreviewJoin(ctx context.Context, uid uint64, in JoinPreviewInput) (*JoinPreview, error) {
	r, e := s.validateJoin(ctx, uid, in, false)
	if e != nil {
		return nil, e
	}
	f, e := s.store.FindFamily(ctx, r.FamilyID)
	if e != nil {
		return nil, e
	}
	if f == nil {
		return nil, ErrJoinInvalid
	}
	return &JoinPreview{f.FamilyID, f.Name, r.JoinCode, r.GenerationID, r.Credential, r.ExpiresAt}, nil
}
func (s *Service) ConfirmJoin(ctx context.Context, uid uint64, in JoinConfirmInput) (*JoinConfirmResult, error) {
	role, ok := joinRole(in.Identity, in.RelationLabel)
	if !ok {
		return nil, ErrBadParam
	}
	r, e := s.validateJoin(ctx, uid, JoinPreviewInput{in.JoinCode, in.GenerationID, in.Credential, in.ClientIP}, true)
	if e != nil {
		return nil, e
	}
	m, already, e := s.store.JoinSingleFamily(ctx, uid, r.FamilyID, role, in.RelationLabel)
	if e != nil {
		return nil, e
	}
	return &JoinConfirmResult{m.FamilyID, m.MemberID, normalizeMemberRole(m.Role), isMemberAdmin(m), m.RelationLabel, already}, nil
}
func joinRole(identity, label string) (string, bool) {
	presets := map[string][]string{"parent": {"妈妈", "爸爸", "其他家长"}, "grandparent": {"奶奶", "爷爷", "姥姥", "姥爷"}, "caregiver": {"阿姨", "保姆阿姨", "接送阿姨"}}
	for _, v := range presets[identity] {
		if label == v {
			if identity == "parent" {
				return RoleParent, true
			}
			return RoleGrandparent, true
		}
	}
	return "", false
}
