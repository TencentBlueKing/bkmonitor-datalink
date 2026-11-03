// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The words a Worker reports for the no-data carry are the ones the metric
// pre-creates and the normalizer keeps.
func TestTheNoDataCarryLoadWordsAreTheObservedVocabulary(t *testing.T) {
	if got := observability.StateCarryResults[noDataCarryLoadScope]; !reflect.DeepEqual(got, NoDataCarryLoadOutcomes) {
		t.Fatalf("observed %s results = %v, want %v", noDataCarryLoadScope, got, NoDataCarryLoadOutcomes)
	}
}

// A carried entry the round neither ended nor rewrote is written under the
// new generation; one it ended or rewrote is left to that decision. The old
// index is then cleared of every carried entry, under the old key.
func TestCarriedEndEntriesMoveToTheNewGeneration(t *testing.T) {
	kept := execution.NoDataEndEntry{Series: "kept", LevelID: 1, Fingerprint: "f1", LastRealPoint: 100}
	ended := execution.NoDataEndEntry{Series: "ended", LevelID: 1, Fingerprint: "f2", LastRealPoint: 100}
	rewritten := execution.NoDataEndEntry{Series: "rewritten", LevelID: 1, Fingerprint: "f3", LastRealPoint: 100}
	fresh := rewritten
	fresh.LastRealPoint = 160
	carry := noDataEndCarry{from: execution.NoDataEndKey{StateGeneration: "old"},
		entries: []execution.NoDataEndEntry{kept, ended, rewritten}}
	write := execution.NoDataEndWrite{Key: execution.NoDataEndKey{StateGeneration: "new"},
		Puts: []execution.NoDataEndEntry{fresh}, Deletes: []execution.NoDataEndEntryKey{ended.Key()}}
	got := withCarriedEnds(write, carry)
	if want := []execution.NoDataEndEntry{fresh, kept}; !reflect.DeepEqual(got.Puts, want) {
		t.Fatalf("puts = %+v, want the round's own and the carried entry it left alone", got.Puts)
	}
	cleared := clearCarriedEnds(carry, got)
	if cleared.Key.StateGeneration != "old" || len(cleared.Deletes) != 3 || len(cleared.Puts) != 0 {
		t.Fatalf("clear = %+v, want every carried entry removed from the old index", cleared)
	}
}

// fakeNoDataEndIndex holds indexes by key and records what was read and
// written; failWrites refuses every write to the generation named.
type fakeNoDataEndIndex struct {
	held       map[execution.NoDataEndKey][]execution.NoDataEndEntry
	read       []execution.NoDataEndKey
	writes     []execution.NoDataEndWrite
	failWrites execution.StateGeneration
}

func (index *fakeNoDataEndIndex) LoadNoDataEnds(_ context.Context, keys []execution.NoDataEndKey) ([]execution.NoDataEndLoaded, error) {
	index.read = append(index.read, keys...)
	loaded := make([]execution.NoDataEndLoaded, 0, len(keys))
	for _, key := range keys {
		loaded = append(loaded, execution.NoDataEndLoaded{Key: key, Entries: index.held[key]})
	}
	return loaded, nil
}

func (index *fakeNoDataEndIndex) ApplyNoDataEnds(_ context.Context, write execution.NoDataEndWrite) error {
	if write.Key.StateGeneration == index.failWrites {
		return errors.New("refused")
	}
	index.writes = append(index.writes, write)
	return nil
}

// carryFixture is a Plan whose Level 5 ends on no data, moved from
// generation "old" to "new" with its end index carried.
func carryFixture(t *testing.T) (execution.DuePlan, execution.NoDataEndKey, execution.NoDataEndKey) {
	plan := internalCompiledPlanRecovering(t, "311", 1, 60, json.RawMessage(`{"enabled":true,"consecutive_windows":2,"missing_data":"evaluate_as_zero"}`))
	due := execution.DuePlan{Identity: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "311"}, CompiledPlan: plan,
		StateGeneration: "new", NoDataCarry: &execution.NoDataCarry{From: "old", Ends: true}}
	return due, execution.NoDataEndKey{Plan: due.Identity, StateGeneration: "new"}, execution.NoDataEndKey{Plan: due.Identity, StateGeneration: "old"}
}

