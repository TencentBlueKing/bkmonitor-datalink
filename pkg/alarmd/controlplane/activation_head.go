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
// Plans). This build writes the head. It also reads a body with its Plans,
// which the build before it wrote and a rollback to it writes again:
//
//   - every reader that wants the head reads it through LoadActivationHead
//     and never touches the records;
//   - the Control Leader, which does need the records, gets them back from
//     the open Segments when the body has none (LoadActivation).
const activationHeadSchemaVersion = "alarmd-control-activation-v3"

// LoadActivationHead is the activation without its Plan records: the
// publication it runs, its revision, the Draining projection, the active set
// reference and the held-back accounting. Every reader but the Control
// Leader's own activation and cutover wants no more than this, and a head is
// what a later build's body is.
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
	state.Plans = nil
	state.Draining = slices.Clone(state.Draining)
	return state, nil
}

// openSegmentReadBatch bounds one pipelined read of timelines when the
// leader reads the open Segments of the whole active set. A timeline is a
// few KiB (p99 about 15 KB measured on the verification deployment), so a
// batch is a few MB at most.
const openSegmentReadBatch = 256

// readOpenSegments reads the open Segment of each Query Group and hands it
// to visit as its timeline is decoded, one at a time. Each caller keeps what
// it uses of a Segment - its Plan records, or only the content it names -
// and the rest goes with the decoded timeline, which only the timeline cache
// keeps, within its bound: a read of the whole active set holds at once what
// its caller keeps, not every Segment. A Query Group with no timeline, or
// none open (retired, closed), is not visited: it runs nothing, which is
// exactly what the answer is about.
//
// A timeline that does not decode is never taken for absent. Taken for
// absent, a Query Group of the current publication would pass for one to
// add, and the cutover script, which adds only where no timeline is, would
// refuse the write on every tick. Without undecodable the read is refused by
// name; with it, the Query Group and its bytes are handed there and the read
// goes on with the rest - the caller names it as unread, never as absent.
// Only a decode failure goes there: a read that fails, or a reply that is an
// error, fails the read whichever is given, because it says nothing about
// the timeline.
func (repository *RedisCatalogRepository) readOpenSegments(
	ctx context.Context, identities []execution.QueryGroupIdentity, version controlVersion,
	visit func(execution.QueryGroupIdentity, persistedScheduleSegment) error,
	undecodable func(execution.QueryGroupIdentity, []byte),
) error {
	// Read live, not from the timeline cache: the cache is keyed by the
	// activation header, and a timeline can be rewritten under an unchanged
	// header (a write outside the cutover does). What this answers decides what
	// the next activation writes, so it is not answered from a copy that may
	// predate such a write. It is rare - a head this process did not write -
	// and what it reads refreshes the cache.
	repository.adoptControlVersion(ctx, version)
	counters := &repository.controlReads.timeline
	accept := func(identity execution.QueryGroupIdentity, timeline persistedScheduleTimeline) error {
		if timeline.RetiredAt != nil || len(timeline.Segments) == 0 {
			return nil
		}
		if open := timeline.Segments[len(timeline.Segments)-1]; open.Schedule.Segment.End == nil {
			return visit(identity, open)
		}
		return nil
	}
	missing := identities
	var reading *cacheReading
	if version.known {
		reading = repository.controlCache.announceTimelines(len(missing))
		defer reading.settle()
	}
	for start := 0; start < len(missing); start += openSegmentReadBatch {
		batch := missing[start:min(start+openSegmentReadBatch, len(missing))]
		replies := make([]*redis.StringCmd, len(batch))
		if _, err := repository.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for index, identity := range batch {
				replies[index] = pipe.Get(ctx, repository.scheduleTimelineKey(identity))
			}
			return nil
		}); err != nil && !errors.Is(err, redis.Nil) {
			return activationDependencyIO(err)
		}
		for index, identity := range batch {
			payload, err := replies[index].Bytes()
			if errors.Is(err, redis.Nil) {
				continue
			}
			if err != nil {
				return activationDependencyIO(err)
			}
			timeline, err := decodeScheduleTimeline(identity, payload)
			if err != nil && undecodable != nil {
				undecodable(identity, payload)
				continue
			}
			if err != nil {
				return &DeterministicScheduleError{Err: fmt.Errorf(
					"the timeline of Query Group %s does not decode (%w); the activation cannot tell what it runs "+
						"until the key is repaired or deleted, after which it is opened again", identity, err)}
			}
			counters.misses.Add(1)
			if version.known {
				repository.controlCache.storeTimeline(reading, version.header, identity, timeline, len(payload))
			}
			if err := accept(identity, timeline); err != nil {
				return err
			}
		}
	}
	return nil
}

// activeOpenSegmentsAt reads the open Segments of the activation's active
// set, under a header the caller already read. A draining Query Group has
// retired its timeline and has no open Segment, so it is not asked about.
func (repository *RedisCatalogRepository) activeOpenSegmentsAt(
	ctx context.Context, state ActivationState, version controlVersion,
	visit func(execution.QueryGroupIdentity, persistedScheduleSegment) error,
	undecodable func(execution.QueryGroupIdentity, []byte),
) error {
	identities, err := repository.LoadActiveQueryGroupSet(ctx, state.ActiveQGSetRef)
	if err != nil {
		return err
	}
	return repository.readOpenSegments(ctx, identities, version, visit, undecodable)
}

// materializeActivationPlans gives a head body back its Plan records, from
// the open Segments that carry the same records. The state it returns is the
// one this build's Control Leader works with, so it is labelled with the
// schema this build writes: the next write persists it with its records.
//
// A Query Group whose open Segment cannot be read contributes no records.
// Held back and without an open Segment, it ran nothing before either; held
// back with one, its records are on that Segment.
//
// A Query Group whose timeline does not decode contributes none either, and
// is named in the state's SkippedTimelines and counted, so that one bad key
// leaves the activation loadable for every other Query Group and the Leader
// can see which one to rebuild (Ensure). A read that fails is not a timeline
// that does not decode, and fails this as before.
func (repository *RedisCatalogRepository) materializeActivationPlans(
	ctx context.Context, state ActivationState, version controlVersion,
) (ActivationState, error) {
	var plans []PlanActivationRecord
	var skipped []SkippedTimeline
	seen := make(map[execution.PlanKey]struct{})
	undecodable := func(identity execution.QueryGroupIdentity, raw []byte) {
		skipped = append(skipped, SkippedTimeline{QueryGroup: identity, Reason: skippedTimelineReason(raw)})
	}
	if err := repository.activeOpenSegmentsAt(ctx, state, version, func(_ execution.QueryGroupIdentity, segment persistedScheduleSegment) error {
		for _, record := range segment.Plans {
			if _, duplicate := seen[record.Fact.Key()]; duplicate {
				return &PersistedActivationCorruptError{
					Err: fmt.Errorf("Plan %v is open in two Query Groups", record.Fact.Key())}
			}
			seen[record.Fact.Key()] = struct{}{}
			plans = append(plans, record)
		}
		return nil
	}, undecodable); err != nil {
		return ActivationState{}, err
	}
	sort.Slice(plans, func(i, j int) bool { return lessPlanIdentity(plans[i].Fact.Plan, plans[j].Fact.Plan) })
	state.SchemaVersion = activationSchemaVersion
	state.Plans = plans
	state.SkippedTimelines = skipped
	repository.skippedTimelines.note(skipped)
	if err := validateActivationState(state); err != nil {
		return ActivationState{}, &PersistedActivationCorruptError{Err: err}
	}
	return state, nil
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
