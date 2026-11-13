// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// recordBackstopRounds is how many rounds it takes the backstop to read
// every record once: each round reads its share, the population over this.
const recordBackstopRounds = 60

// errRecordTermChanged is a read that began in one term and ended in
// another: what it would answer for the records it did not read belongs to
// neither, and a record missing from the answer would be placed.
var errRecordTermChanged = errors.New("alarmd: the Control Leader term changed during a record read")

// leaderActivations is what a Control Leader term needs of the catalog:
// what the activations after a revision wrote onto the records, and the
// activations of its own process told as they are made, for the record
// cache; and the catalog's renewals begun afresh with each term
// (controlplane's catalogRenewals).
type leaderActivations interface {
	ActivationChangesSince(ctx context.Context, since uint64, skip func(uint64) bool) (controlplane.ActivationChanges, error)
	OnRecordTimelineRevisions(func(revision uint64, timelines map[execution.QueryGroupIdentity]uint64))
	BeginLeaderTerm(epoch uint64)
}

// leaderRecordCache is the Control Leader's copy of the Assignment records
// for one term, in front of the store the reconcile round, the moves, the
// content scopes and the stored view read and write the records through.
// A round used to read every record; it now reads one only when the copy
// cannot be trusted to say (observability.RecordReadReasons).
//
// Safety does not rest on the copy. Every publication is a compare-and-set
// on the record's revision, judged in the store's script with the holder's
// liveness, so a copy behind the store costs a refused publication and a read
// of that record, never a wrong write. The copy is kept by what the Leader
// itself writes - the record each publication answers with, the timeline
// revisions its own activations write (noteActivation), the records its
// sweep removes - and by reading again what it cannot see: another
// process's activation (the delta of each activation since, read in
// prepareRound), a publication whose outcome is not known, and a Worker's
// promotion of its pending content, which the backstop's share of reads in
// each round finds within recordBackstopRounds rounds.
type leaderRecordCache struct {
	store       scheduler.AssignmentStore
	activations leaderActivations

	mu sync.Mutex
	// epoch is the term the copy belongs to, zero between terms.
	epoch uint64
	// filled is the term's copy begun by its first read, of everything that
	// read was asked for; until then a read reads everything, as term_start.
	filled bool
	// activation is the record revision of the last activation whose writes
	// the copy holds; pending the one the next read of everything will: the
	// earliest header read before it, so an activation between two such
	// reads is not taken as held.
	activation, pending uint64
	// own are the activations of this process since activation, whose
	// writes are already in the copy.
	own     map[uint64]struct{}
	entries map[execution.QueryGroupIdentity]cachedRecord
	// versions moves on every change the copy makes to a Query Group, so a
	// read that began before one does not overwrite it with what it read.
	versions map[execution.QueryGroupIdentity]uint64
	// owed is the Query Groups whose record is read next, and why.
	owed   map[execution.QueryGroupIdentity]string
	cursor int

	// What the reads found, until the next round line takes it.
	reads      map[string]int
	mismatches map[string]int
	sample     []observability.RecordMismatch
}

// cachedRecord is a record, or that there is none.
type cachedRecord struct {
	record  ownership.AssignmentRecord
	present bool
}

func newLeaderRecordCache(store scheduler.AssignmentStore, activations leaderActivations) *leaderRecordCache {
	return &leaderRecordCache{store: store, activations: activations}
}

// leadLocked moves the copy to the term epoch names, empty unless it was
// that term's already.
func (cache *leaderRecordCache) leadLocked(epoch uint64) {
	if cache.epoch == epoch {
		return
	}
	cache.epoch = epoch
	cache.filled = false
	cache.activation, cache.pending, cache.cursor = 0, 0, 0
	cache.own = map[uint64]struct{}{}
	cache.entries = map[execution.QueryGroupIdentity]cachedRecord{}
	cache.versions = map[execution.QueryGroupIdentity]uint64{}
	cache.owed = map[execution.QueryGroupIdentity]string{}
}

// lead moves the copy to the term an authority names.
func (cache *leaderRecordCache) lead(epoch uint64) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.leadLocked(epoch)
}

// stepDown drops the copy with the term.
func (cache *leaderRecordCache) stepDown() {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.leadLocked(0)
}

