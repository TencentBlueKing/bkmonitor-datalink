// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	alarmdprogress "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/progress"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

const scheduleTimelineSchemaVersion = "alarmd-control-schedule-timeline-v1"

var (
	ErrScheduleUnavailable = errors.New("alarmd controlplane: schedule unavailable")
	ErrScheduleConflict    = errors.New("alarmd controlplane: schedule activation conflict")
)

type FreezeSlotContractFailureClass string

const (
	FreezeSlotFailureScheduleRead       FreezeSlotContractFailureClass = "schedule_read"
	FreezeSlotFailureScheduleMismatch   FreezeSlotContractFailureClass = "schedule_mismatch"
	FreezeSlotFailureSnapshotRead       FreezeSlotContractFailureClass = "snapshot_read"
	FreezeSlotFailurePlanMaterialize    FreezeSlotContractFailureClass = "plan_materialize"
	FreezeSlotFailureInputClosure       FreezeSlotContractFailureClass = "input_closure"
	FreezeSlotFailureContractValidation FreezeSlotContractFailureClass = "contract_validation"
)

// FreezeSlotContractError identifies the bounded stage that rejected a frozen
// Slot. Error deliberately omits the underlying fact while Unwrap preserves it
// for errors.Is/errors.As and local tests.
type FreezeSlotContractError struct {
	Class FreezeSlotContractFailureClass
	Err   error
}

func (err *FreezeSlotContractError) Error() string {
	return "alarmd controlplane: FreezeSlotContract failed"
}

func (err *FreezeSlotContractError) Unwrap() error { return err.Err }

func freezeSlotContractError(class FreezeSlotContractFailureClass, err error) error {
	return &FreezeSlotContractError{Class: class, Err: err}
}

type DeterministicScheduleError struct{ Err error }

func (err *DeterministicScheduleError) Error() string {
	return fmt.Sprintf("alarmd controlplane: deterministic-invalid persisted Schedule: %v", err.Err)
}

func (err *DeterministicScheduleError) Unwrap() error { return err.Err }

type persistedScheduleSegment struct {
	Schedule         execution.FrozenQueryGroupSchedule `json:"schedule"`
	Plans            []PlanActivationRecord             `json:"plans"`
	ReactivatedAfter *execution.EvaluationTime          `json:"reactivated_after,omitempty"`
}

type persistedScheduleTimeline struct {
	SchemaVersion  string                       `json:"schema_version"`
	RecordRevision uint64                       `json:"record_revision"`
	QueryGroup     execution.QueryGroupIdentity `json:"query_group"`
	Segments       []persistedScheduleSegment   `json:"segments"`
	RetiredAt      *execution.EvaluationTime    `json:"retired_at,omitempty"`
}

// retiredBefore reports whether activating this Query Group at boundary
// reopens a retired timeline. It is the one predicate behind reactivation:
// the CAS side uses it to append a ReactivatedAfter Segment instead of
// creating a fresh timeline, and the reconciler uses it to restart the Query
// Group's Plans through WARMING. Both read the persisted timeline, so the
// answer does not depend on whether the Draining projection still remembers
// the Query Group.
func (timeline persistedScheduleTimeline) retiredBefore(boundary execution.EvaluationTime) bool {
	return timeline.RetiredAt != nil && *timeline.RetiredAt < boundary
}

type scheduleTimelineUpdate struct {
	expected []byte
	next     persistedScheduleTimeline
}

// Every Schedule timeline written here carries the same Catalog TTL as the
// Snapshot occurrence and the Active Set written next to it in ARGV[5]. No
// Slot can execute without its Snapshot, so a timeline that outlives the
// Snapshot it describes serves nobody; the Control Leader renews the
// timelines the current Activation still references on the same tick that
// renews the Snapshot, and every other timeline expires with its publication.
//
// The per-key EXISTS guard below runs only on the first activation a
// deployment ever performs: CompareAndSetInitialScheduleActivation rejects
// every expectation whose RecordRevision is not zero, so a running cluster
// never executes this script. A timeline left behind by a retired Query
// Group can only exist after at least one activation, so a retired key cannot
// reach this path through any code path. The one way to reach it is an
// administrative deletion of the activation header and records while the
// timeline keys are left in place: the header is then unavailable, this
// script refuses the leftover keys, InitialScheduleActivator.Ensure swallows
// that conflict and reads the header back, and every tick fails as
// unavailable until the leftover timelines expire with their Catalog TTL.
// That is the intended outcome of a partial deletion, not a gap, and "the key
// must not exist" is the right guard here. It is deliberately not relaxed to
// look symmetric with the branch of the cutover script that takes an empty
// expected value: relaxing it would let a stale timeline be silently
// overwritten on first activation with no test able to catch it. A retired
// Query Group that returns is handled by the caller of the cutover script,
// the reactivation branch of CompareAndSetPublicationScheduleActivation,
// which reads the persisted timeline and appends a ReactivatedAfter Segment
// instead of creating a fresh timeline, so expected carries the real prior
// bytes.
const compareAndSetInitialSchedulesScript = `
local header = redis.call('GET', KEYS[1])
if ARGV[1] == '' then
  if header then return 0 end
elseif not header or header ~= ARGV[1] then
  return 0
end
if redis.call('GET', KEYS[3]) ~= ARGV[4] then return 0 end
for index = 4, #KEYS do
  if redis.call('EXISTS', KEYS[index]) == 1 then return 0 end
end
redis.call('PEXPIRE', KEYS[3], ARGV[5])
redis.call('SET', KEYS[1], ARGV[2])
redis.call('SET', KEYS[2], ARGV[3])
for index = 4, #KEYS do
  redis.call('SET', KEYS[index], ARGV[index + 2], 'PX', ARGV[5])
end
return 1
`

// Each timeline is fenced on the SHA-1 of the bytes the caller read, not on
// the bytes themselves: the call already carries every rewritten timeline
// once, and carrying the old bytes as well doubled a payload that had grown
// past what the write timeout allows. An empty expectation still means the
// key must be absent, and an existing key with empty content is present
// (an empty Lua string is true), so the two cases stay apart.
// compareAndSetCutoverSchedulesScript writes one activation: the header,
// the activation record, the Active Set's expiry, the activation delta and
// every Schedule timeline the activation changed, fenced on the bytes each
// timeline had when it was read. KEYS[4] is the delta of this record
// revision: the Query Groups whose timelines this same script writes, so a
// Worker that sees the new header knows which cached timelines to drop and
// which to keep, from the same write that changed them.
const compareAndSetCutoverSchedulesScript = `
local header = redis.call('GET', KEYS[1])
if not header or header ~= ARGV[1] then return 0 end
if redis.call('GET', KEYS[3]) ~= ARGV[4] then return 0 end
for index = 5, #KEYS do
  local current = redis.call('GET', KEYS[index])
  local expected_index = 2 * index - 3
  local expected = ARGV[expected_index]
  if expected == '' then
    if current then return 0 end
  elseif not current or redis.sha1hex(current) ~= expected then
    return 0
  end
end
redis.call('PEXPIRE', KEYS[3], ARGV[5])
redis.call('SET', KEYS[1], ARGV[2])
redis.call('SET', KEYS[2], ARGV[3])
redis.call('SET', KEYS[4], ARGV[6], 'PX', ARGV[5])
for index = 5, #KEYS do
  local next_index = 2 * index - 2
  redis.call('SET', KEYS[index], ARGV[next_index], 'PX', ARGV[5])
end
return 1
`

const (
	activationDeltaSchemaVersion = "alarmd-control-activation-delta-v1"
	// activationDeltaMaxQueryGroups bounds the delta a Worker reads after a
	// header change. Past it the delta says "full" and the Worker drops every
	// cached timeline, which is what it did before deltas existed and is
	// cheaper than reading a list that size.
	activationDeltaMaxQueryGroups = 2000
)

// activationDelta is what one activation changed, keyed by its record
// revision: the Query Groups whose Schedule timelines it wrote. A Worker
// keeps every other cached timeline across the header change. Full means the
// list was too long to be worth carrying, and reads as "drop everything".
type activationDelta struct {
	SchemaVersion  string                         `json:"schema_version"`
	RecordRevision uint64                         `json:"record_revision"`
	Full           bool                           `json:"full,omitempty"`
	QueryGroups    []execution.QueryGroupIdentity `json:"query_groups,omitempty"`
}

func encodeActivationDelta(revision uint64, changed []execution.QueryGroupIdentity) ([]byte, error) {
	delta := activationDelta{SchemaVersion: activationDeltaSchemaVersion, RecordRevision: revision}
	if len(changed) > activationDeltaMaxQueryGroups {
		delta.Full = true
	} else {
		delta.QueryGroups = append([]execution.QueryGroupIdentity(nil), changed...)
		sort.Slice(delta.QueryGroups, func(i, j int) bool { return delta.QueryGroups[i] < delta.QueryGroups[j] })
	}
	return json.Marshal(delta)
}

func (repository *RedisCatalogRepository) activationDeltaKey(revision uint64) string {
	return repository.prefix + ":activation_delta:" + strconv.FormatUint(revision, 10)
}

// loadActivationDelta reads the delta of one record revision. Absent, or
// unreadable, or written by another schema, it reports not present, and the
// reader drops everything as it would have without deltas.
func (repository *RedisCatalogRepository) loadActivationDelta(ctx context.Context, revision uint64) (activationDelta, bool, error) {
	payload, err := repository.client.Get(ctx, repository.activationDeltaKey(revision)).Bytes()
	if errors.Is(err, redis.Nil) {
		return activationDelta{}, false, nil
	}
	if err != nil {
		return activationDelta{}, false, activationDependencyIO(err)
	}
	var delta activationDelta
	if err := json.Unmarshal(payload, &delta); err != nil ||
		delta.SchemaVersion != activationDeltaSchemaVersion || delta.RecordRevision != revision {
		return activationDelta{}, false, nil
	}
	return delta, true, nil
}

func (repository *RedisCatalogRepository) persistActivationRefUpgrade(ctx context.Context, expected ActivationExpectation, next ActivationState, activePayload []byte) error {
	expectedHeader, err := activationHeader(expected.RecordRevision, expected.Current, expected.Pending)
	if err != nil {
		return err
	}
	nextHeader, err := activationHeader(next.RecordRevision, next.Current, next.Pending)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(next)
	if err != nil {
		return err
	}
	// A ref upgrade rewrites the activation record and nothing else: the delta
	// it leaves names no Query Group, so a Worker keeps every cached timeline.
	delta, err := encodeActivationDelta(next.RecordRevision, nil)
	if err != nil {
		return err
	}
	changed, err := repository.client.Eval(ctx, compareAndSetCutoverSchedulesScript,
		[]string{repository.activationHeaderKey(), repository.activationKey(), repository.activeQGSetKey(next.ActiveQGSetRef.Digest),
			repository.activationDeltaKey(next.RecordRevision)},
		expectedHeader, nextHeader, payload, activePayload, repository.ttl.Milliseconds(), delta).Int()
	if err != nil {
		return activationDependencyIO(fmt.Errorf("persist activation ref upgrade: %w", err))
	}
	if changed != 1 {
		return ErrActivationConflict
	}
	return nil
}

