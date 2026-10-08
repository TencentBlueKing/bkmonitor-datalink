// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
)

// supplementSeries is one prepared series of due with the dimensions given,
// as a supplement reaches it before any State is read.
func supplementSeries(t *testing.T, due execution.DuePlan, at int64, dimensions map[string]string) preparedSeries {
	t.Helper()
	raw := make(map[string]json.RawMessage, len(dimensions))
	for name, value := range dimensions {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		raw[name] = encoded
	}
	dataset := execution.NewDataset([]contract.CanonicalRecordV2{{RecordID: strings.Repeat("a", 64), SourceTime: at - 60,
		BusinessID: "2", DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)},
		Values: map[string]json.RawMessage{"value": json.RawMessage(`1`)}, Dimensions: raw, ReceivedTime: at - 60}})
	view, err := execution.NewDatasetView(dataset, []uint32{0})
	if err != nil {
		t.Fatal(err)
	}
	return preparedSeries{due: due, identity: execution.SeriesIdentityDigest(strings.Repeat("c", 64)),
		inputs: []execution.SeriesEvaluationInputRequest{{Inputs: []execution.NamedInputBinding{{
			Role: execution.InputRolePrimary, Dataset: dataset, View: view, Completeness: execution.CompletenessFull}}}}}
}

// A supplement reads a Plan's no-data memory for what it recorded at the
// Slot: a group recorded absent at the Slot or before it, and not seen since,
// is a fact at the Slot the supplement does not decide against. A group
// absent only after the Slot, or seen at it, is not; a memory that could not
// be read is not known either way; and a series no-data does not track has
// nothing recorded.
func TestASupplementReadsTheNoDataMemoryAsOfItsSlot(t *testing.T) {
	due := noDataWiredPlan(t)
	stream := noDataWiredStream(t, due, &emptyNoDataStore{})
	at := int64(stream.header.Contract.Slot.EvaluationTime)
	host := map[string]string{"bk_target_ip": "192.0.2.10", "bk_target_cloud_id": "0"}
	group, tracked := nodata.Project(host, []string{"bk_target_ip", "bk_target_cloud_id"})
	if !tracked {
		t.Fatal("the fixture's host does not project to a group")
	}
	memory := func(status execution.NoDataLoadStatus, groups ...execution.NoDataGroupMemory) {
		stream.noData = execution.NoDataLoadResult{Items: []execution.NoDataMemorySnapshot{{
			Identity: due.NoDataIdentity(), Status: status, Groups: groups}}}
	}
	for _, tc := range []struct {
		name       string
		status     execution.NoDataLoadStatus
		groups     []execution.NoDataGroupMemory
		dimensions map[string]string
		want       noDataStanding
	}{
		{"no memory yet", execution.NoDataMemoryMissing, nil, host, noDataNone},
		{"absent since before the Slot", execution.NoDataMemoryFound,
			[]execution.NoDataGroupMemory{{GroupKey: group.Key(), LastSeen: at - 120, FirstAbsent: at - 60}}, host, noDataRecorded},
		{"absent from the Slot", execution.NoDataMemoryFound,
			[]execution.NoDataGroupMemory{{GroupKey: group.Key(), LastSeen: at - 60, FirstAbsent: at}}, host, noDataRecorded},
		{"absent only after the Slot", execution.NoDataMemoryFound,
			[]execution.NoDataGroupMemory{{GroupKey: group.Key(), LastSeen: at, FirstAbsent: at + 60}}, host, noDataNone},
		// Last seen before the Slot and called absent only after it: the
		// Slot's round did not judge the group, so nothing was recorded at it.
		{"not judged at the Slot", execution.NoDataMemoryFound,
			[]execution.NoDataGroupMemory{{GroupKey: group.Key(), LastSeen: at - 60, FirstAbsent: at + 60}}, host, noDataNone},
		{"seen at the Slot", execution.NoDataMemoryFound,
			[]execution.NoDataGroupMemory{{GroupKey: group.Key(), LastSeen: at}}, host, noDataNone},
		{"another group absent", execution.NoDataMemoryFound,
			[]execution.NoDataGroupMemory{{GroupKey: "other", FirstAbsent: at - 60}}, host, noDataNone},
		{"memory unavailable", execution.NoDataMemoryUnavailable, nil, host, noDataUnknown},
		{"memory unreadable", execution.NoDataMemoryUnreadable, nil, host, noDataUnknown},
		{"a series no-data does not track", execution.NoDataMemoryFound,
			[]execution.NoDataGroupMemory{{GroupKey: group.Key(), FirstAbsent: at - 60}}, map[string]string{"host": "a"}, noDataNone},
		// The item had nothing at all at the Slot: every late series of it,
		// whatever its group, and one no-data does not track.
		{"the whole item absent at the Slot", execution.NoDataMemoryFound,
			[]execution.NoDataGroupMemory{{GroupKey: nodata.WholeItemGroup().Key(), FirstAbsent: at - 120}}, host, noDataRecorded},
		{"the whole item absent, an untracked series", execution.NoDataMemoryFound,
			[]execution.NoDataGroupMemory{{GroupKey: nodata.WholeItemGroup().Key(), FirstAbsent: at}}, map[string]string{"host": "a"}, noDataRecorded},
		{"the whole item absent only after the Slot", execution.NoDataMemoryFound,
			[]execution.NoDataGroupMemory{{GroupKey: nodata.WholeItemGroup().Key(), FirstAbsent: at + 60}}, host, noDataNone},
	} {
		memory(tc.status, tc.groups...)
		if got := stream.noDataStandingAt(supplementSeries(t, due, at, tc.dimensions)); got != tc.want {
			t.Fatalf("%s: standing %d, want %d", tc.name, got, tc.want)
		}
	}
	// A Plan without no-data detection has nothing recorded, whatever the
	// load holds.
	plain := due
	plain.CompiledPlan = noDataPreflightPlan(t, "7", nil)
	memory(execution.NoDataMemoryUnavailable)
	if got := stream.noDataStandingAt(supplementSeries(t, plain, at, host)); got != noDataNone {
		t.Fatalf("a Plan without no-data: standing %d, want none", got)
	}
}