// prepareRound brings the copy to the activation header before a round:
// one small read when nothing was activated, and when another process
// activated, the delta of each activation since, whose Query Groups are
// read again. A delta that cannot say has every record the copy holds read
// again. Then it owes the round's share of the backstop, in turn over
// population.
func (cache *leaderRecordCache) prepareRound(
	ctx context.Context,
	authority ownership.PublicationAuthority,
	population []execution.QueryGroupIdentity,
) error {
	epoch := authority.Fence.OwnerEpoch
	cache.mu.Lock()
	cache.leadLocked(epoch)
	since := cache.activation
	own := make(map[uint64]struct{}, len(cache.own))
	for revision := range cache.own {
		own[revision] = struct{}{}
	}
	cache.mu.Unlock()
	if cache.activations != nil {
		changes, err := cache.activations.ActivationChangesSince(ctx, since, func(revision uint64) bool {
			_, mine := own[revision]
			return mine
		})
		if err != nil {
			return err
		}
		cache.mu.Lock()
		defer cache.mu.Unlock()
		if cache.epoch != epoch {
			return nil
		}
		switch {
		case !cache.filled:
			cache.notePendingLocked(changes.Revision)
		case cache.activation != since:
			// Another round moved the copy meanwhile; its reading stands.
		case !changes.Complete:
			// A delta that cannot say has every record the copy holds read
			// again, as the reads come to each: not "the next read is of
			// everything", which a read of a few - the content scopes' -
			// would end with the rest unread and no later delta to name
			// them. A copy begun before any header was read has no revision
			// to start from: that is the term's start.
			reason := "foreign_cutover"
			if since == 0 {
				reason = "term_start"
			}
			for queryGroup := range cache.entries {
				cache.oweLocked(queryGroup, reason)
			}
			cache.advanceLocked(changes.Revision)
		default:
			for _, queryGroup := range changes.QueryGroups {
				cache.oweLocked(queryGroup, "foreign_cutover")
			}
			cache.advanceLocked(changes.Revision)
		}
	} else {
		cache.mu.Lock()
		defer cache.mu.Unlock()
	}
	if !cache.filled || len(population) == 0 {
		return nil
	}
	share := (len(population) + recordBackstopRounds - 1) / recordBackstopRounds
	for offset := 0; offset < share; offset++ {
		queryGroup := population[(cache.cursor+offset)%len(population)]
		if _, known := cache.entries[queryGroup]; known {
			cache.oweLocked(queryGroup, "backstop")
		}
	}
	cache.cursor = (cache.cursor + share) % len(population)
	return nil
}

// notePendingLocked is a header read before the next read of everything.
func (cache *leaderRecordCache) notePendingLocked(revision uint64) {
	if cache.pending == 0 || revision < cache.pending {
		cache.pending = revision
	}
}

// readStoredView reads the records of the stored view a new term publishes,
// the header read before them at revision: the read of everything that fills
// the term's copy, which the term's first round then answers from.
func (cache *leaderRecordCache) readStoredView(
	ctx context.Context,
	authority ownership.PublicationAuthority,
	revision uint64,
	queryGroups []execution.QueryGroupIdentity,
) (map[execution.QueryGroupIdentity]ownership.AssignmentRecord, ownership.ControlReadStats, error) {
	cache.mu.Lock()
	cache.leadLocked(authority.Fence.OwnerEpoch)
	if !cache.filled {
		cache.notePendingLocked(revision)
	}
	cache.mu.Unlock()
	return cache.ReadAssignments(ctx, queryGroups)
}

// oweLocked has the Query Group's record read next, for the first reason
// given.
func (cache *leaderRecordCache) oweLocked(queryGroup execution.QueryGroupIdentity, reason string) {
	if _, owed := cache.owed[queryGroup]; !owed {
		cache.owed[queryGroup] = reason
	}
}

// advanceLocked moves the copy to an activation revision, forgetting the
// own activations it has passed.
func (cache *leaderRecordCache) advanceLocked(revision uint64) {
	cache.activation = revision
	for own := range cache.own {
		if own <= revision {
			delete(cache.own, own)
		}
	}
}

// noteActivation is an activation of this process that wrote these timeline
// revisions onto the records that exist: the copy takes them as written,
// and the activation's delta is not read for them.
func (cache *leaderRecordCache) noteActivation(revision uint64, timelines map[execution.QueryGroupIdentity]uint64) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.epoch == 0 {
		return
	}
	for queryGroup, timeline := range timelines {
		cache.versions[queryGroup]++
		if entry, known := cache.entries[queryGroup]; known && entry.present {
			entry.record.TimelineRecordRevision = timeline
			cache.entries[queryGroup] = entry
		}
	}
	if revision > cache.activation {
		cache.own[revision] = struct{}{}
	}
}

// retain forgets the Query Groups a sweep removed the records of: every one
// not in keep. One that comes back is read as entered.
func (cache *leaderRecordCache) retain(keep map[execution.QueryGroupIdentity]struct{}) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for queryGroup := range cache.entries {
		if _, kept := keep[queryGroup]; !kept {
			delete(cache.entries, queryGroup)
			delete(cache.owed, queryGroup)
			delete(cache.versions, queryGroup)
		}
	}
}