// CompareAndSetPublicationScheduleActivation advances one confirmed
// publication at one shared EvaluationTime. Every previously active Query
// Group is either cut over or retired, and every new Query Group is activated
// in the same Redis CAS as the Plan activation fact.
func (repository *RedisCatalogRepository) CompareAndSetPublicationScheduleActivation(
	ctx context.Context,
	expected ActivationExpectation,
	next ActivationState,
	boundary execution.EvaluationTime,
	progress ScheduleActivationProgressReader,
) (err error) {
	if repository == nil || repository.client == nil || boundary <= 0 {
		return fmt.Errorf("%w: publication schedule activation is required", ErrCutoverRequest)
	}
	if err := validateActivationTransition(expected, next); err != nil {
		return err
	}
	if expected.RecordRevision == 0 {
		return fmt.Errorf("%w: publication schedule activation requires an existing activation", ErrCutoverRequest)
	}
	previous, err := repository.LoadActivation(ctx)
	if err != nil {
		return err
	}
	if !activationMatchesExpectation(previous, expected) {
		return ErrActivationConflict
	}
	if next.Pending != nil || next.Current.PublicationEpoch <= previous.Current.PublicationEpoch ||
		next.Current.SnapshotRevision == previous.Current.SnapshotRevision {
		return fmt.Errorf("%w: publication activation must advance to one new current publication", ErrCutoverRequest)
	}
	published, err := repository.loadPublishedGroups(ctx, next.Current)
	if err != nil {
		return err
	}
	next.SchemaVersion = activationSchemaVersion

	newGroups := published.groups
	previousContent, err := repository.loadActivatedContent(ctx, previous)
	if err != nil {
		return err
	}
	oldGroups := previousContent.groups
	reactivating, held, _, err := repository.partitionReactivations(ctx, previous.Draining, newGroups, boundary, progress)
	if err != nil {
		return err
	}
	// A held Query Group is in the publication and stays in Draining: it
	// gets no Segment now, its Plans are not activated, and it is not part
	// of the population this activation must cover. It comes back through
	// the same-publication reactivation once it has drained.
	activeGroups := newGroups
	if len(held) > 0 {
		activeGroups = make(map[execution.QueryGroupIdentity]QueryGroup, len(newGroups))
		for identity, group := range newGroups {
			activeGroups[identity] = group
		}
		for _, projection := range held {
			delete(activeGroups, projection.QueryGroup)
		}
	}
	wantedDraining, err := expectedDrainingProjection(
		previous.Draining, oldGroups, newGroups, reactivating, boundary, repository.drainingRetirement(ctx, progress, boundary),
	)
	if err != nil {
		return err
	}
	if !sameDrainingProjection(wantedDraining, next.Draining) {
		return fmt.Errorf("%w: publication activation has an invalid draining projection", ErrCutoverRequest)
	}

	// next.Plans arrives as a candidate record for every Plan of the new
	// publication. Which candidates are persisted is decided below, per
	// Query Group, against the persisted open Segment; the records of a
	// Query Group whose content did not change are the previous ones.
	candidate := next
	carried, err := activationRecordMap(previous.Plans)
	if err != nil {
		return err
	}
	newContent := published.segmentContents()
	// Until this process has read every open Segment once and found each one
	// naming the content the manifest says it names, it trusts no manifest
	// entry: the first cutover of a process reads every carried-over Query
	// Group, later ones read only the Query Groups whose content or contexts
	// the manifest shows changed. Every writer keeps the open Segments and
	// the manifest in step, and the activation header serializes writers.
	readAll := !previousContent.complete || !repository.contentCutoverVerified.Load()
	// Only the Query Groups this cutover opens or re-cuts need their content:
	// the ones it keeps are decided from the content entries alone.
	needed := make([]execution.QueryGroupIdentity, 0)
	for identity, newGroup := range activeGroups {
		if _, existed := oldGroups[identity]; !existed {
			needed = append(needed, identity)
			continue
		}
		previousRefs, known := previousContent.refsFor(newGroup)
		if readAll || previousContent.digests[identity] != newContent[identity].digest || !known ||
			!execution.SameOutputContextRefs(previousRefs, newContent[identity].refs) {
			needed = append(needed, identity)
		}
	}
	if err := repository.materialize(ctx, published, needed); err != nil {
		return err
	}

	now := time.Unix(int64(boundary), 0)
	cutover := newCutoverFacts()
	cutover.contentSource = previousContent.source
	defer func() { repository.observeCutover(ctx, cutover, err) }()
	updates := make([]scheduleTimelineUpdate, 0, len(oldGroups)+len(newGroups))
	candidates := make([]pruneCandidate, 0, len(oldGroups))
	plans := make([]PlanActivationRecord, 0, len(candidate.Plans))
	oldIdentities := make([]execution.QueryGroupIdentity, 0, len(oldGroups))
	for identity := range oldGroups {
		oldIdentities = append(oldIdentities, identity)
	}
	sort.Slice(oldIdentities, func(i, j int) bool { return oldIdentities[i] < oldIdentities[j] })
	// While every open Segment is read, the current activation is checked
	// the other way round too: a record it holds for a Plan no open Segment
	// carries is an activation that cannot be recovered from the Segments,
	// and a cutover must not quietly drop it.
	coveredPrevious := make(map[execution.PlanIdentity]struct{}, len(carried))
	for _, queryGroup := range oldIdentities {
		// Recorded before anything can fail on it: the cutover returns at its
		// first failure, so this names the one that stopped it.
		cutover.failedAt(queryGroup)
		oldGroup := oldGroups[queryGroup]
		newGroup, remains := newGroups[queryGroup]
		if remains && !readAll {
			previousRefs, known := previousContent.refsFor(newGroup)
			if previousContent.digests[queryGroup] == newContent[queryGroup].digest && known &&
				execution.SameOutputContextRefs(previousRefs, newContent[queryGroup].refs) {
				// Kept without a read: the manifest names the same content and
				// the same contexts, so the open Segment is left as it is.
				for _, plan := range newGroup.Plans {
					record, ok := carried[plan.Identity]
					if !ok {
						return fmt.Errorf("%w: a kept Query Group has a Plan without a current activation record", ErrActivationRecordMissing)
					}
					plans = append(plans, record)
				}
				cutover.decided(cutoverKept)
				continue
			}
		}
		timeline, raw, err := repository.loadScheduleTimelineForUpdate(ctx, queryGroup)
		if err != nil {
			return err
		}
		cutover.read++
		last := len(timeline.Segments) - 1
		if last < 0 || timeline.RetiredAt != nil {
			return scheduleConflict(CutoverReasonTimelineMissing, queryGroup,
				fmt.Sprintf("segments=%d retired_at=%v", len(timeline.Segments), timeline.RetiredAt))
		}
		open := timeline.Segments[last]
		if open.Schedule.Segment.End != nil || open.Schedule.Segment.Start >= boundary {
			end := "open"
			if open.Schedule.Segment.End != nil {
				end = fmt.Sprintf("%d", *open.Schedule.Segment.End)
			}
			return scheduleConflict(CutoverReasonOpenSegmentClosed, queryGroup,
				fmt.Sprintf("open_start=%d open_end=%s boundary=%d",
					open.Schedule.Segment.Start, end, boundary))
		}
		// A source that names the content must agree with the Segment; a
		// Segment that disagrees was changed outside this path. A Segment
		// written before Segments named their content is compared on the
		// revisions the old source carries instead.
		// A source that names the content must agree with the Segment; a
		// Segment that disagrees was changed outside this path, and this path
		// does not repair it. Segments already carrying a name nobody
		// published are cleaned up once, deliberately, by the repair
		// subcommand -- not by a self-healing branch that would have to stay
		// in the code forever for a state that cannot be produced any more.
		if oldDigest, named := previousContent.digests[queryGroup]; named && open.Schedule.Segment.ObjectDigest != "" &&
			open.Schedule.Segment.ObjectDigest != oldDigest {
			return scheduleConflict(CutoverReasonOpenDigestMismatch, queryGroup,
				fmt.Sprintf("open_digest=%s activation_digest=%s published_digest=%s content_source=%s",
					open.Schedule.Segment.ObjectDigest, oldDigest, newContent[queryGroup].digest,
					previousContent.source))
		}
		if open.Schedule.Segment.ObjectDigest == "" && oldGroup.QueryPlan.QueryRevision != "" &&
			(open.Schedule.Segment.QueryRevision != oldGroup.QueryPlan.QueryRevision ||
				open.Schedule.Segment.ScheduleRevision != oldGroup.ScheduleRevision) {
			return scheduleConflict(CutoverReasonLegacyRevisionMismatch, queryGroup,
				fmt.Sprintf("open_query_revision=%s want=%s open_schedule_revision=%s want=%s",
					open.Schedule.Segment.QueryRevision, oldGroup.QueryPlan.QueryRevision,
					open.Schedule.Segment.ScheduleRevision, oldGroup.ScheduleRevision))
		}
		if err := validateOpenSegmentActivation(previous, open); err != nil {
			return err
		}
		for _, record := range open.Plans {
			coveredPrevious[record.Fact.Plan] = struct{}{}
		}
		if remains && open.Schedule.Segment.ObjectDigest != "" && open.Schedule.Segment.ObjectDigest == newContent[queryGroup].digest {
			// Same execution content: the Segment and its records stay. Only
			// the output contexts may have moved, and they move by revision.
			plans = append(plans, open.Plans...)
			segment := open.Schedule.Segment
			inForce := segment.OutputContextRefs
			if count := len(segment.OutputContextRevisions); count > 0 {
				inForce = segment.OutputContextRevisions[count-1].Refs
			}
			if execution.SameOutputContextRefs(inForce, newContent[queryGroup].refs) {
				cutover.decided(cutoverKept)
				continue
			}
			revised := open.Schedule
			revised.Segment.OutputContextRevisions = append(append([]execution.OutputContextRevision(nil), segment.OutputContextRevisions...),
				execution.OutputContextRevision{Since: repository.outputContextRevisionSince(segment, boundary), Refs: newContent[queryGroup].refs})
			cutover.revisionsFolded += pruneOutputContextRevisions(&revised, repository.segmentRetention, now)
			if err := revised.Validate(); err != nil {
				return err
			}
			timeline.Segments[last].Schedule = revised
			timeline.RecordRevision++
			if err := validateScheduleTimeline(timeline); err != nil {
				return err
			}
			if dead := repository.deadSegmentPrefix(timeline, now); dead > 0 {
				candidates = append(candidates, pruneCandidate{update: len(updates), dead: dead})
			}
			updates = append(updates, scheduleTimelineUpdate{expected: raw, next: timeline})
			cutover.decided(cutoverRevised)
			continue
		}
		closed := open.Schedule
		closed.Segment.End = &boundary
		if err := closed.Validate(); err != nil {
			return err
		}
		timeline.Segments[last].Schedule = closed
		timeline.RecordRevision++
		if remains {
			segment, err := scheduleSegmentForGroup(published.content.Publication, newGroup, boundary,
				published.content.Groups[queryGroup])
			if err != nil {
				return err
			}
			opened, err := repository.materializeSchedule(ctx, segment)
			if err != nil {
				return err
			}
			records, err := activationRecordsForSchedule(candidate, opened)
			if err != nil {
				return err
			}
			timeline.Segments = append(timeline.Segments, persistedScheduleSegment{Schedule: opened, Plans: records})
			plans = append(plans, records...)
			if open.Schedule.Segment.ObjectDigest == "" {
				cutover.decided(cutoverLegacyCut)
			} else {
				cutover.decided(cutoverCut)
			}
		} else {
			retiredAt := boundary
			timeline.RetiredAt = &retiredAt
			cutover.decided(cutoverRetired)
		}
		if err := validateScheduleTimeline(timeline); err != nil {
			return err
		}
		if dead := repository.deadSegmentPrefix(timeline, now); dead > 0 {
			candidates = append(candidates, pruneCandidate{update: len(updates), dead: dead})
		}
		updates = append(updates, scheduleTimelineUpdate{expected: raw, next: timeline})
	}
	if readAll && len(coveredPrevious) != len(carried) {
		return fmt.Errorf("%w: current activation names Plans no open Segment carries", ErrActivationRecordMissing)
	}
	newIdentities := make([]execution.QueryGroupIdentity, 0, len(activeGroups))
	for identity := range activeGroups {
		if _, existed := oldGroups[identity]; !existed {
			newIdentities = append(newIdentities, identity)
		}
	}
	sort.Slice(newIdentities, func(i, j int) bool { return newIdentities[i] < newIdentities[j] })
	for _, queryGroup := range newIdentities {
		opened, err := repository.openQueryGroupTimeline(ctx, published.content.Publication, newGroups[queryGroup],
			published.content.Groups[queryGroup], boundary, candidate, now, cutover)
		if err != nil {
			return err
		}
		if opened.dead > 0 {
			candidates = append(candidates, pruneCandidate{update: len(updates), dead: opened.dead})
		}
		updates = append(updates, opened.update)
		plans = append(plans, opened.records...)
	}
	if err := validateContentCoverage(plans, activeGroups); err != nil {
		return err
	}
	sort.Slice(plans, func(i, j int) bool { return lessPlanIdentity(plans[i].Fact.Plan, plans[j].Fact.Plan) })
	next.Plans = plans
	// The assembled state is validated the way the candidate was at the top:
	// the active set reference is only assigned by the persistence step.
	assembled := next
	assembled.SchemaVersion = ""
	if err := validateActivationState(assembled); err != nil {
		return err
	}
	if err := repository.pruneTimelines(ctx, updates, candidates, progress, cutover); err != nil {
		return err
	}
	if err := repository.persistCutoverActivation(ctx, expected, next, updates, cutover); err != nil {
		return err
	}
	if readAll {
		repository.contentCutoverVerified.Store(true)
	}
	return nil
}

// CompareAndSetInitialScheduleActivation establishes zero or more first
// Schedule Segments together with their Plan activation facts. Module 02 owns
// the choice of facts; this method only validates and atomically persists them.
// openedQueryGroupTimeline is one Query Group's timeline with a Segment opened
// for the publication: new when the Query Group had none, reopened after its
// retirement otherwise.
type openedQueryGroupTimeline struct {
	update  scheduleTimelineUpdate
	records []PlanActivationRecord
	dead    int
}