// The gap markers a supplement evaluates under are the loaded ones, with a
// marker cleared at the Slot or after it read as open: it stood when the
// Slot was evaluated. One cleared before the Slot stays cleared, and every
// other status is left as it was loaded.
func TestASupplementReadsAMarkerClearedAtOrAfterItsSlotAsOpen(t *testing.T) {
	due := noDataWiredPlan(t)
	header := execution.InternalExecutionHeader{Contract: noDataPreflightContract(t, []execution.DuePlan{due}),
		DuePlans: []execution.DuePlan{due}}
	at, err := execution.BuildApplyVersion(header.Contract, due.StateApplyEpoch)
	if err != nil {
		t.Fatal(err)
	}
	shifted := func(seconds int64, epoch execution.StateApplyEpoch) execution.ApplyVersion {
		version := at
		version.EvaluationTime += execution.EvaluationTime(seconds)
		version.StateApplyEpoch = epoch
		return version
	}
	for _, tc := range []struct {
		name    string
		status  execution.GapLoadStatus
		version execution.ApplyVersion
		open    bool
	}{
		{"cleared at the Slot", execution.GapClearedTombstone, at, true},
		{"cleared after it", execution.GapClearedTombstone, shifted(60, 1), true},
		{"cleared under a later epoch", execution.GapClearedTombstone, shifted(-60, 2), true},
		{"cleared before it", execution.GapClearedTombstone, shifted(-60, 1), false},
		{"missing", execution.GapMissing, execution.ApplyVersion{}, false},
	} {
		loaded := execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{Identity: due.GapIdentity(), Status: tc.status,
			MarkerRevision: 3, PersistedApplyVersion: tc.version}}}
		guards, err := guardsAsOfSlot(header, loaded)
		if err != nil {
			t.Fatal(err)
		}
		marker := guards.Items[0]
		open := marker.Status == execution.GapFound && len(marker.Scopes) == 1 && !marker.Scopes[0].Scope.HasLevel &&
			marker.Scopes[0].Status == execution.GapStatusGapped
		if open != tc.open || marker.MarkerRevision != 3 {
			t.Fatalf("%s: marker %+v, open %v want %v", tc.name, marker, open, tc.open)
		}
		if loaded.Items[0].Status != tc.status {
			t.Fatalf("%s: the loaded result was changed in place", tc.name)
		}
	}
	// A standing marker is read as it stands.
	standing := execution.GapGuardSnapshot{Identity: due.GapIdentity(), Status: execution.GapFound, MarkerRevision: 2,
		PersistedApplyVersion: shifted(-120, 1),
		Scopes:                []execution.GapScopeState{{Status: execution.GapStatusWarming, RequiredFullSlots: 3, ObservedFullSlots: 1}}}
	guards, err := guardsAsOfSlot(header, execution.GapLoadResult{Items: []execution.GapGuardSnapshot{standing}})
	if err != nil || len(guards.Items) != 1 || guards.Items[0].Scopes[0] != standing.Scopes[0] {
		t.Fatalf("standing marker read as %+v (%v)", guards.Items, err)
	}
}

