// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A Query Group whose Schedule timeline stopped decoding used to stay that
// way. Three scripts write a timeline: the first activation, which writes
// only where no key exists; the cutover, which writes only the Query Groups a
// publication changes and fences each on the bytes it read; and the renewal,
// which only extends the key's life - the bad bytes with it. The cutover
// reads an unchanged Query Group's timeline once per process and then trusts
// the manifest, and when it did read bytes that would not decode it held the
// Query Group back, unwritten. A reconcile with no new publication read
// nothing at all. The Worker meeting the bytes named the round
// SCHEDULE_UNREADABLE and told nobody, so the Query Group ran nothing for as
// long as the key lived, which the renewal made forever.
//
// Now the Worker names such Query Groups on its registration, the Leader
// reads them each round, and the activation reconciler reads each named one
// again itself and rewrites the ones that still do not decode: one Segment
// from the Leader's boundary carrying the content the Query Group runs and
// its activation records unchanged, fenced on the bytes that did not decode,
// with the Segment marked (execution.SegmentRepair) so the Worker records the
// Slots lost with the old timeline instead of jumping past them silently. A
// cutover that meets such bytes rewrites them the same way rather than
// holding the Query Group back. The Leader decodes for itself and rewrites
// only what it cannot read either: a Worker of an older build that cannot
// read a newer timeline names it, and the Leader finds it decodes.
//
// Retire when: the upstream snapshot after the one that adds the repair is
// merged, the older-build case above is gone: every Worker an upgrade then
// meets reads the timelines this build writes. The repair itself and
// decodes_again stay, for corrupt bytes and for a name that outlives its
// rewrite.

// TimelineRepairOutcome is what became of one Query Group named as having an
// unreadable timeline.
type TimelineRepairOutcome string

const (
	// TimelineRepairRewritten: the timeline still did not decode and was
	// rewritten.
	TimelineRepairRewritten TimelineRepairOutcome = "rewritten"
	// TimelineRepairDecodesAgain: read again by the Leader, it decoded; it is
	// left alone. A Worker's name outlives the rewrite until its next Slot
	// runs, and a Worker of another build may name a timeline this build reads.
	TimelineRepairDecodesAgain TimelineRepairOutcome = "decodes_again"
	// TimelineRepairConflict: the rewrite lost its compare-and-set - the
	// timeline or the activation moved since it was read. Nothing is written;
	// the Worker still names it and the next round tries again.
	TimelineRepairConflict TimelineRepairOutcome = "conflict"
	// TimelineRepairFailed: the replacement could not be built - the content
	// its records name could not be read, or the records do not fit it. The
	// timeline is left as it is and the next round tries again.
	TimelineRepairFailed TimelineRepairOutcome = "failed"
	// TimelineRepairOtherSchema: the bytes say they are a timeline of another
	// schema version. A build that reads them wrote them, and rewriting them
	// here would destroy what that build runs by; they are left alone.
	TimelineRepairOtherSchema TimelineRepairOutcome = "other_schema"
)

// TimelineRepairOutcomes is the closed list, for the counter's series.
var TimelineRepairOutcomes = []TimelineRepairOutcome{
	TimelineRepairRewritten, TimelineRepairDecodesAgain, TimelineRepairConflict, TimelineRepairFailed, TimelineRepairOtherSchema,
}

// timelineRepairCounts is every outcome this process reached, read at scrape
// time.
type timelineRepairCounts struct {
	mu     sync.Mutex
	counts map[TimelineRepairOutcome]uint64
}

func (counts *timelineRepairCounts) add(outcome TimelineRepairOutcome, n int) {
	if n <= 0 {
		return
	}
	counts.mu.Lock()
	defer counts.mu.Unlock()
	if counts.counts == nil {
		counts.counts = make(map[TimelineRepairOutcome]uint64, len(TimelineRepairOutcomes))
	}
	counts.counts[outcome] += uint64(n)
}

// TimelineRepairCounts is every outcome, zero included.
func (repository *RedisCatalogRepository) TimelineRepairCounts() map[TimelineRepairOutcome]uint64 {
	result := make(map[TimelineRepairOutcome]uint64, len(TimelineRepairOutcomes))
	if repository == nil {
		return result
	}
	repository.timelineRepairs.mu.Lock()
	defer repository.timelineRepairs.mu.Unlock()
	for _, outcome := range TimelineRepairOutcomes {
		result[outcome] = repository.timelineRepairs.counts[outcome]
	}
	return result
}

