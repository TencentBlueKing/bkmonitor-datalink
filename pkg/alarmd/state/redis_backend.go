// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

type RedisBackendOptions struct {
	Address      string
	Username     string
	Password     string
	DB           int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	PoolSize     int
}

type redisClient interface {
	MGet(context.Context, ...string) *redis.SliceCmd
	Pipelined(context.Context, func(redis.Pipeliner) error) ([]redis.Cmder, error)
	Ping(context.Context) *redis.StatusCmd
	Eval(context.Context, string, []string, ...interface{}) *redis.Cmd
	Close() error
	hashClient
	setClient
}

// fencedBatchWriteSHA addresses the batched script by its SHA-1, so one
// pipeline carries the new values and not the script text once per call.
var fencedBatchWriteSHA = func() string {
	sum := sha1.Sum([]byte(fencedBatchWriteScript))
	return hex.EncodeToString(sum[:])
}()

// Bounds of one fenced batch script. The script is atomic: while it runs
// Redis serves nobody else, so it is cut by the bytes it writes as well as by
// its keys. The key bound is the pipeline's own (runtimeApplyBatchItems), so
// a pipeline of small records is one script and pays for the fence once.
//
// The byte bound keeps a script with both bounds full under 2.5 ms of server
// time, half of a 5 ms budget; the other half is left for the tail a shared
// host adds. A script whose every write overwrites a value it proves by
// digest (a GET, a SHA-1 and a PSETEX each) takes about 4.2 us per write plus
// 5.0 us per KiB of new values, within 6% at every shape measured: 256 writes
// of 800 bytes, the two bounds together, ran 2.1 ms at p50 and 2.3 ms at
// most; 256 writes of 4 KiB, 6.6 ms. Measured from SLOWLOG, 40 to 50 runs a
// shape, on a local redis-server 7.2.5, an x86_64 build under Rosetta with
// libc malloc, which is slower than a native build with jemalloc.
//
// A write larger than the byte bound goes in a script of its own, as every
// write did before (1 MiB takes about 5.3 ms). The bound counts the new
// values only: the script also reads and hashes each current value and pays
// for its size, so a record much larger before this write than after costs
// more than its bound says. Records keep a steady size; if EVALSHA's tail
// runs over the budget, FencedWrite should carry the preflight's size too.
const (
	fencedScriptItems = 256
	fencedScriptBytes = 200 << 10
)

// noScriptReply reports the one reply that means the script body has to be
// sent again: the server does not have it cached. It is matched strictly,
// because a write retried on any other error could be applied twice.
func noScriptReply(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "NOSCRIPT")
}

const compareAndSetScript = `
local current = redis.call('GET', KEYS[1])
if ARGV[1] == '1' then
  if current then return 0 end
else
  if not current or current ~= ARGV[2] then return 0 end
end
if tonumber(ARGV[4]) == 0 then
  redis.call('SET', KEYS[1], ARGV[3])
else
  redis.call('PSETEX', KEYS[1], ARGV[4], ARGV[3])
end
return 1
`

// renewIfBelowScript extends a key's life only when it is running out.
//
// PTTL answers -2 for a key that does not exist and -1 for one that exists with
// no expiry. The first returns 2 rather than 0, so the caller can tell a key
// that vanished from a key with life left; those two are the opposite readings
// and one reply for both is how a lost record reads as a healthy one. The
// second needs no branch of its own: the threshold is never negative, so -1 is
// always below it and the key is renewed - which is what a key that never
// expires needs, that being the state this exists to end. A clause spelling
// that out would be implied by the comparison below it, and a condition no
// test can be written against is a condition that rots quietly.
const renewIfBelowScript = `
local remaining = redis.call('PTTL', KEYS[1])
if remaining == -2 then return 2 end
if remaining >= tonumber(ARGV[2]) then return 0 end
redis.call('PEXPIRE', KEYS[1], ARGV[1])
return 1
`

