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
	"time"

	"github.com/go-redis/redis/v8"
)

// NoDataHashBackend reads and writes a no-data memory held as a hash: one
// field per group beside one header field.
//
// It is its own capability rather than a wider Backend, for the reason
// LifetimeBackend gives: both reach it by type assertion and a backend without
// it would otherwise report every write as the store being down. A backend
// that cannot do this names it, and the Plan's memory pauses while the rest of
// its evaluation continues.
type NoDataHashBackend interface {
	Backend
	// ReadHash returns every field of one key. A missing key returns a nil map
	// and no error: absent and empty are the same record here, because the
	// header field is written by every write, so a hash that exists has one.
	ReadHash(context.Context, string) (map[string][]byte, error)
	// ReadHashField returns one field, nil when the key or the field is not
	// there.
	//
	// It exists so a write does not have to read the record it is about to
	// change. Everything a write decides on -- the schema, the revision, the
	// version and the digest -- is in the header, and reading the groups as
	// well would double what a Plan transfers every round, on exactly the
	// objects whose size this representation exists to bring down.
	ReadHashField(context.Context, string, string) ([]byte, error)
	// ApplyHashDelta applies one delta atomically, proving it was derived from
	// the header the caller read.
	ApplyHashDelta(context.Context, HashDeltaWrite) (HashDeltaOutcome, error)
}

// HashDeltaWrite is one atomic change to a hash record.
//
// The proof is the header field and not the whole record, and that is the
// whole reason this shape exists. Proving against the whole record would mean
// sending it, which is what the representation change removes; the header
// carries the revision and the digest of the memory it belongs to, so a header
// that is still byte-identical is a record no other writer has touched.
type HashDeltaWrite struct {
	Key string
	// HeaderField is the field the header lives in. It is passed rather than
	// compiled in so the one name lives in the store, beside the code that
	// gives it meaning.
	HeaderField string
	// ExpectedMissing says the caller read no record at all. ExpectedDigest is
	// the SHA-1 hex of the header bytes it read, and is empty exactly then.
	ExpectedMissing bool
	ExpectedDigest  string
	Header          []byte
	// Set and Del are the fields to write and to remove. A key in both is a
	// caller defect the contract rejects before reaching here.
	Set []HashField
	Del []string
	// TTL is applied to the whole key on every write. A hash whose fields are
	// written one at a time would otherwise outlive the generation it belongs
	// to, since only the key carries a lifetime.
	TTL time.Duration
	// Replace says Set is the whole record rather than a difference from it,
	// so whatever the key holds is removed first. The header guard above still
	// applies: a replace proves it read the record it is replacing.
	Replace bool
}

type HashField struct {
	Name  string
	Value []byte
}

type HashDeltaStatus string

const (
	HashDeltaApplied HashDeltaStatus = "APPLIED"
	// HashDeltaConflict is a header that is not the one the caller read,
	// including the two directions of existence. Current carries the header
	// Redis holds so the caller can classify it exactly as a fresh read.
	HashDeltaConflict HashDeltaStatus = "CONFLICT"
)

type HashDeltaOutcome struct {
	Status  HashDeltaStatus
	Current []byte
}

// applyHashDeltaScript applies a delta to one hash if its header is untouched.
//
// KEYS[1] the hash key. ARGV[1] header field name; ARGV[2] expected missing
// ('1'/'0'); ARGV[3] SHA-1 hex of the expected header; ARGV[4] the new header;
// ARGV[5] TTL in milliseconds (0 leaves the lifetime alone); ARGV[6] how many
// field/value pairs follow; ARGV[7] replace the whole record ('1'/'0'); then
// that many pairs, then the fields to delete.
//
// Replace exists because a statement can be derived from a record other than
// this one -- the whole-memory record a Plan has not yet moved off -- and such
// a statement carries the entire memory rather than a difference. Applying it
// on top of what the hash already holds would leave behind every group the
// other record no longer has, which is not a memory anybody wrote. The delete
// is inside the same script as the writes, so no reader ever sees the record
// empty.
//
// Replies: {'APPLIED'}, or {'CONFLICT'} / {'CONFLICT', header}.
//
// The writes are chunked rather than issued one call per field. A Plan's first
// write under this schema carries every group it remembers - thousands, on the
// Plans this exists for - and Lua's unpack has a stack bound well under that,
// so neither one call nor one call per field is right.
const applyHashDeltaScript = `
local header = redis.call('HGET', KEYS[1], ARGV[1])
if ARGV[2] == '1' then
  if header then return {'CONFLICT', header} end
else
  if not header then return {'CONFLICT'} end
  if redis.sha1hex(header) ~= ARGV[3] then return {'CONFLICT', header} end
end
if ARGV[7] == '1' then
  redis.call('DEL', KEYS[1])
end
local pairs_count = tonumber(ARGV[6])
local first = 8
local last = first + pairs_count * 2 - 1
local chunk = 256
local index = first
while index <= last do
  local stop = index + chunk - 1
  if stop > last then stop = last end
  redis.call('HSET', KEYS[1], unpack(ARGV, index, stop))
  index = stop + 1
end
index = last + 1
while index <= #ARGV do
  local stop = index + chunk - 1
  if stop > #ARGV then stop = #ARGV end
  redis.call('HDEL', KEYS[1], unpack(ARGV, index, stop))
  index = stop + 1
end
redis.call('HSET', KEYS[1], ARGV[1], ARGV[4])
if tonumber(ARGV[5]) > 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[5])
end
return {'APPLIED'}
`