// ListReadyWorkers is the store's.
func (cache *leaderRecordCache) ListReadyWorkers(ctx context.Context, at time.Time) ([]ownership.WorkerRegistration, ownership.ControlReadStats, error) {
	return cache.store.ListReadyWorkers(ctx, at)
}

// ReadAssignments answers from the copy, reading the records it cannot
// say: every one until the term's copy is filled, and after that the ones
// owed and the ones it has never seen. Outside a term it reads every one and
// keeps none.
func (cache *leaderRecordCache) ReadAssignments(
	ctx context.Context,
	queryGroups []execution.QueryGroupIdentity,
) (map[execution.QueryGroupIdentity]ownership.AssignmentRecord, ownership.ControlReadStats, error) {
	cache.mu.Lock()
	epoch := cache.epoch
	if epoch == 0 {
		cache.mu.Unlock()
		return cache.store.ReadAssignments(ctx, queryGroups)
	}
	fill := !cache.filled
	reasons := make(map[execution.QueryGroupIdentity]string)
	for _, queryGroup := range queryGroups {
		switch reason, owed := cache.owed[queryGroup]; {
		case fill:
			reasons[queryGroup] = "term_start"
		case owed:
			reasons[queryGroup] = reason
		default:
			if _, known := cache.entries[queryGroup]; !known {
				reasons[queryGroup] = "entered"
			}
		}
	}
	read := make([]execution.QueryGroupIdentity, 0, len(reasons))
	versions := make(map[execution.QueryGroupIdentity]uint64, len(reasons))
	for _, queryGroup := range queryGroups {
		if _, reading := reasons[queryGroup]; reading {
			read = append(read, queryGroup)
			versions[queryGroup] = cache.versions[queryGroup]
		}
	}
	pending := cache.pending
	cache.mu.Unlock()

	var fetched map[execution.QueryGroupIdentity]ownership.AssignmentRecord
	var stats ownership.ControlReadStats
	if len(read) > 0 {
		var err error
		fetched, stats, err = cache.store.ReadAssignments(ctx, read)
		if err != nil {
			return nil, stats, err
		}
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.epoch != epoch {
		return nil, stats, errRecordTermChanged
	}
	// A read of everything overwrites each record it read - but one a write
	// overtook while it was out - and leaves the rest: outside the
	// population, they go with the next sweep.
	if fill && !cache.filled {
		cache.filled = true
		cache.advanceLocked(pending)
		cache.pending = 0
	}
	for _, queryGroup := range read {
		record, present := fetched[queryGroup]
		cache.storeReadLocked(queryGroup, reasons[queryGroup], versions[queryGroup], cachedRecord{record: record, present: present})
	}
	// The copy's word, which a write made while the read was out is newer
	// than; what was read only where the copy has none.
	answer := make(map[execution.QueryGroupIdentity]ownership.AssignmentRecord, len(queryGroups))
	for _, queryGroup := range queryGroups {
		if entry, known := cache.entries[queryGroup]; known {
			if entry.present {
				answer[queryGroup] = entry.record
			}
			continue
		}
		if record, found := fetched[queryGroup]; found {
			answer[queryGroup] = record
		}
	}
	return answer, stats, nil
}

// ReadAssignment reads one record, the one a refused publication left owed,
// and keeps it.
func (cache *leaderRecordCache) ReadAssignment(ctx context.Context, queryGroup execution.QueryGroupIdentity) (ownership.AssignmentRecord, error) {
	cache.mu.Lock()
	epoch, version := cache.epoch, cache.versions[queryGroup]
	reason, owed := cache.owed[queryGroup]
	if !owed {
		reason = "entered"
	}
	cache.mu.Unlock()
	record, err := cache.store.ReadAssignment(ctx, queryGroup)
	if err != nil && !errors.Is(err, ownership.ErrAssignmentAbsent) {
		return record, err
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if epoch != 0 && cache.epoch == epoch {
		cache.storeReadLocked(queryGroup, reason, version, cachedRecord{record: record, present: err == nil})
	}
	return record, err
}

// storeReadLocked keeps what a read found, unless the copy changed the
// Query Group since the read began: what the change wrote is newer, and a
// change the copy could only note (an activation over a record it did not
// hold) leaves it unknown, read as entered next.
func (cache *leaderRecordCache) storeReadLocked(queryGroup execution.QueryGroupIdentity, reason string, version uint64, read cachedRecord) {
	if cache.reads == nil {
		cache.reads = map[string]int{}
	}
	cache.reads[reason]++
	if cache.versions[queryGroup] != version {
		return
	}
	if reason == "backstop" {
		if cached, known := cache.entries[queryGroup]; known {
			cache.compareLocked(queryGroup, cached, read)
		}
	}
	cache.entries[queryGroup] = read
	delete(cache.owed, queryGroup)
}

// compareLocked counts each field the copy had wrong.
func (cache *leaderRecordCache) compareLocked(queryGroup execution.QueryGroupIdentity, cached, stored cachedRecord) {
	type field struct{ name, cached, stored string }
	var fields []field
	if cached.present != stored.present {
		fields = append(fields, field{"presence", strconv.FormatBool(cached.present), strconv.FormatBool(stored.present)})
	} else if cached.present {
		left, right := cached.record, stored.record
		for _, candidate := range []field{
			{"record_revision", strconv.FormatUint(left.RecordRevision, 10), strconv.FormatUint(right.RecordRevision, 10)},
			{"desired_worker_id", left.DesiredWorkerID, right.DesiredWorkerID},
			{"placement_reason", string(left.PlacementReason), string(right.PlacementReason)},
			{"timeline_record_revision", strconv.FormatUint(left.TimelineRecordRevision, 10), strconv.FormatUint(right.TimelineRecordRevision, 10)},
			{"content_scope", left.ContentScope, right.ContentScope},
			{"pending_content_scope", left.PendingContentScope, right.PendingContentScope},
			{"effective_at_ms", strconv.FormatInt(left.EffectiveAt.UnixMilli(), 10), strconv.FormatInt(right.EffectiveAt.UnixMilli(), 10)},
		} {
			if candidate.cached != candidate.stored {
				fields = append(fields, candidate)
			}
		}
	}
	if len(fields) == 0 {
		return
	}
	if cache.mismatches == nil {
		cache.mismatches = map[string]int{}
	}
	for _, mismatch := range fields {
		cache.mismatches[mismatch.name]++
		if !observability.RecordFieldWorkerWritten(mismatch.name) && len(cache.sample) < observability.MaxRecordMismatchSamples {
			cache.sample = append(cache.sample, observability.RecordMismatch{
				QueryGroup: string(queryGroup), Field: mismatch.name, Cached: mismatch.cached, Stored: mismatch.stored,
			})
		}
	}
}

// PublishAssignment is the store's, kept in the copy (wroteLocked).
func (cache *leaderRecordCache) PublishAssignment(
	ctx context.Context,
	authority ownership.PublicationAuthority,
	decision ownership.AssignmentDecision,
) (ownership.AssignmentRecord, error) {
	record, err := cache.store.PublishAssignment(ctx, authority, decision)
	cache.wrote(decision.QueryGroup, record, err)
	return record, err
}

// PublishAssignmentReporting is the store's, kept in the copy (wroteLocked).
func (cache *leaderRecordCache) PublishAssignmentReporting(
	ctx context.Context,
	authority ownership.PublicationAuthority,
	decision ownership.AssignmentDecision,
) (ownership.AssignmentRecord, ownership.HolderKind, error) {
	record, holder, err := cache.store.PublishAssignmentReporting(ctx, authority, decision)
	cache.wrote(decision.QueryGroup, record, err)
	return record, holder, err
}

// wrote keeps what a publication did: the record it answered with; for one
// refused as changed, that it is read again; for one that failed without
// saying whether it was written, the same, and the copy keeps nothing from
// before it - the CAS would refuse a stale revision, but the round would
// plan on the holder it names until then. Between terms there is no copy to
// keep it in.
func (cache *leaderRecordCache) wrote(queryGroup execution.QueryGroupIdentity, record ownership.AssignmentRecord, err error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.epoch == 0 {
		return
	}
	cache.versions[queryGroup]++
	switch {
	case err == nil:
		cache.entries[queryGroup] = cachedRecord{record: record, present: true}
		delete(cache.owed, queryGroup)
	case errors.Is(err, ownership.ErrAssignmentConflict):
		delete(cache.entries, queryGroup)
		cache.owed[queryGroup] = "conflict"
	default:
		delete(cache.entries, queryGroup)
		cache.owed[queryGroup] = "unknown_outcome"
	}
}

// takeRoundFacts is what the reads found since the last round line.
func (cache *leaderRecordCache) takeRoundFacts() (map[string]int, map[string]int, []observability.RecordMismatch) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	reads, mismatches, sample := cache.reads, cache.mismatches, cache.sample
	cache.reads, cache.mismatches, cache.sample = nil, nil, nil
	return reads, mismatches, sample
}
