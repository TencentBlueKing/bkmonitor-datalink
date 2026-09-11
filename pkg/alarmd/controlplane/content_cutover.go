// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A publication cutover is decided per Query Group by its execution
// content, not by the publication it arrived in. The one persisted fact that
// decides it is the ObjectDigest on the Query Group's open Segment: equal to
// the digest of the Query Group in the new publication, the Segment is kept
// and its Plan records are carried over verbatim; different, or absent on a
// Segment written before Segments named their content, the Segment is cut
// the way every Segment used to be. An edit that changes only what Plans
// render with appends an output context revision to the open Segment.
//
// The decision lives here and nowhere else. The reconciler hands in a
// candidate record for every Plan of the new publication; which candidates
// are persisted and which previous records are carried instead is settled
// against the open Segments, so the two sides cannot disagree. The existing
// checks that an open Segment's records equal the current activation and
// that a newly opened Segment's records name its own publication then act as
// the self-check of that decision.

// contentCutoverClockAllowance is the clock difference between the Control
// Leader and a Worker that an output context revision tolerates. It is a
// placed value, not a measured one: nodes of one cluster under NTP usually
// differ by well under a second. Measure the actual bound from the leader's
// activation boundary against the Workers' freeze timestamps and tighten it;
// every second here delays a rendering-only edit by a second.
const contentCutoverClockAllowance = 18 * time.Second

// contentCutoverDefaultWriteBound is what a Redis client without options
// reports: go-redis v8 defaults of a 3 s write timeout and 3 retries.
const contentCutoverDefaultWriteBound = 3 * time.Second * 4

// outputContextRevisionFloor is how far past the activation boundary an
// output context revision takes effect. A Worker may freeze a Slot at
// evaluation time T as early as T minus the clock difference, and the cutover
// write lands at most the write bound after the boundary; a revision that
// took effect earlier than boundary + write bound + clock difference could
// be missed by the first attempt at a Slot and seen by a retry, which would
// render one judgement under two contexts. The write bound is derived from
// the client's write timeout and retry count so that changing either moves
// the floor with it; the schedule period of the Query Group plays no part,
// it only decides which Slot after the floor is the first one affected.
func (repository *RedisCatalogRepository) outputContextRevisionFloor() time.Duration {
	writeBound := contentCutoverDefaultWriteBound
	if configured, ok := repository.client.(interface{ Options() *redis.Options }); ok && configured.Options() != nil {
		options := configured.Options()
		timeout := options.WriteTimeout
		if timeout <= 0 {
			timeout = options.ReadTimeout
		}
		if timeout <= 0 {
			timeout = 3 * time.Second
		}
		// A constructed client reports normalized options: the default of
		// three retries is already filled in and a disabled retry count is
		// already zero.
		retries := options.MaxRetries
		if retries < 0 {
			retries = 0
		}
		writeBound = timeout * time.Duration(1+retries)
	}
	return writeBound + contentCutoverClockAllowance
}

// outputContextRevisionSince places a new revision on an open Segment: the
// floor past the boundary, and past any revision already on the Segment so
// the revisions stay ordered when two edits land within one floor.
func (repository *RedisCatalogRepository) outputContextRevisionSince(segment execution.ScheduleSegmentFact, boundary execution.EvaluationTime) execution.EvaluationTime {
	since := boundary + execution.EvaluationTime(repository.outputContextRevisionFloor()/time.Second)
	if since <= segment.Start {
		since = segment.Start + 1
	}
	if count := len(segment.OutputContextRevisions); count > 0 && segment.OutputContextRevisions[count-1].Since >= since {
		since = segment.OutputContextRevisions[count-1].Since + 1
	}
	return since
}

// segmentContent is what a Segment names about a Query Group: its execution
// content and the output context of each Plan.
type segmentContent struct {
	digest execution.ObjectDigest
	refs   []execution.OutputContextRef
}

