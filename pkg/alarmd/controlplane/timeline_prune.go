// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A Schedule timeline is rewritten whole on every publication cutover, and
// until now every Segment ever opened stayed in it: each publication added
// one closed Segment to every Query Group's timeline, so the bytes one
// cutover had to send grew with the number of publications since the
// timeline was created, with no bound. The cutover is one compare-and-set
// call carrying every timeline twice, and a payload that grows every day
// against a fixed write timeout fails on some day for good.
//
// A closed Segment is dead once no Slot in it is read or executed anymore.
// Whether that is so is the scheduler's decision, taken here through the
// same functions the scheduler calls (execution.SegmentKeepUntilUnixMilli),
// and additionally bounded by the Query Group's Progress: the Segment that
// holds the next Slot to run, or an unfinished Slot, is kept whatever its
// age, because the Worker will read it. A Progress that cannot be read
// keeps every Segment for this cutover; pruning never blocks an activation
// and never guesses.

// ConfigureSegmentRetention gives the repository the three durations that
// bound a Slot's lifetime, from the same configuration the scheduler reads.
// Without it no Segment is ever pruned.
func (repository *RedisCatalogRepository) ConfigureSegmentRetention(retention execution.SlotRetention) error {
	if repository == nil {
		return errors.New("alarmd controlplane: Redis catalog repository is required")
	}
	if err := retention.Validate(); err != nil {
		return err
	}
	repository.segmentRetention = retention
	return nil
}

const (
	pruneSkipProgressUnavailable = "progress_unavailable"
	pruneSkipProgressMissing     = "progress_missing"
)

// progressFloor is the earliest Slot the Worker will still read for a Query
// Group: the next Slot to run, or an unfinished Slot or range still being
// completed, whichever is earliest.
func progressFloor(progress *execution.ScheduleProgress) execution.EvaluationTime {
	if progress == nil {
		return 0
	}
	floor := progress.NextSlot
	lower := func(at execution.EvaluationTime) {
		if at > 0 && (floor == 0 || at < floor) {
			floor = at
		}
	}
	if progress.UnfinishedSlot != nil {
		lower(progress.UnfinishedSlot.Contract.Slot.EvaluationTime)
	}
	if progress.UnfinishedRange != nil {
		lower(progress.UnfinishedRange.First.Contract.Slot.EvaluationTime)
	}
	return floor
}

// ScheduleActivationProgressBatchReader is the optional batched form of
// ScheduleActivationProgressReader. The cutover consults the Progress of
// every Query Group with a dead Segment prefix, which on a large deployment
// is most of them, and it sits on the path that already runs close to the
// Redis write timeout; a reader that offers the batch answers in a few round
// trips instead of one per Query Group.
type ScheduleActivationProgressBatchReader interface {
	ScheduleActivationProgressReader
	LoadProgressBatch(context.Context, []execution.ProgressIdentity) ([]execution.ProgressLoadResult, []error)
}

// deadSegmentPrefix counts the leading closed Segments of a timeline whose
// every Slot is past its keep-until instant at now. Segments are
// chronological, so the first one still read bounds the count and nothing
// after it is examined. The final Segment is never counted: an open
// timeline reads it as the current Segment and a retired one closes exactly
// at it.
func (repository *RedisCatalogRepository) deadSegmentPrefix(timeline persistedScheduleTimeline, now time.Time) int {
	if repository == nil || repository.segmentRetention.Validate() != nil {
		return 0
	}
	nowMillis := now.UnixMilli()
	dead := 0
	for index := 0; index+1 < len(timeline.Segments); index++ {
		schedule := timeline.Segments[index].Schedule
		if schedule.Segment.End == nil {
			break
		}
		keepUntil, err := execution.SegmentKeepUntilUnixMilli(schedule, repository.segmentRetention)
		if err != nil || nowMillis < keepUntil {
			break
		}
		dead++
	}
	return dead
}

// prunePrefix drops up to dead leading Segments of a timeline, stopping at
// the first one that ends after floor, the earliest Slot the Worker still
// reads. It returns how many it dropped.
func prunePrefix(timeline *persistedScheduleTimeline, dead int, floor execution.EvaluationTime) int {
	if timeline == nil || dead <= 0 || floor <= 0 {
		return 0
	}
	dropped := 0
	for dropped < dead && dropped+1 < len(timeline.Segments) {
		end := timeline.Segments[dropped].Schedule.Segment.End
		if end == nil || *end > floor {
			break
		}
		dropped++
	}
	if dropped == 0 {
		return 0
	}
	timeline.Segments = append([]persistedScheduleSegment(nil), timeline.Segments[dropped:]...)
	// The tombstone only records the gap to the Segment that preceded it,
	// which is gone; a timeline's first Segment carries none.
	timeline.Segments[0].ReactivatedAfter = nil
	return dropped
}