// openQueryGroupTimeline opens the publication's Segment for a Query Group
// that the current activation does not run: one the publication adds, one
// that returns from a retirement its Draining projection has forgotten, or
// one that was held out of an earlier activation and has now drained. A
// retired timeline may only be reopened once its retirement lies before the
// boundary.
func (repository *RedisCatalogRepository) openQueryGroupTimeline(
	ctx context.Context,
	publication SnapshotPublicationRef,
	group QueryGroup,
	named ContentEntry,
	boundary execution.EvaluationTime,
	candidate ActivationState,
	now time.Time,
	cutover *cutoverFacts,
) (openedQueryGroupTimeline, error) {
	segment, err := scheduleSegmentForGroup(publication, group, boundary, named)
	if err != nil {
		return openedQueryGroupTimeline{}, err
	}
	opened, err := repository.materializeSchedule(ctx, segment)
	if err != nil {
		return openedQueryGroupTimeline{}, err
	}
	records, err := activationRecordsForSchedule(candidate, opened)
	if err != nil {
		return openedQueryGroupTimeline{}, err
	}
	result := openedQueryGroupTimeline{records: records}
	timeline, raw, err := repository.loadScheduleTimelineForUpdate(ctx, group.Identity)
	cutover.read++
	switch {
	case errors.Is(err, ErrScheduleUnavailable):
		timeline = persistedScheduleTimeline{SchemaVersion: scheduleTimelineSchemaVersion,
			RecordRevision: 1, QueryGroup: group.Identity,
			Segments: []persistedScheduleSegment{{Schedule: opened, Plans: records}}}
	case err != nil:
		return openedQueryGroupTimeline{}, err
	default:
		// Whether the Query Group is new or returning is decided by its
		// persisted timeline, not by the Draining projection: the projection
		// drops a Query Group as soon as one activation sees it drained, while
		// its retired timeline lives on for the Catalog TTL. Appending to the
		// retired timeline keeps the Segments a Slot replayed from before the
		// retirement still reads, and hands the CAS the real prior bytes to
		// fence on.
		if !timeline.retiredBefore(boundary) {
			return openedQueryGroupTimeline{}, ErrScheduleConflict
		}
		retiredAt := *timeline.RetiredAt
		timeline.RetiredAt = nil
		timeline.RecordRevision++
		timeline.Segments = append(timeline.Segments, persistedScheduleSegment{
			Schedule: opened, Plans: records, ReactivatedAfter: &retiredAt,
		})
		result.dead = repository.deadSegmentPrefix(timeline, now)
	}
	if err := validateScheduleTimeline(timeline); err != nil {
		return openedQueryGroupTimeline{}, err
	}
	result.update = scheduleTimelineUpdate{expected: raw, next: timeline}
	cutover.decided(cutoverAdded)
	return result, nil
}

// partitionReactivations splits the Draining Query Groups the publication
// brings back into the ones whose retirement has drained (reactivating) and
// the ones whose has not (held), and says how many reappeared in all. The
// reconciler and both CAS paths decide by it, so the Draining projection the
// reconciler proposes is the one the CAS accepts.
func (repository *RedisCatalogRepository) partitionReactivations(
	ctx context.Context,
	draining []DrainingQueryGroup,
	newGroups map[execution.QueryGroupIdentity]QueryGroup,
	boundary execution.EvaluationTime,
	progress ScheduleActivationProgressReader,
) (map[execution.QueryGroupIdentity]struct{}, []DrainingQueryGroup, int, error) {
	reactivating := make(map[execution.QueryGroupIdentity]struct{})
	var held []DrainingQueryGroup
	reappeared := 0
	for _, projection := range draining {
		if _, exists := newGroups[projection.QueryGroup]; !exists {
			continue
		}
		reappeared++
		reactivatable, err := repository.drainingReactivatable(ctx, projection, boundary, progress)
		if err != nil {
			return nil, nil, 0, err
		}
		if !reactivatable {
			held = append(held, projection)
			continue
		}
		reactivating[projection.QueryGroup] = struct{}{}
	}
	return reactivating, held, reappeared, nil
}

// CompareAndSetHeldReactivation brings Query Groups that an earlier
// activation held out of the current publication back into it, once their
// retirement has drained. The publication does not change: the record
// revision advances by one, the reactivated Query Groups leave Draining and
// get their Segment opened, and their Plans join the activation. Nothing
// else about the activation moves. next carries the reconciler's proposal;
// reactivating names the Query Groups it decided have drained, and the CAS
// decides the same way before it writes.
func (repository *RedisCatalogRepository) CompareAndSetHeldReactivation(
	ctx context.Context,
	expected ActivationExpectation,
	next ActivationState,
	reactivating map[execution.QueryGroupIdentity]struct{},
	boundary execution.EvaluationTime,
	progress ScheduleActivationProgressReader,
) (err error) {
	if repository == nil || repository.client == nil || boundary <= 0 || len(reactivating) == 0 {
		return errors.New("alarmd controlplane: held reactivation is required")
	}
	if err := validateActivationTransition(expected, next); err != nil {
		return err
	}
	previous, err := repository.LoadActivation(ctx)
	if err != nil {
		return err
	}
	if !activationMatchesExpectation(previous, expected) {
		return ErrActivationConflict
	}
	if next.Pending != nil || next.Current != previous.Current {
		return errors.New("alarmd controlplane: held reactivation must keep the current publication")
	}
	published, err := repository.loadPublishedGroups(ctx, previous.Current)
	if err != nil {
		return err
	}
	groups := published.groups
	drained, _, _, err := repository.partitionReactivations(ctx, previous.Draining, groups, boundary, progress)
	if err != nil {
		return err
	}
	for identity := range reactivating {
		if _, ok := drained[identity]; !ok {
			return ErrReactivationNotDrained
		}
	}
	wantedDraining := make([]DrainingQueryGroup, 0, len(previous.Draining))
	for _, projection := range previous.Draining {
		if _, reactivated := reactivating[projection.QueryGroup]; reactivated {
			continue
		}
		wantedDraining = append(wantedDraining, projection)
	}
	if !sameDrainingProjection(wantedDraining, next.Draining) {
		return errors.New("alarmd controlplane: held reactivation has an invalid draining projection")
	}
	next.SchemaVersion = activationSchemaVersion
	now := time.Unix(int64(boundary), 0)
	cutover := newCutoverFacts()
	cutover.contentSource = "held"
	defer func() { repository.observeCutover(ctx, cutover, err) }()
	identities := make([]execution.QueryGroupIdentity, 0, len(reactivating))
	for identity := range reactivating {
		identities = append(identities, identity)
	}
	sort.Slice(identities, func(i, j int) bool { return identities[i] < identities[j] })
	if err := repository.materialize(ctx, published, identities); err != nil {
		return err
	}
	updates := make([]scheduleTimelineUpdate, 0, len(identities))
	candidates := make([]pruneCandidate, 0, len(identities))
	plans := append([]PlanActivationRecord(nil), previous.Plans...)
	for _, identity := range identities {
		opened, err := repository.openQueryGroupTimeline(ctx, previous.Current, groups[identity],
			published.content.Groups[identity], boundary, next, now, cutover)
		if err != nil {
			return err
		}
		if opened.dead > 0 {
			candidates = append(candidates, pruneCandidate{update: len(updates), dead: opened.dead})
		}
		updates = append(updates, opened.update)
		plans = append(plans, opened.records...)
	}
	sort.Slice(plans, func(i, j int) bool { return lessPlanIdentity(plans[i].Fact.Plan, plans[j].Fact.Plan) })
	next.Plans = plans
	assembled := next
	assembled.SchemaVersion = ""
	if err := validateActivationState(assembled); err != nil {
		return err
	}
	if err := repository.pruneTimelines(ctx, updates, candidates, progress, cutover); err != nil {
		return err
	}
	return repository.persistCutoverActivation(ctx, expected, next, updates, cutover)
}

func (repository *RedisCatalogRepository) CompareAndSetInitialScheduleActivation(
	ctx context.Context,
	expected ActivationExpectation,
	next ActivationState,
	facts []execution.InitialScheduleActivationFact,
) error {
	if repository == nil || repository.client == nil {
		return errors.New("alarmd controlplane: initial schedule activation is required")
	}
	if err := validateActivationTransition(expected, next); err != nil {
		return err
	}
	if expected.RecordRevision != 0 {
		return errors.New("alarmd controlplane: initial schedule activation requires empty persisted state")
	}
	next.SchemaVersion = activationSchemaVersion
	timelines := make([]persistedScheduleTimeline, 0, len(facts))
	seen := make(map[execution.QueryGroupIdentity]struct{}, len(facts))
	for _, fact := range facts {
		schedule, err := repository.materializeSchedule(ctx, fact.Segment)
		if err != nil {
			return err
		}
		if err := fact.Validate(schedule); err != nil {
			return err
		}
		if _, duplicate := seen[fact.Segment.QueryGroup]; duplicate {
			return errors.New("alarmd controlplane: duplicate initial Query Group schedule")
		}
		seen[fact.Segment.QueryGroup] = struct{}{}
		records, err := activationRecordsForSchedule(next, schedule)
		if err != nil {
			return err
		}
		timelines = append(timelines, persistedScheduleTimeline{SchemaVersion: scheduleTimelineSchemaVersion,
			RecordRevision: 1, QueryGroup: fact.Segment.QueryGroup,
			Segments: []persistedScheduleSegment{{Schedule: schedule, Plans: records}}})
	}
	if err := validateInitialActivationCoverage(next, timelines); err != nil {
		return err
	}
	return repository.persistInitialActivation(ctx, expected, next, timelines)
}

// CompareAndSetScheduleCutover atomically closes each old Segment, opens its
// adjacent successor and advances the shared Plan activation record.
func (repository *RedisCatalogRepository) CompareAndSetScheduleCutover(
	ctx context.Context,
	expected ActivationExpectation,
	next ActivationState,
	facts []execution.ScheduleCutoverFact,
) error {
	if repository == nil || repository.client == nil || len(facts) == 0 {
		return errors.New("alarmd controlplane: schedule cutover is required")
	}
	if err := validateActivationTransition(expected, next); err != nil {
		return err
	}
	if expected.RecordRevision == 0 {
		return errors.New("alarmd controlplane: schedule cutover requires an existing activation")
	}
	previous, err := repository.LoadActivation(ctx)
	if err != nil {
		return err
	}
	if !activationMatchesExpectation(previous, expected) {
		return ErrActivationConflict
	}
	next.SchemaVersion = activationSchemaVersion
	updates := make([]scheduleTimelineUpdate, 0, len(facts))
	seen := make(map[execution.QueryGroupIdentity]struct{}, len(facts))
	affectedPlans := make(map[execution.PlanIdentity]struct{})
	for _, fact := range facts {
		if _, duplicate := seen[fact.NewSegment.QueryGroup]; duplicate {
			return errors.New("alarmd controlplane: duplicate Query Group cutover")
		}
		seen[fact.NewSegment.QueryGroup] = struct{}{}
		oldSchedule, err := repository.materializeSchedule(ctx, fact.OldSegment)
		if err != nil {
			return err
		}
		newSchedule, err := repository.materializeSchedule(ctx, fact.NewSegment)
		if err != nil {
			return err
		}
		if err := fact.Validate(oldSchedule, newSchedule); err != nil {
			return err
		}
		for _, plan := range oldSchedule.Plans {
			affectedPlans[plan.Identity] = struct{}{}
		}
		for _, plan := range newSchedule.Plans {
			affectedPlans[plan.Identity] = struct{}{}
		}
		timeline, raw, err := repository.loadScheduleTimelineForUpdate(ctx, fact.OldSegment.QueryGroup)
		if err != nil {
			return err
		}
		last := len(timeline.Segments) - 1
		if last < 0 || !sameFrozenSchedule(timeline.Segments[last].Schedule, execution.FrozenQueryGroupSchedule{
			Segment: openVersion(fact.OldSegment), Plans: oldSchedule.Plans,
		}) {
			return ErrScheduleConflict
		}
		if err := validateOpenSegmentActivation(previous, timeline.Segments[last]); err != nil {
			return err
		}
		timeline.Segments[last].Schedule = oldSchedule
		records, err := activationRecordsForSchedule(next, newSchedule)
		if err != nil {
			return err
		}
		timeline.Segments = append(timeline.Segments, persistedScheduleSegment{Schedule: newSchedule, Plans: records})
		timeline.RecordRevision++
		if err := validateScheduleTimeline(timeline); err != nil {
			return err
		}
		updates = append(updates, scheduleTimelineUpdate{expected: raw, next: timeline})
	}
	if err := validateUnchangedActivationRecords(previous, next, affectedPlans); err != nil {
		return err
	}
	return repository.persistCutoverActivation(ctx, expected, next, updates, nil)
}

func validateInitialActivationCoverage(state ActivationState, timelines []persistedScheduleTimeline) error {
	wanted, err := activationRecordMap(state.Plans)
	if err != nil {
		return err
	}
	covered := make(map[execution.PlanIdentity]PlanActivationRecord, len(wanted))
	for _, timeline := range timelines {
		for _, record := range timeline.Segments[0].Plans {
			if _, duplicate := covered[record.Fact.Plan]; duplicate {
				return errors.New("alarmd controlplane: initial Schedule Segments contain duplicate Plan activation")
			}
			covered[record.Fact.Plan] = record
		}
	}
	if len(covered) != len(wanted) {
		return errors.New("alarmd controlplane: initial Schedule Segments must exactly cover Plan activation")
	}
	for identity, record := range wanted {
		if covered[identity] != record {
			return errors.New("alarmd controlplane: initial Schedule Segments must exactly cover Plan activation")
		}
	}
	return nil
}

func activationMatchesExpectation(state ActivationState, expected ActivationExpectation) bool {
	if state.RecordRevision != expected.RecordRevision || state.Current != expected.Current {
		return false
	}
	if state.Pending == nil || expected.Pending == nil {
		return state.Pending == nil && expected.Pending == nil
	}
	return *state.Pending == *expected.Pending
}

func validateOpenSegmentActivation(state ActivationState, segment persistedScheduleSegment) error {
	current, err := activationRecordMap(state.Plans)
	if err != nil {
		return err
	}
	for _, record := range segment.Plans {
		if current[record.Fact.Plan] != record {
			return errors.New("alarmd controlplane: open Schedule Segment differs from current Plan activation")
		}
	}
	return nil
}

