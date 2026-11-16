// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

// The no-data memory read in parts (decision-020 batch 8, 2.7, plan 5.1-8).
//
// A Slot used to read a Plan's whole memory in one HGETALL and decode it whole,
// which at the bounds the per-group record exists for is hundreds of thousands
// of fields in one reply, one server block and one map held for the Slot. The
// round now reads the header, scans the groups a segment at a time, reads the
// header again to prove the scan saw one record, and reads the groups it judged
// absent by name, a window at a time. No read command returns more than one
// segment's fields, and nothing holds the memory whole.

// NoDataRosterSegmentGroups is how many fields one scan command of a memory
// asks for (HSCAN COUNT), and how many groups one by-name read carries.
//
// A field is a group key of tens to a hundred bytes and a value of one byte
// (present) or an absence's three timestamps, so a segment is some hundred
// kilobytes: 500 commands at 500,000 groups, none of which holds the server
// for more than a thousand fields' work. Above Redis's hash listpack bound
// (128 entries by default) a hash is a hashtable and the count is honoured; a
// smaller hash is returned whole in one reply, which is smaller than a segment.
const NoDataRosterSegmentGroups = 1000

// PlanNoDataRosterReader reads one Plan's no-data memory in parts.
//
// It is part of PlanNoDataStore: every store the worker writes the memory to
// is one it reads it from this way. The whole read (LoadNoData) stays for the
// readers that want a record whole and small - the evidence pages - and
// neither a Slot's round nor a supplement uses it: a supplement reads its own
// series' groups by name (ReadNoDataGroups).
type PlanNoDataRosterReader interface {
	// ReadNoDataHeaders reads, per item, the memory's header and nothing of its
	// groups: the snapshot LoadNoData returns with Groups empty. The record
	// is renewed on the read as LoadNoData renews it, and the strategy's
	// manual closes are read beside it, on their own: the close hash is
	// separate and small, and its failure costs the closes alone.
	ReadNoDataHeaders(context.Context, NoDataLoadRequest) (NoDataLoadResult, error)
	// RecheckNoDataHeader reads one memory's header again, and nothing else:
	// no renewal, no closes. A scan is one record's only when the header it
	// ends on is the header it began on.
	RecheckNoDataHeader(context.Context, PlanNoDataIdentity) (NoDataMemorySnapshot, error)
	// ScanNoDataGroups reads one segment of a memory's groups.
	ScanNoDataGroups(context.Context, NoDataScanRequest) (NoDataScanPage, error)
	// ReadNoDataGroups reads the named groups of a memory.
	ReadNoDataGroups(context.Context, NoDataGroupsRequest) (NoDataGroupsResult, error)
}

// NoDataScanRequest asks for one segment of a memory's groups.
type NoDataScanRequest struct {
	Identity PlanNoDataIdentity
	// Cursor is zero for the first segment and the previous page's Next after
	// it.
	Cursor uint64
	// Count is the most fields the segment asks for; the store may return
	// fewer, and a small hash more (NoDataRosterSegmentGroups).
	Count int
	// PresentAsOf is the header's, which a group stored as present is read
	// against: its last-seen time is the round the header names.
	PresentAsOf int64
}

// NoDataScanPage is one segment.
//
// A scan returns every field present for its whole length at least once and
// may return one more than once (HSCAN), so Groups may repeat a group of an
// earlier page or of this one. Deduplicating is the reader's, and it has to
// be: the round judges each group once, and the memory digest folds each once.
type NoDataScanPage struct {
	Groups []NoDataGroupMemory
	// Next is the cursor of the next segment; zero ends the scan.
	Next uint64
	// Status is FOUND for a page read, UNAVAILABLE for a read that failed and
	// may be retried, TERMINAL for a field this build cannot decode.
	Status     NoDataLoadStatus
	ReasonCode ReasonCode
}

// NoDataGroupsRequest asks for some groups of a memory by key.
type NoDataGroupsRequest struct {
	Identity    PlanNoDataIdentity
	Keys        []string
	PresentAsOf int64
}

// NoDataGroupsResult answers a NoDataGroupsRequest in its order: a group whose
// field is not there has an empty GroupKey.
type NoDataGroupsResult struct {
	Groups     []NoDataGroupMemory
	Status     NoDataLoadStatus
	ReasonCode ReasonCode
}

