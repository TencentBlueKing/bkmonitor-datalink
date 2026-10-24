// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodata

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// ManualCloses is what the owning worker read of one strategy's manual
// closes (decision-024): alert fingerprint to the time alarmd recorded the
// close, in unix seconds.
//
// A close stops the absence that was open when it was recorded and nothing
// after it. A group whose absence began at or before the close stops being
// tracked; once its data returns the group is remembered afresh, and an
// absence that begins later starts after the close and is tracked as any
// other. So a close never has to be consumed or deleted: it simply stops
// matching anything.
type ManualCloses struct {
	// Fingerprint names the alert a group's absence is sent as. It is the
	// emission's own function (GroupFingerprint) bound to the Plan, so the
	// fingerprint a close names and the one the round computes are the same
	// bytes.
	Fingerprint func(Group) (string, error)
	ClosedAt    map[string]int64
}

// GroupFingerprint is the alert fingerprint a group's no-data event carries:
// the group as the event's dimensions (SyntheticSeries.IdentityFields), hashed
// by the Plan's output identity exactly as the trigger hashes the record.
func GroupFingerprint(strategyID, businessID string, identity contract.MonitorOutputIdentity, group Group) (string, error) {
	return contract.MonitorDedupeMD5(strategyID, businessID, SyntheticSeries{Group: group}.IdentityFields(), identity)
}

// Matches says whether a group whose absence began at firstAbsent is stopped
// by a close of fingerprint.
func (closes *ManualCloses) Matches(fingerprint string, firstAbsent int64) bool {
	if closes == nil || firstAbsent == 0 {
		return false
	}
	at, closed := closes.ClosedAt[fingerprint]
	return closed && firstAbsent <= at
}

// applyManualCloses stops tracking every absence a close names, before the
// roster is derived and the round judges anything, and returns the memory the
// round goes on with and how many groups it stopped.
//
// The stop is recorded as suppressed_at whatever the roster source, which is
// how the horizon records a stop for a roster that expects the group from
// outside the memory: a stopped group gets no verdict, and its data arriving
// is the one thing that restarts it.
//
// A history group is marked too rather than forgotten, although forgetting is
// what the horizon does there. Forgetting the last group of a history roster
// empties it, and an empty roster with nothing present is read as an item
// with no groups, which opens the whole-item absence: a new alert in the
// round right after the user closed one. The horizon avoids that with its
// Plan-level exhausted mark; a close has no such mark, and a marked group
// keeps the roster from being empty by the same rule the target rosters use.
//
// A group the close cannot be computed for is left alone: a close that names
// nothing stops nothing, and that is never worse than not having the close.
func applyManualCloses(
	memory map[string]GroupMemory, closes *ManualCloses, evaluationTime int64,
) (map[string]GroupMemory, uint64) {
	if closes == nil || closes.Fingerprint == nil || len(closes.ClosedAt) == 0 || len(memory) == 0 {
		return memory, 0
	}
	var applied map[string]GroupMemory
	var stopped uint64
	for key, entry := range memory {
		if entry.FirstAbsent == 0 || entry.SuppressedAt != 0 {
			continue
		}
		group, named := ParseGroupKey(key)
		if !named {
			continue
		}
		fingerprint, err := closes.Fingerprint(group)
		if err != nil || !closes.Matches(fingerprint, entry.FirstAbsent) {
			continue
		}
		if applied == nil {
			applied = copyGroupMemory(memory)
		}
		stopped++
		entry.SuppressedAt = evaluationTime
		applied[key] = entry
	}
	if applied == nil {
		return memory, 0
	}
	return applied, stopped
}
