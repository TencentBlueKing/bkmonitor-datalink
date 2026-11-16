// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// prepassPorts are the windowed ports with the Plan new to its activation and
// forced to warm up, and a State store that holds a record from an earlier
// Slot for the series in persisted and nothing for the rest.
type prepassPorts struct {
	*windowedPorts
	older     execution.ApplyVersion
	persisted map[execution.SeriesIdentityDigest]string
	// levels are a found record's Level views, one FULL view for each Level
	// of the Plan, as the store hands every found record.
	levels []execution.RuntimeLevelStateView
	// mu guards loads: the next batch's State is read beside the batch before.
	mu    sync.Mutex
	loads []execution.StatePreflightRequest
	// during runs inside every load, before it answers.
	during func()
}

func (ports *prepassPorts) LoadActivations(ctx context.Context, request execution.PlanActivationRequest) (execution.PlanActivationResult, error) {
	result, err := ports.windowedPorts.LoadActivations(ctx, request)
	for index := range result.Facts {
		result.Facts[index].Selected.ForceWarming, result.Facts[index].Selected.Arrived = true, true
	}
	return result, err
}

func (ports *prepassPorts) LoadRuntime(_ context.Context, request execution.StatePreflightRequest) (execution.StatePreflightResult, error) {
	ports.mu.Lock()
	ports.loads = append(ports.loads, request)
	ports.mu.Unlock()
	if ports.during != nil {
		ports.during()
	}
	result := execution.StatePreflightResult{Items: make([]execution.RuntimeStateView, len(request.Items))}
	for index, item := range request.Items {
		view := execution.RuntimeStateView{Identity: item.Identity, Status: execution.StateMissingWarming}
		if digest, found := ports.persisted[item.Identity.SeriesIdentityDigest]; found {
			view = execution.RuntimeStateView{Identity: item.Identity, Status: execution.StateFoundReady, BlobRevision: 1,
				PersistedApplyVersion: ports.older, PersistedMutationDigest: execution.MutationDigest(digest),
				Levels: ports.levels}
		}
		result.Items[index] = view
	}
	return result, nil
}

// newPrepassFixture is the windowed fixture with a Slot cap of two State
// mutations, so every two series make a window and the preflight reads two
// keys a batch, and series series streamed and prepared.
func newPrepassFixture(t *testing.T, series int) (*windowedFixture, *prepassPorts) {
	t.Helper()
	fixture := newWindowedFixture(t)
	older := fixture.version
	older.EvaluationTime -= 60
	refs, err := execution.DeriveRuntimeLevelContractRefs(fixture.due.CompiledPlan)
	if err != nil {
		t.Fatal(err)
	}
	ports := &prepassPorts{windowedPorts: fixture.ports, older: older, persisted: map[execution.SeriesIdentityDigest]string{}}
	for _, ref := range refs {
		ports.levels = append(ports.levels, execution.RuntimeLevelStateView{LevelID: ref.LevelID,
			LevelStateCompatibility: ref.LevelStateCompatibility, WarmupRequirementRef: ref.WarmupRequirementRef,
			HistoryCompleteness: execution.HistoryFull, LastProcessedEventTime: int64(older.EvaluationTime)})
	}
	coordinator := fixture.stream.coordinator
	coordinator.ports.Activation, coordinator.ports.State = ports, ports
	coordinator.budget.MaxStateMutations = 2
	for index := range series {
		fixture.streamInput(t, prepassSeries(index), 100)
	}
	return fixture, ports
}

func prepassSeries(index int) string { return fmt.Sprintf("series-%02d", index) }

// mergeNew merges one evaluated series whose State the Slot's preflight found
// missing, through the evaluation's own merge, window check first.
func (fixture *windowedFixture) mergeNew(t *testing.T, position int) {
	t.Helper()
	series := prepassSeries(position)
	key := fixture.key(series)
	result := execution.EvaluationResult{Contract: fixture.stream.header.Contract, Result: observability.ResultSuccess,
		ReasonCode: observability.ReasonNone, Plans: []execution.PlanEvaluationResult{{Plan: fixture.due.Identity,
			Disposition: execution.PlanDecided, StateResults: []execution.StateEvaluation{{
				Mutation: execution.StateMutation{Identity: key, ApplyVersion: fixture.version, MutationDigest: execution.MutationDigest("digest-" + series)},
				Events:   []contract.TriggerEventV1{{EventID: "event-" + series}},
			}}}}}
	view := execution.RuntimeStateView{Identity: key, Status: execution.StateMissingWarming, VersionComparison: execution.ApplyVersionPersistedOlder}
	item := execution.StatePreflightItem{Identity: key, ApplyVersion: fixture.version}
	if err := fixture.stream.mergeEvaluatedSeries(context.Background(), position, result, 100,
		[]execution.RuntimeStateView{view}, []execution.StatePreflightItem{item}); err != nil {
		t.Fatalf("merge %s: %v", series, err)
	}
}

func (fixture *windowedFixture) candidate() execution.PlanActivationFact {
	due := fixture.due
	return execution.PlanActivationFact{Plan: due.Identity, Selection: execution.ActivationCurrent,
		Selected: execution.ActivatedPlan{Identity: due.Identity, StateGeneration: due.StateGeneration, StateApplyEpoch: 1,
			ScheduleRevision: due.ScheduleRevision, RequiredFullSlots: 1, Arrived: true, ForceWarming: true}}
}

