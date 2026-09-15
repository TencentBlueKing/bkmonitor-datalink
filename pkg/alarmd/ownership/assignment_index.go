// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package ownership

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The Assignment index is a per-round snapshot the Control Leader writes
// after reconciling every Query Group: one small index hash carrying the
// round number and, per ready worker, the round its assigned set last
// changed; plus one set key per worker holding the Query Groups whose
// desired owner it is. Workers read the index once per round and their own
// set only when the index says it changed, instead of one read per Query
// Group of the whole population.
//
// The index is an accelerator, never an authority: the Assignment record and
// the lease fence stay the truth, and a reader confirms every change it
// takes from the index against the record before acting. A stale or missing
// index therefore costs at most one late round or one extra confirmation.

// ErrAssignmentIndexAbsent reports that no Control Leader has written an
// Assignment index yet, or that it expired; readers fall back to reading
// every record.
var ErrAssignmentIndexAbsent = errors.New("alarmd ownership: Assignment index is absent")

// ErrAssignedSetAbsent reports that a worker's assigned set key is missing
// although the index names the worker; readers fall back to reading every
// record and the next Leader round rewrites the set.
var ErrAssignedSetAbsent = errors.New("alarmd ownership: assigned set is absent")

// assignedSetTTL bounds how long a worker's set outlives the Leader that
// wrote it. Every Leader round refreshes it, so it only expires when no
// Leader has run for this long, and an expired set means a full read, not a
// wrong one.
const assignedSetTTL = time.Hour

// AssignmentIndex is the round-level view of the index hash.
type AssignmentIndex struct {
	Round        uint64
	ControlEpoch uint64
	WrittenAt    time.Time
	// SetRounds maps each ready worker the Leader covered to the round its
	// assigned set was last rewritten. A worker absent from the map was not
	// in the Leader's ready set when the round ran.
	SetRounds map[string]uint64
}

// AssignedSet is one worker's set key: the Query Groups whose desired owner
// the worker was when the set was written, and the round of that write.
type AssignedSet struct {
	WorkerID    string
	Round       uint64
	QueryGroups []execution.QueryGroupIdentity
}

// AssignedSetWrite is one worker's entry in a round: its Query Groups and
// whether the set key must be rewritten this round. A caller that knows the
// set is unchanged passes Rewrite false and the round only refreshes the
// key's lifetime.
type AssignedSetWrite struct {
	WorkerID    string
	QueryGroups []execution.QueryGroupIdentity
	Rewrite     bool
}

// AssignmentIndexPublication reports one written round. Missing lists the
// workers whose set key was absent although the caller did not ask for a
// rewrite; the caller rewrites those in a follow-up round.
type AssignmentIndexPublication struct {
	Round   uint64
	Missing []string
}