func validateUnchangedActivationRecords(previous, next ActivationState, affected map[execution.PlanIdentity]struct{}) error {
	before, err := activationRecordMap(previous.Plans)
	if err != nil {
		return err
	}
	after, err := activationRecordMap(next.Plans)
	if err != nil {
		return err
	}
	for identity, record := range before {
		if _, changed := affected[identity]; changed {
			continue
		}
		if after[identity] != record {
			return errors.New("alarmd controlplane: activation changed outside affected Query Groups")
		}
	}
	for identity, record := range after {
		if _, changed := affected[identity]; changed {
			continue
		}
		if before[identity] != record {
			return errors.New("alarmd controlplane: activation changed outside affected Query Groups")
		}
	}
	return nil
}

func activationRecordMap(records []PlanActivationRecord) (map[execution.PlanIdentity]PlanActivationRecord, error) {
	result := make(map[execution.PlanIdentity]PlanActivationRecord, len(records))
	for _, record := range records {
		if _, duplicate := result[record.Fact.Plan]; duplicate {
			return nil, errors.New("alarmd controlplane: duplicate Plan activation")
		}
		result[record.Fact.Plan] = record
	}
	return result, nil
}

func validateActivationTransition(expected ActivationExpectation, next ActivationState) error {
	if err := validateActivationState(next); err != nil {
		return err
	}
	if next.RecordRevision != expected.RecordRevision+1 {
		return errors.New("alarmd controlplane: activation record revision must advance by one")
	}
	if expected.RecordRevision == 0 && (expected.Current != (SnapshotPublicationRef{}) || expected.Pending != nil) {
		return errors.New("alarmd controlplane: initial activation expectation must be empty")
	}
	return nil
}

func (repository *RedisCatalogRepository) persistInitialActivation(
	ctx context.Context,
	expected ActivationExpectation,
	next ActivationState,
	timelines []persistedScheduleTimeline,
) error {
	identities := make([]execution.QueryGroupIdentity, 0, len(timelines))
	for _, timeline := range timelines {
		identities = append(identities, timeline.QueryGroup)
	}
	ref, activePayload, err := repository.persistAndVerifyActiveQGSet(ctx, identities)
	if err != nil {
		return err
	}
	next.ActiveQGSetRef = ref
	next.SchemaVersion = activationSchemaVersion
	expectedHeader := ""
	nextHeader, err := activationHeader(next.RecordRevision, next.Current, next.Pending)
	if err != nil {
		return err
	}
	activationPayload, err := json.Marshal(next)
	if err != nil {
		return err
	}
	keys := []string{repository.activationHeaderKey(), repository.activationKey(), repository.activeQGSetKey(ref.Digest)}
	args := []interface{}{expectedHeader, nextHeader, activationPayload, activePayload, repository.ttl.Milliseconds()}
	sort.Slice(timelines, func(i, j int) bool { return timelines[i].QueryGroup < timelines[j].QueryGroup })
	for _, timeline := range timelines {
		if err := validateScheduleTimeline(timeline); err != nil {
			return err
		}
		payload, err := json.Marshal(timeline)
		if err != nil {
			return err
		}
		keys = append(keys, repository.scheduleTimelineKey(timeline.QueryGroup))
		args = append(args, payload)
	}
	changed, err := repository.client.Eval(ctx, compareAndSetInitialSchedulesScript, keys, args...).Int()
	if err != nil {
		return activationDependencyIO(fmt.Errorf("persist initial schedule activation: %w", err))
	}
	if changed != 1 {
		return ErrActivationConflict
	}
	return nil
}

func (repository *RedisCatalogRepository) persistCutoverActivation(
	ctx context.Context,
	expected ActivationExpectation,
	next ActivationState,
	updates []scheduleTimelineUpdate,
	cutover *cutoverFacts,
) error {
	active := make(map[execution.QueryGroupIdentity]struct{})
	previous, loadErr := repository.LoadActivation(ctx)
	if loadErr != nil {
		return loadErr
	}
	if previous.SchemaVersion == activationSchemaVersion {
		current, activeErr := repository.LoadActiveQueryGroupSet(ctx, previous.ActiveQGSetRef)
		if activeErr != nil {
			return activeErr
		}
		for _, identity := range current {
			active[identity] = struct{}{}
		}
	} else {
		return errors.New("alarmd controlplane: legacy Activation must be upgraded before cutover persistence")
	}
	for _, update := range updates {
		if update.next.RetiredAt == nil {
			active[update.next.QueryGroup] = struct{}{}
		} else {
			delete(active, update.next.QueryGroup)
		}
	}
	identities := make([]execution.QueryGroupIdentity, 0, len(active))
	for identity := range active {
		identities = append(identities, identity)
	}
	ref, activePayload, err := repository.persistAndVerifyActiveQGSet(ctx, identities)
	if err != nil {
		return err
	}
	next.ActiveQGSetRef = ref
	next.SchemaVersion = activationSchemaVersion
	expectedHeader, err := activationHeader(expected.RecordRevision, expected.Current, expected.Pending)
	if err != nil {
		return err
	}
	nextHeader, err := activationHeader(next.RecordRevision, next.Current, next.Pending)
	if err != nil {
		return err
	}
	activationPayload, err := json.Marshal(next)
	if err != nil {
		return err
	}
	sort.Slice(updates, func(i, j int) bool { return updates[i].next.QueryGroup < updates[j].next.QueryGroup })
	changedGroups := make([]execution.QueryGroupIdentity, 0, len(updates))
	for _, update := range updates {
		changedGroups = append(changedGroups, update.next.QueryGroup)
	}
	deltaPayload, err := encodeActivationDelta(next.RecordRevision, changedGroups)
	if err != nil {
		return err
	}
	keys := []string{repository.activationHeaderKey(), repository.activationKey(), repository.activeQGSetKey(ref.Digest),
		repository.activationDeltaKey(next.RecordRevision)}
	args := []interface{}{expectedHeader, nextHeader, activationPayload, activePayload, repository.ttl.Milliseconds(), deltaPayload}
	payloadBytes := len(expectedHeader) + len(nextHeader) + len(activationPayload) + len(activePayload) + len(deltaPayload)
	timelineBytes := make([]int, 0, len(updates))
	for _, update := range updates {
		payload, err := json.Marshal(update.next)
		if err != nil {
			return err
		}
		keys = append(keys, repository.scheduleTimelineKey(update.next.QueryGroup))
		fence := timelineFence(update.expected)
		args = append(args, fence, payload)
		payloadBytes += len(fence) + len(payload)
		timelineBytes = append(timelineBytes, len(payload))
	}
	cutover.persisted(payloadBytes, timelineBytes)
	changed, err := repository.client.Eval(ctx, compareAndSetCutoverSchedulesScript, keys, args...).Int()
	if err != nil {
		return activationDependencyIO(fmt.Errorf("persist schedule cutover: %w", err))
	}
	if changed != 1 {
		return ErrActivationConflict
	}
	return nil
}

func (repository *RedisCatalogRepository) materializeSchedule(
	ctx context.Context,
	segment execution.ScheduleSegmentFact,
) (execution.FrozenQueryGroupSchedule, error) {
	if err := segment.Validate(); err != nil {
		return execution.FrozenQueryGroupSchedule{}, err
	}
	publication := SnapshotPublicationRef{SnapshotRevision: segment.Publication.SnapshotRevision,
		PublicationEpoch: uint64(segment.Publication.PublicationEpoch)}
	group, err := repository.loadPublishedQueryGroup(ctx, publication, segment.QueryGroup)
	if err != nil {
		return execution.FrozenQueryGroupSchedule{}, err
	}
	if group.QueryPlan.QueryRevision != segment.QueryRevision || group.ScheduleRevision != segment.ScheduleRevision {
		return execution.FrozenQueryGroupSchedule{}, errors.New("alarmd controlplane: Schedule Segment differs from frozen Catalog")
	}
	plans := make([]execution.FrozenPlanSchedule, len(group.Plans))
	for index, plan := range group.Plans {
		plans[index] = execution.FrozenPlanSchedule{Identity: plan.Identity, ScheduleRevision: plan.ScheduleRevision, Spec: plan.ScheduleSpec}
	}
	schedule := execution.FrozenQueryGroupSchedule{Segment: segment, Plans: plans}
	if err := schedule.Validate(); err != nil {
		return execution.FrozenQueryGroupSchedule{}, err
	}
	return schedule, nil
}

func activationRecordsForSchedule(state ActivationState, schedule execution.FrozenQueryGroupSchedule) ([]PlanActivationRecord, error) {
	byPlan := make(map[execution.PlanIdentity]PlanActivationRecord, len(state.Plans))
	for _, record := range state.Plans {
		byPlan[record.Fact.Plan] = record
	}
	records := make([]PlanActivationRecord, len(schedule.Plans))
	for index, plan := range schedule.Plans {
		record, ok := byPlan[plan.Identity]
		if !ok || record.Publication.SnapshotRevision != schedule.Segment.Publication.SnapshotRevision ||
			execution.PublicationEpoch(record.Publication.PublicationEpoch) != schedule.Segment.Publication.PublicationEpoch ||
			record.Fact.Selected.ScheduleRevision != plan.ScheduleRevision {
			return nil, errors.New("alarmd controlplane: Plan activation differs from frozen Schedule Segment")
		}
		records[index] = record
	}
	return records, nil
}

func openVersion(segment execution.ScheduleSegmentFact) execution.ScheduleSegmentFact {
	segment.End = nil
	return segment
}

// scheduleSegmentForGroup opens the Segment a Query Group runs under from
// start, naming the execution content and the per-Plan output contexts it
// was activated with so a Worker can read them by content.
func scheduleSegmentForGroup(
	publication SnapshotPublicationRef,
	group QueryGroup,
	start execution.EvaluationTime,
	named ContentEntry,
) (execution.ScheduleSegmentFact, error) {
	// The names come from the publication's manifest; they are not derived
	// again here.
	//
	// They used to be, from the group this function is handed -- and that group
	// has been through the object store and back, while the manifest's names
	// were computed from the Catalog the leader built. Two derivations of one
	// name, and they agree only for as long as assembly returns exactly what
	// was published. When assembly dropped a field, every Segment cut from an
	// assembled group named an object the manifest did not, the cutover refused
	// the mismatch on every later round, and the fleet stopped taking up
	// published content for eleven hours with every other signal healthy.
	//
	// Copying makes that class impossible rather than unlikely: the manifest is
	// what a publication is called, the assembled group is only what it
	// contains, and a field lost in assembly can no longer change a name.
	if named.Digest == "" {
		return execution.ScheduleSegmentFact{}, fmt.Errorf(
			"%w: the publication names no object for Query Group %s", ErrCutoverRequest, group.Identity)
	}
	// And the content is checked against the names before either is written.
	//
	// Copying stops assembly from changing a name. It does not stop assembly
	// from changing the content, and a Segment naming one object while
	// carrying another is the same fault wearing the other mask. Recomputing
	// here is the only place both are in hand at once, so it is where they are
	// compared -- and a disagreement is refused rather than written, because
	// what follows would put a Segment on the store that says one thing and
	// executes another.
	if err := verifySegmentContent(group, named); err != nil {
		return execution.ScheduleSegmentFact{}, err
	}
	objectDigest := named.Digest
	refs := append([]execution.OutputContextRef(nil), named.Refs...)
	sort.Slice(refs, func(i, j int) bool { return lessPlanIdentity(refs[i].Plan, refs[j].Plan) })
	return execution.ScheduleSegmentFact{
		Publication: execution.SnapshotPublicationRef{SnapshotRevision: publication.SnapshotRevision,
			PublicationEpoch: execution.PublicationEpoch(publication.PublicationEpoch)},
		QueryGroup: group.Identity, QueryRevision: group.QueryPlan.QueryRevision,
		ScheduleRevision: group.ScheduleRevision, Start: start,
		ObjectDigest: objectDigest, OutputContextRefs: refs,
	}, nil
}

func queryGroupMap(groups []QueryGroup) (map[execution.QueryGroupIdentity]QueryGroup, error) {
	result := make(map[execution.QueryGroupIdentity]QueryGroup, len(groups))
	for _, group := range groups {
		if group.Identity == "" {
			return nil, errors.New("alarmd controlplane: Snapshot contains an empty Query Group")
		}
		if _, duplicate := result[group.Identity]; duplicate {
			return nil, errors.New("alarmd controlplane: Snapshot contains a duplicate Query Group")
		}
		result[group.Identity] = group
	}
	return result, nil
}