// fencedBatchWriteScript applies many digest-proven compare-and-set writes
// under one owner fence. The caller proves it saw each current value by
// sending the SHA-1 of the preflight bytes instead of the bytes themselves.
// When fenced, the script first verifies the owner fence once -- with exactly
// the rule the ownership store uses (assignment desired worker, ACTIVE
// disposition, owner id, epoch, lease token and a deadline still in the
// future, then the content scope) -- and writes nothing at all for a stale
// owner or a moved content scope: every write of the call is refused with
// the same answer. Each write then uses the same GET, digest comparison and
// SET / PSETEX as compareAndSetScript, so the stored bytes and TTL are
// identical to a write made alone.
//
// The fence used to run once per write, one script each: a TIME, six HGETs
// and an HMGET before every GET and PSETEX, so the fence was most of what a
// state write cost the server. The writes of one call share one owner and one
// lease, and the script is atomic, so one verification covers every write it
// makes.
//
// ARGV[1] fenced ('1'/'0'); ARGV[2] how many writes. Fenced, KEYS[1] is the
// assignment HASH and KEYS[2] the ownership HASH, and ARGV[3..7] are require
// assignment ('1'/'0'), owner id, owner epoch, lease token and the content
// scope the writer executes (empty: not compared). Then, for each write, its
// state key in KEYS and four ARGV: expected missing ('1'/'0'), SHA-1 hex of
// the expected value, new value, TTL in milliseconds (0 keeps the key
// persistent). No instant is passed: the lease deadline is compared with the
// server's clock, the one it was minted on.
//
// Each write's commands are called with pcall, so a command the server
// refuses -- a key holding another type -- is that write's answer and not
// the script's: its siblings are still written and reported, as they were
// when every write was a script of its own.
//
// Replies: {'STALE_OWNER'} or {'CONTENT_MOVED'} for the whole call, or
// {'WRITES', w1, w2, ...} with one reply per write, in order: {'APPLIED'},
// {'CONFLICT_MISSING'} when the key vanished, {'CONFLICT', current} when the
// current bytes differ, {'ERROR', message} when the server refused the
// write's command.
const fencedBatchWriteScript = ownership.FenceLua + `
local fenced = ARGV[1] == '1'
local count = tonumber(ARGV[2])
local first_key, first_arg = 1, 3
if fenced then
  local refusal = fence_refusal(KEYS[1], KEYS[2], ARGV[3], ARGV[4], ARGV[5], ARGV[6], ARGV[7], redis_now_ms())
  if refusal == 'CONTENT_MOVED' then return {'CONTENT_MOVED'} end
  if refusal then return {'STALE_OWNER'} end
  first_key, first_arg = 3, 8
end
local replies = {'WRITES'}
for index = 0, count - 1 do
  local key = KEYS[first_key + index]
  local arg = first_arg + index * 4
  local current = redis.pcall('GET', key)
  local reply = nil
  if type(current) == 'table' and current.err then
    reply = {'ERROR', current.err}
  elseif ARGV[arg] == '1' then
    if current then reply = {'CONFLICT', current} end
  else
    if not current then
      reply = {'CONFLICT_MISSING'}
    elseif redis.sha1hex(current) ~= ARGV[arg + 1] then
      reply = {'CONFLICT', current}
    end
  end
  if not reply then
    local written
    if tonumber(ARGV[arg + 3]) == 0 then
      written = redis.pcall('SET', key, ARGV[arg + 2])
    else
      written = redis.pcall('PSETEX', key, ARGV[arg + 3], ARGV[arg + 2])
    end
    if type(written) == 'table' and written.err then
      reply = {'ERROR', written.err}
    else
      reply = {'APPLIED'}
    end
  end
  replies[#replies + 1] = reply
end
return replies
`

// FenceGuard is the owner fence one batched write verifies inside Redis. It
// combines the ownership store's key descriptor with the lease facts the
// worker was admitted with. It carries no instant: the deadline is compared
// with Redis's own clock inside the script.
type FenceGuard struct {
	Keys       ownership.FenceKeys
	OwnerID    string
	OwnerEpoch uint64
	LeaseToken string
	// ContentScope, when set, is the executable view the writer is acting
	// on; the fence then also refuses an Assignment record that names
	// another (decision-016). Empty keeps the five comparisons as they were.
	ContentScope string
}