// publishAssignmentIndexScript writes one round under the Control Leader's
// fence. KEYS[1] is the Control Leader ownership key, KEYS[2] the index
// hash, KEYS[3..] one set key per worker in ARGV order. ARGV carries the
// fence, the time, the set lifetime, the worker count, then per worker its
// identity, a rewrite flag and the encoded Query Group list. The round
// number lives in the index hash and is incremented server-side, so it
// stays monotonic across Leaders. Set keys are hashes of round plus
// content, written before the index field that names that round.
var publishAssignmentIndexScript = redis.NewScript(`
local leader_id = ARGV[1]
local leader_epoch = ARGV[2]
local leader_token = ARGV[3]
local now_ms = tonumber(ARGV[4])
if redis.call('HGET', KEYS[1], 'execution_disposition') ~= 'ACTIVE' or
   redis.call('HGET', KEYS[1], 'owner_id') ~= leader_id or
   redis.call('HGET', KEYS[1], 'owner_epoch') ~= leader_epoch or
   redis.call('HGET', KEYS[1], 'lease_token') ~= leader_token or
   tonumber(redis.call('HGET', KEYS[1], 'deadline_ms') or '0') <= now_ms then
  return {'STALE', 0}
end
local ttl_ms = tonumber(ARGV[5])
local count = tonumber(ARGV[6])
local round = redis.call('HINCRBY', KEYS[2], 'round', 1)
redis.call('HSET', KEYS[2], 'control_epoch', leader_epoch, 'written_at_ms', now_ms)
local keep = {}
local missing = {}
for index = 1, count do
  local base = 6 + 3 * (index - 1)
  local worker = ARGV[base + 1]
  local rewrite = ARGV[base + 2] == '1'
  local payload = ARGV[base + 3]
  local key = KEYS[2 + index]
  local field = 'set_round:' .. worker
  keep[field] = true
  if rewrite then
    redis.call('HSET', key, 'round', round, 'query_groups', payload)
    redis.call('PEXPIRE', key, ttl_ms)
    redis.call('HSET', KEYS[2], field, round)
  elseif redis.call('EXISTS', key) == 1 and redis.call('HEXISTS', KEYS[2], field) == 1 then
    redis.call('PEXPIRE', key, ttl_ms)
  else
    missing[#missing + 1] = worker
  end
end
for _, field in ipairs(redis.call('HKEYS', KEYS[2])) do
  if string.sub(field, 1, 10) == 'set_round:' and not keep[field] then
    redis.call('HDEL', KEYS[2], field)
  end
end
local result = {'OK', round}
for _, worker in ipairs(missing) do
  result[#result + 1] = worker
end
return result
`)

// PublishAssignmentIndex writes one index round for the given workers under
// the Leader's authority. Workers not in the list lose their index entry;
// their set keys expire on their own. A stale authority returns
// ErrStaleFence and writes nothing.
func (store *RedisStore) PublishAssignmentIndex(
	ctx context.Context,
	authority PublicationAuthority,
	at time.Time,
	sets []AssignedSetWrite,
) (AssignmentIndexPublication, error) {
	if authority.Fence.QueryGroup != ControlLeaderIdentity || at.IsZero() {
		return AssignmentIndexPublication{}, errors.New("alarmd ownership: invalid Assignment index publication")
	}
	keys := make([]string, 0, 2+len(sets))
	keys = append(keys, store.ownershipKey(ControlLeaderIdentity), store.assignmentIndexKey())
	args := make([]interface{}, 0, 6+3*len(sets))
	args = append(args, authority.Fence.OwnerID, authority.Fence.OwnerEpoch, authority.Fence.LeaseToken,
		at.UnixMilli(), assignedSetTTL.Milliseconds(), len(sets))
	seen := make(map[string]struct{}, len(sets))
	for _, set := range sets {
		if set.WorkerID == "" {
			return AssignmentIndexPublication{}, errors.New("alarmd ownership: assigned set without worker identity")
		}
		if _, duplicate := seen[set.WorkerID]; duplicate {
			return AssignmentIndexPublication{}, errors.New("alarmd ownership: duplicate worker in Assignment index round")
		}
		seen[set.WorkerID] = struct{}{}
		payload := ""
		if set.Rewrite {
			encoded, err := encodeAssignedQueryGroups(set.QueryGroups)
			if err != nil {
				return AssignmentIndexPublication{}, err
			}
			payload = encoded
		}
		rewrite := "0"
		if set.Rewrite {
			rewrite = "1"
		}
		keys = append(keys, store.assignedSetKey(set.WorkerID))
		args = append(args, set.WorkerID, rewrite, payload)
	}
	result, err := publishAssignmentIndexScript.Run(ctx, store.client, keys, args...).Result()
	if err != nil {
		return AssignmentIndexPublication{}, err
	}
	values, err := scriptValues(result, 2)
	if err != nil {
		return AssignmentIndexPublication{}, err
	}
	if scriptText(values[0]) == "STALE" {
		return AssignmentIndexPublication{}, ErrStaleFence
	}
	publication := AssignmentIndexPublication{Round: uint64(scriptInt(values[1]))}
	for _, value := range values[2:] {
		publication.Missing = append(publication.Missing, scriptText(value))
	}
	return publication, nil
}

