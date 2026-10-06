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
	"fmt"
	"slices"
	"sort"
	"sync"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The activation body carries every Plan's activation record, and so does the
// open Segment of the Query Group the Plan is in: the cutover writes both from
// one list in one script. The copy in the body is what makes a publication
// write, and every replica read after it, cost the whole population rather
// than what changed (N15, review C1/C2).
//
// A later build stops writing that copy: its body is a head (schema v3, no
// Plans), and a publication cut over in pieces leaves a CutoverProgress on it.
// This build writes the head. It also reads a body with its Plans, which the
// build before it wrote and a rollback to it writes again:
//
//   - every reader that wants the head reads it through LoadActivationHead
//     and never touches the records;
//   - the Control Leader, which does need the records, gets them back from
//     the open Segments when the body has none (LoadActivation);
//   - a body carrying progress is finished in one piece on the first tick
//     (see ScheduleActivationReconciler.Ensure), the previous content taken
//     from what each open Segment names rather than from the manifest, which
//     the Query Groups past the cursor are not running yet.
const activationHeadSchemaVersion = "alarmd-control-activation-v3"

// CutoverProgress is how far a publication cutover committed in pieces got.
type CutoverProgress struct {
	From            SnapshotPublicationRef `json:"from"`
	Cursor          CutoverCursor          `json:"cursor"`
	ChangesetDigest string                 `json:"changeset_digest"`
	ChangesetCount  int                    `json:"changeset_count"`
	Remaining       int                    `json:"remaining"`
}

// CutoverCursor is the last Query Group committed, in the order the pieces
// commit in: by class, then by identity.
type CutoverCursor struct {
	Class      string                       `json:"class"`
	QueryGroup execution.QueryGroupIdentity `json:"query_group"`
}

const (
	CutoverClassRetired = "retired"
	CutoverClassAdded   = "added"
	CutoverClassChanged = "changed"
)

func (progress *CutoverProgress) validate(current SnapshotPublicationRef) error {
	if progress == nil {
		return nil
	}
	switch progress.Cursor.Class {
	case CutoverClassRetired, CutoverClassAdded, CutoverClassChanged:
	default:
		return errors.New("alarmd controlplane: cutover progress names an unknown class")
	}
	// From is empty only for a first activation cut in pieces: the Query
	// Groups past the cursor ran nothing before it.
	if progress.From != (SnapshotPublicationRef{}) {
		if progress.From.validate() != nil || progress.From.PublicationEpoch >= current.PublicationEpoch {
			return errors.New("alarmd controlplane: cutover progress must come from an earlier publication")
		}
	}
	if progress.Cursor.QueryGroup == "" || progress.ChangesetDigest == "" || progress.ChangesetCount <= 0 ||
		progress.Remaining < 0 || progress.Remaining >= progress.ChangesetCount {
		return errors.New("alarmd controlplane: incomplete cutover progress")
	}
	return nil
}

// LoadActivationHead is the activation without its Plan records: the
// publication it runs, its revision, the Draining projection, the active set
// reference, the held-back accounting and any cutover progress. Every reader
// but the Control Leader's own activation and cutover wants no more than
// this, and a head is what a later build's body is.
func (repository *RedisCatalogRepository) LoadActivationHead(ctx context.Context) (ActivationState, error) {
	entry, err := repository.loadParsedActivation(ctx)
	if err != nil {
		return ActivationState{}, err
	}
	state := entry.state
	if state.Pending != nil {
		pending := *state.Pending
		state.Pending = &pending
	}
	if state.CutoverProgress != nil {
		progress := *state.CutoverProgress
		state.CutoverProgress = &progress
	}
	state.Plans = nil
	state.Draining = slices.Clone(state.Draining)
	return state, nil
}

// openSegmentReadBatch bounds one pipelined read of timelines when the
// leader reads the open Segments of the whole active set. A timeline is a
// few KiB (p99 about 15 KB measured on the verification deployment), so a
// batch is a few MB at most.
const openSegmentReadBatch = 256