// loadActivatedGroupsFromOpenSchedules recovers only the old group facts
// needed to close an activation boundary. It is intentionally limited to
// Query Groups also present in the new publication: every active Plan must be
// covered by one immutable open Schedule Segment from the old publication.
// If that exact coverage cannot be proven, the missing Snapshot remains
// unavailable instead of guessing from the new source.
// loadActivatedGroupsFromOpenSchedules rebuilds the activated population
// from the open Segments the activation still references. An open Segment
// is one without an end whose records equal the current activation; which
// publication it names does not matter, because a Query Group whose content
// did not change keeps its Segment across publications. The digest each
// open Segment names is returned beside the skeleton Query Groups, empty for
// a Segment written before Segments named their content.
func (repository *RedisCatalogRepository) loadActivatedGroupsFromOpenSchedules(
	ctx context.Context,
	activation ActivationState,
	candidates map[execution.QueryGroupIdentity]QueryGroup,
) (map[execution.QueryGroupIdentity]QueryGroup, map[execution.QueryGroupIdentity]execution.ObjectDigest, error) {
	expected, err := activationRecordMap(activation.Plans)
	if err != nil {
		return nil, nil, err
	}
	covered := make(map[execution.PlanIdentity]struct{}, len(expected))
	groups := make(map[execution.QueryGroupIdentity]QueryGroup)
	digests := make(map[execution.QueryGroupIdentity]execution.ObjectDigest)
	identities := make([]execution.QueryGroupIdentity, 0, len(candidates))
	for identity := range candidates {
		identities = append(identities, identity)
	}
	sort.Slice(identities, func(i, j int) bool { return identities[i] < identities[j] })
	for _, identity := range identities {
		timeline, loadErr := repository.loadScheduleTimeline(ctx, identity)
		if errors.Is(loadErr, ErrScheduleUnavailable) {
			continue
		}
		if loadErr != nil {
			return nil, nil, loadErr
		}
		last := len(timeline.Segments) - 1
		if last < 0 || timeline.RetiredAt != nil {
			continue
		}
		open := timeline.Segments[last]
		if open.Schedule.Segment.End != nil {
			continue
		}
		if err := validateOpenSegmentActivation(activation, open); err != nil {
			return nil, nil, err
		}
		for _, record := range open.Plans {
			if expected[record.Fact.Plan] != record {
				return nil, nil, ErrSnapshotUnavailable
			}
			if _, duplicate := covered[record.Fact.Plan]; duplicate {
				return nil, nil, ErrSnapshotUnavailable
			}
			covered[record.Fact.Plan] = struct{}{}
		}
		groups[identity] = QueryGroup{
			Identity: identity,
			QueryPlan: execution.QueryPlanFacts{
				QueryRevision: open.Schedule.Segment.QueryRevision,
			},
			ScheduleRevision: open.Schedule.Segment.ScheduleRevision,
		}
		if open.Schedule.Segment.ObjectDigest != "" {
			digests[identity] = open.Schedule.Segment.ObjectDigest
		}
	}
	if len(covered) != len(expected) {
		return nil, nil, ErrSnapshotUnavailable
	}
	return groups, digests, nil
}

// loadActivatedGroupsFromScheduleScan is the one-time v1 migration fallback.
// It is never called for a v2 Activation and is bounded independently of the
// Redis SCAN COUNT hint.
func (repository *RedisCatalogRepository) loadActivatedGroupsFromScheduleScan(ctx context.Context, activation ActivationState) (groupsResult map[execution.QueryGroupIdentity]QueryGroup, resultErr error) {
	started := time.Now()
	scanned := 0
	defer func() {
		result := "success"
		reason := "none"
		observationResult := observability.Result(observability.ResultSuccess)
		observationReason := observability.ReasonNone
		if resultErr != nil {
			result, reason, observationResult = "fail_closed", "contract", observability.ResultFailed
			observationReason = observability.ReasonContractDeterministic
			if errors.Is(resultErr, context.Canceled) || errors.Is(resultErr, context.DeadlineExceeded) {
				result, reason = "canceled", "dependency"
				observationReason = observability.ReasonContractRetryable
			}
		}
		repository.observe(ctx, observability.Observation{Component: observability.ComponentControlPlane, Stage: observability.StageLegacyQGMigration,
			Result: observationResult, ReasonCode: observationReason, LegacyMigration: &observability.LegacyQGMigrationFacts{Result: result, ReasonClass: reason, ScanKeys: scanned, Duration: time.Since(started)}})
	}()
	if repository.legacyMigrationMaxScanKeys <= 0 || repository.legacyMigrationTimeout <= 0 {
		return nil, errors.New("alarmd controlplane: legacy migration bounds are required")
	}
	migrationCtx, cancel := context.WithTimeout(ctx, repository.legacyMigrationTimeout)
	defer cancel()
	expected, err := activationRecordMap(activation.Plans)
	if err != nil {
		return nil, err
	}
	covered := make(map[execution.PlanIdentity]struct{}, len(expected))
	groups := make(map[execution.QueryGroupIdentity]QueryGroup)
	var cursor uint64
	pattern := repository.prefix + ":schedule_timeline:*"
	for {
		keys, next, scanErr := repository.client.Scan(migrationCtx, cursor, pattern, 500).Result()
		if scanErr != nil {
			return nil, activationDependencyIO(scanErr)
		}
		scanned += len(keys)
		if scanned > repository.legacyMigrationMaxScanKeys {
			return nil, errors.New("alarmd controlplane: legacy active Query Group migration scan limit exceeded")
		}
		for _, key := range keys {
			identity := execution.QueryGroupIdentity(strings.TrimPrefix(key, repository.prefix+":schedule_timeline:"))
			timeline, loadErr := repository.loadScheduleTimeline(migrationCtx, identity)
			if loadErr != nil {
				return nil, loadErr
			}
			if timeline.RetiredAt != nil || len(timeline.Segments) == 0 {
				continue
			}
			open := timeline.Segments[len(timeline.Segments)-1]
			if open.Schedule.Segment.End != nil {
				return nil, ErrSnapshotUnavailable
			}
			if err := validateOpenSegmentActivation(activation, open); err != nil {
				return nil, err
			}
			for _, record := range open.Plans {
				if expected[record.Fact.Plan] != record {
					return nil, ErrSnapshotUnavailable
				}
				if _, duplicate := covered[record.Fact.Plan]; duplicate {
					return nil, ErrSnapshotUnavailable
				}
				covered[record.Fact.Plan] = struct{}{}
			}
			groups[identity] = QueryGroup{Identity: identity, QueryPlan: execution.QueryPlanFacts{QueryRevision: open.Schedule.Segment.QueryRevision}, ScheduleRevision: open.Schedule.Segment.ScheduleRevision}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	if len(covered) != len(expected) {
		return nil, ErrSnapshotUnavailable
	}
	return groups, nil
}

// DrainingTerminationWindow is the age past its retirement boundary after
// which a draining Query Group can no longer execute any Slot: every Slot
// before the boundary is then older than the replay age, so neither a normal
// tick nor a replay can still pick it up. The doubled replay age keeps the
// window clear of one replay window worth of clock and delivery skew.
func DrainingTerminationWindow(maxReplayAge time.Duration) time.Duration {
	if maxReplayAge <= 0 {
		return 0
	}
	return 2 * maxReplayAge
}

// DrainingQueryGroupTerminated reports whether the draining Query Group is
// past its termination window at now. A non-positive window never terminates.
func DrainingQueryGroupTerminated(draining DrainingQueryGroup, now execution.EvaluationTime, window time.Duration) bool {
	if window <= 0 || draining.RetiredBoundary <= 0 || now <= draining.RetiredBoundary {
		return false
	}
	return time.Duration(now-draining.RetiredBoundary)*time.Second > window
}

// drainingReactivatable reports whether a previously draining Query Group
// that reappears in the new Snapshot may be reactivated at boundary. Past the
// termination window the entry is retired by rule: nothing can execute its
// retired Slots anymore, so waiting for Progress that cannot advance would
// block every later activation of that Query Group. Inside the window the
// Query Group must have drained, which needs the single Progress reader. The
// reconciler and the CAS side take the same decision from the same inputs.
func (repository *RedisCatalogRepository) drainingReactivatable(
	ctx context.Context,
	draining DrainingQueryGroup,
	boundary execution.EvaluationTime,
	progress ScheduleActivationProgressReader,
) (bool, error) {
	if DrainingQueryGroupTerminated(draining, boundary, repository.drainingRetireAfter) {
		return true, nil
	}
	if progress == nil {
		return false, errors.New("alarmd controlplane: reactivation requires the single Progress reader")
	}
	return repository.queryGroupDrained(ctx, draining.QueryGroup, draining.RetiredBoundary, progress)
}

// retiredQueryGroupsReturning lists the Query Groups of the new publication
// that are absent from the old one but still hold a timeline retired before
// boundary. Activating such a Query Group appends a ReactivatedAfter Segment
// to that timeline rather than opening a fresh one, and every Plan it carries
// restarts through WARMING as a tombstoned interval requires. The Draining
// projection cannot answer this: it forgets a Query Group as soon as one
// activation sees it drained, while the timeline outlives it for the Catalog
// TTL. A missing timeline means a genuinely new Query Group.
//
// The reads go through loadScheduleTimeline, the cached path keyed on the
// activation header, and must stay there: a live read per candidate would
// turn every publication into a keyspace walk. The header changes on every
// activation, so the activation that follows a publication runs on a cold
// cache and this loop costs one GET per candidate. The candidate set is the
// Query Groups the new publication adds, bounded by the size of the new
// publication itself; it is a handful under ordinary editing and reaches the
// whole population only when a publication onboards it at once. The cutover
// that follows already reads every carried-over timeline live, and
// CompareAndSetPublicationScheduleActivation reads each added Query Group
// live as well, so a publication costs at most one read per old Query Group
// plus two per added one. That is the same order as the existing floor, which
// is why this loop is a plain sequential walk rather than a pipeline.
func (repository *RedisCatalogRepository) retiredQueryGroupsReturning(
	ctx context.Context,
	oldGroups map[execution.QueryGroupIdentity]QueryGroup,
	newGroups map[execution.QueryGroupIdentity]QueryGroup,
	boundary execution.EvaluationTime,
) (map[execution.QueryGroupIdentity]struct{}, error) {
	candidates := make([]execution.QueryGroupIdentity, 0, len(newGroups))
	for queryGroup := range newGroups {
		if _, existed := oldGroups[queryGroup]; !existed {
			candidates = append(candidates, queryGroup)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i] < candidates[j] })
	returning := make(map[execution.QueryGroupIdentity]struct{})
	for _, queryGroup := range candidates {
		timeline, err := repository.loadScheduleTimeline(ctx, queryGroup)
		if errors.Is(err, ErrScheduleUnavailable) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if timeline.retiredBefore(boundary) {
			returning[queryGroup] = struct{}{}
		}
	}
	return returning, nil
}

// drainingRetirement decides whether one previously draining Query Group may
// leave the persisted Draining projection at boundary. A terminated entry is
// dropped by age alone; otherwise the entry is dropped only when the Progress
// reader confirms it drained. Every read failure keeps the entry, so pruning
// never blocks an activation and the age bound remains the only guarantee.
func (repository *RedisCatalogRepository) drainingRetirement(
	ctx context.Context,
	progress ScheduleActivationProgressReader,
	boundary execution.EvaluationTime,
) func(DrainingQueryGroup) bool {
	return func(draining DrainingQueryGroup) bool {
		if DrainingQueryGroupTerminated(draining, boundary, repository.drainingRetireAfter) {
			return true
		}
		if progress == nil {
			return false
		}
		drained, err := repository.queryGroupDrained(ctx, draining.QueryGroup, draining.RetiredBoundary, progress)
		return err == nil && drained
	}
}

// expectedDrainingProjection derives the Draining list of the next activation.
// retired, when non-nil, drops previous entries that no longer carry execution
// meaning; it must be the same decision on the reconciler and the CAS side.
func expectedDrainingProjection(
	previous []DrainingQueryGroup,
	oldGroups map[execution.QueryGroupIdentity]QueryGroup,
	newGroups map[execution.QueryGroupIdentity]QueryGroup,
	reactivating map[execution.QueryGroupIdentity]struct{},
	boundary execution.EvaluationTime,
	retired func(DrainingQueryGroup) bool,
) ([]DrainingQueryGroup, error) {
	byGroup := make(map[execution.QueryGroupIdentity]DrainingQueryGroup, len(previous)+len(oldGroups))
	for _, draining := range previous {
		if _, reactivated := reactivating[draining.QueryGroup]; reactivated {
			continue
		}
		if _, duplicate := byGroup[draining.QueryGroup]; duplicate {
			return nil, errors.New("alarmd controlplane: duplicate previous draining Query Group")
		}
		if retired != nil && retired(draining) {
			continue
		}
		byGroup[draining.QueryGroup] = draining
	}
	for queryGroup := range oldGroups {
		if _, remains := newGroups[queryGroup]; remains {
			continue
		}
		if _, alreadyDraining := byGroup[queryGroup]; alreadyDraining {
			return nil, errors.New("alarmd controlplane: active Query Group is already draining")
		}
		byGroup[queryGroup] = DrainingQueryGroup{QueryGroup: queryGroup, RetiredBoundary: boundary}
	}
	result := make([]DrainingQueryGroup, 0, len(byGroup))
	for _, draining := range byGroup {
		result = append(result, draining)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].QueryGroup < result[j].QueryGroup })
	return result, nil
}

func sameDrainingProjection(left, right []DrainingQueryGroup) bool {
	if len(left) != len(right) {
		return false
	}
	ordered := append([]DrainingQueryGroup(nil), right...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].QueryGroup < ordered[j].QueryGroup })
	for index := range left {
		if left[index] != ordered[index] {
			return false
		}
	}
	return true
}

func (repository *RedisCatalogRepository) queryGroupDrained(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	retiredBoundary execution.EvaluationTime,
	progress ScheduleActivationProgressReader,
) (bool, error) {
	timeline, err := repository.loadScheduleTimeline(ctx, queryGroup)
	if err != nil {
		return false, err
	}
	if timeline.RetiredAt == nil || *timeline.RetiredAt != retiredBoundary {
		return false, errors.New("alarmd controlplane: draining projection differs from Schedule retirement")
	}
	identity := execution.ProgressIdentity{QueryGroup: queryGroup}
	load, err := progress.LoadProgress(ctx, identity)
	if err != nil {
		var deterministicControlFact interface{ DeterministicControlFact() }
		if errors.As(err, &deterministicControlFact) {
			return false, err
		}
		return false, activationDependencyIO(err)
	}
	if err := load.Validate(identity); err != nil {
		return false, &alarmdprogress.DeterministicInvalidError{Err: err}
	}
	if load.Status == execution.ProgressFound {
		return load.Progress.UnfinishedRange == nil && load.Progress.NextSlot >= retiredBoundary, nil
	}
	for _, segment := range timeline.Segments {
		if _, hasSlot := segment.Schedule.FirstSlot(); hasSlot {
			return false, nil
		}
	}
	return true, nil
}

