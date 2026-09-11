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

// pruneClosedSegments drops the leading closed Segments of a timeline that no
// Slot will read anymore. It returns how many it dropped and, when it dropped
// nothing because the Query Group's Progress could not be consulted, the
// reason. The final Segment is never dropped: an open timeline reads it as
// the current Segment and a retired one closes exactly at it.
func (repository *RedisCatalogRepository) pruneClosedSegments(
	ctx context.Context,
	timeline *persistedScheduleTimeline,
	now time.Time,
	progress ScheduleActivationProgressReader,
) (dropped int, skipped string) {
	if repository == nil || timeline == nil || repository.segmentRetention.Validate() != nil || progress == nil {
		return 0, ""
	}
	nowMillis := now.UnixMilli()
	// Segments are chronological, so the first one still read bounds the
	// prefix that can go; nothing after it is examined.
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
	if dead == 0 {
		return 0, ""
	}
	load, err := progress.LoadProgress(ctx, execution.ProgressIdentity{QueryGroup: timeline.QueryGroup})
	if err != nil {
		return 0, pruneSkipProgressUnavailable
	}
	if load.Status != execution.ProgressFound || load.Progress == nil {
		return 0, pruneSkipProgressMissing
	}
	floor := progressFloor(load.Progress)
	for dropped < dead {
		end := timeline.Segments[dropped].Schedule.Segment.End
		if end == nil || floor <= 0 || *end > floor {
			break
		}
		dropped++
	}
	if dropped == 0 {
		return 0, ""
	}
	timeline.Segments = append([]persistedScheduleSegment(nil), timeline.Segments[dropped:]...)
	// The tombstone only records the gap to the Segment that preceded it,
	// which is gone; a timeline's first Segment carries none.
	timeline.Segments[0].ReactivatedAfter = nil
	return dropped, ""
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
	started       time.Time
	pruned        int
	skipped       map[string]int
	payloadBytes  int
	timelineBytes []int
}

func newCutoverFacts() *cutoverFacts {
	return &cutoverFacts{started: time.Now(), skipped: make(map[string]int)}
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
		},
	})
}
