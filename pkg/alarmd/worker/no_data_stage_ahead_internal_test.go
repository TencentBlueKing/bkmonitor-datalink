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
	"sync"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
)

// stagingRosterStore is a roster store that stages as the store does: each
// segment folded from the values it was handed, the marker kept, and what
// was discarded.
type stagingRosterStore struct {
	*rosterStore
	segments  []int
	discarded int
}

func (store *stagingRosterStore) StageNoData(_ context.Context, request execution.NoDataStageRequest) (execution.NoDataStageResult, error) {
	if err := execution.ValidateNoDataStageSegment(request); err != nil {
		return execution.NoDataStageResult{Status: execution.NoDataRejected}, nil
	}
	memory := request.Staged.Fold()
	for _, value := range request.Set {
		if err := memory.Add(execution.NoDataGroupMemoryOfValue(value, request.PresentAsOf)); err != nil {
			return execution.NoDataStageResult{}, err
		}
	}
	store.segments = append(store.segments, len(request.Set))
	return execution.NoDataStageResult{Status: execution.NoDataApplied,
		Staged: execution.NewNoDataStaged([]byte("marker"), request.PresentAsOf, memory)}, nil
}

func (store *stagingRosterStore) DiscardNoDataStage(context.Context, execution.NoDataStageDiscard) error {
	store.discarded++
	return nil
}