// pruneCandidate is one timeline of a cutover with a dead Segment prefix,
// waiting for its Query Group's Progress to bound the pruning.
type pruneCandidate struct {
	update int
	dead   int
}

// pruneTimelines applies the Progress floor to every candidate timeline of a
// cutover. Progress is read in one batch when the reader offers it. A
// timeline whose Progress could not be read, or is missing, is left whole
// and the reason counted; pruning never blocks an activation and never
// guesses. Pruned timelines are validated again before they are written.
func (repository *RedisCatalogRepository) pruneTimelines(
	ctx context.Context,
	updates []scheduleTimelineUpdate,
	candidates []pruneCandidate,
	progress ScheduleActivationProgressReader,
	facts *cutoverFacts,
) error {
	if len(candidates) == 0 || progress == nil {
		return nil
	}
	identities := make([]execution.ProgressIdentity, len(candidates))
	for index, candidate := range candidates {
		identities[index] = execution.ProgressIdentity{QueryGroup: updates[candidate.update].next.QueryGroup}
	}
	var loads []execution.ProgressLoadResult
	var errs []error
	if batched, ok := progress.(ScheduleActivationProgressBatchReader); ok {
		loads, errs = batched.LoadProgressBatch(ctx, identities)
	} else {
		loads, errs = make([]execution.ProgressLoadResult, len(identities)), make([]error, len(identities))
		for index, identity := range identities {
			loads[index], errs[index] = progress.LoadProgress(ctx, identity)
		}
	}
	if len(loads) != len(candidates) || len(errs) != len(candidates) {
		return errors.New("alarmd controlplane: batched Progress load returned the wrong shape")
	}
	for index, candidate := range candidates {
		switch {
		case errs[index] != nil:
			facts.prune(0, pruneSkipProgressUnavailable)
			continue
		case loads[index].Status != execution.ProgressFound || loads[index].Progress == nil:
			facts.prune(0, pruneSkipProgressMissing)
			continue
		}
		timeline := &updates[candidate.update].next
		dropped := prunePrefix(timeline, candidate.dead, progressFloor(loads[index].Progress))
		if dropped == 0 {
			continue
		}
		if err := validateScheduleTimeline(*timeline); err != nil {
			return err
		}
		facts.prune(dropped, "")
	}
	return nil
}

// timelineFence is what the cutover script compares a stored timeline
// against: the SHA-1 of the bytes the caller read, or the empty string when
// the caller read nothing and the key must be absent.
func timelineFence(expected []byte) string {
	if len(expected) == 0 {
		return ""
	}
	sum := sha1.Sum(expected)
	return hex.EncodeToString(sum[:])
}

// cutoverFacts accumulates what one publication cutover did, for the
// observation emitted when it returns. Nil is a valid receiver so the
// per-Query-Group cutover, which has no Progress reader and prunes nothing,
// can share the persistence path.
type cutoverFacts struct {
	started         time.Time
	pruned          int
	skipped         map[string]int
	payloadBytes    int
	timelineBytes   []int
	decisions       map[string]int
	read            int
	revisionsFolded int
	contentSource   string
}

func newCutoverFacts() *cutoverFacts {
	return &cutoverFacts{started: time.Now(), skipped: make(map[string]int), decisions: make(map[string]int)}
}

func (facts *cutoverFacts) decided(decision contentCutoverDecision) {
	if facts == nil {
		return
	}
	facts.decisions[string(decision)]++
}

func (facts *cutoverFacts) prune(dropped int, skipped string) {
	if facts == nil {
		return
	}
	facts.pruned += dropped
	if skipped != "" {
		facts.skipped[skipped]++
	}
}

func (facts *cutoverFacts) persisted(payloadBytes int, timelineBytes []int) {
	if facts == nil {
		return
	}
	facts.payloadBytes, facts.timelineBytes = payloadBytes, timelineBytes
}

func (repository *RedisCatalogRepository) observeCutover(ctx context.Context, facts *cutoverFacts, err error) {
	if repository == nil || facts == nil {
		return
	}
	result := "success"
	if err != nil {
		result = "failure"
	}
	largest := 0
	for _, size := range facts.timelineBytes {
		if size > largest {
			largest = size
		}
	}
	repository.observe(ctx, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageScheduleCutover,
		Result: observability.Result(result), ScheduleCutover: &observability.ScheduleCutoverFacts{
			Result: result, Timelines: len(facts.timelineBytes), PayloadBytes: facts.payloadBytes,
			MaxTimelineBytes: largest, TimelineBytes: facts.timelineBytes, SegmentsPruned: facts.pruned,
			PrunesSkipped: facts.skipped, Duration: time.Since(facts.started),
			QueryGroups: facts.decisions, TimelinesRead: facts.read, RevisionsFolded: facts.revisionsFolded,
			ContentSource: facts.contentSource,
		},
	})
}