// A Plan new to its activation, of more series than one window holds, is read
// whole before the head (2.3), found to hold nothing from before the Slot,
// judged first-seen, and written window by window, every series once. The
// marker that records it ran waits for the tail (TestAFirstSeenPlanWritesItsMarkerInTheTail):
// nothing of it is written in the head. Its reads witness nothing.
func TestABrandNewPlanOfManyWindowsIsJudgedFirstSeenAndWrittenInWindows(t *testing.T) {
	const series = 6
	fixture, ports := newPrepassFixture(t, series)
	for position := range series {
		fixture.mergeNew(t, position)
	}
	stream := fixture.stream
	if !stream.windows.started {
		t.Fatal("the Slot was not cut into windows")
	}
	if err := stream.coordinator.applyWindow(context.Background(), stream, true); err != nil {
		t.Fatal(err)
	}

	if len(ports.loads) != 3 {
		t.Fatalf("pre-pass loads = %d, want every series read in batches of two (3)", len(ports.loads))
	}
	for _, load := range ports.loads {
		if !load.Unwitnessed {
			t.Fatal("a pre-pass read asked the store to witness what it read")
		}
	}
	apply := stream.windows.apply
	if _, first := apply.firstSeen[fixture.due.Key()]; !first || apply.changed(fixture.due) {
		t.Fatalf("first-seen=%v changed=%v, want the Plan judged first-seen and written", first, apply.changed(fixture.due))
	}
	if guards := fixture.ports.guards; len(guards) != 0 {
		t.Fatalf("statements before the tail = %+v, want none: the first-seen marker is the tail's", guards)
	}
	if stream.windows.count != 3 || len(fixture.ports.stateApplied) != series {
		t.Fatalf("windows=%d State written=%d, want 3 windows writing all %d series", stream.windows.count,
			len(fixture.ports.stateApplied), series)
	}
	if _, unseen := stream.unseenPlans()[fixture.due.Key()]; !unseen {
		t.Fatal("the tail would read the Plan as seen while the head judged it first-seen")
	}
}

// A Plan with a record from an earlier Slot is judged seen at the first batch
// that shows it, and its later series are not read: only a Plan that is really
// new reads every series. Seen, the forced Plan warms up and writes nothing.
func TestAPlanWithAnEarlyPersistedSeriesIsJudgedSeenAfterReadingOnlyUpToIt(t *testing.T) {
	const series = 8
	fixture, ports := newPrepassFixture(t, series)
	// In the second batch of two, and not among the series merged before the
	// head runs (the first two): the views the Slot holds show nothing yet.
	ports.persisted[execution.SeriesIdentityDigest(prepassSeries(3))] = "stored"
	for position := range 3 {
		fixture.mergeNew(t, position)
	}
	stream := fixture.stream
	if !stream.windows.started {
		t.Fatal("the head did not run")
	}
	if len(ports.loads) != 2 {
		t.Fatalf("pre-pass loads = %d, want two batches read, up to the persisted series, and none after", len(ports.loads))
	}
	if last := ports.loads[1].Items; last[len(last)-1].Identity.SeriesIdentityDigest != execution.SeriesIdentityDigest(prepassSeries(3)) {
		t.Fatalf("the last pre-pass batch ended at %s, want the persisted series", last[len(last)-1].Identity.SeriesIdentityDigest)
	}
	apply := stream.windows.apply
	if _, first := apply.firstSeen[fixture.due.Key()]; first || !apply.changed(fixture.due) {
		t.Fatalf("first-seen=%v changed=%v, want the Plan seen and warmed up", first, apply.changed(fixture.due))
	}
	if len(fixture.ports.events) != 0 || len(fixture.ports.stateApplied) != 0 || len(fixture.ports.guards) != 0 {
		t.Fatalf("a warmed-up Plan wrote events %v, State %v, gaps %+v", fixture.ports.events, fixture.ports.stateApplied, fixture.ports.guards)
	}
	if _, unseen := stream.unseenPlans()[fixture.due.Key()]; unseen {
		t.Fatal("the pre-pass judgement did not reach unseenPlans")
	}
}

// The pre-pass holds one batch of views at a time, charged under the share
// and given back before the next read; a batch the share cannot hold is
// refused by name, with nothing kept.
func TestTheFirstSeenPrepassHoldsOneBatchUnderTheShare(t *testing.T) {
	t.Run("within the share", func(t *testing.T) {
		fixture, ports := newPrepassFixture(t, 6)
		stream := fixture.stream
		before := stream.retainedTotal()
		share := stream.coordinator.qgShareBytes()
		ports.during = func() {
			if held := stream.retainedTotal(); held != before || held > share {
				t.Fatalf("a pre-pass read began with %d held, %d before the pre-pass, share %d: a batch was kept", held, before, share)
			}
		}
		if err := stream.firstSeenPrepass(context.Background(), []execution.PlanActivationFact{fixture.candidate()}); err != nil {
			t.Fatal(err)
		}
		if len(ports.loads) != 3 || stream.retainedTotal() != before {
			t.Fatalf("loads=%d held=%d, want 3 reads and %d held after them", len(ports.loads), stream.retainedTotal(), before)
		}
	})
	t.Run("over the share", func(t *testing.T) {
		fixture, ports := newPrepassFixture(t, 6)
		stream := fixture.stream
		before := stream.retainedTotal()
		ports.persisted[execution.SeriesIdentityDigest(prepassSeries(4))] = strings.Repeat("d", int(stream.coordinator.qgShareBytes()))
		err := stream.firstSeenPrepass(context.Background(), []execution.PlanActivationFact{fixture.candidate()})
		var exceeded *provisionalBudgetExceededError
		if !errors.As(err, &exceeded) || !exceeded.share {
			t.Fatalf("err = %v, want the batch refused by the share", err)
		}
		if stream.retainedTotal() != before {
			t.Fatalf("held %d after the refused batch, %d before", stream.retainedTotal(), before)
		}
		if _, seen := stream.windows.seen[fixture.due.Identity]; seen {
			t.Fatal("a refused batch still judged the Plan")
		}
	})
}