var applyHashDeltaSHA = func() string {
	sum := sha1.Sum([]byte(applyHashDeltaScript))
	return hex.EncodeToString(sum[:])
}()

// HeaderDigest derives the digest a HashDeltaWrite proves against.
func HeaderDigest(header []byte) string {
	sum := sha1.Sum(header)
	return hex.EncodeToString(sum[:])
}

func (backend *RedisBackend) ReadHash(ctx context.Context, key string) (map[string][]byte, error) {
	if backend == nil || backend.client == nil {
		return nil, fmt.Errorf("state: redis backend is not configured")
	}
	fields, err := backend.client.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, nil
	}
	record := make(map[string][]byte, len(fields))
	for name, value := range fields {
		record[name] = []byte(value)
	}
	return record, nil
}

func (backend *RedisBackend) ReadHashField(ctx context.Context, key, field string) ([]byte, error) {
	if backend == nil || backend.client == nil {
		return nil, fmt.Errorf("state: redis backend is not configured")
	}
	value, err := backend.client.HGet(ctx, key, field).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return value, nil
}

func (backend *RedisBackend) ApplyHashDelta(
	ctx context.Context, write HashDeltaWrite,
) (HashDeltaOutcome, error) {
	if backend == nil || backend.client == nil {
		return HashDeltaOutcome{}, fmt.Errorf("state: redis backend is not configured")
	}
	args := make([]interface{}, 0, 7+len(write.Set)*2+len(write.Del))
	args = append(args, write.HeaderField, boolArg(write.ExpectedMissing), write.ExpectedDigest,
		write.Header, write.TTL.Milliseconds(), len(write.Set), boolArg(write.Replace))
	for _, field := range write.Set {
		args = append(args, field.Name, field.Value)
	}
	for _, name := range write.Del {
		args = append(args, name)
	}
	keys := []string{write.Key}
	reply, err := backend.client.EvalSha(ctx, applyHashDeltaSHA, keys, args...).Result()
	if noScriptReply(err) {
		reply, err = backend.client.Eval(ctx, applyHashDeltaScript, keys, args...).Result()
	}
	if err != nil {
		return HashDeltaOutcome{}, err
	}
	return decodeHashDeltaReply(reply)
}

func decodeHashDeltaReply(reply interface{}) (HashDeltaOutcome, error) {
	items, ok := reply.([]interface{})
	if !ok || len(items) == 0 {
		return HashDeltaOutcome{}, fmt.Errorf("state: Redis hash delta reply %T is not a status array", reply)
	}
	code, ok := items[0].(string)
	if !ok {
		return HashDeltaOutcome{}, fmt.Errorf("state: Redis hash delta status %T is not text", items[0])
	}
	switch HashDeltaStatus(code) {
	case HashDeltaApplied:
		return HashDeltaOutcome{Status: HashDeltaApplied}, nil
	case HashDeltaConflict:
		outcome := HashDeltaOutcome{Status: HashDeltaConflict}
		if len(items) > 1 {
			current, isText := items[1].(string)
			if !isText {
				return HashDeltaOutcome{}, fmt.Errorf("state: Redis hash delta header %T is not text", items[1])
			}
			outcome.Current = []byte(current)
		}
		return outcome, nil
	default:
		return HashDeltaOutcome{}, fmt.Errorf("state: unknown Redis hash delta status %q", code)
	}
}

var _ NoDataHashBackend = (*RedisBackend)(nil)

// hashClient is the part of the Redis client this file needs beyond the
// phase-one set.
type hashClient interface {
	HGetAll(context.Context, string) *redis.StringStringMapCmd
	HGet(context.Context, string, string) *redis.StringCmd
	EvalSha(context.Context, string, []string, ...interface{}) *redis.Cmd
}