func (guard FenceGuard) validate() error {
	if guard.Keys.OwnershipKey == "" || (guard.Keys.RequireAssignment && guard.Keys.AssignmentKey == "") ||
		guard.OwnerID == "" || guard.OwnerEpoch == 0 || guard.LeaseToken == "" {
		return fmt.Errorf("state: invalid fence guard")
	}
	return nil
}

// FencedWrite is one compare-and-set whose expected value is proven by digest.
type FencedWrite struct {
	Key             string
	ExpectedMissing bool
	// ExpectedDigest is the lowercase SHA-1 hex of the value observed at
	// preflight. It is empty exactly when ExpectedMissing is true.
	ExpectedDigest string
	Value          []byte
	TTL            time.Duration
}

type FencedWriteStatus string

const (
	FencedWriteApplied         FencedWriteStatus = "APPLIED"
	FencedWriteStaleOwner      FencedWriteStatus = "STALE_OWNER"
	FencedWriteConflictMissing FencedWriteStatus = "CONFLICT_MISSING"
	FencedWriteConflict        FencedWriteStatus = "CONFLICT"
	// FencedWriteContentMoved is the fence refusing a writer whose declared
	// content scope the Assignment record no longer names: the lease holds,
	// the view is behind. Reported with ownership.ErrContentScopeMoved.
	FencedWriteContentMoved FencedWriteStatus = "CONTENT_MOVED"
)

// FencedWriteOutcome is one per-key result. Current carries the bytes Redis
// holds only for FencedWriteConflict so the caller can classify them exactly
// as a fresh read. Err marks a per-command failure whose effect is unknown.
type FencedWriteOutcome struct {
	Status  FencedWriteStatus
	Current []byte
	Err     error
}

// FencedBatchBackend executes many digest-proven compare-and-set writes in one
// round trip. Implementations return an error only when the whole batch could
// not be exchanged with storage; per-key failures are reported per outcome.
type FencedBatchBackend interface {
	Backend
	CompareAndSetManyByDigest(context.Context, *FenceGuard, []FencedWrite) ([]FencedWriteOutcome, error)
}

// ExpectedValueDigest derives the digest a FencedWrite proves against. It is
// the SHA-1 hex Redis computes with redis.sha1hex over the same bytes.
func ExpectedValueDigest(value []byte) string {
	sum := sha1.Sum(value)
	return hex.EncodeToString(sum[:])
}

// RedisBackend implements the minimum phase-one Redis String command set. The
// go-redis client owns pooling and reconnect; dependency errors are returned to
// M7 without being converted into NORMAL/RECOVERY state.
type RedisBackend struct {
	address    string
	client     redisClient
	ownsClient bool
}

func NewRedisBackend(options RedisBackendOptions) (*RedisBackend, error) {
	if options.Address == "" || options.DB < 0 || options.DialTimeout <= 0 || options.ReadTimeout <= 0 ||
		options.WriteTimeout <= 0 || options.PoolSize <= 0 {
		return nil, fmt.Errorf("state: invalid Redis backend options")
	}
	client := redis.NewClient(&redis.Options{
		Addr: options.Address, Username: options.Username, Password: options.Password, DB: options.DB,
		DialTimeout: options.DialTimeout, ReadTimeout: options.ReadTimeout, WriteTimeout: options.WriteTimeout,
		PoolSize: options.PoolSize,
	})
	return &RedisBackend{address: options.Address, client: client, ownsClient: true}, nil
}

// NewRedisBackendWithClient binds the state backend to a runtime-owned
// universal Redis client. The caller retains client lifecycle ownership.
func NewRedisBackendWithClient(address string, client redis.UniversalClient) (*RedisBackend, error) {
	if address == "" || client == nil {
		return nil, fmt.Errorf("state: Redis backend address and client are required")
	}
	return &RedisBackend{address: address, client: client}, nil
}

func (backend *RedisBackend) Address() string {
	if backend == nil {
		return ""
	}
	return backend.address
}