func sameFrozenSchedule(left, right execution.FrozenQueryGroupSchedule) bool {
	leftPayload, leftErr := json.Marshal(left)
	rightPayload, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftPayload) == string(rightPayload)
}

func validateScheduleTimeline(timeline persistedScheduleTimeline) error {
	if timeline.SchemaVersion != scheduleTimelineSchemaVersion || timeline.RecordRevision == 0 ||
		timeline.QueryGroup == "" || len(timeline.Segments) == 0 {
		return errors.New("alarmd controlplane: incomplete persisted Schedule timeline")
	}
	for index, segment := range timeline.Segments {
		if err := segment.Schedule.Validate(); err != nil || segment.Schedule.Segment.QueryGroup != timeline.QueryGroup {
			return errors.New("alarmd controlplane: invalid persisted Schedule Segment")
		}
		if _, err := activationRecordsForPersistedSchedule(segment.Plans, segment.Schedule); err != nil {
			return err
		}
		if index == 0 && segment.ReactivatedAfter != nil {
			return errors.New("alarmd controlplane: initial Schedule Segment cannot be a reactivation")
		}
		if index+1 < len(timeline.Segments) {
			nextRecord := timeline.Segments[index+1]
			next := nextRecord.Schedule.Segment
			if segment.Schedule.Segment.End == nil {
				return errors.New("alarmd controlplane: persisted Schedule Segments overlap")
			}
			end := *segment.Schedule.Segment.End
			if end == next.Start {
				if nextRecord.ReactivatedAfter != nil {
					return errors.New("alarmd controlplane: adjacent Schedule Segment cannot carry a reactivation tombstone")
				}
			} else if nextRecord.ReactivatedAfter == nil || *nextRecord.ReactivatedAfter != end || next.Start <= end {
				return errors.New("alarmd controlplane: persisted Schedule Segments are neither adjacent nor tombstone-reactivated")
			}
		} else if timeline.RetiredAt == nil {
			if segment.Schedule.Segment.End != nil {
				return errors.New("alarmd controlplane: final persisted Schedule Segment must remain open")
			}
		} else if segment.Schedule.Segment.End == nil || *segment.Schedule.Segment.End != *timeline.RetiredAt {
			return errors.New("alarmd controlplane: retired Schedule timeline must close exactly at its retirement boundary")
		}
	}
	return nil
}

func activationRecordsForPersistedSchedule(records []PlanActivationRecord, schedule execution.FrozenQueryGroupSchedule) ([]PlanActivationRecord, error) {
	if len(records) != len(schedule.Plans) {
		return nil, errors.New("alarmd controlplane: persisted Plan activation cardinality differs from schedule")
	}
	byPlan := make(map[execution.PlanIdentity]PlanActivationRecord, len(records))
	for _, record := range records {
		if _, duplicate := byPlan[record.Fact.Plan]; duplicate {
			return nil, errors.New("alarmd controlplane: duplicate persisted Plan activation")
		}
		byPlan[record.Fact.Plan] = record
	}
	for _, plan := range schedule.Plans {
		record, ok := byPlan[plan.Identity]
		if !ok || record.Fact.Selected.ScheduleRevision != plan.ScheduleRevision ||
			record.Publication.SnapshotRevision != schedule.Segment.Publication.SnapshotRevision ||
			execution.PublicationEpoch(record.Publication.PublicationEpoch) != schedule.Segment.Publication.PublicationEpoch {
			return nil, errors.New("alarmd controlplane: persisted Plan activation differs from Schedule Segment")
		}
	}
	return records, nil
}

// loadScheduleTimeline reads the small activation header live and serves the
// decoded timeline cached under that header when present. Callers that already
// hold the header for the same authorization use loadScheduleTimelineAt.
//
// The timeline is shared with every other reader under this header. Readers
// only read it; a caller that intends to write uses
// loadScheduleTimelineForUpdate instead.
func (repository *RedisCatalogRepository) loadScheduleTimeline(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
) (persistedScheduleTimeline, error) {
	if repository == nil || repository.client == nil || queryGroup == "" {
		return persistedScheduleTimeline{}, errors.New("alarmd controlplane: Query Group schedule is required")
	}
	version, err := repository.readControlVersion(ctx)
	if err != nil {
		return persistedScheduleTimeline{}, err
	}
	return repository.loadScheduleTimelineAt(ctx, queryGroup, version)
}

// loadScheduleTimelineForUpdate is the loader for the compare-and-set paths,
// which close the open Segment in place, append the next one, and then hand
// the bytes they read back as the value the write must find unchanged.
//
// It deliberately does not consult the cache. The persisted bytes are the
// expectation a publication is fenced on, and reading them live is the shape
// that operation should have anyway: their freshness then rests on the read
// itself rather than on the version header standing in for it. A publication
// is rare enough that the extra round trip does not register, and keeping the
// cache out of it means the cache never has to hold bytes, nor promise that
// bytes it holds still match the object decoded from them.
//
// Reading live also means the returned timeline is private, so the in-place
// Segment writes that follow cannot reach a shared object.
func (repository *RedisCatalogRepository) loadScheduleTimelineForUpdate(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
) (persistedScheduleTimeline, []byte, error) {
	if repository == nil || repository.client == nil || queryGroup == "" {
		return persistedScheduleTimeline{}, nil, errors.New("alarmd controlplane: Query Group schedule is required")
	}
	return repository.readScheduleTimeline(ctx, queryGroup)
}

func decodeScheduleTimeline(queryGroup execution.QueryGroupIdentity, payload []byte) (persistedScheduleTimeline, error) {
	var timeline persistedScheduleTimeline
	if err := json.Unmarshal(payload, &timeline); err != nil {
		return persistedScheduleTimeline{}, &DeterministicScheduleError{
			Err: fmt.Errorf("decode Schedule timeline: %w", err),
		}
	}
	if timeline.QueryGroup != queryGroup {
		return persistedScheduleTimeline{}, &DeterministicScheduleError{
			Err: errors.New("persisted Schedule timeline identity mismatch"),
		}
	}
	if err := validateScheduleTimeline(timeline); err != nil {
		return persistedScheduleTimeline{}, &DeterministicScheduleError{Err: err}
	}
	return timeline, nil
}

func (repository *RedisCatalogRepository) scheduleTimelineKey(queryGroup execution.QueryGroupIdentity) string {
	return repository.prefix + ":schedule_timeline:" + string(queryGroup)
}

// scheduleTimelineRenewBatch bounds one renewal round trip. The renewal walks
// the Query Groups the current Activation references, never the keyspace, so
// this only caps how many replies one pipeline buffers at once.
const scheduleTimelineRenewBatch = 512

// renewScheduleTimelines extends the Schedule timelines the current Activation
// still references. The active Query Group set and the Draining projection are
// exactly the Query Groups a Worker can still schedule, and every Slot they can
// still execute also needs the Snapshot that the same renewal keeps alive, so
// both objects carry one Catalog TTL and the timeline is never the weaker half.
// A Query Group that leaves both sets stops being renewed and its timeline
// expires with the publication it belonged to. PEXPIRE never creates a key, so
// a timeline retired by a concurrent cutover is at worst kept one more TTL.
func (repository *RedisCatalogRepository) renewScheduleTimelines(
	ctx context.Context,
	active []execution.QueryGroupIdentity,
	draining []DrainingQueryGroup,
) error {
	if repository == nil || repository.client == nil {
		return errors.New("alarmd controlplane: Redis catalog repository is required")
	}
	referenced := make(map[execution.QueryGroupIdentity]struct{}, len(active)+len(draining))
	keys := make([]string, 0, len(active)+len(draining))
	appendKey := func(queryGroup execution.QueryGroupIdentity) {
		if queryGroup == "" {
			return
		}
		if _, duplicate := referenced[queryGroup]; duplicate {
			return
		}
		referenced[queryGroup] = struct{}{}
		keys = append(keys, repository.scheduleTimelineKey(queryGroup))
	}
	for _, queryGroup := range active {
		appendKey(queryGroup)
	}
	for _, entry := range draining {
		appendKey(entry.QueryGroup)
	}
	for start := 0; start < len(keys); start += scheduleTimelineRenewBatch {
		end := start + scheduleTimelineRenewBatch
		if end > len(keys) {
			end = len(keys)
		}
		batch := keys[start:end]
		if _, err := repository.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for _, key := range batch {
				pipe.PExpire(ctx, key, repository.ttl)
			}
			return nil
		}); err != nil {
			return activationDependencyIO(fmt.Errorf("renew Schedule timelines: %w", err))
		}
	}
	return nil
}

type RuntimePlanCompiler interface {
	Compile(context.Context, strategy.CompileRequest) (strategy.CompileResult, error)
}

// RedisCatalogRuntime adapts the persisted Catalog and Schedule facts to the
// Scheduler and Progress ports. It owns no local cursor or activation state.
type RedisCatalogRuntime struct {
	repository        *RedisCatalogRepository
	compiler          RuntimePlanCompiler
	stateSemantics    strategy.StateSemantics
	downstreamReserve time.Duration
}

func NewRedisCatalogRuntime(
	repository *RedisCatalogRepository,
	compiler RuntimePlanCompiler,
	stateSemantics strategy.StateSemantics,
	downstreamReserve time.Duration,
) (*RedisCatalogRuntime, error) {
	if repository == nil || compiler == nil || downstreamReserve <= 0 ||
		stateSemantics.StateSchemaVersion == "" || stateSemantics.CodecSemanticsVersion == "" ||
		stateSemantics.IdentitySchemaDigest == "" || stateSemantics.SourceTimeSemanticsVersion == "" ||
		stateSemantics.HistoryCellSemanticsVersion == "" {
		return nil, errors.New("alarmd controlplane: invalid Redis Catalog runtime")
	}
	return &RedisCatalogRuntime{repository: repository, compiler: compiler,
		stateSemantics: stateSemantics, downstreamReserve: downstreamReserve}, nil
}

func (runtime *RedisCatalogRuntime) ReadInitialFrozenSchedule(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
) (execution.FrozenQueryGroupSchedule, error) {
	timeline, err := runtime.repository.loadScheduleTimeline(ctx, queryGroup)
	if err != nil {
		return execution.FrozenQueryGroupSchedule{}, err
	}
	return timeline.Segments[0].Schedule, nil
}

func (runtime *RedisCatalogRuntime) ReadFrozenSchedule(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	evaluationTime execution.EvaluationTime,
) (execution.FrozenQueryGroupSchedule, error) {
	segment, err := runtime.readPersistedSegment(ctx, queryGroup, evaluationTime)
	if err != nil {
		return execution.FrozenQueryGroupSchedule{}, err
	}
	return segment.Schedule, nil
}

func (runtime *RedisCatalogRuntime) ReadSuccessorFrozenSchedule(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	segmentEnd execution.EvaluationTime,
) (execution.FrozenQueryGroupSchedule, error) {
	segment, err := runtime.readPersistedSuccessor(ctx, queryGroup, segmentEnd)
	if err != nil {
		return execution.FrozenQueryGroupSchedule{}, err
	}
	return segment.Schedule, nil
}

// ReadScheduleRetirement answers one question - has this Query Group been
// retired, and when - and to answer it loads the whole timeline. In a
// production profile the Slot source spent 23.3% of the process here, which
// before the decoded timelines were cached meant deserializing every Segment
// and recomputing a canonical digest over every Plan to read one nullable
// field. Serving the question from a cached object is what makes that cost
// the map lookup it should always have been.
func (runtime *RedisCatalogRuntime) ReadScheduleRetirement(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
) (execution.EvaluationTime, bool, error) {
	if runtime == nil || runtime.repository == nil || queryGroup == "" {
		return 0, false, errors.New("alarmd controlplane: valid Query Group retirement read is required")
	}
	timeline, err := runtime.repository.loadScheduleTimeline(ctx, queryGroup)
	if err != nil {
		return 0, false, err
	}
	if timeline.RetiredAt == nil {
		return 0, false, nil
	}
	return *timeline.RetiredAt, true, nil
}