func contentOf(group QueryGroup) (segmentContent, error) {
	digest, err := DeriveQueryGroupObjectDigest(group)
	if err != nil {
		return segmentContent{}, err
	}
	refs := make([]execution.OutputContextRef, 0, len(group.Plans))
	for _, plan := range group.Plans {
		contextDigest, err := DeriveOutputContextDigest(plan)
		if err != nil {
			return segmentContent{}, err
		}
		refs = append(refs, execution.OutputContextRef{Plan: plan.Identity, Digest: contextDigest})
	}
	sort.Slice(refs, func(i, j int) bool { return lessPlanIdentity(refs[i].Plan, refs[j].Plan) })
	return segmentContent{digest: digest, refs: refs}, nil
}

// activatedContent is the population the current activation runs, with the
// content each Query Group runs when the source can tell. The manifest of
// the current publication tells it in one small read; the whole Snapshot
// tells it at the cost of deriving every digest; open Segments tell it when
// neither is stored anymore. Complete is false only when the population
// came from a source that could not name the content, which makes every
// Query Group a candidate for reading its timeline.
type activatedContent struct {
	groups   map[execution.QueryGroupIdentity]QueryGroup
	digests  map[execution.QueryGroupIdentity]execution.ObjectDigest
	contexts map[execution.PlanIdentity]execution.OutputContextDigest
	complete bool
	source   string
}

func (content activatedContent) refsFor(group QueryGroup) ([]execution.OutputContextRef, bool) {
	refs := make([]execution.OutputContextRef, 0, len(group.Plans))
	for _, plan := range group.Plans {
		digest, ok := content.contexts[plan.Identity]
		if !ok {
			return nil, false
		}
		refs = append(refs, execution.OutputContextRef{Plan: plan.Identity, Digest: digest})
	}
	return refs, true
}

func (repository *RedisCatalogRepository) loadActivatedContent(ctx context.Context, activation ActivationState) (activatedContent, error) {
	manifest, err := repository.LoadCatalogManifest(ctx, activation.Current.SnapshotRevision)
	if err == nil {
		content := activatedContent{
			groups:   make(map[execution.QueryGroupIdentity]QueryGroup, len(manifest.QueryGroups)),
			digests:  make(map[execution.QueryGroupIdentity]execution.ObjectDigest, len(manifest.QueryGroups)),
			contexts: make(map[execution.PlanIdentity]execution.OutputContextDigest, len(manifest.Plans)),
			complete: true, source: "manifest",
		}
		for _, entry := range manifest.QueryGroups {
			if _, duplicate := content.groups[entry.QueryGroup]; duplicate || entry.QueryGroup == "" || entry.ObjectDigest == "" {
				return activatedContent{}, errors.New("alarmd controlplane: catalog manifest names a Query Group twice or without content")
			}
			content.groups[entry.QueryGroup] = QueryGroup{Identity: entry.QueryGroup}
			content.digests[entry.QueryGroup] = entry.ObjectDigest
		}
		for _, entry := range manifest.Plans {
			content.contexts[entry.Plan] = entry.ContextDigest
		}
		return content, nil
	}
	if !errors.Is(err, ErrCatalogManifestUnavailable) {
		return activatedContent{}, err
	}
	snapshot, err := repository.LoadPublishedSnapshot(ctx, activation.Current)
	if err == nil {
		groups, err := queryGroupMap(snapshot.QueryGroups)
		if err != nil {
			return activatedContent{}, err
		}
		content := activatedContent{groups: groups,
			digests:  make(map[execution.QueryGroupIdentity]execution.ObjectDigest, len(groups)),
			contexts: make(map[execution.PlanIdentity]execution.OutputContextDigest),
			complete: true, source: "snapshot"}
		for identity, group := range groups {
			named, err := contentOf(group)
			if err != nil {
				return activatedContent{}, err
			}
			content.digests[identity] = named.digest
			for _, ref := range named.refs {
				content.contexts[ref.Plan] = ref.Digest
			}
		}
		return content, nil
	}
	if !errors.Is(err, ErrSnapshotUnavailable) {
		return activatedContent{}, err
	}
	var groups map[execution.QueryGroupIdentity]QueryGroup
	digests := map[execution.QueryGroupIdentity]execution.ObjectDigest{}
	if activation.SchemaVersion == activationSchemaVersion {
		identities, err := repository.LoadActiveQueryGroupSet(ctx, activation.ActiveQGSetRef)
		if err != nil {
			return activatedContent{}, err
		}
		candidates := make(map[execution.QueryGroupIdentity]QueryGroup, len(identities))
		for _, identity := range identities {
			candidates[identity] = QueryGroup{Identity: identity}
		}
		groups, digests, err = repository.loadActivatedGroupsFromOpenSchedules(ctx, activation, candidates)
		if err != nil {
			return activatedContent{}, err
		}
	} else {
		groups, err = repository.loadActivatedGroupsFromScheduleScan(ctx, activation)
		if err != nil {
			return activatedContent{}, err
		}
	}
	// Open Segments name the execution content but not the output contexts
	// in a form that can be compared per Plan without reading them again, so
	// this source is incomplete: every carried-over Query Group is read at
	// cutover.
	return activatedContent{groups: groups, digests: digests,
		contexts: map[execution.PlanIdentity]execution.OutputContextDigest{},
		complete: false, source: "open_segments"}, nil
}

