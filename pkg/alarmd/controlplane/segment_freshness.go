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
	"encoding/json"
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The states a Segment's execution content can be in relative to what is
// published now.
const (
	// SegmentContentCurrent is a Segment naming the same object the latest
	// publication names for its Query Group.
	SegmentContentCurrent = "current"
	// SegmentContentStale is a Segment naming a different object. The fleet is
	// executing content that is no longer what the control plane publishes,
	// and every field of that content is whatever it was when the Segment was
	// cut.
	SegmentContentStale = "stale"
	// SegmentContentLegacy is a Segment that names no object at all and is
	// served from the Snapshot instead. Nothing to compare.
	SegmentContentLegacy = "legacy"
	// SegmentContentUnknown is a comparison that could not be made: no
	// publication, no manifest, or the manifest does not list the Query Group.
	// Reported rather than folded into either answer, because "could not
	// check" and "checked and it is current" are the two a reader must not
	// confuse.
	SegmentContentUnknown = "unknown"
)

// SegmentContentStates is every state, for the metric to create each label at
// startup and for a reader to bound the family by.
var SegmentContentStates = []string{
	SegmentContentCurrent, SegmentContentStale, SegmentContentLegacy, SegmentContentUnknown,
}

// observeSegmentContentFreshness reports whether the Segment a Slot executes
// names the object the latest publication names.
//
// Nothing else says this. A Segment is cut when the schedule changes, and it
// carries the object digest it was cut with; a publication that changes
// execution content writes new objects under new digests and leaves the old
// ones in place, renewed. A fleet whose Segments are not recut then keeps
// executing the old content indefinitely, with every metric healthy: the
// objects load, the digests verify, the Plans compile, the Slots pass. The
// only symptom is that a change made in the source never takes effect, which
// looks like the change being wrong.
//
// This is not specific to no-data. Any execution field behaves this way; no-data
// is only how it was found, after three releases of looking for a dropped field
// on a path that was carrying it correctly the whole time.
//
// It never fails the Slot. A Slot executing stale content is executing content
// that was valid when it was cut, and stopping it would turn a reporting gap
// into an outage. The number is the point.
func (repository *RedisCatalogRepository) observeSegmentContentFreshness(
	ctx context.Context, segment execution.ScheduleSegmentFact,
) {
	state, published := repository.segmentContentState(ctx, segment)
	repository.observe(ctx, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageFrozenPlanGeneration,
		Result: observability.ResultSuccess,
		Trace: observability.TraceFields{
			QueryGroupKey:    string(segment.QueryGroup),
			SnapshotRevision: string(segment.Publication.SnapshotRevision),
			RecordID:         string(segment.ObjectDigest),
			QueryRevision:    string(published),
		},
		SegmentContent: &observability.SegmentContentFacts{State: state},
	})
}

// segmentContentState compares the Segment's object with the published one and
// returns the state and the digest the latest publication names, which is empty
// whenever the comparison could not be made.
//
// It runs on every frozen Slot, so both reads it makes are served from this
// process where they can be; see segment_freshness_cache.go for what that
// costs and what it does not change. Anything but "current" is re-asked
// against a freshly read publication pointer, once per reading, because a
// Segment a cutover has just recut is newer than the memo and must not be
// reported stale on account of this process's own cache.
func (repository *RedisCatalogRepository) segmentContentState(
	ctx context.Context, segment execution.ScheduleSegmentFact,
) (string, execution.ObjectDigest) {
	if segment.ObjectDigest == "" {
		return SegmentContentLegacy, ""
	}
	state, digest := repository.compareSegmentWithPublished(ctx, segment)
	if state == SegmentContentCurrent {
		return state, digest
	}
	if !repository.refreshFreshnessPublication(ctx) {
		return state, digest
	}
	return repository.compareSegmentWithPublished(ctx, segment)
}

