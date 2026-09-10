// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"sync"
)

// stateMutationDigestPayload is the exact content the State mutation digest is
// derived over. It is a named type so the digest input and the memo key are
// built from one declaration: a field that reaches the digest also reaches the
// key, and a field that does not reach the key cannot reach the digest.
type stateMutationDigestPayload struct {
	Identity        StateKeyIdentity            `json:"identity"`
	ApplyVersion    ApplyVersion                `json:"apply_version"`
	AffectedRecords []RecordAnchor              `json:"affected_records"`
	SeriesGuard     *StateGuardFact             `json:"series_guard,omitempty"`
	Levels          []RuntimeLevelStateMutation `json:"levels"`
	Points          []StateHistoryPoint         `json:"points"`
}

// One evaluated series derives its mutation digest once and is then asked for
// it again by every contract check the mutation passes on its way to storage:
// the evaluation result contract, the store admission and the store apply. All
// of them re-derive from content, and the canonical encoder is the expensive
// part of that: it encodes, rescans, decodes into generic values and re-encodes
// with sorted keys, which measures at about 19 us for one retained point and
// 130 us for thirty. Keying that result by the content itself turns every
// repeat into a plain JSON encode plus SHA-256, roughly a tenth of the cost,
// and leaves the derived digest bit-identical.
//
// The table is direct-mapped and fixed size: 64 shards of 64 entries, about
// 460 KiB in total. A bucket collision only overwrites and costs the next
// caller a re-derivation, and a hit requires the full 32-byte key to match, so
// returning a digest for content that did not produce it would take a SHA-256
// collision.
const (
	stateMutationMemoShards  = 64
	stateMutationMemoBuckets = 64
)

type stateMutationMemoShard struct {
	mu      sync.Mutex
	keys    [stateMutationMemoBuckets][sha256.Size]byte
	digests [stateMutationMemoBuckets]MutationDigest
}

type stateMutationMemo struct {
	shards [stateMutationMemoShards]stateMutationMemoShard
}

var stateMutationDigestMemo stateMutationMemo

// stateMutationDigestKey reports the content key of payload. A payload that
// cannot be encoded has no key; the caller then derives without the memo and
// the canonical encoder reports the real error.
func stateMutationDigestKey(payload stateMutationDigestPayload) ([sha256.Size]byte, bool) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return [sha256.Size]byte{}, false
	}
	return sha256.Sum256(encoded), true
}

func (memo *stateMutationMemo) position(key [sha256.Size]byte) (*stateMutationMemoShard, int) {
	shard := binary.LittleEndian.Uint32(key[0:4]) % stateMutationMemoShards
	bucket := binary.LittleEndian.Uint32(key[4:8]) % stateMutationMemoBuckets
	return &memo.shards[shard], int(bucket)
}

func (memo *stateMutationMemo) load(key [sha256.Size]byte) (MutationDigest, bool) {
	shard, bucket := memo.position(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	if shard.digests[bucket] == "" || shard.keys[bucket] != key {
		return "", false
	}
	return shard.digests[bucket], true
}

func (memo *stateMutationMemo) store(key [sha256.Size]byte, digest MutationDigest) {
	if digest == "" {
		return
	}
	shard, bucket := memo.position(key)
	shard.mu.Lock()
	shard.keys[bucket] = key
	shard.digests[bucket] = digest
	shard.mu.Unlock()
}
