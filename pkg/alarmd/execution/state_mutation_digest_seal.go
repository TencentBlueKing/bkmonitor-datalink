// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import (
	"crypto/sha256"
	"encoding/json"
)

// stateMutationDigestPayload is the exact content the State mutation digest is
// derived over. It is a named type so the digest input and the content key are
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
// the evaluation result contract, the series warming convergence check, the
// store admission and the store apply. All of them establish the digest from
// content, and the canonical encoder is the expensive part of that: it encodes,
// rescans, decodes into generic values and re-encodes with sorted keys, which
// measures at about 19 us for one retained point and 130 us for thirty. A plain
// JSON encode plus SHA-256 over the same payload is roughly a tenth of that.
//
// The seal carries the derived digest on the mutation that produced it,
// together with the content key of the payload it was derived from. A repeat
// check still encodes the payload and hashes it, so content that changed after
// the build is caught exactly as before; what the seal removes is the canonical
// round trip, not the comparison against content.
//
// A per-mutation seal is what a shared table cannot be, and the reason is the
// distance between a build and each check rather than how much runs at once. A
// build and the two checks that follow it are consecutive statements in one
// goroutine, so no concurrent evaluation has a window to land in that bucket
// and those two are answered whatever the concurrency. The other two run after
// the whole Slot has been evaluated, with one Slot's worth of unrelated builds
// in between, and a fixed table that overwrites on collision loses the entry to
// any one of them. So what decides the outcome is a property of the Slot, not
// of the scheduler: a table has to hold one Slot's worth of mutations to answer
// the later two checks, and a Slot's size is the thing that grows with the
// tenant. The seal holds one entry per mutation and lives exactly as long as
// the mutation does.
//
// The seal cannot reach the digest or the stored envelope: it is unexported, so
// the canonical encoder skips it and the runtime envelope lists its fields by
// name. It is written once, before BuildStateMutation returns, and is never
// modified afterwards, so a copy of the mutation shares an immutable value.
type sealedStateMutationDigest struct {
	contentKey [sha256.Size]byte
	digest     MutationDigest
}

// stateMutationDigestPayloadOf reports the digest input of mutation. Both the
// derivation and the content key read it, so neither can drift from the other.
func stateMutationDigestPayloadOf(mutation StateMutation) stateMutationDigestPayload {
	return stateMutationDigestPayload{
		Identity: mutation.Identity, ApplyVersion: mutation.ApplyVersion,
		AffectedRecords: mutation.AffectedRecords, SeriesGuard: mutation.SeriesGuard,
		Levels: mutation.Levels, Points: mutation.Points,
	}
}

// stateMutationContentKey reports the content key of payload. A payload that
// cannot be encoded has no key; the caller then derives without the seal and
// the canonical encoder reports the real error.
func stateMutationContentKey(payload stateMutationDigestPayload) ([sha256.Size]byte, bool) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return [sha256.Size]byte{}, false
	}
	return sha256.Sum256(encoded), true
}

// answers reports whether the seal was derived from this exact content and
// pins this exact digest. An absent seal answers nothing, and so does a payload
// that has no key. Answering for content that did not produce the digest would
// take a SHA-256 collision.
func (sealed *sealedStateMutationDigest) answers(digest MutationDigest, key [sha256.Size]byte, keyed bool) bool {
	return sealed != nil && keyed && sealed.digest == digest && sealed.contentKey == key
}