// A first write whose present groups landed in windows is staged as they
// land, a segment's worth a script: the round's statement carries only the
// groups the windows left short of a segment, its digests the whole read's,
// and the staged groups are not in the delta the worker holds. A store that
// cannot stage writes it whole, as before.
func TestAFirstWriteIsStagedAsItsWindowsLand(t *testing.T) {
	due := noDataWiredPlan(t)
	evaluation := int64(1_788_000_000)
	_, hosts := rosterGroups(t, 2*execution.NoDataFirstWriteSegmentGroups+500, evaluation-60)
	store := &stagingRosterStore{rosterStore: &rosterStore{}}
	stream := rosterStream(t, due, store.rosterStore, 7)
	stream.coordinator.ports.NoData = store
	ctx := context.Background()
	// As the windows land them (dropWindowInputs).
	present := stream.windows.presentSet(due)
	for _, host := range hosts {
		if key, fresh := present.Add(host); fresh {
			stream.stageAhead(ctx, due, key)
		}
	}
	if len(store.segments) != 2 || store.segments[0] != execution.NoDataFirstWriteSegmentGroups {
		t.Fatalf("staged segments %v, want two of a segment each", store.segments)
	}
	round, err := stream.decideNoDataRound(ctx, due, present, execution.CompletenessFull, func(completedSeries) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	snapshot := execution.NoDataMemorySnapshot{Identity: due.NoDataIdentity(), Status: execution.NoDataMemoryMissing,
		Representation: execution.NoDataRepresentationNone}
	want := wholeReadOf(t, stream, due, snapshot, hosts)
	got := round.mutation
	if got == nil || want.Mutation == nil || got.Staged.Groups() != uint64(2*execution.NoDataFirstWriteSegmentGroups) ||
		len(got.Set) != 500 || got.MemoryDigest != want.Mutation.MemoryDigest ||
		got.MutationDigest != want.Mutation.MutationDigest || got.GroupCount != want.Mutation.GroupCount {
		t.Fatalf("statement %+v, want the whole read's %+v with %d groups staged ahead", got, want.Mutation,
			2*execution.NoDataFirstWriteSegmentGroups)
	}
	if store.discarded != 0 || stream.stageAheadEnds != [4]uint64{stageAheadCarried: 1} ||
		stream.groupsStagedAhead != uint64(2*execution.NoDataFirstWriteSegmentGroups) {
		t.Fatalf("stage ended %v with %d groups after %d discards, want carried once with the two segments",
			stream.stageAheadEnds, stream.groupsStagedAhead, store.discarded)
	}

	// The same Slot over a store that cannot stage.
	plain := &rosterStore{}
	whole := rosterStream(t, due, plain, 7)
	wholePresent := whole.windows.presentSet(due)
	for _, host := range hosts {
		if key, fresh := wholePresent.Add(host); fresh {
			whole.stageAhead(ctx, due, key)
		}
	}
	unstaged, err := whole.decideNoDataRound(ctx, due, wholePresent, execution.CompletenessFull, func(completedSeries) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if unstaged.mutation == nil || unstaged.mutation.Staged != nil || len(unstaged.mutation.Set) != len(hosts) {
		t.Fatalf("statement over a store that cannot stage %+v, want every group with it", unstaged.mutation)
	}
}

// A host of another business present in a first write is not staged - the
// round does not keep it, by the rule it keeps groups by
// (nodata.KeptAsSeen) - so the rest are staged and carried as before, with
// no fallback to a whole write. Without such hosts every present group is.
func TestAHostOfAnotherBusinessIsNotStagedAndTheRestAreCarried(t *testing.T) {
	due := noDataWiredPlan(t)
	evaluation := int64(1_788_000_000)
	_, hosts := rosterGroups(t, execution.NoDataFirstWriteSegmentGroups+1, evaluation-60)
	foreign, _ := nodata.Project(hosts[0], []string{"bk_target_ip", "bk_target_cloud_id"})
	for _, test := range []struct {
		name          string
		outOfBusiness map[string]struct{}
		set           int
	}{
		{"one host of another business", map[string]struct{}{foreign.Key(): {}}, 0},
		{"every host this business's", nil, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &stagingRosterStore{rosterStore: &rosterStore{}}
			stream := rosterStream(t, due, store.rosterStore, 7)
			stream.coordinator.ports.NoData = store
			resolution := stream.noDataHosts[due.NoDataIdentity()]
			resolution.OutOfBusiness = test.outOfBusiness
			if stream.noDataHosts == nil {
				stream.noDataHosts = map[execution.PlanNoDataIdentity]nodata.HostResolution{}
			}
			stream.noDataHosts[due.NoDataIdentity()] = resolution
			ctx := context.Background()
			present := stream.windows.presentSet(due)
			for _, host := range hosts {
				if key, fresh := present.Add(host); fresh {
					stream.stageAhead(ctx, due, key)
				}
			}
			round, err := stream.decideNoDataRound(ctx, due, present, execution.CompletenessFull, func(completedSeries) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			if stream.stageAheadEnds != [4]uint64{stageAheadCarried: 1} || stream.stageAheadDiffers != [3]uint64{} ||
				round.mutation == nil || round.mutation.Staged.Groups() != uint64(execution.NoDataFirstWriteSegmentGroups) ||
				len(round.mutation.Set) != test.set {
				t.Fatalf("stage ended %v (differs %v), statement %+v; want it carried with %d groups left to the statement",
					stream.stageAheadEnds, stream.stageAheadDiffers, round.mutation, test.set)
			}
		})
	}
}

// A round that ends with no statement of the Plan - not judged - takes back
// what its windows staged; one whose statement writes the whole record over
// the stage does too, counted by why.
func TestAStageTheRoundWritesNoStatementOfIsDiscarded(t *testing.T) {
	due := noDataWiredPlan(t)
	for _, test := range []struct {
		name     string
		finished nodata.SegmentedResult
		end      int
		differs  [3]uint64
	}{
		{"not judged", nodata.SegmentedResult{Judged: false}, stageAheadDiscarded, [3]uint64{}},
		{"written whole over it", nodata.SegmentedResult{Judged: true, StagedDiffers: nodata.StagedDiffersCount,
			Mutation: &execution.PlanNoDataMutation{}}, stageAheadDiffers, [3]uint64{1: 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &stagingRosterStore{rosterStore: &rosterStore{}}
			stream := rosterStream(t, due, store.rosterStore, 7)
			stream.coordinator.ports.NoData = store
			stream.stageAheadFor(due).staged = execution.NewNoDataStaged([]byte("marker"), 1, execution.NoDataMemoryDigestAccumulator{})
			stream.endStagedAhead(context.Background(), due, test.finished)
			var want [4]uint64
			want[test.end] = 1
			if store.discarded != 1 || stream.stageAheadEnds != want || stream.stageAheadDiffers != test.differs ||
				stream.stagedAhead(due.Identity) != nil {
				t.Fatalf("after %d discards the stage ended %v (differs %v), want it discarded once as %v",
					store.discarded, stream.stageAheadEnds, stream.stageAheadDiffers, want)
			}
		})
	}
}

// readCountingRosterStore is a roster store that says how many reads by
// name it has been asked for.
type readCountingRosterStore struct {
	*rosterStore
	mu    sync.Mutex
	reads int
	asked chan struct{}
}

func (store *readCountingRosterStore) ReadNoDataGroups(ctx context.Context, request execution.NoDataGroupsRequest) (execution.NoDataGroupsResult, error) {
	store.mu.Lock()
	store.reads++
	store.mu.Unlock()
	select {
	case store.asked <- struct{}{}:
	default:
	}
	return store.rosterStore.ReadNoDataGroups(ctx, request)
}

// The absences of a memory are read back a window ahead: the next window's
// read is asked for while this window's series are said and evaluated, not
// after (way 4).
func TestTheNextAbsenceWindowIsReadWhileThisOneIsSaid(t *testing.T) {
	due := noDataWiredPlan(t)
	evaluation := int64(1_788_000_000)
	groups, _ := rosterGroups(t, 12, evaluation-60)
	header := rosterHeader(evaluation - 60)
	inner := &rosterStore{headers: map[execution.StateGeneration]execution.NoDataMemorySnapshot{due.StateGeneration: header},
		groups: map[execution.StateGeneration][]execution.NoDataGroupMemory{due.StateGeneration: groups}}
	store := &readCountingRosterStore{rosterStore: inner, asked: make(chan struct{}, 16)}
	stream := rosterStream(t, due, inner, 4)
	stream.coordinator.ports.NoData = store
	present := stream.windows.presentSet(due)
	first := true
	_, err := stream.decideNoDataRound(context.Background(), due, present, execution.CompletenessFull, func(completedSeries) error {
		if !first {
			return nil
		}
		first = false
		// The first window's first series: the second window is asked for
		// already, or is while this one waits.
		deadline := testwait.Expired(t)
		for {
			store.mu.Lock()
			reads := store.reads
			store.mu.Unlock()
			if reads >= 2 {
				return nil
			}
			select {
			case <-store.asked:
			case <-deadline:
				return errors.New("the next absence window was not read while this one was said")
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if first {
		t.Fatal("setup: the round said no absence")
	}
}