// readOpenSegments reads the open Segment of each Query Group. A Query Group
// with no timeline, or none open (retired, closed), is left out: it runs
// nothing, which is exactly what the answer is about.
//
// A timeline that does not decode is not left out. Taken for absent, a Query
// Group of the current publication would pass for one to add, and the
// cutover script, which adds only where no timeline is, would refuse the
// write on every tick; and its records would vanish from the body written
// next. It is refused by name instead: a Worker cannot run it either, and
// deleting the key lets the next cutover open it again.
func (repository *RedisCatalogRepository) readOpenSegments(
	ctx context.Context, identities []execution.QueryGroupIdentity, version controlVersion,
) (map[execution.QueryGroupIdentity]persistedScheduleSegment, error) {
	// Read live, not from the timeline cache: the cache is keyed by the
	// activation header, and a timeline can be rewritten under an unchanged
	// header (the repair subcommand does). What this answers decides what
	// the next activation writes, so it is not answered from a copy that may
	// predate such a write. It is rare - a head this process did not write,
	// or a cutover in progress - and what it reads refreshes the cache.
	repository.adoptControlVersion(ctx, version)
	counters := &repository.controlReads.timeline
	result := make(map[execution.QueryGroupIdentity]persistedScheduleSegment, len(identities))
	accept := func(identity execution.QueryGroupIdentity, timeline persistedScheduleTimeline) {
		if timeline.RetiredAt != nil || len(timeline.Segments) == 0 {
			return
		}
		if open := timeline.Segments[len(timeline.Segments)-1]; open.Schedule.Segment.End == nil {
			result[identity] = open
		}
	}
	missing := identities
	for start := 0; start < len(missing); start += openSegmentReadBatch {
		batch := missing[start:min(start+openSegmentReadBatch, len(missing))]
		replies := make([]*redis.StringCmd, len(batch))
		if _, err := repository.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for index, identity := range batch {
				replies[index] = pipe.Get(ctx, repository.scheduleTimelineKey(identity))
			}
			return nil
		}); err != nil && !errors.Is(err, redis.Nil) {
			return nil, activationDependencyIO(err)
		}
		for index, identity := range batch {
			payload, err := replies[index].Bytes()
			if errors.Is(err, redis.Nil) {
				continue
			}
			if err != nil {
				return nil, activationDependencyIO(err)
			}
			timeline, err := decodeScheduleTimeline(identity, payload)
			if err != nil {
				return nil, &DeterministicScheduleError{Err: fmt.Errorf(
					"the timeline of Query Group %s does not decode (%w); a cutover in progress cannot tell what it runs "+
						"until the key is repaired or deleted, after which it is opened again", identity, err)}
			}
			counters.misses.Add(1)
			if version.known {
				repository.controlCache.storeTimeline(version.header, identity, timeline, len(payload))
			}
			accept(identity, timeline)
		}
	}
	return result, nil
}

// openSegmentRefs is the output context refs in force on a Segment: the
// last revision's when it has any, else the ones it opened with.
func openSegmentRefs(segment persistedScheduleSegment) []execution.OutputContextRef {
	refs := segment.Schedule.Segment.OutputContextRefs
	if count := len(segment.Schedule.Segment.OutputContextRevisions); count > 0 {
		refs = segment.Schedule.Segment.OutputContextRevisions[count-1].Refs
	}
	return refs
}

// activeOpenSegments reads the open Segments of the Query Groups the
// activation may be running. That is its active set; and while a cutover is
// in progress, every Query Group of the current publication as well. A later
// build writes the active set once, after the last piece of a class, so a
// Query Group added by an earlier piece of that class has its timeline and
// is not in the set yet: taken from the set alone it would pass for one to
// add, and the cutover script refuses to add over a timeline that is there.
// A draining Query Group has retired its timeline and has no open Segment,
// so it is not asked about.
func (repository *RedisCatalogRepository) activeOpenSegments(
	ctx context.Context, state ActivationState,
) (map[execution.QueryGroupIdentity]persistedScheduleSegment, error) {
	version, err := repository.readControlVersion(ctx)
	if err != nil {
		return nil, err
	}
	return repository.activeOpenSegmentsAt(ctx, state, version)
}

// activeOpenSegmentsAt is activeOpenSegments under a header the caller
// already read.
func (repository *RedisCatalogRepository) activeOpenSegmentsAt(
	ctx context.Context, state ActivationState, version controlVersion,
) (map[execution.QueryGroupIdentity]persistedScheduleSegment, error) {
	identities, err := repository.LoadActiveQueryGroupSet(ctx, state.ActiveQGSetRef)
	if err != nil {
		return nil, err
	}
	if state.CutoverProgress != nil {
		manifest, err := repository.LoadCatalogManifest(ctx, state.Current.SnapshotRevision)
		if err != nil {
			return nil, err
		}
		seen := make(map[execution.QueryGroupIdentity]struct{}, len(identities)+len(manifest.QueryGroups))
		for _, identity := range identities {
			seen[identity] = struct{}{}
		}
		for _, entry := range manifest.QueryGroups {
			if _, listed := seen[entry.QueryGroup]; !listed && entry.QueryGroup != "" {
				seen[entry.QueryGroup] = struct{}{}
				identities = append(identities, entry.QueryGroup)
			}
		}
	}
	return repository.readOpenSegments(ctx, identities, version)
}