// Ping is readiness for this backend: the server answers, and it runs the
// owner fence its batched writes carry (fencedBatchWriteScript reads
// TIME and then writes, which not every Redis accepts). The probe names a
// key of its own that is never created, so it can share a server with the
// ownership store without touching a key of either.
func (backend *RedisBackend) Ping(ctx context.Context) error {
	if backend == nil || backend.client == nil {
		return fmt.Errorf("state: Redis backend is required")
	}
	if err := backend.client.Ping(ctx).Err(); err != nil {
		return err
	}
	if _, err := ownership.ProbeFenceClock(ctx, backend.client, "alarmd-state"); err != nil {
		return fmt.Errorf("state: %w", err)
	}
	return nil
}

func (backend *RedisBackend) MGet(ctx context.Context, keys []string) ([][]byte, error) {
	if backend == nil || backend.client == nil {
		return nil, fmt.Errorf("state: Redis backend is required")
	}
	if len(keys) == 0 {
		return [][]byte{}, nil
	}
	values, err := backend.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	if len(values) != len(keys) {
		return nil, fmt.Errorf("state: Redis MGET returned %d values for %d keys", len(values), len(keys))
	}
	result := make([][]byte, len(values))
	for index, value := range values {
		switch typed := value.(type) {
		case nil:
		case string:
			result[index] = []byte(typed)
		case []byte:
			result[index] = append([]byte(nil), typed...)
		default:
			return nil, fmt.Errorf("state: Redis MGET value %d has unsupported type %T", index, value)
		}
	}
	return result, nil
}

func (backend *RedisBackend) SetMany(ctx context.Context, writes []BackendWrite) error {
	if backend == nil || backend.client == nil {
		return fmt.Errorf("state: Redis backend is required")
	}
	if len(writes) == 0 {
		return nil
	}
	for index, write := range writes {
		if write.Key == "" || write.TTL <= 0 {
			return fmt.Errorf("state: invalid Redis write %d", index)
		}
	}
	_, err := backend.client.Pipelined(ctx, func(pipeline redis.Pipeliner) error {
		for _, write := range writes {
			pipeline.Set(ctx, write.Key, write.Value, write.TTL)
		}
		return nil
	})
	return err
}