// TimelineRepairSource names the Query Groups the Workers report meeting an
// unreadable timeline for, as the Leader last read them. A name is a request
// to look, not a verdict: the reconciler reads each again itself.
type TimelineRepairSource interface {
	UnreadableTimelines() []execution.QueryGroupIdentity
}

// TimelineRepairRequests is the Leader's copy of the names, replaced whole by
// each round that reads the registrations and read by the next activation.
type TimelineRepairRequests struct {
	mu    sync.Mutex
	named []execution.QueryGroupIdentity
}

// Replace makes the names the round read the ones the next activation looks
// at; nothing named empties them.
func (requests *TimelineRepairRequests) Replace(named []execution.QueryGroupIdentity) {
	if requests == nil {
		return
	}
	copied := append([]execution.QueryGroupIdentity(nil), named...)
	sort.Slice(copied, func(i, j int) bool { return copied[i] < copied[j] })
	requests.mu.Lock()
	requests.named = copied
	requests.mu.Unlock()
}

// UnreadableTimelines is the names last replaced, sorted.
func (requests *TimelineRepairRequests) UnreadableTimelines() []execution.QueryGroupIdentity {
	if requests == nil {
		return nil
	}
	requests.mu.Lock()
	defer requests.mu.Unlock()
	return append([]execution.QueryGroupIdentity(nil), requests.named...)
}

// WithTimelineRepairs gives the reconciler the names to look at. Without it
// nothing is looked at again and an unreadable timeline is rewritten only
// when a cutover reads it.
func (reconciler *ScheduleActivationReconciler) WithTimelineRepairs(source TimelineRepairSource) *ScheduleActivationReconciler {
	if reconciler != nil {
		reconciler.repairs = source
	}
	return reconciler
}

// reportedTimelines is the names as a set, nil when there are none.
func (reconciler *ScheduleActivationReconciler) reportedTimelines() map[execution.QueryGroupIdentity]struct{} {
	if reconciler == nil || reconciler.repairs == nil {
		return nil
	}
	named := reconciler.repairs.UnreadableTimelines()
	if len(named) == 0 {
		return nil
	}
	set := make(map[execution.QueryGroupIdentity]struct{}, len(named))
	for _, queryGroup := range named {
		if queryGroup != "" {
			set[queryGroup] = struct{}{}
		}
	}
	return set
}

// writtenByAnotherSchema says the bytes, which did not decode as a timeline
// of this build, still say they are a timeline of another schema version.
func writtenByAnotherSchema(raw []byte) bool {
	var header struct {
		SchemaVersion string `json:"schema_version"`
	}
	if json.Unmarshal(raw, &header) != nil {
		return false
	}
	return header.SchemaVersion != "" && header.SchemaVersion != scheduleTimelineSchemaVersion
}

// assignmentTimelineRevision is the timeline revision the Query Group's
// Assignment record names now, zero when there is no record (or the
// repository does not know where records live). A rewritten timeline goes
// one above it: a Worker reads its timeline at the revision its lease brings
// back from the record, and a revision that did not move forward could be
// answered from a body cached before the old timeline went bad.
func (repository *RedisCatalogRepository) assignmentTimelineRevision(ctx context.Context, queryGroup execution.QueryGroupIdentity) (uint64, error) {
	if repository.assignmentRecordKey == nil {
		return 0, nil
	}
	value, err := repository.client.HGet(ctx, repository.assignmentRecordKey(queryGroup), "timeline_record_revision").Result()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, activationDependencyIO(err)
	}
	revision, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, nil
	}
	return revision, nil
}

