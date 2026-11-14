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
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// rosterStore holds one record per generation and answers every read the way
// the Redis store does, recording what each command carried: a scan pages
// the groups in key order Count at a time and returns the keys in repeat
// twice, a read by name answers in the order asked.
type rosterStore struct {
	headers map[execution.StateGeneration]execution.NoDataMemorySnapshot
	groups  map[execution.StateGeneration][]execution.NoDataGroupMemory
	repeat  map[string]bool
	// moved has the header read after a scan name the next revision.
	moved bool
	// commands is each read in order; fields how many distinct groups each
	// scan and each read by name carried.
	commands   []string
	fields     []int
	wholeLoads int
	applied    []execution.PlanNoDataMutation
}

func (store *rosterStore) header(identity execution.PlanNoDataIdentity) execution.NoDataMemorySnapshot {
	header, held := store.headers[identity.StateGeneration]
	if !held {
		return execution.NoDataMemorySnapshot{Identity: identity, Status: execution.NoDataMemoryMissing,
			Representation: execution.NoDataRepresentationNone}
	}
	header.Identity = identity
	return header
}

func (store *rosterStore) LoadNoData(_ context.Context, request execution.NoDataLoadRequest) (execution.NoDataLoadResult, error) {
	store.wholeLoads++
	store.commands = append(store.commands, "HGETALL")
	result := execution.NoDataLoadResult{}
	for _, item := range request.Items {
		snapshot := store.header(item.Identity)
		snapshot.Groups = store.groups[item.Identity.StateGeneration]
		result.Items = append(result.Items, snapshot)
	}
	return result, nil
}

func (store *rosterStore) ReadNoDataHeaders(_ context.Context, request execution.NoDataLoadRequest) (execution.NoDataLoadResult, error) {
	store.commands = append(store.commands, "HGET")
	result := execution.NoDataLoadResult{}
	for _, item := range request.Items {
		result.Items = append(result.Items, store.header(item.Identity))
	}
	return result, nil
}

func (store *rosterStore) RecheckNoDataHeader(_ context.Context, identity execution.PlanNoDataIdentity) (execution.NoDataMemorySnapshot, error) {
	store.commands = append(store.commands, "HGET")
	header := store.header(identity)
	if store.moved {
		header.MarkerRevision++
	}
	return header, nil
}

func (store *rosterStore) ScanNoDataGroups(_ context.Context, request execution.NoDataScanRequest) (execution.NoDataScanPage, error) {
	store.commands = append(store.commands, "HSCAN")
	groups := append([]execution.NoDataGroupMemory(nil), store.groups[request.Identity.StateGeneration]...)
	sort.Slice(groups, func(left, right int) bool { return groups[left].GroupKey < groups[right].GroupKey })
	start := min(int(request.Cursor), len(groups))
	end := min(start+request.Count, len(groups))
	page := execution.NoDataScanPage{Status: execution.NoDataMemoryFound}
	for _, group := range groups[start:end] {
		page.Groups = append(page.Groups, group)
		if store.repeat[group.GroupKey] {
			page.Groups = append(page.Groups, group)
		}
	}
	store.fields = append(store.fields, end-start)
	if end < len(groups) {
		page.Next = uint64(end)
	}
	return page, nil
}

func (store *rosterStore) ReadNoDataGroups(_ context.Context, request execution.NoDataGroupsRequest) (execution.NoDataGroupsResult, error) {
	store.commands = append(store.commands, "HMGET")
	store.fields = append(store.fields, len(request.Keys))
	held := map[string]execution.NoDataGroupMemory{}
	for _, group := range store.groups[request.Identity.StateGeneration] {
		held[group.GroupKey] = group
	}
	result := execution.NoDataGroupsResult{Status: execution.NoDataMemoryFound}
	for _, key := range request.Keys {
		result.Groups = append(result.Groups, held[key])
	}
	return result, nil
}

