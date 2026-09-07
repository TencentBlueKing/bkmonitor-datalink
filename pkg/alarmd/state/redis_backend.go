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

// compareAndSetByDigestScript is the batched form of compareAndSetScript. The
// caller proves it saw the current value by sending the SHA-1 of the preflight
// bytes instead of the bytes themselves, so one pipeline round trip carries
// only the new values. When three keys are given the script first verifies the
// owner fence with exactly the rule used by the ownership store (assignment
// desired worker, ACTIVE disposition, owner id, epoch, lease token and a
// deadline still in the future) and refuses to write for a stale owner. The
// write itself uses the same SET / PSETEX commands as compareAndSetScript so
// the stored bytes and TTL are identical.
//
// KEYS[1] runtime state key; KEYS[2] assignment HASH; KEYS[3] ownership HASH.
// ARGV[1] expected missing ('1'/'0'); ARGV[2] SHA-1 hex of the expected value;
// ARGV[3] new value; ARGV[4] TTL in milliseconds (0 keeps the key persistent);
// ARGV[5] require assignment ('1'/'0'); ARGV[6] owner id; ARGV[7] owner epoch;
// ARGV[8] lease token; ARGV[9] now in milliseconds.
//
// Replies: {'APPLIED'}, {'STALE_OWNER'}, {'CONFLICT_MISSING'} when the key
// vanished, {'CONFLICT', current} when the current bytes differ.
const compareAndSetByDigestScript = `
if #KEYS == 3 then
  if ARGV[5] == '1' then
    local desired = redis.call('HGET', KEYS[2], 'desired_worker_id')
    if not desired or desired ~= ARGV[6] then return {'STALE_OWNER'} end
  end
  if redis.call('HGET', KEYS[3], 'execution_disposition') ~= 'ACTIVE' or
     redis.call('HGET', KEYS[3], 'owner_id') ~= ARGV[6] or
     redis.call('HGET', KEYS[3], 'owner_epoch') ~= ARGV[7] or
     redis.call('HGET', KEYS[3], 'lease_token') ~= ARGV[8] or
     tonumber(redis.call('HGET', KEYS[3], 'deadline_ms') or '0') <= tonumber(ARGV[9]) then return {'STALE_OWNER'} end
end
local current = redis.call('GET', KEYS[1])
if ARGV[1] == '1' then
  if current then return {'CONFLICT', current} end
else
  if not current then return {'CONFLICT_MISSING'} end
  if redis.sha1hex(current) ~= ARGV[2] then return {'CONFLICT', current} end
end
if tonumber(ARGV[4]) == 0 then
  redis.call('SET', KEYS[1], ARGV[3])
else
  redis.call('PSETEX', KEYS[1], ARGV[4], ARGV[3])
end
return {'APPLIED'}
`

// FenceGuard is the owner fence one batched write verifies inside Redis. It
// combines the ownership store's key descriptor with the lease facts the
// worker was admitted with and the instant the deadline is compared against.
type FenceGuard struct {
	Keys       ownership.FenceKeys
	OwnerID    string
	OwnerEpoch uint64
	LeaseToken string
	NowMillis  int64
}

func (guard FenceGuard) validate() error {
	if guard.Keys.OwnershipKey == "" || (guard.Keys.RequireAssignment && guard.Keys.AssignmentKey == "") ||
		guard.OwnerID == "" || guard.OwnerEpoch == 0 || guard.LeaseToken == "" || guard.NowMillis <= 0 {
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

func (backend *RedisBackend) Ping(ctx context.Context) error {
	if backend == nil || backend.client == nil {
		return fmt.Errorf("state: Redis backend is required")
	}
	return backend.client.Ping(ctx).Err()
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

// CompareAndSetManyByDigest sends one EVAL per write in a single pipeline. A
// transport failure is returned as an error for the whole batch because the
// effect of every command is then unknown; a Redis reply error on one command
// only marks that outcome. Replies that were never read (for example after a
// mid-pipeline disconnect) are reported as per-outcome errors as well, so a
// caller never mistakes silence for success.
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
	cmds, err := backend.client.Pipelined(ctx, func(pipeline redis.Pipeliner) error {
		for _, write := range writes {
			keys := []string{write.Key}
			args := []interface{}{boolArg(write.ExpectedMissing), write.ExpectedDigest, write.Value, write.TTL.Milliseconds()}
			if guard != nil {
				keys = append(keys, guard.Keys.AssignmentKey, guard.Keys.OwnershipKey)
				args = append(args, boolArg(guard.Keys.RequireAssignment), guard.OwnerID,
					strconv.FormatUint(guard.OwnerEpoch, 10), guard.LeaseToken, guard.NowMillis)
			}
			pipeline.Eval(ctx, compareAndSetByDigestScript, keys, args...)
		}
		return nil
	})
	if err != nil && !isRedisReplyError(err) {
		return nil, err
	}
	if len(cmds) != len(writes) {
		return nil, fmt.Errorf("state: Redis pipeline returned %d replies for %d writes", len(cmds), len(writes))
	}
	outcomes := make([]FencedWriteOutcome, len(writes))
	for index, cmd := range cmds {
		outcomes[index] = decodeFencedWriteReply(cmd)
	}
	return outcomes, nil
}

func decodeFencedWriteReply(cmd redis.Cmder) FencedWriteOutcome {
	typed, ok := cmd.(*redis.Cmd)
	if !ok {
		return FencedWriteOutcome{Err: fmt.Errorf("state: unexpected Redis pipeline command %T", cmd)}
	}
	value, err := typed.Result()
	if err != nil {
		return FencedWriteOutcome{Err: err}
	}
	items, ok := value.([]interface{})
	if !ok || len(items) == 0 {
		return FencedWriteOutcome{Err: fmt.Errorf("state: Redis fenced write reply %T is not a status array", value)}
	}
	code, ok := items[0].(string)
	if !ok {
		return FencedWriteOutcome{Err: fmt.Errorf("state: Redis fenced write status %T is not text", items[0])}
	}
	switch FencedWriteStatus(code) {
	case FencedWriteApplied, FencedWriteStaleOwner, FencedWriteConflictMissing:
		return FencedWriteOutcome{Status: FencedWriteStatus(code)}
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

func (router *FixedRouter) Route(_, _ string) (StorageTarget, error) {
	if router == nil || router.target.Name == "" || router.target.Backend == nil {
		return StorageTarget{}, fmt.Errorf("state: fixed storage router is not configured")
	}
	return router.target, nil
}