// segmentContentFor is the publication, the Query Group and the content
// names a Segment opened for it at a boundary carries: the publication's own
// when the candidate records are its, and the publication the records were
// activated under otherwise - the content did not change, the records are
// kept as they were, and a Segment must name what its records name.
func (repository *RedisCatalogRepository) segmentContentFor(
	ctx context.Context,
	published *publishedGroups,
	candidate ActivationState,
	group QueryGroup,
) (SnapshotPublicationRef, QueryGroup, ContentEntry, error) {
	publication, named := published.content.Publication, published.content.Groups[group.Identity]
	carried, ok := candidatePublication(candidate, group)
	if !ok || carried == publication {
		return publication, group, named, nil
	}
	carriedGroup, err := repository.loadPublishedQueryGroup(ctx, carried, group.Identity)
	if err != nil {
		return SnapshotPublicationRef{}, QueryGroup{}, ContentEntry{}, err
	}
	content, err := repository.LoadPublishedContent(ctx, carried)
	if err != nil {
		return SnapshotPublicationRef{}, QueryGroup{}, ContentEntry{}, err
	}
	return carried, carriedGroup, content.Groups[group.Identity], nil
}

// repairedQueryGroupTimeline is the timeline a Query Group gets in place of
// one the Leader could not carry on: one Segment opened at boundary with the
// content the Query Group runs and its activation records unchanged - no new
// state generation, nothing re-warms - marked with repair, at a revision
// above the one its Assignment record names, written only if the key still
// holds expected (the bytes read). group must be materialized.
func (repository *RedisCatalogRepository) repairedQueryGroupTimeline(
	ctx context.Context,
	published *publishedGroups,
	candidate ActivationState,
	group QueryGroup,
	boundary execution.EvaluationTime,
	expected []byte,
	repair execution.SegmentRepair,
) (openedQueryGroupTimeline, error) {
	publication, openGroup, named, err := repository.segmentContentFor(ctx, published, candidate, group)
	if err != nil {
		return openedQueryGroupTimeline{}, err
	}
	segment, err := scheduleSegmentForGroup(publication, openGroup, boundary, named)
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
	revision, err := repository.assignmentTimelineRevision(ctx, group.Identity)
	if err != nil {
		return openedQueryGroupTimeline{}, err
	}
	mark := repair
	timeline := persistedScheduleTimeline{SchemaVersion: scheduleTimelineSchemaVersion,
		RecordRevision: revision + 1, QueryGroup: group.Identity,
		Segments: []persistedScheduleSegment{{Schedule: opened, Plans: records, Repair: &mark}}}
	if err := validateScheduleTimeline(timeline); err != nil {
		return openedQueryGroupTimeline{}, err
	}
	return openedQueryGroupTimeline{update: scheduleTimelineUpdate{expected: expected, next: timeline}, records: records}, nil
}