func (store *rosterStore) ApplyNoData(_ context.Context, request execution.NoDataApplyRequest) (execution.NoDataApplyResult, error) {
	result := execution.NoDataApplyResult{}
	for _, mutation := range request.Items {
		store.applied = append(store.applied, mutation)
		result.Items = append(result.Items, execution.NoDataApplyItemResult{Identity: mutation.Identity, Status: execution.NoDataApplied})
	}
	return result, nil
}

// rosterGroups is count remembered hosts of the wired Plan, every third last
// seen long ago and absent since, the rest seen in the round before.
func rosterGroups(t *testing.T, count int, lastRound int64) ([]execution.NoDataGroupMemory, []map[string]string) {
	t.Helper()
	var groups []execution.NoDataGroupMemory
	var dimensions []map[string]string
	for index := range count {
		host := map[string]string{"bk_target_ip": fmt.Sprintf("10.0.%d.%d", index/256, index%256), "bk_target_cloud_id": "0"}
		group, ok := nodata.Project(host, []string{"bk_target_ip", "bk_target_cloud_id"})
		if !ok {
			t.Fatal("fixture: a host did not project")
		}
		dimensions = append(dimensions, host)
		memory := execution.NoDataGroupMemory{GroupKey: group.Key(), LastSeen: lastRound}
		if index%3 == 0 {
			memory = execution.NoDataGroupMemory{GroupKey: group.Key(), LastSeen: lastRound - 600, FirstAbsent: lastRound - 300}
		}
		groups = append(groups, memory)
	}
	sort.Slice(groups, func(left, right int) bool { return groups[left].GroupKey < groups[right].GroupKey })
	return groups, dimensions
}

func rosterHeader(lastRound int64) execution.NoDataMemorySnapshot {
	return execution.NoDataMemorySnapshot{Status: execution.NoDataMemoryFound, MarkerRevision: 3,
		SchemaVersion: execution.WrittenNoDataMemorySchema, Representation: execution.NoDataRepresentationPerGroup,
		PersistedApplyVersion:   execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: execution.EvaluationTime(lastRound), SlotDigest: "earlier"},
		PersistedMutationDigest: "stored", LastScheduleRevision: "plan-schedule-v1", RosterVersion: "HISTORY/1",
		PresentAsOf: lastRound}
}

// rosterStream is the wired Plan's Slot over store, with room in the pool for
// a few thousand groups and segments of segment fields.
func rosterStream(t *testing.T, due execution.DuePlan, store *rosterStore, segment int) *streamedExecution {
	t.Helper()
	stream := noDataWiredStream(t, due, store)
	stream.coordinator.budget.MaxRetainedBytes = 256 << 20
	stream.coordinator.noDataSegment = segment
	if err := stream.loadNoDataMemory(context.Background()); err != nil {
		t.Fatal(err)
	}
	return stream
}