// The old index is read only while the new one is empty, and only the
// entries of a Level that still ends on no data are taken.
func TestCarriedEndsAreReadOnlyIntoAnEmptyIndexAndOnlyForLevelsThatEndOnNoData(t *testing.T) {
	due, newKey, oldKey := carryFixture(t)
	ending := noDataEndEntryFor("a", "fa", 100)
	other := noDataEndEntryFor("b", "fb", 100)
	other.LevelID = 9
	index := &fakeNoDataEndIndex{held: map[execution.NoDataEndKey][]execution.NoDataEndEntry{oldKey: {ending, other}}}
	coordinator := &SlotExecutionCoordinator{}
	stream := &streamedExecution{coordinator: coordinator, header: execution.InternalExecutionHeader{DuePlans: []execution.DuePlan{due}},
		noDataEnds: map[execution.PlanIdentity]execution.NoDataEndLoaded{due.Identity: {Key: newKey}}}
	stream.carryNoDataEnds(context.Background(), index)
	if got := stream.noDataEnds[due.Identity].Entries; !reflect.DeepEqual(got, []execution.NoDataEndEntry{ending}) {
		t.Fatalf("the round decides on %+v, want only the entry of the Level that ends on no data", got)
	}
	if carry := stream.noDataEndCarries[due.Identity]; carry.from != oldKey || len(carry.entries) != 1 {
		t.Fatalf("carry = %+v, want the one entry from the old index", carry)
	}

	own := noDataEndEntryFor("c", "fc", 160)
	index.read = nil
	stream = &streamedExecution{coordinator: coordinator, header: execution.InternalExecutionHeader{DuePlans: []execution.DuePlan{due}},
		noDataEnds: map[execution.PlanIdentity]execution.NoDataEndLoaded{due.Identity: {Key: newKey, Entries: []execution.NoDataEndEntry{own}}}}
	stream.carryNoDataEnds(context.Background(), index)
	if len(index.read) != 0 || len(stream.noDataEndCarries) != 0 {
		t.Fatalf("an index the new generation holds read %v and carried %+v, want neither", index.read, stream.noDataEndCarries)
	}
}

// The carried entries leave the old index only once the new one holds them:
// a write to the new index that fails leaves the old one as it was.
func TestCarriedEndsLeaveTheOldIndexOnlyAfterTheNewOneHoldsThem(t *testing.T) {
	due, newKey, oldKey := carryFixture(t)
	entry := noDataEndEntryFor("a", "fa", 100)
	plan := noDataEndPlan{loaded: execution.NoDataEndLoaded{Key: newKey, Entries: []execution.NoDataEndEntry{entry}},
		present: map[execution.SeriesIdentityDigest]struct{}{}, carry: &noDataEndCarry{from: oldKey, entries: []execution.NoDataEndEntry{entry}}}
	coordinator := &SlotExecutionCoordinator{}
	for name, test := range map[string]struct {
		fail    execution.StateGeneration
		cleared bool
	}{
		"the new index takes them":   {cleared: true},
		"the new index refuses them": {fail: "new"},
	} {
		index := &fakeNoDataEndIndex{failWrites: test.fail}
		if err := coordinator.applyPlanNoDataEnds(context.Background(), execution.SlotExecutionRequest{}, "execution", 1000, due, plan, nil, index); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cleared := false
		for _, write := range index.writes {
			if write.Key == oldKey && len(write.Deletes) == 1 {
				cleared = true
			}
		}
		if cleared != test.cleared {
			t.Fatalf("%s: writes %+v, want the old index cleared=%v", name, index.writes, test.cleared)
		}
	}
}

// generationNoDataStore holds one Plan's memory per generation; a generation
// it does not hold reads as MISSING. It records the generations read.
type generationNoDataStore struct {
	rememberingNoDataStore
	held map[execution.StateGeneration][]execution.NoDataGroupMemory
	read []execution.StateGeneration
}