// contentCutoverDecision is what the cutover did with one carried-over
// Query Group; the counts are reported with the cutover.
type contentCutoverDecision string

const (
	cutoverKept      contentCutoverDecision = "kept"
	cutoverRevised   contentCutoverDecision = "revised"
	cutoverCut       contentCutoverDecision = "cut"
	cutoverLegacyCut contentCutoverDecision = "legacy_cut"
	cutoverRetired   contentCutoverDecision = "retired"
	cutoverAdded     contentCutoverDecision = "added"
)

// validateContentCoverage checks that the assembled activation names every
// Plan of the new publication exactly once, and no other.
func validateContentCoverage(plans []PlanActivationRecord, groups map[execution.QueryGroupIdentity]QueryGroup) error {
	wanted := make(map[execution.PlanIdentity]struct{})
	for _, group := range groups {
		for _, plan := range group.Plans {
			if _, duplicate := wanted[plan.Identity]; duplicate {
				return errors.New("alarmd controlplane: publication names a Plan in two Query Groups")
			}
			wanted[plan.Identity] = struct{}{}
		}
	}
	covered := make(map[execution.PlanIdentity]struct{}, len(plans))
	for _, record := range plans {
		if _, duplicate := covered[record.Fact.Plan]; duplicate {
			return errors.New("alarmd controlplane: content cutover activated a Plan twice")
		}
		if _, ok := wanted[record.Fact.Plan]; !ok {
			return errors.New("alarmd controlplane: content cutover activated a Plan outside the publication")
		}
		covered[record.Fact.Plan] = struct{}{}
	}
	if len(covered) != len(wanted) {
		return errors.New("alarmd controlplane: content cutover must activate every Plan of the publication")
	}
	return nil
}

// pruneOutputContextRevisions folds into the base refs every revision of an
// open Segment that no Slot can distinguish from the base anymore: once
// every Slot before a revision's Since is past its keep-until instant,
// nothing resolves the refs that revision replaced, so the revision becomes
// the base. The bound is the conservative one used for closed Segments,
// taken from the revision's Since instead of the Segment end, so a revision
// is never folded early. Returns how many revisions were folded.
func pruneOutputContextRevisions(schedule *execution.FrozenQueryGroupSchedule, retention execution.SlotRetention, now time.Time) int {
	if schedule == nil || retention.Validate() != nil || len(schedule.Segment.OutputContextRevisions) == 0 {
		return 0
	}
	var maxOffset int64
	for _, plan := range schedule.Plans {
		if offset := plan.Spec.CompletionOffsetSeconds(); offset > maxOffset {
			maxOffset = offset
		}
	}
	keep := int64(retention.MaxReplayAge/time.Second) + int64(retention.TerminalDelay/time.Second) + maxOffset
	folded := 0
	revisions := schedule.Segment.OutputContextRevisions
	for len(revisions) > 0 && int64(revisions[0].Since)+keep < now.Unix() {
		schedule.Segment.OutputContextRefs = revisions[0].Refs
		revisions = revisions[1:]
		folded++
	}
	if folded > 0 {
		schedule.Segment.OutputContextRevisions = nil
		if len(revisions) > 0 {
			schedule.Segment.OutputContextRevisions = revisions
		}
	}
	return folded
}