// Before any State is read, a supplement takes the series of its scope and
// counts each it does not take by why: its input was not whole, its group
// was recorded absent at the Slot, or its Plan's memory could not be read.
// A series outside the scope is the Slot's and is not counted.
func TestASupplementTakesItsScopeAndCountsWhatItLeaves(t *testing.T) {
	due := noDataWiredPlan(t)
	stream := noDataWiredStream(t, due, &emptyNoDataStore{})
	at := int64(stream.header.Contract.Slot.EvaluationTime)
	host := map[string]string{"bk_target_ip": "192.0.2.10", "bk_target_cloud_id": "0"}
	group, _ := nodata.Project(host, []string{"bk_target_ip", "bk_target_cloud_id"})
	prepared := supplementSeries(t, due, at, host)
	stream.supplement = newSupplementRun(execution.SupplementScope{Series: []execution.SeriesIdentityDigest{prepared.identity}})
	memory := func(status execution.NoDataLoadStatus, groups ...execution.NoDataGroupMemory) {
		stream.noData = execution.NoDataLoadResult{Items: []execution.NoDataMemorySnapshot{{
			Identity: due.NoDataIdentity(), Status: status, Groups: groups}}}
	}

	memory(execution.NoDataMemoryMissing)
	outside := prepared
	outside.identity = execution.SeriesIdentityDigest(strings.Repeat("d", 64))
	if stream.supplementTakes(outside) || stream.supplement.facts != (execution.SupplementFacts{}) {
		t.Fatalf("took or counted a series outside the scope: %+v", stream.supplement.facts)
	}
	if !stream.supplementTakes(prepared) {
		t.Fatal("did not take a scoped series with nothing recorded")
	}
	memory(execution.NoDataMemoryFound, execution.NoDataGroupMemory{GroupKey: group.Key(), FirstAbsent: at - 60})
	if stream.supplementTakes(prepared) {
		t.Fatal("took a series whose group was recorded absent at the Slot")
	}
	memory(execution.NoDataMemoryUnavailable)
	if stream.supplementTakes(prepared) {
		t.Fatal("took a series whose Plan's memory could not be read")
	}
	partial := prepared
	partial.inputs = []execution.SeriesEvaluationInputRequest{{Inputs: []execution.NamedInputBinding{{
		Role: execution.InputRolePrimary, Completeness: execution.CompletenessPartial}}}}
	if stream.supplementTakes(partial) {
		t.Fatal("took a series whose primary input was not whole")
	}
	if want := (execution.SupplementFacts{Candidates: 4, NoDataFact: 1, Withheld: 1, InputIncomplete: 1}); stream.supplement.facts != want {
		t.Fatalf("facts %+v, want %+v", stream.supplement.facts, want)
	}
}

// A supplement takes no census, however heavy its Query Group: its series
// are a few late ones, not what the Query Group has. The same gate opens for
// a Slot of that Query Group.
func TestASupplementTakesNoCensus(t *testing.T) {
	coordinator := &SlotExecutionCoordinator{budget: ProvisionalBudget{MaxRetainedBytes: 1 << 20}}
	coordinator.censusPeaks.record("qg-heavy", coordinator.qgShareBytes())
	slot := &streamedExecution{coordinator: coordinator}
	slot.openCensusGate("qg-heavy")
	supplement := &streamedExecution{coordinator: coordinator,
		supplement: newSupplementRun(execution.SupplementScope{Series: []execution.SeriesIdentityDigest{"a"}})}
	supplement.openCensusGate("qg-heavy")
	if !slot.censusCandidate || supplement.censusCandidate || supplement.censusShareBytes != 0 {
		t.Fatalf("Slot candidate %v, supplement candidate %v share %d: want only the Slot's gate open",
			slot.censusCandidate, supplement.censusCandidate, supplement.censusShareBytes)
	}
}
