// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
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
	"fmt"
	"time"
)

// HashStageSegment is one segment of a whole record written into a staging
// key.
type HashStageSegment struct {
	Key string
	// MarkerField holds Marker, which names the attempt the staging key
	// belongs to. The first segment writes it; every later one proves it.
	MarkerField string
	Marker      []byte
	// First says this segment starts the staging key over: whatever it holds
	// is deleted before the marker and this segment are written.
	First bool
	Set   []HashField
	// TTL is the staging key's short life, set again by every segment, so a
	// write abandoned between two segments leaves a key that goes on its own.
	TTL time.Duration
}

// HashStageSwap puts a staged record in place of the formal key.
type HashStageSwap struct {
	Key      string
	StageKey string
	// HeaderField, ExpectedMissing, ExpectedDigest and Header are the formal
	// key's guard and the new header, as HashDeltaWrite carries them.
	HeaderField     string
	ExpectedMissing bool
	ExpectedDigest  string
	Header          []byte
	MarkerField     string
	Marker          []byte
	// Fields is how many fields the staging key must hold: the memory's
	// distinct groups and the marker. A group written by two segments is one
	// field, so this is the number of groups the memory has, not the number
	// of times a group was sent.
	Fields int
	// TTL is the formal key's lifetime after the swap. RENAME keeps the
	// staging key's short one, so the swap sets it, and a swap without one is
	// refused before it reaches the server.
	TTL time.Duration
}

// The staged write's two scripts. Each touches the staging key and the
// second the formal key as well - two keys with no hash tag between them.
// That is only correct because Redis Cluster is refused by configuration
// (config/redis.go) and multi-key scripts without a hash tag are this
// module's convention (redisatomic); a deployment on Cluster needs the staging
// key to carry the formal key's tag first (decision-020 plan 5.1-7c).

// stageHashSegmentScript writes one segment into the attempt's staging key.
//
// KEYS[1] the staging key. ARGV[1] the marker field; ARGV[2] the marker;
// ARGV[3] first segment ('1'/'0'); ARGV[4] the staging TTL in milliseconds;
// then field/value pairs.
//
// The first segment deletes the key and writes the marker, so a key a retry
// of the same name left behind is not read as this attempt's. Every later
// segment proves the marker before it writes: a staging key that expired
// between two segments would otherwise be recreated by the HSET with no
// lifetime and only the later segments, and the swap would put a memory
// missing its first segments in place. The PEXPIRE is in the same script as
// the HSET for the same reason - a key never exists without its lifetime.
//
// Replies: {'APPLIED'} or {'STAGE_LOST'}.
const stageHashSegmentScript = `
if ARGV[3] == '1' then
  redis.call('DEL', KEYS[1])
  redis.call('HSET', KEYS[1], ARGV[1], ARGV[2])
else
  local marker = redis.call('HGET', KEYS[1], ARGV[1])
  if marker ~= ARGV[2] then return {'STAGE_LOST'} end
end
local chunk = 256
local index = 5
while index <= #ARGV do
  local stop = index + chunk - 1
  if stop > #ARGV then stop = #ARGV end
  redis.call('HSET', KEYS[1], unpack(ARGV, index, stop))
  index = stop + 1
end
redis.call('PEXPIRE', KEYS[1], ARGV[4])
return {'APPLIED'}
`

// swapStagedHashScript puts a staged record in place of the formal key.
//
// KEYS[1] the formal key; KEYS[2] the staging key. ARGV[1] header field;
// ARGV[2] expected missing ('1'/'0'); ARGV[3] SHA-1 hex of the expected
// header; ARGV[4] the new header; ARGV[5] the formal TTL in milliseconds;
// ARGV[6] the marker field; ARGV[7] the marker; ARGV[8] the fields the
// staging key must hold, the marker among them.
//
// The formal header is guarded first and exactly as applyHashDeltaScript
// guards it, so a writer that landed since the read is the same CONFLICT on
// either path. Then the staging key must be this attempt's and whole. A
// check that fails renames nothing; a staging key that is this attempt's is
// deleted then, in this script, rather than left to its TTL, since nothing
// will rename it - and only then: the delete compares the marker, nonce and
// all, before it deletes, so a refused swap never takes the staging key
// another write under the same name has started over since. The
// marker goes before the rename - a decoder meeting a field that is neither
// the header nor a group reads the whole record as corrupt - and the header
// goes on with it, so the record appears with its header or not at all.
//
// RENAME onto a formal key that exists frees the old value synchronously,
// O(its groups), unless the server runs lazyfree-lazy-server-del. A first
// write meets no formal key, so this is paid only by a whole-record write
// that replaces one.
//
// Replies: {'APPLIED'}, {'CONFLICT'} / {'CONFLICT', header}, {'STAGE_LOST'}
// or {'STAGE_SHORT'}.
const swapStagedHashScript = `
local header = redis.call('HGET', KEYS[1], ARGV[1])
local marker = redis.call('HGET', KEYS[2], ARGV[6])
local own = marker == ARGV[7]
local refused = nil
if ARGV[2] == '1' then
  if header then refused = {'CONFLICT', header} end
else
  if not header then
    refused = {'CONFLICT'}
  elseif redis.sha1hex(header) ~= ARGV[3] then
    refused = {'CONFLICT', header}
  end
end
if not refused then
  if not own then
    refused = {'STAGE_LOST'}
  elseif redis.call('HLEN', KEYS[2]) ~= tonumber(ARGV[8]) then
    refused = {'STAGE_SHORT'}
  end
end
if refused then
  if own then redis.call('DEL', KEYS[2]) end
  return refused
end
redis.call('HDEL', KEYS[2], ARGV[6])
redis.call('HSET', KEYS[2], ARGV[1], ARGV[4])
redis.call('RENAME', KEYS[2], KEYS[1])
redis.call('PEXPIRE', KEYS[1], ARGV[5])
return {'APPLIED'}
`