func (backend *RedisBackend) CompareAndSet(
	ctx context.Context, key string, expected []byte, expectedMissing bool, value []byte, ttl time.Duration,
) (bool, error) {
	if backend == nil || backend.client == nil || key == "" || len(value) == 0 || ttl < 0 ||
		(expectedMissing && len(expected) != 0) {
		return false, fmt.Errorf("state: invalid Redis compare-and-set")
	}
	missing := "0"
	if expectedMissing {
		missing = "1"
	}
	result, err := backend.client.Eval(ctx, compareAndSetScript, []string{key}, missing, expected, value, ttl.Milliseconds()).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

// RenewIfBelow extends a key's life when less than threshold remains, and does
// nothing otherwise.
//
// One script rather than a PTTL followed by a PEXPIRE, so the condition is
// evaluated where the answer lives: two round trips would decide on a remaining
// life that is already stale by the time the second one arrives, and would
// spend a round trip on every key rather than on the few that need one.
//
// A key with no expiry at all is renewed. That is not a special case bolted on:
// a key that will never expire is exactly the state this mechanism exists to
// end, and keys written before it existed are in it. They acquire a life the
// first time they are loaded, so the ones still in use repair themselves and
// only the ones nothing loads any more are left for a one-off sweep.
//
// A missing key is left alone and reported as MISSING. Creating it here would
// write a key with no value, which every reader would then classify as corrupt
// state.
func (backend *RedisBackend) RenewIfBelow(
	ctx context.Context, key string, ttl, threshold time.Duration,
) (RenewalOutcome, error) {
	outcomes, err := backend.RenewManyIfBelow(ctx, []string{key}, ttl, threshold)
	if err != nil {
		return "", err
	}
	return outcomes[0], nil
}

// RenewManyIfBelow runs the same script for every key in one pipeline.
//
// One round trip rather than one per key, because the caller that needs this
// is holding a whole Slot's frozen series at once. The largest query group in
// production carries about 5,100 of them; sending those one at a time would
// add seconds to a Slot that already takes twelve, and it would do it to the
// Slot that is already the slowest one on the deployment.
//
// A transport failure fails the whole call: the pipeline's effect is then
// unknown, and a renewal whose outcome is unknown must not be recorded as
// either a renewal or a loss. A reply error on one key is that key's own
// error, since the others were still answered.
func (backend *RedisBackend) RenewManyIfBelow(
	ctx context.Context, keys []string, ttl, threshold time.Duration,
) ([]RenewalOutcome, error) {
	if backend == nil || backend.client == nil || len(keys) == 0 || ttl <= 0 || threshold < 0 || threshold > ttl {
		return nil, fmt.Errorf("state: invalid Redis lifetime renewal")
	}
	for _, key := range keys {
		if key == "" {
			return nil, fmt.Errorf("state: invalid Redis lifetime renewal")
		}
	}
	cmds, err := backend.client.Pipelined(ctx, func(pipeline redis.Pipeliner) error {
		for _, key := range keys {
			pipeline.Eval(ctx, renewIfBelowScript, []string{key},
				ttl.Milliseconds(), threshold.Milliseconds())
		}
		return nil
	})
	if err != nil && !isRedisReplyError(err) {
		return nil, err
	}
	if len(cmds) != len(keys) {
		return nil, fmt.Errorf("state: Redis pipeline returned %d replies for %d renewals", len(cmds), len(keys))
	}
	outcomes := make([]RenewalOutcome, len(keys))
	for index, cmd := range cmds {
		command, ok := cmd.(*redis.Cmd)
		if !ok {
			return nil, fmt.Errorf("state: Redis pipeline returned an unexpected reply for renewal %d", index)
		}
		result, err := command.Int()
		if err != nil {
			return nil, err
		}
		switch result {
		case 1:
			outcomes[index] = RenewalRenewed
		case 2:
			outcomes[index] = RenewalMissing
		default:
			outcomes[index] = RenewalFresh
		}
	}
	return outcomes, nil
}

// CompareAndSetManyByDigest sends the writes in one pipeline, as few fenced
// scripts as fencedScriptItems and fencedScriptBytes allow, each verifying
// the owner fence once for every write it carries. A transport failure is
// returned as an error for the whole batch because the effect of every
// command is then unknown; a Redis reply error on one script marks the
// outcomes of the writes it carried. Replies that were never read (for
// example after a mid-pipeline disconnect) are reported as per-outcome errors
// as well, so a caller never mistakes silence for success.
func (backend *RedisBackend) CompareAndSetManyByDigest(
	ctx context.Context, guard *FenceGuard, writes []FencedWrite,
) ([]FencedWriteOutcome, error) {
	if backend == nil || backend.client == nil {
		return nil, fmt.Errorf("state: Redis backend is required")
	}
	if len(writes) == 0 {
		return []FencedWriteOutcome{}, nil
	}
	if guard != nil {
		if err := guard.validate(); err != nil {
			return nil, err
		}
	}
	for index, write := range writes {
		if write.Key == "" || len(write.Value) == 0 || write.TTL < 0 ||
			(write.ExpectedMissing && write.ExpectedDigest != "") ||
			(!write.ExpectedMissing && len(write.ExpectedDigest) != hex.EncodedLen(sha1.Size)) {
			return nil, fmt.Errorf("state: invalid Redis fenced write %d", index)
		}
	}
	chunks := fencedScriptChunks(writes)
	cmds, err := backend.evalFencedChunks(ctx, guard, writes, chunks, true)
	if err != nil {
		return nil, err
	}
	if len(cmds) != len(chunks) {
		return nil, fmt.Errorf("state: Redis pipeline returned %d replies for %d scripts", len(cmds), len(chunks))
	}
	outcomes := make([]FencedWriteOutcome, len(writes))
	var uncached []fencedChunk
	for position, cmd := range cmds {
		if noScriptReply(cmd.Err()) {
			// The server never ran this one, so sending it again cannot apply
			// its writes twice.
			uncached = append(uncached, chunks[position])
			continue
		}
		decodeFencedChunkReply(cmd, chunks[position], outcomes)
	}
	if len(uncached) == 0 {
		return outcomes, nil
	}
	replies, err := backend.evalFencedChunks(ctx, guard, writes, uncached, false)
	if err != nil {
		return nil, err
	}
	if len(replies) != len(uncached) {
		return nil, fmt.Errorf("state: Redis pipeline returned %d replies for %d scripts", len(replies), len(uncached))
	}
	for position, reply := range replies {
		decodeFencedChunkReply(reply, uncached[position], outcomes)
	}
	return outcomes, nil
}

// fencedChunk is the writes[start:end] one script carries.
type fencedChunk struct{ start, end int }

// fencedScriptChunks cuts the writes, in order, into scripts of at most
// fencedScriptItems writes and fencedScriptBytes of new values; a write
// larger than the byte bound is a script of its own.
func fencedScriptChunks(writes []FencedWrite) []fencedChunk {
	var chunks []fencedChunk
	start, bytes := 0, 0
	for index, write := range writes {
		if index > start && (index-start >= fencedScriptItems || bytes+len(write.Value) > fencedScriptBytes) {
			chunks = append(chunks, fencedChunk{start, index})
			start, bytes = index, 0
		}
		bytes += len(write.Value)
	}
	return append(chunks, fencedChunk{start, len(writes)})
}

// evalFencedChunks sends one pipeline with one fenced script per chunk,
// addressing the script by SHA-1 or carrying its text.
func (backend *RedisBackend) evalFencedChunks(
	ctx context.Context, guard *FenceGuard, writes []FencedWrite, chunks []fencedChunk, byDigest bool,
) ([]redis.Cmder, error) {
	cmds, err := backend.client.Pipelined(ctx, func(pipeline redis.Pipeliner) error {
		if byDigest {
			// Caching the script in the same round trip that uses it puts the
			// text on the wire once per call and costs no extra round trip on
			// a server that has never seen it. Pipelined commands run in
			// order, so the calls below find it.
			pipeline.ScriptLoad(ctx, fencedBatchWriteScript)
		}
		for _, chunk := range chunks {
			keys, args := fencedScriptArguments(guard, writes[chunk.start:chunk.end])
			if byDigest {
				pipeline.EvalSha(ctx, fencedBatchWriteSHA, keys, args...)
				continue
			}
			pipeline.Eval(ctx, fencedBatchWriteScript, keys, args...)
		}
		return nil
	})
	if err != nil && !isRedisReplyError(err) {
		return nil, err
	}
	if byDigest && len(cmds) > 0 {
		// Drop the reply of the caching command, which is not one of the
		// scripts.
		cmds = cmds[1:]
	}
	return cmds, nil
}

// fencedScriptArguments lays one chunk out as fencedBatchWriteScript reads it.
func fencedScriptArguments(guard *FenceGuard, writes []FencedWrite) ([]string, []interface{}) {
	keys := make([]string, 0, 2+len(writes))
	args := make([]interface{}, 0, 7+4*len(writes))
	args = append(args, boolArg(guard != nil), len(writes))
	if guard != nil {
		keys = append(keys, guard.Keys.AssignmentKey, guard.Keys.OwnershipKey)
		args = append(args, boolArg(guard.Keys.RequireAssignment), guard.OwnerID,
			strconv.FormatUint(guard.OwnerEpoch, 10), guard.LeaseToken, guard.ContentScope)
	}
	for _, write := range writes {
		keys = append(keys, write.Key)
		args = append(args, boolArg(write.ExpectedMissing), write.ExpectedDigest, write.Value, write.TTL.Milliseconds())
	}
	return keys, args
}

// decodeFencedChunkReply writes the outcomes of one script's writes. A script
// that failed as a whole -- a reply error, a transport failure after it was
// sent, an answer of the wrong shape -- leaves every write it carried in
// doubt; a fence refusal is every write's answer.
func decodeFencedChunkReply(cmd redis.Cmder, chunk fencedChunk, outcomes []FencedWriteOutcome) {
	each := func(outcome FencedWriteOutcome) {
		for index := chunk.start; index < chunk.end; index++ {
			outcomes[index] = outcome
		}
	}
	typed, ok := cmd.(*redis.Cmd)
	if !ok {
		each(FencedWriteOutcome{Err: fmt.Errorf("state: unexpected Redis pipeline command %T", cmd)})
		return
	}
	value, err := typed.Result()
	if err != nil {
		each(FencedWriteOutcome{Err: err})
		return
	}
	items, ok := value.([]interface{})
	if !ok || len(items) == 0 {
		each(FencedWriteOutcome{Err: fmt.Errorf("state: Redis fenced batch reply %T is not a status array", value)})
		return
	}
	code, _ := items[0].(string)
	switch FencedWriteStatus(code) {
	case FencedWriteStaleOwner, FencedWriteContentMoved:
		each(FencedWriteOutcome{Status: FencedWriteStatus(code)})
		return
	}
	if code != "WRITES" || len(items) != 1+chunk.end-chunk.start {
		each(FencedWriteOutcome{Err: fmt.Errorf("state: Redis fenced batch reply %q carries %d replies for %d writes", code, len(items)-1, chunk.end-chunk.start)})
		return
	}
	for position, item := range items[1:] {
		outcomes[chunk.start+position] = decodeFencedWriteItem(item)
	}
}

// decodeFencedWriteItem is one write's reply within a batch script's answer.
func decodeFencedWriteItem(value interface{}) FencedWriteOutcome {
	items, ok := value.([]interface{})
	if !ok || len(items) == 0 {
		return FencedWriteOutcome{Err: fmt.Errorf("state: Redis fenced write reply %T is not a status array", value)}
	}
	code, ok := items[0].(string)
	if !ok {
		return FencedWriteOutcome{Err: fmt.Errorf("state: Redis fenced write status %T is not text", items[0])}
	}
	switch FencedWriteStatus(code) {
	case FencedWriteApplied, FencedWriteConflictMissing:
		return FencedWriteOutcome{Status: FencedWriteStatus(code)}
	case "ERROR":
		message, _ := items[len(items)-1].(string)
		return FencedWriteOutcome{Err: redisCommandError(message)}
	case FencedWriteConflict:
		if len(items) != 2 {
			return FencedWriteOutcome{Err: fmt.Errorf("state: Redis fenced write conflict without current value")}
		}
		switch current := items[1].(type) {
		case string:
			return FencedWriteOutcome{Status: FencedWriteConflict, Current: []byte(current)}
		case []byte:
			return FencedWriteOutcome{Status: FencedWriteConflict, Current: append([]byte(nil), current...)}
		default:
			return FencedWriteOutcome{Err: fmt.Errorf("state: Redis fenced write current value has unsupported type %T", current)}
		}
	default:
		return FencedWriteOutcome{Err: fmt.Errorf("state: unknown Redis fenced write status %q", code)}
	}
}

// redisCommandError is a command the server refused inside a batch script,
// reported the way a refused command outside one is: a Redis reply error.
type redisCommandError string

func (err redisCommandError) Error() string { return string(err) }
func (redisCommandError) RedisError()       {}

func boolArg(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

// isRedisReplyError reports whether err was produced by the server as a reply
// (script or command error) rather than by the transport. Reply errors are
// scoped to one command; transport errors leave every command in doubt.
func isRedisReplyError(err error) bool {
	var reply redis.Error
	return errors.As(err, &reply)
}

func (backend *RedisBackend) Close() error {
	if backend == nil || backend.client == nil || !backend.ownsClient {
		return nil
	}
	return backend.client.Close()
}

type FixedRouter struct {
	target StorageTarget
}

func NewFixedRouter(name string, backend Backend) (*FixedRouter, error) {
	if name == "" || backend == nil {
		return nil, fmt.Errorf("state: fixed storage target name and backend are required")
	}
	return &FixedRouter{target: StorageTarget{Name: name, Backend: backend}}, nil
}

func (router *FixedRouter) Targets() []StorageTarget {
	if router == nil || router.target.Name == "" || router.target.Backend == nil {
		return nil
	}
	return []StorageTarget{router.target}
}

func (router *FixedRouter) Route(_, _ string) (StorageTarget, error) {
	if router == nil || router.target.Name == "" || router.target.Backend == nil {
		return StorageTarget{}, fmt.Errorf("state: fixed storage router is not configured")
	}
	return router.target, nil
}