// RepairUnreadableTimelines rewrites, under the current publication, the
// timelines of the named Query Groups that the Leader cannot decode either,
// and reports whether it wrote. It is the half of the repair a reconcile
// with no new publication runs: nothing else would read these timelines
// again before the next publication, however long that is.
//
// Only Query Groups the current activation runs are looked at: its active
// set, which never holds a draining one - a retirement takes a Query Group
// out of the set in the write that retires it. Each is read live and decided
// alone: one that decodes, one
// whose bytes belong to another schema, one whose replacement cannot be built
// is counted and left, and the others go ahead. All the rewrites of a round
// go in one write that advances the activation's record revision once and
// changes nothing else in it: the same Plans, the same Draining, the same
// held-back set. A lost compare-and-set writes nothing and is counted; the
// names are still there next round.
func (repository *RedisCatalogRepository) RepairUnreadableTimelines(
	ctx context.Context,
	previous ActivationState,
	active []execution.QueryGroupIdentity,
	named map[execution.QueryGroupIdentity]struct{},
	boundary execution.EvaluationTime,
) (bool, error) {
	if repository == nil || repository.client == nil || boundary <= 0 || previous.RecordRevision == 0 {
		return false, errors.New("alarmd controlplane: a timeline repair needs a current activation and a boundary")
	}
	candidates := make([]execution.QueryGroupIdentity, 0, len(named))
	for _, queryGroup := range active {
		if _, wanted := named[queryGroup]; wanted {
			candidates = append(candidates, queryGroup)
		}
	}
	if len(candidates) == 0 {
		return false, nil
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i] < candidates[j] })
	var published *publishedGroups
	cutover := newCutoverFacts()
	cutover.contentSource = "timeline_repair"
	updates := make([]scheduleTimelineUpdate, 0, len(candidates))
	rewritten := make(map[execution.QueryGroupIdentity]struct{}, len(candidates))
	for _, queryGroup := range candidates {
		_, raw, readErr := repository.readScheduleTimeline(ctx, queryGroup)
		cutover.read++
		var unreadable *DeterministicScheduleError
		switch {
		case readErr == nil:
			repository.timelineRepairs.add(TimelineRepairDecodesAgain, 1)
			continue
		case errors.Is(readErr, ErrScheduleUnavailable):
			// Gone rather than unreadable: not what was named, and the
			// cutover opens a missing key again on its own.
			continue
		case !errors.As(readErr, &unreadable):
			return false, readErr
		case writtenByAnotherSchema(raw):
			repository.timelineRepairs.add(TimelineRepairOtherSchema, 1)
			continue
		}
		if published == nil {
			loaded, loadErr := repository.loadPublishedGroups(ctx, previous.Current)
			if loadErr != nil {
				return false, loadErr
			}
			published = loaded
		}
		if _, ok := published.groups[queryGroup]; !ok {
			continue
		}
		opened, buildErr := func() (openedQueryGroupTimeline, error) {
			if err := repository.materialize(ctx, published, []execution.QueryGroupIdentity{queryGroup}); err != nil {
				return openedQueryGroupTimeline{}, err
			}
			return repository.repairedQueryGroupTimeline(ctx, published, previous, published.groups[queryGroup], boundary, raw,
				execution.SegmentRepair{Kind: execution.SegmentRepairUnreadable, AtUnixMilli: int64(boundary) * 1000})
		}()
		if buildErr != nil {
			repository.timelineRepairs.add(TimelineRepairFailed, 1)
			repository.observeTimelineRepairFailure(ctx, queryGroup, buildErr)
			continue
		}
		updates = append(updates, opened.update)
		rewritten[queryGroup] = struct{}{}
		cutover.decided(cutoverRepaired)
	}
	if len(updates) == 0 {
		return false, nil
	}
	next := previous
	next.RecordRevision = previous.RecordRevision + 1
	next.Plans = append([]PlanActivationRecord(nil), previous.Plans...)
	next.Draining = append([]DrainingQueryGroup(nil), previous.Draining...)
	// What this process still could not read once the write lands: the
	// timelines it rewrote are read back from now on, the rest stay named.
	next.SkippedTimelines = nil
	for _, skipped := range previous.SkippedTimelines {
		if _, done := rewritten[skipped.QueryGroup]; !done {
			next.SkippedTimelines = append(next.SkippedTimelines, skipped)
		}
	}
	expected := ActivationExpectation{RecordRevision: previous.RecordRevision, Current: previous.Current, Pending: previous.Pending}
	if err := validateActivationTransition(expected, next); err != nil {
		return false, err
	}
	persistErr := repository.persistCutoverActivation(ctx, expected, next, updates, cutover, blockedSetUnchanged)
	// The write is a cutover script's like any other, and is reported as one:
	// a lost compare-and-set reads as the failure it was, though the round
	// itself goes on.
	repository.observeCutover(ctx, cutover, persistErr)
	if errors.Is(persistErr, ErrActivationConflict) {
		repository.timelineRepairs.add(TimelineRepairConflict, len(updates))
		return false, nil
	}
	if persistErr != nil {
		return false, persistErr
	}
	repository.timelineRepairs.add(TimelineRepairRewritten, len(updates))
	if len(previous.SkippedTimelines) > 0 {
		repository.skippedTimelines.stillUnread(previous.SkippedTimelines, rewritten)
	}
	return true, nil
}

// observeTimelineRepairFailure names the Query Group whose replacement could
// not be built, and why: the counter says how many, and the line says which.
// It is the cutover's stage without the cutover's facts, so it is a line and
// not a cutover in the cutover's own counters.
func (repository *RedisCatalogRepository) observeTimelineRepairFailure(ctx context.Context, queryGroup execution.QueryGroupIdentity, err error) {
	repository.observe(ctx, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageScheduleCutover,
		Result: observability.ResultFailed, ReasonCode: observability.ReasonCode(contract.ReasonScheduleUnreadable),
		Trace: observability.TraceFields{QueryGroupKey: string(queryGroup)},
		Err:   fmt.Errorf("rewrite an unreadable timeline: %w", err),
	})
}