// discardStagedHashScript deletes the staging key KEYS[1] when its marker
// field ARGV[1] holds ARGV[2]: compared before deleted, as the swap's refusal
// deletes, so a discard never takes another write's key.
const discardStagedHashScript = `
if redis.call('HGET', KEYS[1], ARGV[1]) == ARGV[2] then redis.call('DEL', KEYS[1]) end
return 1
`

var (
	discardStagedHashSHA = func() string {
		sum := sha1.Sum([]byte(discardStagedHashScript))
		return hex.EncodeToString(sum[:])
	}()
	stageHashSegmentSHA = func() string {
		sum := sha1.Sum([]byte(stageHashSegmentScript))
		return hex.EncodeToString(sum[:])
	}()
	swapStagedHashSHA = func() string {
		sum := sha1.Sum([]byte(swapStagedHashScript))
		return hex.EncodeToString(sum[:])
	}()
)

func (backend *RedisBackend) StageHashSegment(ctx context.Context, segment HashStageSegment) (HashDeltaOutcome, error) {
	if backend == nil || backend.client == nil {
		return HashDeltaOutcome{}, fmt.Errorf("state: redis backend is not configured")
	}
	if segment.TTL <= 0 {
		return HashDeltaOutcome{}, fmt.Errorf("state: a staging key needs a lifetime")
	}
	args := make([]interface{}, 0, 4+len(segment.Set)*2)
	args = append(args, segment.MarkerField, segment.Marker, boolArg(segment.First), segment.TTL.Milliseconds())
	for _, field := range segment.Set {
		args = append(args, field.Name, field.Value)
	}
	keys := []string{segment.Key}
	reply, err := backend.client.EvalSha(ctx, stageHashSegmentSHA, keys, args...).Result()
	if noScriptReply(err) {
		reply, err = backend.client.Eval(ctx, stageHashSegmentScript, keys, args...).Result()
	}
	if err != nil {
		return HashDeltaOutcome{}, err
	}
	return decodeHashDeltaReply(reply)
}

func (backend *RedisBackend) DiscardStagedHash(ctx context.Context, key, markerField string, marker []byte) error {
	if backend == nil || backend.client == nil {
		return fmt.Errorf("state: redis backend is not configured")
	}
	keys := []string{key}
	_, err := backend.client.EvalSha(ctx, discardStagedHashSHA, keys, markerField, marker).Result()
	if noScriptReply(err) {
		_, err = backend.client.Eval(ctx, discardStagedHashScript, keys, markerField, marker).Result()
	}
	return err
}

func (backend *RedisBackend) SwapStagedHash(ctx context.Context, swap HashStageSwap) (HashDeltaOutcome, error) {
	if backend == nil || backend.client == nil {
		return HashDeltaOutcome{}, fmt.Errorf("state: redis backend is not configured")
	}
	if swap.TTL <= 0 {
		return HashDeltaOutcome{}, fmt.Errorf("state: a swapped record needs a lifetime")
	}
	args := []interface{}{swap.HeaderField, boolArg(swap.ExpectedMissing), swap.ExpectedDigest, swap.Header,
		swap.TTL.Milliseconds(), swap.MarkerField, swap.Marker, swap.Fields}
	keys := []string{swap.Key, swap.StageKey}
	reply, err := backend.client.EvalSha(ctx, swapStagedHashSHA, keys, args...).Result()
	if noScriptReply(err) {
		reply, err = backend.client.Eval(ctx, swapStagedHashScript, keys, args...).Result()
	}
	if err != nil {
		return HashDeltaOutcome{}, err
	}
	return decodeHashDeltaReply(reply)
}