// ReadAssignmentIndex reads the round-level index; ErrAssignmentIndexAbsent
// when no Leader has written one.
func (store *RedisStore) ReadAssignmentIndex(ctx context.Context) (AssignmentIndex, error) {
	values, err := store.client.HGetAll(ctx, store.assignmentIndexKey()).Result()
	if err != nil {
		return AssignmentIndex{}, err
	}
	if len(values) == 0 {
		return AssignmentIndex{}, ErrAssignmentIndexAbsent
	}
	index := AssignmentIndex{
		Round: parseUint(values["round"]), ControlEpoch: parseUint(values["control_epoch"]),
		WrittenAt: time.UnixMilli(parseInt(values["written_at_ms"])), SetRounds: map[string]uint64{},
	}
	if index.Round == 0 {
		return AssignmentIndex{}, errors.New("alarmd ownership: Assignment index without a round")
	}
	for field, value := range values {
		if strings.HasPrefix(field, "set_round:") {
			index.SetRounds[strings.TrimPrefix(field, "set_round:")] = parseUint(value)
		}
	}
	return index, nil
}

// ReadAssignedSet reads one worker's set; ErrAssignedSetAbsent when the key
// is missing or expired.
func (store *RedisStore) ReadAssignedSet(ctx context.Context, workerID string) (AssignedSet, error) {
	if workerID == "" {
		return AssignedSet{}, errors.New("alarmd ownership: worker identity is required")
	}
	values, err := store.client.HGetAll(ctx, store.assignedSetKey(workerID)).Result()
	if err != nil {
		return AssignedSet{}, err
	}
	if len(values) == 0 {
		return AssignedSet{}, ErrAssignedSetAbsent
	}
	set := AssignedSet{WorkerID: workerID, Round: parseUint(values["round"])}
	if set.Round == 0 {
		return AssignedSet{}, errors.New("alarmd ownership: assigned set without a round")
	}
	var encoded []string
	if err := json.Unmarshal([]byte(values["query_groups"]), &encoded); err != nil {
		return AssignedSet{}, fmt.Errorf("alarmd ownership: decode assigned set: %w", err)
	}
	set.QueryGroups = make([]execution.QueryGroupIdentity, 0, len(encoded))
	for _, queryGroup := range encoded {
		if queryGroup == "" {
			return AssignedSet{}, errors.New("alarmd ownership: assigned set names an empty Query Group")
		}
		set.QueryGroups = append(set.QueryGroups, execution.QueryGroupIdentity(queryGroup))
	}
	return set, nil
}

func encodeAssignedQueryGroups(queryGroups []execution.QueryGroupIdentity) (string, error) {
	encoded := make([]string, 0, len(queryGroups))
	for _, queryGroup := range queryGroups {
		if queryGroup == "" {
			return "", errors.New("alarmd ownership: assigned set names an empty Query Group")
		}
		encoded = append(encoded, string(queryGroup))
	}
	sort.Strings(encoded)
	payload, err := json.Marshal(encoded)
	if err != nil {
		return "", fmt.Errorf("alarmd ownership: encode assigned set: %w", err)
	}
	return string(payload), nil
}

func (store *RedisStore) assignmentIndexKey() string {
	return store.prefix + ":assignment-index"
}

func (store *RedisStore) assignedSetKey(workerID string) string {
	digest := sha256.Sum256([]byte(workerID))
	return store.prefix + ":assigned:" + hex.EncodeToString(digest[:])
}

// AssignedSetDigest is the content identity the Leader keeps per worker to
// decide whether a set must be rewritten; it covers the sorted Query Group
// identities only.
func AssignedSetDigest(queryGroups []execution.QueryGroupIdentity) [sha256.Size]byte {
	sorted := make([]string, 0, len(queryGroups))
	for _, queryGroup := range queryGroups {
		sorted = append(sorted, string(queryGroup))
	}
	sort.Strings(sorted)
	hash := sha256.New()
	for _, queryGroup := range sorted {
		hash.Write([]byte(queryGroup))
		hash.Write([]byte{0})
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return digest
}
