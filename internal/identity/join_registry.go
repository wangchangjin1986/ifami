package identity

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	fieldcrypto "ifami/internal/pkg/crypto"
)

var errJoinCollision = errors.New("join code collision")

// JoinRegistration is encrypted before persistence; plaintext never enters Redis.
type JoinRegistration struct {
	FamilyID     uint64    `json:"family_id"`
	ActorUserID  uint64    `json:"actor_user_id"`
	JoinCode     string    `json:"join_code"`
	GenerationID string    `json:"generation_id"`
	Credential   string    `json:"credential"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// JoinCodeRegistry must preserve atomic rotation/revocation and fail closed on outages.
type JoinCodeRegistry interface {
	Current(context.Context, uint64, time.Time) (*JoinRegistration, error)
	Rotate(context.Context, *JoinRegistration, string, time.Time) (*JoinRegistration, error)
	Revoke(context.Context, uint64, string, time.Time) error
	Resolve(context.Context, string, time.Time) (*JoinRegistration, error)
	Allow(context.Context, uint64, string) error
	Failed(context.Context, uint64, string) error
}

type RedisJoinCodeRegistry struct {
	client         redis.UniversalClient
	field          *fieldcrypto.FieldCipher
	secret, prefix string
	protection     JoinProtectionOptions
}

// JoinProtectionOptions tunes failed-input protection; these are not issuance/member quotas.
type JoinProtectionOptions struct {
	UserLimit int
	IPLimit   int
	Window    time.Duration
}

func NewRedisJoinCodeRegistry(client redis.UniversalClient, field *fieldcrypto.FieldCipher, secret, prefix string, options ...JoinProtectionOptions) *RedisJoinCodeRegistry {
	protection := JoinProtectionOptions{UserLimit: 10, IPLimit: 30, Window: 15 * time.Minute}
	if len(options) > 0 {
		if options[0].UserLimit > 0 {
			protection.UserLimit = options[0].UserLimit
		}
		if options[0].IPLimit > 0 {
			protection.IPLimit = options[0].IPLimit
		}
		if options[0].Window >= time.Second {
			protection.Window = options[0].Window
		}
	}
	return &RedisJoinCodeRegistry{client: client, field: field, secret: secret, prefix: prefix + "join:", protection: protection}
}

func (r *RedisJoinCodeRegistry) digest(v string) string {
	m := hmac.New(sha256.New, []byte(r.secret))
	m.Write([]byte(v))
	return hex.EncodeToString(m.Sum(nil))
}
func (r *RedisJoinCodeRegistry) family(fid uint64) string {
	return r.prefix + "family:" + strconv.FormatUint(fid, 10)
}
func (r *RedisJoinCodeRegistry) ready() bool {
	return r.client != nil && r.field != nil && r.secret != ""
}
func (r *RedisJoinCodeRegistry) decode(v string) (*JoinRegistration, error) {
	if v == "" {
		return nil, nil
	}
	var raw []byte
	if e := json.Unmarshal([]byte(v), &raw); e != nil {
		return nil, ErrJoinUnavailable
	}
	plain, e := r.field.Decrypt(raw)
	if e != nil {
		return nil, ErrJoinUnavailable
	}
	var out JoinRegistration
	if e = json.Unmarshal(plain, &out); e != nil {
		return nil, ErrJoinUnavailable
	}
	return &out, nil
}

const currentJoinLua = `
local expires=tonumber(redis.call('HGET',KEYS[1],'expires') or '0')
if expires<=tonumber(ARGV[1]) then return '' end
local index=redis.call('HGET',KEYS[1],'index')
if not index or redis.call('GET',index)~=KEYS[1] then return '' end
return redis.call('HGET',KEYS[1],'data') or ''`

func (r *RedisJoinCodeRegistry) Current(ctx context.Context, fid uint64, now time.Time) (*JoinRegistration, error) {
	if !r.ready() {
		return nil, ErrJoinUnavailable
	}
	v, e := r.client.Eval(ctx, currentJoinLua, []string{r.family(fid)}, now.Unix()).Text()
	if e != nil {
		return nil, ErrJoinUnavailable
	}
	return r.decode(v)
}

const rotateJoinLua = `
local function current()
 if tonumber(redis.call('HGET',KEYS[1],'expires') or '0')<=tonumber(ARGV[1]) then return '' end
 local index=redis.call('HGET',KEYS[1],'index')
 if not index or redis.call('GET',index)~=KEYS[1] then return '' end
 return redis.call('HGET',KEYS[1],'data') or ''
end
if redis.call('EXISTS',KEYS[3])==1 then return current() end
if redis.call('EXISTS',KEYS[2])==1 then return 'COLLISION' end
local old=redis.call('HGET',KEYS[1],'index')
if old==KEYS[2] then return 'COLLISION' end
if old and redis.call('GET',old)==KEYS[1] then redis.call('DEL',old) end
redis.call('HSET',KEYS[1],'generation',ARGV[2],'index',KEYS[2],'data',ARGV[3],'expires',ARGV[4])
redis.call('EXPIREAT',KEYS[1],ARGV[4])
redis.call('SET',KEYS[2],KEYS[1],'EXAT',ARGV[4])
redis.call('SET',KEYS[3],ARGV[2],'EX',1800)
return ARGV[3]`

func (r *RedisJoinCodeRegistry) Rotate(ctx context.Context, in *JoinRegistration, key string, now time.Time) (*JoinRegistration, error) {
	if !r.ready() {
		return nil, ErrJoinUnavailable
	}
	raw, e := json.Marshal(in)
	if e != nil {
		return nil, e
	}
	enc, e := r.field.Encrypt(raw)
	if e != nil {
		return nil, ErrJoinUnavailable
	}
	data, e := json.Marshal(enc)
	if e != nil {
		return nil, e
	}
	index := r.prefix + "digit:" + r.digest(in.JoinCode)
	op := r.prefix + "op:" + r.digest(strconv.FormatUint(in.ActorUserID, 10)+":"+strconv.FormatUint(in.FamilyID, 10)+":"+key)
	v, e := r.client.Eval(ctx, rotateJoinLua, []string{r.family(in.FamilyID), index, op}, now.Unix(), in.GenerationID, string(data), in.ExpiresAt.Unix()).Text()
	if e != nil {
		return nil, ErrJoinUnavailable
	}
	if v == "COLLISION" {
		return nil, errJoinCollision
	}
	return r.decode(v)
}

const revokeJoinLua = `
if redis.call('HGET',KEYS[1],'generation')~=ARGV[1] then return 0 end
local old=redis.call('HGET',KEYS[1],'index')
if old and redis.call('GET',old)==KEYS[1] then redis.call('DEL',old) end
redis.call('DEL',KEYS[1]); return 1`

func (r *RedisJoinCodeRegistry) Revoke(ctx context.Context, fid uint64, generation string, now time.Time) error {
	if !r.ready() {
		return ErrJoinUnavailable
	}
	if e := r.client.Eval(ctx, revokeJoinLua, []string{r.family(fid)}, generation).Err(); e != nil {
		return ErrJoinUnavailable
	}
	return nil
}

const resolveJoinLua = `
local family=redis.call('GET',KEYS[1]);if not family then return '' end
if redis.call('HGET',family,'index')~=KEYS[1] then return '' end
if tonumber(redis.call('HGET',family,'expires') or '0')<=tonumber(ARGV[1]) then return '' end
return redis.call('HGET',family,'data') or ''`

func (r *RedisJoinCodeRegistry) Resolve(ctx context.Context, code string, now time.Time) (*JoinRegistration, error) {
	if !r.ready() {
		return nil, ErrJoinUnavailable
	}
	v, e := r.client.Eval(ctx, resolveJoinLua, []string{r.prefix + "digit:" + r.digest(code)}, now.Unix()).Text()
	if e != nil {
		return nil, ErrJoinUnavailable
	}
	return r.decode(v)
}
func (r *RedisJoinCodeRegistry) guardKeys(uid uint64, ip string) []string {
	return []string{r.prefix + "failed:user:" + strconv.FormatUint(uid, 10), r.prefix + "failed:ip:" + r.digest(ip)}
}

const allowJoinLua = `if tonumber(redis.call('GET',KEYS[1]) or '0')>=tonumber(ARGV[1]) or tonumber(redis.call('GET',KEYS[2]) or '0')>=tonumber(ARGV[2]) then return 0 end;return 1`
const failedJoinLua = `for _,key in ipairs(KEYS) do if redis.call('INCR',key)==1 then redis.call('EXPIRE',key,ARGV[1]) end end;return 1`

func (r *RedisJoinCodeRegistry) Allow(ctx context.Context, uid uint64, ip string) error {
	if !r.ready() {
		return ErrJoinUnavailable
	}
	v, e := r.client.Eval(ctx, allowJoinLua, r.guardKeys(uid, ip), r.protection.UserLimit, r.protection.IPLimit).Int()
	if e != nil {
		return ErrJoinUnavailable
	}
	if v == 0 {
		return ErrJoinLimited
	}
	return nil
}
func (r *RedisJoinCodeRegistry) Failed(ctx context.Context, uid uint64, ip string) error {
	if !r.ready() {
		return ErrJoinUnavailable
	}
	if e := r.client.Eval(ctx, failedJoinLua, r.guardKeys(uid, ip), int64(r.protection.Window/time.Second)).Err(); e != nil {
		return ErrJoinUnavailable
	}
	return nil
}