func (store *generationNoDataStore) LoadNoData(
	ctx context.Context, request execution.NoDataLoadRequest,
) (execution.NoDataLoadResult, error) {
	result := execution.NoDataLoadResult{Items: make([]execution.NoDataMemorySnapshot, len(request.Items))}
	for index, item := range request.Items {
		store.read = append(store.read, item.Identity.StateGeneration)
		groups, held := store.held[item.Identity.StateGeneration]
		if !held {
			result.Items[index] = execution.NoDataMemorySnapshot{Identity: item.Identity, Status: execution.NoDataMemoryMissing,
				Representation: execution.NoDataRepresentationNone}
			continue
		}
		store.groups = groups
		one, err := store.rememberingNoDataStore.LoadNoData(ctx, execution.NoDataLoadRequest{Contract: request.Contract,
			Items: []execution.PlanNoDataLoadItem{item}})
		if err != nil {
			return execution.NoDataLoadResult{}, err
		}
		result.Items[index] = one.Items[0]
		result.Items[index].PresentAsOf = 940
	}
	return result, nil
}

// The previous generation's memory is read only while the new generation
// holds no record, and the round then starts from its groups under a
// snapshot that still names no record, so its first write replaces the
// whole record under the new generation.
func TestCarriedMemoryIsReadOnlyWhileTheNewGenerationHoldsNoRecord(t *testing.T) {
	carried := []execution.NoDataGroupMemory{{GroupKey: "bk_target_ip=192.0.2.2,__NO_DATA_DIMENSION__=true", LastSeen: 880, FirstAbsent: 940}}
	own := []execution.NoDataGroupMemory{{GroupKey: "bk_target_ip=192.0.2.1,__NO_DATA_DIMENSION__=true", LastSeen: 940}}
	for name, test := range map[string]struct {
		held map[execution.StateGeneration][]execution.NoDataGroupMemory
		want []execution.NoDataGroupMemory
		read int
	}{
		"the new generation holds none": {held: map[execution.StateGeneration][]execution.NoDataGroupMemory{"old": carried},
			want: carried, read: 2},
		"the new generation holds its own": {held: map[execution.StateGeneration][]execution.NoDataGroupMemory{"old": carried, "new": own},
			want: own, read: 1},
	} {
		t.Run(name, func(t *testing.T) {
			store := &generationNoDataStore{held: test.held}
			duePlans := []execution.DuePlan{{
				Identity:        execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"},
				CompiledPlan:    noDataPreflightPlan(t, "7", &contract.NoDataConfigV1{Continuous: 3, Level: 2, AggDimension: []string{"bk_target_ip"}}),
				StateGeneration: "new", StateApplyEpoch: 1, ScheduleRevision: "plan-schedule-v1",
				NoDataCarry: &execution.NoDataCarry{From: "old", Memory: true},
			}}
			stream := &streamedExecution{
				coordinator: &SlotExecutionCoordinator{
					ports:  Ports{NoData: store},
					budget: ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxGapMutations: 10},
				},
				header:     execution.InternalExecutionHeader{Contract: noDataPreflightContract(t, duePlans), DuePlans: duePlans},
				supplement: &supplementRun{},
			}
			if err := stream.loadNoDataMemory(context.Background()); err != nil {
				t.Fatal(err)
			}
			snapshot, _ := stream.noData.Find(duePlans[0].NoDataIdentity())
			if !reflect.DeepEqual(snapshot.Groups, test.want) || len(store.read) != test.read {
				t.Fatalf("the round starts from %+v after %d reads, want %+v after %d", snapshot.Groups, len(store.read), test.want, test.read)
			}
			if test.read == 2 && (snapshot.Status != execution.NoDataMemoryMissing || snapshot.PresentAsOf != 940) {
				t.Fatalf("carried snapshot = %+v, want MISSING with the previous present-as-of", snapshot)
			}
		})
	}
}