func (runtime *RedisCatalogRuntime) NextSlotAfter(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	completed execution.EvaluationTime,
) (execution.EvaluationTime, error) {
	segment, err := runtime.readPersistedSegment(ctx, queryGroup, completed)
	containsCompletion := true
	if errors.Is(err, ErrScheduleUnavailable) {
		// No Segment holds the anchor. That is the timeline's doing, not the
		// Worker's: a retirement closes the open Segment at the activation's
		// own second, and a Slot begun on it in that same second, then
		// abandoned by the return, leaves its time in the hole between the
		// closed Segment and the reactivated one; a pruned prefix leaves an
		// old anchor before the first retained Segment the same way. The
		// Slots between the anchor and the next Segment never existed on the
		// timeline, so the continuation is that Segment's first Slot, the way
		// a retired Segment's end continues into its successor. Refusing
		// here instead failed every BeginSlot of the Query Group with the
		// same error for as long as the anchor stood, and nothing moved it.
		segment, err = runtime.readPersistedSegmentAfter(ctx, queryGroup, completed)
		containsCompletion = false
	}
	if err != nil {
		return 0, err
	}
	for {
		var next execution.EvaluationTime
		var ok bool
		if containsCompletion {
			next, ok = segment.Schedule.NextSlotAfter(completed)
		} else {
			next, ok = segment.Schedule.FirstSlot()
		}
		if ok && next > completed {
			return next, nil
		}
		if segment.Schedule.Segment.End == nil {
			return 0, ErrScheduleUnavailable
		}
		boundary := *segment.Schedule.Segment.End
		successor, successorErr := runtime.readPersistedSuccessor(ctx, queryGroup, boundary)
		if errors.Is(successorErr, ErrScheduleUnavailable) {
			retiredAt, retired, retirementErr := runtime.ReadScheduleRetirement(ctx, queryGroup)
			if retirementErr != nil {
				return 0, retirementErr
			}
			if retired && retiredAt == boundary && boundary > completed {
				return boundary, nil
			}
		}
		if successorErr != nil {
			return 0, successorErr
		}
		segment = successor
		containsCompletion = false
	}
}

// readPersistedSegmentAfter returns the earliest Segment that starts after
// evaluationTime, for an anchor no Segment holds. Segments are kept in time
// order, so the first one past the anchor is the continuation.
func (runtime *RedisCatalogRuntime) readPersistedSegmentAfter(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	evaluationTime execution.EvaluationTime,
) (persistedScheduleSegment, error) {
	if runtime == nil || runtime.repository == nil || evaluationTime <= 0 {
		return persistedScheduleSegment{}, errors.New("alarmd controlplane: valid Catalog runtime and EvaluationTime are required")
	}
	timeline, err := runtime.repository.loadScheduleTimeline(ctx, queryGroup)
	if err != nil {
		return persistedScheduleSegment{}, err
	}
	for _, segment := range timeline.Segments {
		if segment.Schedule.Segment.Start > evaluationTime {
			return segment, nil
		}
	}
	return persistedScheduleSegment{}, ErrScheduleUnavailable
}

func (runtime *RedisCatalogRuntime) readPersistedSuccessor(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	segmentEnd execution.EvaluationTime,
) (persistedScheduleSegment, error) {
	if runtime == nil || runtime.repository == nil || queryGroup == "" || segmentEnd <= 0 {
		return persistedScheduleSegment{}, errors.New("alarmd controlplane: valid Schedule successor read is required")
	}
	timeline, err := runtime.repository.loadScheduleTimeline(ctx, queryGroup)
	if err != nil {
		return persistedScheduleSegment{}, err
	}
	for index := 1; index < len(timeline.Segments); index++ {
		previous := timeline.Segments[index-1].Schedule.Segment
		if previous.End == nil || *previous.End != segmentEnd {
			continue
		}
		return timeline.Segments[index], nil
	}
	return persistedScheduleSegment{}, ErrScheduleUnavailable
}

func (runtime *RedisCatalogRuntime) FreezeSlotContract(
	ctx context.Context,
	request execution.FreezeSlotContractRequest,
) (execution.FrozenSlotContractFact, error) {
	if err := request.Validate(); err != nil {
		return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailureContractValidation, err)
	}
	segment, err := runtime.readPersistedSegment(ctx, request.QueryGroup, request.EvaluationTime)
	if err != nil {
		return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailureScheduleRead, err)
	}
	schedule := segment.Schedule
	if schedule.Segment.ScheduleRevision != request.ScheduleRevision || schedule.Segment.Start != request.ScheduleSegmentStart {
		return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailureScheduleMismatch,
			errors.New("alarmd controlplane: frozen Slot request differs from persisted Segment"))
	}
	expectedDue := schedule.DuePlanRefs(request.EvaluationTime)
	if !equalDuePlanRefs(expectedDue, request.DuePlans) {
		return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailureScheduleMismatch,
			errors.New("alarmd controlplane: frozen Slot request differs from exact due Plan set"))
	}
	publication := SnapshotPublicationRef{SnapshotRevision: schedule.Segment.Publication.SnapshotRevision,
		PublicationEpoch: uint64(schedule.Segment.Publication.PublicationEpoch)}
	group, err := runtime.repository.LoadSegmentQueryGroup(ctx, schedule.Segment, request.EvaluationTime, func(ctx context.Context) (QueryGroup, error) {
		return runtime.repository.loadPublishedQueryGroup(ctx, publication, request.QueryGroup)
	})
	if err != nil {
		if errors.Is(err, ErrCatalogObjectUnavailable) {
			return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailurePlanMaterialize, err)
		}
		return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailureSnapshotRead, err)
	}
	activationByPlan := make(map[execution.PlanIdentity]PlanActivationRecord, len(segment.Plans))
	for _, record := range segment.Plans {
		activationByPlan[record.Fact.Plan] = record
	}
	planByID := make(map[execution.PlanIdentity]FrozenPlan, len(group.Plans))
	for _, plan := range group.Plans {
		planByID[plan.Identity] = plan
	}
	duePlans := make([]execution.DuePlan, 0, len(request.DuePlans))
	// What the object store handed this worker back for the Segment it is
	// executing, before any of it is compiled. Counted over the assembled
	// group rather than the due set, because a Plan can be dropped by either
	// and the point of the two counts is to say which.
	assembled := 0
	for _, plan := range group.Plans {
		if plan.Plan.NoData != nil {
			assembled++
		}
	}
	frozen := 0
	for _, dueRef := range request.DuePlans {
		plan, ok := planByID[dueRef.Identity]
		record, active := activationByPlan[dueRef.Identity]
		if !ok || !active || plan.ScheduleRevision != dueRef.ScheduleRevision ||
			record.Fact.Selected.ScheduleRevision != dueRef.ScheduleRevision {
			return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailurePlanMaterialize,
				errors.New("alarmd controlplane: due Plan is absent from frozen Catalog or activation"))
		}
		compiledResult, err := runtime.compiler.Compile(ctx, strategy.CompileRequest{Plan: plan.Plan,
			DatasetContract: group.QueryPlan.Normalization.DatasetContract, StateSemantics: runtime.stateSemantics})
		if err != nil {
			return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailurePlanMaterialize, err)
		}
		compiled, compiledOK := compiledResult.Plan()
		if !compiledOK || compiledResult.PlanTerminal() != nil || len(compiledResult.LevelTerminals()) != 0 {
			return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailurePlanMaterialize,
				errors.New("alarmd controlplane: frozen Plan cannot be compiled for G1 FULL execution"))
		}
		compiledGeneration := execution.StateGeneration(compiled.StateCompatibilityHash())
		if err := runtime.checkFrozenPlanGeneration(ctx, request, plan, record, segment, compiledGeneration); err != nil {
			return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailurePlanMaterialize, err)
		}
		deadline, err := completionDeadline(request.EvaluationTime, plan.ScheduleSpec)
		if err != nil {
			return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailureContractValidation, err)
		}
		capabilities := make([]execution.LevelPartialCapability, 0, len(compiled.Levels()))
		for _, level := range compiled.Levels() {
			capabilities = append(capabilities, execution.LevelPartialCapability{LevelID: level.Definition().LevelID, Policy: execution.PartialRequiresFull})
		}
		if compiled.NoData() != nil {
			frozen++
		}
		duePlans = append(duePlans, execution.DuePlan{Identity: plan.Identity, CompiledPlan: compiled,
			StateGeneration: record.Fact.Selected.StateGeneration, StateApplyEpoch: record.Fact.Selected.StateApplyEpoch,
			ScheduleRevision: plan.ScheduleRevision, ScheduleSpec: plan.ScheduleSpec,
			CompletionDeadlineUnixMilli: deadline, PartialCapabilities: capabilities})
	}
	// Whether this Segment still names what the control plane publishes. Not
	// part of the no-data hops: it is the same question one level up, asked of
	// the whole execution content rather than one field of it.
	runtime.repository.observeSegmentContentFreshness(ctx, schedule.Segment)
	// Both hops, reported on every Slot whether either is any or none. The
	// leader's published count and these two are read in order: the first that
	// reads zero while the one before it does not is where the section is lost.
	for hop, plans := range map[string]int{
		observability.NoDataHopAssembled: assembled,
		observability.NoDataHopFrozen:    frozen,
	} {
		runtime.repository.observe(ctx, observability.Observation{
			Component: observability.ComponentControlPlane, Stage: observability.StageFrozenPlanGeneration,
			Result: observability.ResultSuccess,
			Trace: observability.TraceFields{
				QueryGroupKey:    string(request.QueryGroup),
				EvaluationTime:   int64(request.EvaluationTime),
				SnapshotRevision: string(schedule.Segment.Publication.SnapshotRevision),
			},
			NoDataCensus: &observability.NoDataCensusFacts{Hop: hop, Plans: plans},
		})
	}
	requirements, err := runtime.slotRequirements(group, duePlans, planByID)
	if err != nil {
		return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailureInputClosure, err)
	}
	digest, err := execution.DeriveDuePlanSetDigest(duePlans, requirements)
	if err != nil {
		return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailureContractValidation, err)
	}
	fact := execution.FrozenSlotContractFact{Contract: execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: request.QueryGroup, EvaluationTime: request.EvaluationTime},
		SnapshotRevision: schedule.Segment.Publication.SnapshotRevision, QueryRevision: schedule.Segment.QueryRevision,
		ScheduleRevision: request.ScheduleRevision, ScheduleSegmentStart: request.ScheduleSegmentStart,
		DuePlanSetDigest: digest,
	}, DuePlans: duePlans, Requirements: requirements}
	if err := fact.Validate(request); err != nil {
		return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailureContractValidation, err)
	}
	return fact, nil
}

// checkFrozenPlanGeneration decides whether a due Plan may be frozen under the
// state generation its activation record names. That generation is the one the
// Slot executes under: the Coordinator keys the Plan's state by it and compares
// it with the live activation to decide when the Plan re-warms. Three
// generations are in play, and only two of them are persisted facts:
//
//   - recorded: named by the activation record the Control Leader published
//     with the Segment;
//   - stored: carried by the Plan inside the Query Group object the Segment
//     names by digest, computed by the Leader that wrote the object from that
//     same Plan;
//   - compiled: what this process derives by compiling that same Plan.
//
// stored and recorded were published together and have to agree. A record
// that names a generation its own object does not carry is refused: the Slot
// would mutate state keyed by one generation with a Plan of another, which is
// what the generation exists to prevent.
//
// compiled is not a check on content. The Plan it is derived from is the
// stored Plan, whose bytes the object read already hashed against the digest
// the Segment names, so the content this process compiles is the content the
// Leader published, by construction. A compiled generation that differs from
// the stored one can therefore mean one thing only: this process derives the
// hash by another formula (or state semantics) than the Leader that wrote the
// object. That is the ordinary state of a rolling release for the minutes
// until the new Leader republishes, and a version mismatch if it persists. It
// is reported on state_generation_skew_total{kind="formula"} and tolerated.
// Enforcing it, as this did until the release that moved the formula to
// strategy-state-input-closure-v2, blocked every Slot of every Query Group
// from the first Worker restart until the objects written under the old
// formula expired a day later: the Progress cursor cannot pass a Slot that
// will not freeze, and the Leader's own cutover only opens Segments ahead of
// it. The Sep 4 move to the G4 formula had the same shape and was answered
// with the pre-G4 tolerance below, which fits that one migration only; this
// rule holds for any formula because it compares no generation to a formula.
//
// A Plan stored without a generation predates the field, so its record is the
// only persisted generation and nothing published with it can vouch for it.
// Such a record is accepted for a closed Segment when it names the generation
// the pre-G4 formula derives, which is the only generation such a Segment can
// legitimately carry; an open Segment must be cut by the Leader first.
func (runtime *RedisCatalogRuntime) checkFrozenPlanGeneration(
	ctx context.Context,
	request execution.FreezeSlotContractRequest,
	plan FrozenPlan,
	record PlanActivationRecord,
	segment persistedScheduleSegment,
	compiled execution.StateGeneration,
) error {
	recorded := record.Fact.Selected.StateGeneration
	if plan.StateGeneration != "" {
		if plan.StateGeneration != recorded {
			runtime.observeStateGenerationSkew(ctx, request, segment, plan, "record")
			return errors.New("alarmd controlplane: Plan activation state generation differs from the frozen Plan")
		}
		if compiled != plan.StateGeneration {
			runtime.observeStateGenerationSkew(ctx, request, segment, plan, "formula")
		}
		return nil
	}
	if compiled == recorded {
		return nil
	}
	if segment.Schedule.Segment.End == nil {
		return errors.New("alarmd controlplane: open Segment stores a Plan without a state generation")
	}
	executionSemantics := plan.Plan.StrategyIR.ExecutionSemantics
	legacy, err := contract.DeriveStateCompatibilityHashV1(contract.StateCompatibilityInputV1{
		StateSchemaVersion: runtime.stateSemantics.StateSchemaVersion, CodecSemanticsVersion: runtime.stateSemantics.CodecSemanticsVersion,
		IdentitySchemaDigest: runtime.stateSemantics.IdentitySchemaDigest, EvaluationScope: executionSemantics.EvaluationScope,
		AggregationInterval: executionSemantics.AggregationInterval, EvaluationInterval: executionSemantics.EvaluationInterval,
		SourceTimeSemanticsVersion:  runtime.stateSemantics.SourceTimeSemanticsVersion,
		HistoryCellSemanticsVersion: runtime.stateSemantics.HistoryCellSemanticsVersion,
	})
	if err != nil {
		return err
	}
	if execution.StateGeneration(legacy) != recorded {
		return errors.New("alarmd controlplane: closed Segment stores a Plan without a state generation and its activation names neither the compiled nor the pre-G4 one")
	}
	return nil
}

