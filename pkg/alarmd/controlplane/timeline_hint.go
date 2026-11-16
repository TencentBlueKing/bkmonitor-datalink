// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"context"
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A timeline revision hint is the Worker's proof, for one Query Group, that
// the timeline it wants is at a known revision: the installed executable
// view previews the number and the lease's renewal brought the same number
// back from the Assignment record (decision-016 batch 4, judgement 3). With
// it, a timeline read is answered from the cache when the cached body is at
// that revision and by one read of the body otherwise - never by the
// activation header, which is the poll this replaces. A body that comes back
// at another revision is not the timeline the hint was about: the read
// falls back to the header path, so a stale hint costs a probe and never a
// wrong Segment.
type timelineRevisionHintKey struct{}

// WithTimelineRevisionHint carries the hint for the Query Group the caller
// is about to read. Zero is no hint.
func WithTimelineRevisionHint(ctx context.Context, revision uint64) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, timelineRevisionHintKey{}, revision)
}

func timelineRevisionHint(ctx context.Context) uint64 {
	if ctx == nil {
		return 0
	}
	revision, _ := ctx.Value(timelineRevisionHintKey{}).(uint64)
	return revision
}

// loadScheduleTimelineAtRevision answers a hinted read: the cached body when
// it is at revision, else one read of the body. The body read is stored
// under whatever version the cache is on, because the hint is its own
// freshness proof. A body at another revision is returned with
// errTimelineRevisionMoved so the caller can take the header path.
func (repository *RedisCatalogRepository) loadScheduleTimelineAtRevision(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	revision uint64,
) (persistedScheduleTimeline, error) {
	counters := &repository.controlReads.hinted
	if timeline, ok := repository.controlCache.lookupTimelineAtRevision(queryGroup, revision); ok {
		counters.hits.Add(1)
		return timeline, nil
	}
	reading := repository.controlCache.announceTimelines(1)
	defer reading.settle()
	timeline, payload, err := repository.readScheduleTimeline(ctx, queryGroup)
	if err != nil {
		return persistedScheduleTimeline{}, err
	}
	if timeline.RecordRevision != revision {
		counters.refreshes.Add(1)
		return persistedScheduleTimeline{}, errTimelineRevisionMoved
	}
	counters.misses.Add(1)
	repository.controlCache.storeTimelineAtCurrentVersion(reading, queryGroup, timeline, len(payload))
	return timeline, nil
}

var errTimelineRevisionMoved = errors.New("alarmd controlplane: the timeline is not at the hinted revision")

// TimelineRevisionHint reads the hint a context carries, zero for none. For
// the callers that build the context and want to see what they built.
func TimelineRevisionHint(ctx context.Context) uint64 {
	return timelineRevisionHint(ctx)
}