// materializeActivationPlans gives a head body back its Plan records, from
// the open Segments that carry the same records. The state it returns is the
// one this build's Control Leader works with, so it is labelled with the
// schema this build writes: the next write persists it with its records.
//
// A Query Group whose open Segment cannot be read contributes no records.
// Held back and without an open Segment, it ran nothing before either; held
// back with one, its records are on that Segment.
func (repository *RedisCatalogRepository) materializeActivationPlans(
	ctx context.Context, state ActivationState, version controlVersion,
) (ActivationState, error) {
	segments, err := repository.activeOpenSegmentsAt(ctx, state, version)
	if err != nil {
		return ActivationState{}, err
	}
	plans := make([]PlanActivationRecord, 0, len(segments))
	seen := make(map[execution.PlanKey]struct{})
	for _, segment := range segments {
		for _, record := range segment.Plans {
			if _, duplicate := seen[record.Fact.Key()]; duplicate {
				return ActivationState{}, &PersistedActivationCorruptError{
					Err: fmt.Errorf("Plan %v is open in two Query Groups", record.Fact.Key())}
			}
			seen[record.Fact.Key()] = struct{}{}
			plans = append(plans, record)
		}
	}
	sort.Slice(plans, func(i, j int) bool { return lessPlanIdentity(plans[i].Fact.Plan, plans[j].Fact.Plan) })
	state.SchemaVersion = activationSchemaVersion
	state.Plans = plans
	if err := validateActivationState(state); err != nil {
		return ActivationState{}, &PersistedActivationCorruptError{Err: err}
	}
	return state, nil
}

// activatedContentFromOpenSegments is the content the activation runs when
// the manifest of its current publication does not say: while a cutover is
// in progress the Query Groups past the cursor still run the content they
// ran before it. Each open Segment names its own content, so the answer is
// read off them - the Query Groups that have one, with the digest and the
// output contexts it names.
func (repository *RedisCatalogRepository) activatedContentFromOpenSegments(
	ctx context.Context, activation ActivationState,
) (activatedContent, error) {
	segments, err := repository.activeOpenSegments(ctx, activation)
	if err != nil {
		return activatedContent{}, err
	}
	content := activatedContent{
		groups:   make(map[execution.QueryGroupIdentity]QueryGroup, len(segments)),
		digests:  make(map[execution.QueryGroupIdentity]execution.ObjectDigest, len(segments)),
		contexts: make(map[execution.PlanIdentity]execution.OutputContextDigest),
		complete: true, source: "open_segments",
	}
	for identity, segment := range segments {
		content.groups[identity] = QueryGroup{Identity: identity}
		content.digests[identity] = segment.Schedule.Segment.ObjectDigest
		for _, ref := range openSegmentRefs(segment) {
			content.contexts[ref.Plan] = ref.Digest
		}
	}
	return content, nil
}

// ApplyCutoverProgress is the content the Query Groups run while a cutover
// is in progress: what each open Segment names, for the Query Groups of the
// active set that have one. The view, the content scopes and the split
// census read this instead of the manifest then, since the manifest names
// content the Query Groups past the cursor are not running yet. Without
// progress it returns content as given.
func (repository *RedisCatalogRepository) ApplyCutoverProgress(
	ctx context.Context, state ActivationState, content map[execution.QueryGroupIdentity]ContentEntry,
) (map[execution.QueryGroupIdentity]ContentEntry, error) {
	if state.CutoverProgress == nil {
		return content, nil
	}
	segments, err := repository.activeOpenSegments(ctx, state)
	if err != nil {
		return nil, err
	}
	running := make(map[execution.QueryGroupIdentity]ContentEntry, len(segments))
	for identity, segment := range segments {
		plans := make([]execution.PlanKey, 0, len(segment.Plans))
		for _, record := range segment.Plans {
			plans = append(plans, record.Fact.Key())
		}
		running[identity] = ContentEntry{Digest: segment.Schedule.Segment.ObjectDigest, Plans: plans,
			Refs: append([]execution.OutputContextRef(nil), openSegmentRefs(segment)...)}
	}
	return running, nil
}

// encodeActivationHead is the body this build writes: the activation
// without its Plan records, which are on the open Segments.
func encodeActivationHead(state ActivationState) ([]byte, error) {
	head := state
	head.SchemaVersion = activationHeadSchemaVersion
	head.Plans = nil
	return json.Marshal(head)
}

// writtenActivation is the last activation this process wrote, records and
// all, keyed by the exact head it wrote. The Control Leader reads its own
// write back on the next round; with it the records come from here, and
// only a head another process wrote - a new leader, a rollback, a rebuild -
// is read back from the open Segments.
type writtenActivation struct {
	mu      sync.Mutex
	payload string
	state   *ActivationState
}

func (written *writtenActivation) remember(payload []byte, state ActivationState) {
	full := (&parsedActivation{state: state}).cloneState()
	full.SchemaVersion = activationSchemaVersion
	written.mu.Lock()
	written.payload, written.state = string(payload), &full
	written.mu.Unlock()
}

func (written *writtenActivation) lookup(payload string) (ActivationState, bool) {
	written.mu.Lock()
	defer written.mu.Unlock()
	if written.state == nil || payload == "" || written.payload != payload {
		return ActivationState{}, false
	}
	return (&parsedActivation{state: *written.state}).cloneState(), true
}

// ErrCutoverInProgress refuses an operation that would act on the current
// publication's manifest while Query Groups are still running the one before.
var ErrCutoverInProgress = errors.New("alarmd controlplane: CUTOVER_IN_PROGRESS: a publication cutover has not finished")