// observeStateGenerationSkew names the Slot, the Segment and the Plan the
// skew was found on. The counter alone says how many; the line has to say
// which, because a "record" skew is a refused Slot and the only way to find
// the record it refused is from here.
func (runtime *RedisCatalogRuntime) observeStateGenerationSkew(
	ctx context.Context,
	request execution.FreezeSlotContractRequest,
	segment persistedScheduleSegment,
	plan FrozenPlan,
	kind string,
) {
	runtime.repository.observe(ctx, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageFrozenPlanGeneration,
		Result: observability.ResultSuccess,
		Trace: observability.TraceFields{
			QueryGroupKey: string(request.QueryGroup), EvaluationTime: int64(request.EvaluationTime),
			ScheduleSegmentStart: int64(segment.Schedule.Segment.Start),
			SnapshotRevision:     string(segment.Schedule.Segment.Publication.SnapshotRevision),
		},
		StateGenerationSkew: &observability.StateGenerationSkewFacts{Kind: kind, StrategyID: plan.Identity.StrategyID},
	})
}

func (runtime *RedisCatalogRuntime) slotRequirements(
	group QueryGroup,
	duePlans []execution.DuePlan,
	planByID map[execution.PlanIdentity]FrozenPlan,
) ([]execution.DataRequirement, error) {
	byID := make(map[execution.RequirementID]*execution.DataRequirement)
	legacyDuePlans := make([]execution.DuePlan, 0, len(duePlans))
	legacyLevels := make(map[execution.PlanIdentity]map[uint32]struct{}, len(duePlans))

	for _, due := range duePlans {
		plan, ok := planByID[due.Identity]
		if !ok {
			return nil, errors.New("alarmd controlplane: due Plan has no frozen input facts")
		}
		reserve := runtime.downstreamReserve.Milliseconds()
		if reserve <= 0 || due.ScheduleSpec.EvaluationIntervalSeconds > math.MaxInt64/1000 ||
			reserve >= due.ScheduleSpec.EvaluationIntervalSeconds*1000 {
			return nil, errors.New("alarmd controlplane: invalid downstream execution reserve")
		}

		expected := make(map[execution.RequirementID]uint32)
		explicitPrimary := make(map[uint32]map[execution.RequirementID]struct{})
		for _, level := range due.CompiledPlan.Levels() {
			levelID := level.Definition().LevelID
			for _, algorithm := range level.Algorithms() {
				algorithmRequirements := algorithm.InputRequirements()
				if len(algorithmRequirements) == 0 {
					continue
				}
				for _, requirement := range algorithmRequirements {
					if requirement.ConsumerLevelID != levelID {
						return nil, errors.New("alarmd controlplane: compiled algorithm input references a different Level")
					}
					identity := execution.RequirementID(requirement.RequirementID)
					if identity == "" {
						return nil, errors.New("alarmd controlplane: compiled algorithm input identity is missing")
					}
					if existingLevel, exists := expected[identity]; exists && existingLevel != levelID {
						return nil, errors.New("alarmd controlplane: compiled algorithm input identity is shared across Levels")
					}
					expected[identity] = levelID
					if requirement.Role == strategy.AlgorithmInputPrimary {
						if explicitPrimary[levelID] == nil {
							explicitPrimary[levelID] = make(map[execution.RequirementID]struct{})
						}
						explicitPrimary[levelID][identity] = struct{}{}
					}
				}
			}
		}
		for _, level := range due.CompiledPlan.Levels() {
			levelID := level.Definition().LevelID
			switch len(explicitPrimary[levelID]) {
			case 0:
				if legacyLevels[due.Identity] == nil {
					legacyLevels[due.Identity] = make(map[uint32]struct{})
					legacyDuePlans = append(legacyDuePlans, due)
				}
				legacyLevels[due.Identity][levelID] = struct{}{}
			case 1:
			default:
				return nil, errors.New("alarmd controlplane: compiled Level has multiple PRIMARY input requirements")
			}
		}

		templates := make(map[execution.RequirementID]execution.DataRequirementTemplate, len(plan.RequirementTemplates))
		for _, template := range plan.RequirementTemplates {
			levelID, required := expected[template.RequirementID]
			if !required {
				continue
			}
			if template.ConsumerLevelID != levelID {
				return nil, errors.New("alarmd controlplane: frozen DataRequirement template references a different Level")
			}
			if _, duplicate := templates[template.RequirementID]; duplicate {
				return nil, errors.New("alarmd controlplane: duplicate frozen DataRequirement template")
			}
			originalID := template.RequirementID
			template.RequirementID = ""
			rebuilt, err := execution.BuildDataRequirementTemplate(template)
			if err != nil {
				return nil, fmt.Errorf("alarmd controlplane: invalid frozen DataRequirement template: %w", err)
			}
			if rebuilt.RequirementID != originalID || !equalStrings(rebuilt.RequiredColumns, template.RequiredColumns) {
				return nil, errors.New("alarmd controlplane: frozen DataRequirement template differs from its identity")
			}
			facts, exists := plan.QueryPlans[rebuilt.LogicalQueryRef]
			if !exists {
				return nil, errors.New("alarmd controlplane: frozen DataRequirement has no QueryPlanFacts")
			}
			if err := facts.Validate(); err != nil {
				return nil, fmt.Errorf("alarmd controlplane: invalid frozen QueryPlanFacts: %w", err)
			}
			if execution.LogicalQueryRef(facts.QueryRevision) != rebuilt.LogicalQueryRef {
				return nil, errors.New("alarmd controlplane: frozen QueryPlanFacts differ from logical query reference")
			}
			templates[originalID] = rebuilt
		}

		for identity, levelID := range expected {
			template, exists := templates[identity]
			if !exists {
				return nil, errors.New("alarmd controlplane: compiled algorithm input has no frozen DataRequirement template")
			}
			bound := template.Bind(execution.DataRequirementConsumer{
				Consumer:                           execution.ConsumerRef{Plan: due.Identity, LevelID: levelID, HasLevel: true},
				ConsumerDeadlineUnixMilli:          due.CompletionDeadlineUnixMilli,
				DownstreamExecutionReserveMilliSec: reserve,
			})
			if existing := byID[identity]; existing != nil {
				existing.Consumers = append(existing.Consumers, bound.Consumers...)
			} else {
				copy := bound
				byID[identity] = &copy
			}
		}
	}

	legacy, err := runtime.primaryRequirements(group, legacyDuePlans, legacyLevels)
	if err != nil {
		return nil, err
	}
	for _, requirement := range legacy {
		if existing := byID[requirement.RequirementID]; existing != nil {
			existing.Consumers = append(existing.Consumers, requirement.Consumers...)
		} else {
			copy := requirement
			byID[requirement.RequirementID] = &copy
		}
	}

	result := make([]execution.DataRequirement, 0, len(byID))
	for _, requirement := range byID {
		sort.Slice(requirement.Consumers, func(i, j int) bool {
			left := requirement.Consumers[i].Consumer
			right := requirement.Consumers[j].Consumer
			if left.Plan != right.Plan {
				return lessPlanIdentity(left.Plan, right.Plan)
			}
			if left.HasLevel != right.HasLevel {
				return !left.HasLevel
			}
			return left.LevelID < right.LevelID
		})
		result = append(result, *requirement)
	}
	if err := validateExactlyOnePrimaryPerLevel(duePlans, result); err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return result[i].RequirementID < result[j].RequirementID })
	return result, nil
}

func validateExactlyOnePrimaryPerLevel(duePlans []execution.DuePlan, requirements []execution.DataRequirement) error {
	primary := make(map[execution.ConsumerRef]int)
	for _, requirement := range requirements {
		if requirement.Role != execution.InputRolePrimary {
			continue
		}
		for _, consumer := range requirement.Consumers {
			primary[consumer.Consumer]++
		}
	}
	for _, due := range duePlans {
		for _, level := range due.CompiledPlan.Levels() {
			consumer := execution.ConsumerRef{Plan: due.Identity, LevelID: level.Definition().LevelID, HasLevel: true}
			if primary[consumer] != 1 {
				return errors.New("alarmd controlplane: compiled Level must have exactly one PRIMARY input requirement")
			}
		}
	}
	return nil
}

func (runtime *RedisCatalogRuntime) readPersistedSegment(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	evaluationTime execution.EvaluationTime,
) (persistedScheduleSegment, error) {
	if runtime == nil || runtime.repository == nil || evaluationTime <= 0 {
		return persistedScheduleSegment{}, errors.New("alarmd controlplane: valid Catalog runtime and EvaluationTime are required")
	}
	timeline, err := runtime.repository.loadScheduleTimeline(ctx, queryGroup)
	if err != nil {
		return persistedScheduleSegment{}, err
	}
	for _, segment := range timeline.Segments {
		if segment.Schedule.Segment.Contains(evaluationTime) {
			return segment, nil
		}
	}
	return persistedScheduleSegment{}, ErrScheduleUnavailable
}

func (runtime *RedisCatalogRuntime) primaryRequirements(
	group QueryGroup,
	duePlans []execution.DuePlan,
	levelsByPlan map[execution.PlanIdentity]map[uint32]struct{},
) ([]execution.DataRequirement, error) {
	type requirementKey struct {
		window int64
	}
	byWindow := make(map[requirementKey]*execution.DataRequirement)
	columns := append([]string{"value"}, group.QueryPlan.Normalization.DatasetContract.IdentityFields...)
	sort.Strings(columns)
	for _, due := range duePlans {
		window := int64(due.CompiledPlan.EvaluationSemantics().QueryWindow)
		key := requirementKey{window: window}
		requirement := byWindow[key]
		if requirement == nil {
			identity, err := contract.DeriveCanonicalDigestV2("alarmd-primary-requirement-v1", struct {
				QueryRevision execution.QueryRevision `json:"query_revision"`
				WindowSeconds int64                   `json:"window_seconds"`
			}{group.QueryPlan.QueryRevision, window})
			if err != nil {
				return nil, err
			}
			requirement = &execution.DataRequirement{RequirementID: execution.RequirementID(identity),
				DatasetName: execution.DatasetName("primary:" + identity), Role: execution.InputRolePrimary,
				LogicalQueryRef: execution.LogicalQueryRef(group.QueryPlan.QueryRevision),
				RelativeWindow:  execution.RelativeQueryWindow{StartOffsetSeconds: -group.QueryPlan.QueryDelaySeconds - window, EndOffsetSeconds: -group.QueryPlan.QueryDelaySeconds, HalfOpen: true},
				StepMillis:      group.QueryPlan.StepMillis, AlignmentMillis: group.QueryPlan.AlignmentMillis,
				ResultWindowPolicy: execution.ResultWindowExactHalfOpen, ReadinessClass: execution.ReadinessEager,
				RequiredColumns: append([]string(nil), columns...)}
			byWindow[key] = requirement
		}
		reserve := runtime.downstreamReserve.Milliseconds()
		if reserve <= 0 || due.ScheduleSpec.EvaluationIntervalSeconds > math.MaxInt64/1000 ||
			reserve >= due.ScheduleSpec.EvaluationIntervalSeconds*1000 {
			return nil, errors.New("alarmd controlplane: invalid downstream execution reserve")
		}
		levelIDs := make([]uint32, 0, len(levelsByPlan[due.Identity]))
		for levelID := range levelsByPlan[due.Identity] {
			levelIDs = append(levelIDs, levelID)
		}
		sort.Slice(levelIDs, func(i, j int) bool { return levelIDs[i] < levelIDs[j] })
		for _, levelID := range levelIDs {
			requirement.Consumers = append(requirement.Consumers, execution.DataRequirementConsumer{
				Consumer: execution.ConsumerRef{
					Plan: due.Identity, LevelID: levelID, HasLevel: true,
				},
				ConsumerDeadlineUnixMilli:          due.CompletionDeadlineUnixMilli,
				DownstreamExecutionReserveMilliSec: reserve,
			})
		}
	}
	result := make([]execution.DataRequirement, 0, len(byWindow))
	for _, requirement := range byWindow {
		sort.Slice(requirement.Consumers, func(i, j int) bool {
			left := requirement.Consumers[i].Consumer
			right := requirement.Consumers[j].Consumer
			if left.Plan != right.Plan {
				return lessPlanIdentity(left.Plan, right.Plan)
			}
			return left.LevelID < right.LevelID
		})
		result = append(result, *requirement)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].RequirementID < result[j].RequirementID })
	return result, nil
}

func completionDeadline(at execution.EvaluationTime, spec execution.ScheduleSpec) (int64, error) {
	deadline, ok := spec.CompletionDeadlineUnixMilli(at)
	if !ok {
		return 0, errors.New("alarmd controlplane: invalid frozen Plan deadline")
	}
	return deadline, nil
}

func equalDuePlanRefs(left, right []execution.FrozenPlanScheduleRef) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