// ValidateNoDataScannedGroup checks one group read out of a record, by the
// rules a whole load checks every group by: a key, no negative time, and
// something remembered. Whether it repeats an earlier group is the reader's,
// since a scan may return a field twice.
func ValidateNoDataScannedGroup(group NoDataGroupMemory) error {
	if group.GroupKey == "" {
		return errors.New("alarmd execution: stored no-data group requires a key")
	}
	if group.LastSeen < 0 || group.FirstAbsent < 0 || group.SuppressedAt < 0 {
		return errors.New("alarmd execution: stored no-data timestamps must not be negative")
	}
	if group.LastSeen == 0 && group.FirstAbsent == 0 {
		return errors.New("alarmd execution: stored no-data group remembers nothing")
	}
	return nil
}

// NoDataStoredChange is how one group's stored value moves in a round: the
// value it is written as, or that it is deleted, or neither. loaded is what the
// record held for it (inMemory says it held anything) against the record's
// present-as-of, after what the round leaves it with (stored says it is kept
// with something to remember) against the round's. It is the per-group half of
// noDataMemoryDelta, for a round that sees one group at a time; a statement
// that replaces the whole record (replace) writes every stored group and
// deletes none.
func NoDataStoredChange(
	key string, loaded NoDataGroupMemory, inMemory bool, loadedPresentAsOf int64,
	after NoDataGroupMemory, stored bool, presentAsOf int64, replace bool,
) (set *NoDataGroupDelta, del bool) {
	loaded.GroupKey, after.GroupKey = key, key
	if !stored {
		return nil, inMemory && !replace
	}
	value := noDataGroupValue(after, presentAsOf)
	if !replace && inMemory && sameNoDataGroupValue(noDataGroupValue(loaded, loadedPresentAsOf), value) {
		return nil, false
	}
	return &value, false
}

// PlanNoDataSegmentedUpdate is one round's statement assembled one group at a
// time: what PlanNoDataMemoryUpdate carries, with the memory folded rather
// than held and the delta already taken.
type PlanNoDataSegmentedUpdate struct {
	Identity                  PlanNoDataIdentity
	GroupLimit                uint32
	DerivedFrom               NoDataRepresentation
	ExpectedMarkerRevision    uint64
	LoadedApplyVersion        ApplyVersion
	ApplyVersion              ApplyVersion
	ScheduleRevision          PlanScheduleRevision
	RosterVersion             string
	PresentAsOf               int64
	TrackingExhaustedAt       int64
	LoadedPresentAsOf         int64
	LoadedTrackingExhaustedAt int64
	// Set and Del are the delta, each group once, in any order
	// (NoDataStoredChange). A statement that replaces the whole record sets
	// every group the memory holds and deletes none.
	Set []NoDataGroupDelta
	Del []string
	// Memory is every group the resulting memory holds, folded once each.
	Memory *NoDataMemoryDigestAccumulator
	// Staged is the groups of a whole record staged ahead of the statement,
	// which Set then leaves out (PlanNoDataMutation.Staged).
	Staged *NoDataStaged
}