// activationsFromTimeline answers a Plan activation request from the Query
// Group's timeline: the Segment the contract's Slot falls in must be the one
// the contract names, and it answers with its own activation records - an
// open one's are the records the activation state carries for the Query
// Group, written from one list in the cutover's one script.
//
// A closed Segment answers a Plan with its own record only while the Plan
// never left: every Segment from this one on carries it, and the last of
// them is open. That is a Slot still in flight, or in a backlog, when an
// edit to the Query Group's content closed the Segment it falls in - the
// Plan ran then under the record this Segment holds, and answering it
// ActivationNone threw the Slot away as PLAN_NOT_ACTIVE although the Plan
// was there. Any other Plan of a closed Segment is ActivationNone, as every
// Plan of one used to be: one deleted or disabled after the Slot, one that
// left and came back (the Segments between do not carry it, and the stretch
// it was away is the PLAN_NOT_ACTIVE / PLAN_REACTIVATED pair the contract
// tells), one whose Query Group retired (no open Segment follows), and one
// that moved to another Query Group. The Segments after the Slot's are all
// still there: pruning drops a prefix (deadSegmentPrefix).
func activationsFromTimeline(request execution.PlanActivationRequest, timeline persistedScheduleTimeline) (execution.PlanActivationResult, error) {
	contractRef := request.Contract
	for index, segment := range timeline.Segments {
		schedule := segment.Schedule
		if !schedule.Segment.Contains(contractRef.Slot.EvaluationTime) {
			continue
		}
		if schedule.Segment.Start != contractRef.ScheduleSegmentStart ||
			schedule.Segment.ScheduleRevision != contractRef.ScheduleRevision ||
			schedule.Segment.Publication.SnapshotRevision != contractRef.SnapshotRevision ||
			schedule.Segment.QueryRevision != contractRef.QueryRevision {
			return execution.PlanActivationResult{}, errors.New("alarmd controlplane: activation request does not reference its persisted Schedule Segment")
		}
		historical := schedule.Segment.End != nil
		byPlan := make(map[execution.PlanKey]execution.PlanActivationFact, len(segment.Plans))
		for _, record := range segment.Plans {
			byPlan[record.Fact.Key()] = record.Fact
		}
		result := execution.PlanActivationResult{Contract: contractRef, Facts: make([]execution.PlanActivationFact, 0, len(request.Plans))}
		for _, plan := range request.Plans {
			fact, found := byPlan[plan]
			if !found || (historical && !carriedToTheOpenSegment(timeline.Segments[index+1:], plan)) {
				fact = execution.PlanActivationFact{Plan: plan.PlanIdentity, Selection: execution.ActivationNone, Shard: shardPointerOf(plan)}
			}
			result.Facts = append(result.Facts, fact)
		}
		if err := result.Validate(request); err != nil {
			return execution.PlanActivationResult{}, err
		}
		return result, nil
	}
	return execution.PlanActivationResult{}, errors.New("alarmd controlplane: activation request has no persisted Schedule Segment")
}

// carriedToTheOpenSegment reports whether every one of the Segments after a
// closed one carries the Plan and the last of them is open: the Plan stayed
// in the activation from that Segment to now.
func carriedToTheOpenSegment(later []persistedScheduleSegment, plan execution.PlanKey) bool {
	if len(later) == 0 || later[len(later)-1].Schedule.Segment.End != nil {
		return false
	}
	for _, segment := range later {
		carried := false
		for _, record := range segment.Plans {
			if record.Fact.Key() == plan {
				carried = true
				break
			}
		}
		if !carried {
			return false
		}
	}
	return true
}

// DrainingContent is what a Query Group the current publication no longer
// carries still executes: the content its timeline's last Segment names,
// with the output context refs in force at the end of it. A draining Query
// Group is one whose timeline has retired; it keeps its assignment until
// Progress reaches the retired boundary, and the Slots left before that
// boundary - the backlog at the retirement, a replay - are run from that
// content. The executable view carries it so a Worker executing from the
// view (decision-016 batch 4b) can finish them. Nothing when the timeline
// is absent or its last Segment names no content.
func (repository *RedisCatalogRepository) DrainingContent(ctx context.Context, queryGroup execution.QueryGroupIdentity) (execution.ObjectDigest, []execution.OutputContextRef, bool, error) {
	timeline, err := repository.loadScheduleTimeline(ctx, queryGroup)
	if errors.Is(err, ErrScheduleUnavailable) {
		return "", nil, false, nil
	}
	if err != nil {
		return "", nil, false, err
	}
	if len(timeline.Segments) == 0 {
		return "", nil, false, nil
	}
	segment := timeline.Segments[len(timeline.Segments)-1].Schedule.Segment
	if segment.ObjectDigest == "" {
		return "", nil, false, nil
	}
	refs := segment.OutputContextRefs
	if count := len(segment.OutputContextRevisions); count > 0 {
		refs = segment.OutputContextRevisions[count-1].Refs
	}
	return segment.ObjectDigest, append([]execution.OutputContextRef(nil), refs...), true, nil
}