func (repository *RedisCatalogRepository) compareSegmentWithPublished(
	ctx context.Context, segment execution.ScheduleSegmentFact,
) (string, execution.ObjectDigest) {
	publication, err := repository.freshnessPublication(ctx)
	if err != nil {
		return SegmentContentUnknown, ""
	}
	manifest, err := repository.cachedCatalogManifest(ctx, publication.SnapshotRevision)
	if err != nil {
		var corrupt *PersistedSnapshotCorruptError
		if errors.Is(err, ErrCatalogManifestUnavailable) || errors.As(err, &corrupt) {
			return SegmentContentUnknown, ""
		}
		return SegmentContentUnknown, ""
	}
	for _, group := range manifest.QueryGroups {
		if group.QueryGroup != segment.QueryGroup {
			continue
		}
		if group.ObjectDigest == segment.ObjectDigest {
			return SegmentContentCurrent, group.ObjectDigest
		}
		return SegmentContentStale, group.ObjectDigest
	}
	// The latest publication does not contain this Query Group at all. That is
	// its own answer - the Segment is executing something the control plane no
	// longer publishes - but it is not the same as naming a different object,
	// and calling it stale would hide a Query Group that has been retired and
	// is still running.
	return SegmentContentUnknown, ""
}

// ObserveSegmentContentFreshnessForTest drives the freshness comparison for a
// Segment directly, so the states a Slot cannot reach -- no publication to
// compare against, a Segment naming no object -- can be asserted. Those are
// the two that must not be reported as current, and a repository in either
// state cannot freeze a Slot to get there the ordinary way.
func ObserveSegmentContentFreshnessForTest(
	repository *RedisCatalogRepository, ctx context.Context, segment execution.ScheduleSegmentFact,
) {
	repository.observeSegmentContentFreshness(ctx, segment)
}

// SetOpenSegmentObjectDigestForTest puts a Query Group's open Segment on the
// given object digest.
//
// It exists to reproduce the state production reached and nothing else can
// construct: a Segment naming content that neither the activation nor any
// publication names. That state was produced by a Segment being cut from a
// group an assembly had quietly changed -- a path the code no longer has, and
// which therefore cannot be reached by publishing.
func SetOpenSegmentObjectDigestForTest(
	ctx context.Context, repository *RedisCatalogRepository,
	group execution.QueryGroupIdentity, digest execution.ObjectDigest,
	refs ...execution.OutputContextRef,
) error {
	timeline, raw, err := repository.loadScheduleTimelineForUpdate(ctx, group)
	if err != nil {
		return err
	}
	last := len(timeline.Segments) - 1
	if last < 0 {
		return errors.New("alarmd controlplane: the Query Group has no Segment to put on a digest")
	}
	timeline.Segments[last].Schedule.Segment.ObjectDigest = digest
	if len(refs) > 0 {
		timeline.Segments[last].Schedule.Segment.OutputContextRefs = refs
		timeline.Segments[last].Schedule.Segment.OutputContextRevisions = nil
	}
	timeline.RecordRevision++
	payload, err := json.Marshal(timeline)
	if err != nil {
		return err
	}
	_ = raw
	return repository.client.Set(ctx, repository.scheduleTimelineKey(group), payload, 0).Err()
}

// OpenSegmentObjectDigestForTest reads the open Segment's object digest
// straight from the timeline, past the caches a Worker read goes through.
func OpenSegmentObjectDigestForTest(
	ctx context.Context, repository *RedisCatalogRepository, group execution.QueryGroupIdentity,
) (execution.ObjectDigest, error) {
	timeline, _, err := repository.loadScheduleTimelineForUpdate(ctx, group)
	if err != nil {
		return "", err
	}
	last := len(timeline.Segments) - 1
	if last < 0 {
		return "", errors.New("alarmd controlplane: the Query Group has no Segment")
	}
	return timeline.Segments[last].Schedule.Segment.ObjectDigest, nil
}

// ScheduleTimelineBytesForTest is the stored bytes of a Query Group's schedule
// timeline, so a test can assert a refused cutover wrote nothing.
//
// Reading the bytes rather than the decoded timeline is the point: "the
// cutover returned an error before the write" is a statement about code order,
// and code order is what the next edit changes.
func ScheduleTimelineBytesForTest(
	ctx context.Context, repository *RedisCatalogRepository, group execution.QueryGroupIdentity,
) ([]byte, error) {
	return repository.client.Get(ctx, repository.scheduleTimelineKey(group)).Bytes()
}

// ActivationBytesForTest is the stored bytes of the current activation.
func ActivationBytesForTest(ctx context.Context, repository *RedisCatalogRepository) ([]byte, error) {
	return repository.client.Get(ctx, repository.activationKey()).Bytes()
}