// BuildPlanNoDataSegmentedMutation is BuildPlanNoDataMutation for a round that
// never held the memory: the same Plan-level checks, the per-group one through
// the fold's newest last-seen time, and the same statement, digest for digest,
// as the whole memory would have produced.
func BuildPlanNoDataSegmentedMutation(update PlanNoDataSegmentedUpdate) (PlanNoDataMutation, error) {
	if update.Memory == nil {
		return PlanNoDataMutation{}, errors.New("alarmd execution: a segmented no-data statement folds no memory")
	}
	groups := update.Memory.Groups()
	if update.PresentAsOf < 0 || update.LoadedPresentAsOf < 0 {
		return PlanNoDataMutation{}, errors.New("alarmd execution: no-data present-as-of must not be negative")
	}
	if update.TrackingExhaustedAt < 0 || update.LoadedTrackingExhaustedAt < 0 {
		return PlanNoDataMutation{}, errors.New(
			"alarmd execution: no-data tracking-exhausted-at must not be negative")
	}
	// The three Plan-level refusals of BuildPlanNoDataMutation, stated against
	// the group count rather than the memory; see there for why each exists.
	if update.TrackingExhaustedAt != 0 && groups != 0 {
		return PlanNoDataMutation{}, fmt.Errorf(
			"alarmd execution: a Plan no-data memory exhausted at %d still holds %d groups",
			update.TrackingExhaustedAt, groups)
	}
	if update.LoadedTrackingExhaustedAt != 0 && update.TrackingExhaustedAt == 0 &&
		update.PresentAsOf <= update.LoadedPresentAsOf && groups == 0 {
		return PlanNoDataMutation{}, fmt.Errorf(
			"alarmd execution: a Plan no-data memory exhausted at %d was cleared while still empty and "+
				"without data arriving (present-as-of stayed at %d)",
			update.LoadedTrackingExhaustedAt, update.PresentAsOf)
	}
	if update.PresentAsOf < update.LoadedPresentAsOf {
		return PlanNoDataMutation{}, fmt.Errorf(
			"alarmd execution: no-data present-as-of %d is older than the record's %d",
			update.PresentAsOf, update.LoadedPresentAsOf)
	}
	if update.Memory.lastSeen > update.PresentAsOf {
		return PlanNoDataMutation{}, fmt.Errorf(
			"alarmd execution: no-data group %q was last seen at %d, after the Plan last had data at %d",
			update.Memory.lastSeenKey, update.Memory.lastSeen, update.PresentAsOf)
	}
	if groups > uint64(^uint32(0)) {
		return PlanNoDataMutation{}, errors.New("alarmd execution: no-data memory holds more groups than can be counted")
	}
	mutation := PlanNoDataMutation{
		Identity:               update.Identity,
		SchemaVersion:          WrittenNoDataMemorySchema,
		DerivedFrom:            update.DerivedFrom,
		LoadedApplyVersion:     update.LoadedApplyVersion,
		ExpectedMarkerRevision: update.ExpectedMarkerRevision,
		ApplyVersion:           update.ApplyVersion,
		ScheduleRevision:       update.ScheduleRevision,
		RosterVersion:          update.RosterVersion,
		PresentAsOf:            update.PresentAsOf,
		TrackingExhaustedAt:    update.TrackingExhaustedAt,
		GroupCount:             uint32(groups),
		GroupLimit:             update.GroupLimit,
		Set:                    update.Set,
		Del:                    update.Del,
		Staged:                 update.Staged,
	}
	sort.Slice(mutation.Set, func(left, right int) bool { return mutation.Set[left].GroupKey < mutation.Set[right].GroupKey })
	sort.Strings(mutation.Del)
	if update.Staged != nil && !mutation.ReplacesWholeRecord() {
		return PlanNoDataMutation{}, errors.New("alarmd execution: a no-data delta carries staged groups")
	}
	if update.Staged != nil && update.Staged.PresentAsOf != update.PresentAsOf {
		return PlanNoDataMutation{}, fmt.Errorf(
			"alarmd execution: no-data groups were staged against present-as-of %d and the statement is of %d",
			update.Staged.PresentAsOf, update.PresentAsOf)
	}
	if mutation.ReplacesWholeRecord() && update.Staged.Groups()+uint64(len(mutation.Set)) != groups {
		// A statement derived from no record is the whole memory, and the
		// staged write proves the staged field count against GroupCount: a
		// whole statement that sets fewer groups than it counts would be
		// refused there on every attempt.
		return PlanNoDataMutation{}, fmt.Errorf(
			"alarmd execution: a no-data statement replacing the record sets %d groups and staged %d of the %d it holds",
			len(mutation.Set), update.Staged.Groups(), groups)
	}
	if len(mutation.Set) == 0 {
		mutation.Set = nil
	}
	if len(mutation.Del) == 0 {
		mutation.Del = nil
	}
	memoryDigest, err := update.Memory.MemoryDigest(mutation)
	if err != nil {
		return PlanNoDataMutation{}, err
	}
	mutation.MemoryDigest = memoryDigest
	statement, err := derivePlanNoDataStatementDigest(mutation)
	if err != nil {
		return PlanNoDataMutation{}, err
	}
	mutation.MutationDigest = statement
	return mutation, nil
}