// wholeReadOf is the round the whole read decides over the same record: what
// the segmented round has to equal.
func wholeReadOf(t *testing.T, stream *streamedExecution, due execution.DuePlan, snapshot execution.NoDataMemorySnapshot,
	seen []map[string]string,
) nodata.PlanSlotResult {
	t.Helper()
	config := due.CompiledPlan.NoData()
	version, err := execution.BuildApplyVersion(stream.header.Contract, due.StateApplyEpoch)
	if err != nil {
		t.Fatal(err)
	}
	result, err := nodata.EvaluatePlanSlot(nodata.PlanSlotInput{
		NoData: config, Scope: due.CompiledPlan.TargetScope(), Plan: due.CompiledPlan.TargetPlan(),
		Identity: due.NoDataIdentity(), Snapshot: snapshot, ApplyVersion: version, ScheduleRevision: due.ScheduleRevision,
		EvaluationTime: int64(stream.header.Contract.Slot.EvaluationTime),
		PeriodSeconds:  int64(due.CompiledPlan.EvaluationSemantics().EvaluationInterval),
		Completeness:   execution.CompletenessFull, Series: seen,
		TrackingHorizonSeconds: config.TrackingHorizonSeconds, MaxGroups: config.MaxGroups,
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func syntheticKeys(t *testing.T, series []completedSeries) []string {
	t.Helper()
	keys := make([]string, 0, len(series))
	for _, entry := range series {
		keys = append(keys, string(entry.series))
	}
	sort.Strings(keys)
	return keys
}

func wholeSyntheticKeys(t *testing.T, stream *streamedExecution, due execution.DuePlan, want nodata.PlanSlotResult) []string {
	t.Helper()
	view, err := execution.PlanViewFor(due, execution.SeriesKindNoData)
	if err != nil {
		t.Fatal(err)
	}
	version, err := execution.BuildApplyVersion(stream.header.Contract, due.StateApplyEpoch)
	if err != nil {
		t.Fatal(err)
	}
	var entries []completedSeries
	for _, synthetic := range want.Series {
		entry, err := stream.noDataCompletedSeries(view, synthetic, version)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	return syntheticKeys(t, entries)
}

// B2 at the worker, a small-scale stand-in for 500,000 groups: three thousand
// remembered hosts, two thirds reporting, read in segments of seven. The
// round says the series and writes the statement the whole read does, no read
// carries more than a segment's fields, and the memory is never read whole.
func TestASegmentedRoundOfThousandsOfGroupsEqualsTheWholeReadOneSegmentACommand(t *testing.T) {
	due := noDataWiredPlan(t)
	const segment = 7
	evaluation := int64(1_788_000_000)
	groups, dimensions := rosterGroups(t, 3000, evaluation-60)
	var seen []map[string]string
	for index, host := range dimensions {
		if index%3 != 1 {
			seen = append(seen, host)
		}
	}
	header := rosterHeader(evaluation - 60)
	store := &rosterStore{headers: map[execution.StateGeneration]execution.NoDataMemorySnapshot{due.StateGeneration: header},
		groups: map[execution.StateGeneration][]execution.NoDataGroupMemory{due.StateGeneration: groups}}
	stream := rosterStream(t, due, store, segment)

	round, err := stream.noDataRoundFor(due, seen, execution.CompletenessFull)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := header
	snapshot.Identity, snapshot.Groups = due.NoDataIdentity(), groups
	want := wholeReadOf(t, stream, due, snapshot, seen)
	if !reflect.DeepEqual(round.mutation, want.Mutation) {
		t.Fatalf("segmented statement %+v\nwhole read %+v", round.mutation, want.Mutation)
	}
	if round.outcome != want.Outcome || !reflect.DeepEqual(round.facts, want.Facts) {
		t.Fatalf("segmented %s %+v, whole read %s %+v", round.outcome, round.facts, want.Outcome, want.Facts)
	}
	if got, wantKeys := syntheticKeys(t, round.series), wholeSyntheticKeys(t, stream, due, want); !reflect.DeepEqual(got, wantKeys) {
		t.Fatalf("segmented round said %d series, the whole read %d", len(got), len(wantKeys))
	}
	if store.wholeLoads != 0 {
		t.Fatalf("the Slot read the memory whole %d times: %v", store.wholeLoads, store.commands)
	}
	for _, fields := range store.fields {
		if fields > segment {
			t.Fatalf("a read carried %d groups, more than a segment of %d", fields, segment)
		}
	}
}

// R3: a scan that returns a present field and an absent field twice says one
// series for the absent group and folds each group once: the statement is
// the whole read's, digest for digest.
func TestAFieldTheScanReturnsTwiceIsJudgedAndFoldedOnce(t *testing.T) {
	due := noDataWiredPlan(t)
	evaluation := int64(1_788_000_000)
	groups, dimensions := rosterGroups(t, 12, evaluation-60)
	// The first host does not report this round; the second does.
	seen := dimensions[1:]
	project := func(host map[string]string) string {
		group, _ := nodata.Project(host, []string{"bk_target_ip", "bk_target_cloud_id"})
		return group.Key()
	}
	absentKey, presentKey := project(dimensions[0]), project(dimensions[1])
	header := rosterHeader(evaluation - 60)
	store := &rosterStore{headers: map[execution.StateGeneration]execution.NoDataMemorySnapshot{due.StateGeneration: header},
		groups: map[execution.StateGeneration][]execution.NoDataGroupMemory{due.StateGeneration: groups},
		repeat: map[string]bool{absentKey: true, presentKey: true}}
	stream := rosterStream(t, due, store, 4)

	round, err := stream.noDataRoundFor(due, seen, execution.CompletenessFull)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := header
	snapshot.Identity, snapshot.Groups = due.NoDataIdentity(), groups
	want := wholeReadOf(t, stream, due, snapshot, seen)
	if !reflect.DeepEqual(round.mutation, want.Mutation) {
		t.Fatalf("a repeated field moved the statement: %+v\nwant %+v", round.mutation, want.Mutation)
	}
	got := syntheticKeys(t, round.series)
	for index := 1; index < len(got); index++ {
		if got[index] == got[index-1] {
			t.Fatalf("series %s was said twice", got[index])
		}
	}
	if wantKeys := wholeSyntheticKeys(t, stream, due, want); !reflect.DeepEqual(got, wantKeys) {
		t.Fatalf("said %v, want %v", got, wantKeys)
	}
}

// A header that moved between the scan's two reads is a record another writer
// changed under it: the Slot fails as a retryable read, by name, and nothing
// of the round is said.
func TestAHeaderThatMovedDuringTheScanFailsTheSlotAsRetryable(t *testing.T) {
	due := noDataWiredPlan(t)
	evaluation := int64(1_788_000_000)
	groups, _ := rosterGroups(t, 9, evaluation-60)
	store := &rosterStore{headers: map[execution.StateGeneration]execution.NoDataMemorySnapshot{due.StateGeneration: rosterHeader(evaluation - 60)},
		groups: map[execution.StateGeneration][]execution.NoDataGroupMemory{due.StateGeneration: groups}, moved: true}
	stream := rosterStream(t, due, store, 4)

	round, err := stream.noDataRoundFor(due, nil, execution.CompletenessFull)
	var moved *NoDataRosterMovedError
	if !errors.As(err, &moved) {
		t.Fatalf("err = %v, want the record named as moved", err)
	}
	if _, local := noDataLocalOutcome(err); local {
		t.Fatal("a moved record was contained as this Plan's outcome; it has to fail the Slot for a retry")
	}
	if reason, named := NoDataRosterReadReason(err); !named || reason != contract.ReasonStateWriteRetryable {
		t.Fatalf("reason = %q, %v; want %s", reason, named, contract.ReasonStateWriteRetryable)
	}
	if len(round.series) != 0 {
		t.Fatalf("a round whose record moved said %d series", len(round.series))
	}
}

// A memory carried from the generation before is scanned by segments under
// that generation's key, and the round equals the whole carry: the same
// series and the same whole-record statement under the new generation.
func TestACarriedMemoryReadBySegmentsEqualsTheWholeCarry(t *testing.T) {
	due := noDataWiredPlan(t)
	due.StateGeneration = "state-v2"
	due.NoDataCarry = &execution.NoDataCarry{From: "state-v1", Memory: true}
	evaluation := int64(1_788_000_000)
	groups, dimensions := rosterGroups(t, 40, evaluation-60)
	seen := dimensions[:25]
	previous := rosterHeader(evaluation - 60)
	store := &rosterStore{headers: map[execution.StateGeneration]execution.NoDataMemorySnapshot{"state-v1": previous},
		groups: map[execution.StateGeneration][]execution.NoDataGroupMemory{"state-v1": groups}}
	stream := rosterStream(t, due, store, 6)

	round, err := stream.noDataRoundFor(due, seen, execution.CompletenessFull)
	if err != nil {
		t.Fatal(err)
	}
	carried := execution.NoDataMemorySnapshot{Identity: due.NoDataIdentity(), Status: execution.NoDataMemoryMissing,
		Representation: execution.NoDataRepresentationNone, PresentAsOf: previous.PresentAsOf,
		RosterVersion: previous.RosterVersion, Groups: groups}
	want := wholeReadOf(t, stream, due, carried, seen)
	if want.Mutation == nil || !want.Mutation.ReplacesWholeRecord() {
		t.Fatalf("whole carry statement %+v, want a whole record under the new generation", want.Mutation)
	}
	if !reflect.DeepEqual(round.mutation, want.Mutation) {
		t.Fatalf("carried by segments %+v\nwhole carry %+v", round.mutation, want.Mutation)
	}
	if got, wantKeys := syntheticKeys(t, round.series), wholeSyntheticKeys(t, stream, due, want); !reflect.DeepEqual(got, wantKeys) {
		t.Fatalf("carried by segments said %d series, the whole carry %d", len(got), len(wantKeys))
	}
	if store.wholeLoads != 0 {
		t.Fatalf("the carry read a memory whole: %v", store.commands)
	}
}

// preparedHost is one prepared series of the wired Plan reporting host.
func preparedHost(t *testing.T, due execution.DuePlan, host map[string]string, index int) preparedSeries {
	t.Helper()
	dimensions := map[string]json.RawMessage{}
	for name, value := range host {
		dimensions[name] = json.RawMessage(strconv.Quote(value))
	}
	digest := fmt.Sprintf("%064x", index+1)
	dataset := execution.NewDataset([]contract.CanonicalRecordV2{{RecordID: digest, SourceTime: 1_787_999_940, BusinessID: "2",
		DimensionIdentity: contract.DimensionIdentityV2{Digest: digest}, Dimensions: dimensions,
		Values: map[string]json.RawMessage{"value": json.RawMessage("1")}, ReceivedTime: 1_787_999_940}})
	view, err := execution.NewDatasetView(dataset, []uint32{0})
	if err != nil {
		t.Fatal(err)
	}
	return preparedSeries{due: &due, identity: execution.SeriesIdentityDigest(digest),
		inputs: []execution.SeriesEvaluationInputRequest{{Inputs: []execution.NamedInputBinding{{
			Role: execution.InputRolePrimary, Dataset: dataset, View: view}}}}}
}

// A series whose window landed - written, or resumed by a retry from what an
// earlier attempt wrote, which is the same prepared series - stays in the
// present set once its inputs are let go: its group is judged present and
// says no absence. Left out, every group of the first window would read as
// absent on the retry.
func TestTheSeriesOfALandedWindowStayInThePresentSet(t *testing.T) {
	due := noDataWiredPlan(t)
	evaluation := int64(1_788_000_000)
	groups, dimensions := rosterGroups(t, 6, evaluation-60)
	store := &rosterStore{headers: map[execution.StateGeneration]execution.NoDataMemorySnapshot{due.StateGeneration: rosterHeader(evaluation - 60)},
		groups: map[execution.StateGeneration][]execution.NoDataGroupMemory{due.StateGeneration: groups}}
	stream := rosterStream(t, due, store, 4)
	var prepared []preparedSeries
	for index, host := range dimensions {
		prepared = append(prepared, preparedHost(t, due, host, index))
	}
	stream.windows.prepared = prepared
	// The first window, of three series, landed: its inputs go, its groups
	// stay in the set.
	if err := stream.dropWindowInputs(context.Background(), 3); err != nil {
		t.Fatal(err)
	}
	if stream.windows.present[due.Identity].Len() != 3 {
		t.Fatalf("the landed window kept %d groups, want its 3", stream.windows.present[due.Identity].Len())
	}
	var said []completedSeries
	round, err := stream.noDataRoundOf(context.Background(), due, prepared, func(entry completedSeries) error {
		said = append(said, entry)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if round.facts.Absent != 0 || round.facts.Present != uint64(len(dimensions)) {
		t.Fatalf("facts %+v, want every host present and none absent", round.facts)
	}
	for _, entry := range said {
		if entry.closing {
			continue
		}
		record, _ := entry.inputs[0].Inputs[0].View.Record(0)
		if value := string(record.Values()[strategy.NoDataValueField]); value == strconv.Itoa(nodata.AbsentValue) {
			t.Fatalf("series %s was said absent", entry.series)
		}
	}
}
