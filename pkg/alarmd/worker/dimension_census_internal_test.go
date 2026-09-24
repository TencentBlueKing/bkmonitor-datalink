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
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The gate's undercounting direction first: a Query Group that has reached
// its share is the one the split trigger is about, so the boundary belongs to
// the candidate side. Judged the other way, the object the trigger names is
// the one object that never has a census, and the planner reads "no census"
// as "do not split" - a deployment that would quietly never split anything,
// with no reading that says so.
func TestAQueryGroupAtItsShareIsACensusCandidate(t *testing.T) {
	const share = 512 << 20

	candidate, got := censusCandidate(share, share)
	if !candidate {
		t.Fatalf("a Query Group peaking at exactly its %d-byte share is not a candidate; the split trigger "+
			"names that object, and without a census nothing will ever split it", uint64(share))
	}
	if got != share {
		t.Fatalf("the gate reported a %d-byte share, want %d: the line has to carry the number it decided on",
			got, uint64(share))
	}
	if candidate, _ := censusCandidate(share-1, share); candidate {
		t.Fatalf("a Query Group one byte under its share is a candidate; every Slot on the replica would " +
			"then count series it has no use for")
	}
}

// And the share the gate is judged against is the one the worker enforces,
// not a second number that agrees with it today.
//
// The two were derived apart once - the gate recomputed the share as a
// percentage of the pool while the refusal used qgShareBytes - and they even
// disagreed at the edges, because the percentage truncates the pool to a
// multiple of a hundred first. Nothing could have failed in between: one
// relation with two derivations has no place to be wrong.
func TestTheCensusGateIsJudgedByTheShareTheWorkerRefusesBy(t *testing.T) {
	for _, pool := range []uint64{0, 199, 1 << 20, (1 << 30) + 7} {
		coordinator := &SlotExecutionCoordinator{budget: ProvisionalBudget{MaxRetainedBytes: pool}}
		enforced := coordinator.qgShareBytes()
		// Through the gate the Slot actually opens, not through the predicate
		// alone: the second derivation lived at the call site, so a test that
		// hands the share in cannot see it.
		coordinator.censusPeaks.record("qg-share", enforced)
		stream := &streamedExecution{coordinator: coordinator}
		stream.openCensusGate("qg-share")
		if stream.censusShareBytes != enforced {
			t.Fatalf("pool %d: the gate judged by %d, the worker refuses by %d",
				pool, stream.censusShareBytes, enforced)
		}
		if want := enforced > 0; stream.censusCandidate != want {
			t.Fatalf("pool %d: a Query Group peaking at exactly the enforced share is a candidate = %v, want %v",
				pool, stream.censusCandidate, want)
		}
	}
}

// A worker with no share figure judges nothing. Zero is not a small share, it
// is no reading - and read as a small one, every Query Group clears it and
// every Slot on the replica takes a census.
func TestAWorkerWithNoShareReadingTakesNoCensus(t *testing.T) {
	if candidate, share := censusCandidate(1<<30, 0); candidate || share != 0 {
		t.Fatalf("censusCandidate(_, 0) = %v, %d, want no candidate and no share", candidate, share)
	}
	if candidate, _ := censusCandidate(0, 1<<30); candidate {
		t.Fatalf("a Query Group with no peak is a candidate, want the gate to need a reading on both sides")
	}
}

// A census value is what a split matcher would have to write. A dimension
// that cannot be one is left out rather than stringified into a value no
// matcher will ever equal: a piece cut for `{"a":1}` matches nothing, and the
// census would have said it holds series.
func TestACensusValueIsOnlyWhatASplitCouldMatchOn(t *testing.T) {
	for raw, want := range map[string]string{
		`"host-01"`: "host-01",
		`12`:        "12",
		`-3`:        "-3",
		`1.5`:       "1.5",
	} {
		value, ok := censusDimensionValue([]byte(raw))
		if !ok || value != want {
			t.Fatalf("censusDimensionValue(%s) = %q, %v, want %q", raw, value, ok, want)
		}
	}
	for _, raw := range []string{`{"a":1}`, `[1,2]`, `null`, `true`, `""`, ``} {
		if value, ok := censusDimensionValue([]byte(raw)); ok {
			t.Fatalf("censusDimensionValue(%s) = %q, want it left out of the census", raw, value)
		}
	}
}

func censusDatasetView(t *testing.T, dimensions map[string]json.RawMessage) *execution.DatasetView {
	t.Helper()
	dataset := execution.NewDataset([]contract.CanonicalRecordV2{{
		RecordID: "record", SourceTime: 600, BusinessID: "2", Dimensions: dimensions,
	}})
	view, err := execution.NewDatasetView(dataset, []uint32{0})
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func censusRecordView(t *testing.T, dimensions map[string]json.RawMessage) execution.RecordView {
	t.Helper()
	record, ok := censusDatasetView(t, dimensions).Record(0)
	if !ok {
		t.Fatal("the fixture dataset has no record")
	}
	return record
}

func censusTestIdentity() execution.PlanCensusIdentity {
	return execution.PlanCensusIdentity{
		Plan:            execution.PlanIdentity{TenantID: "system", BusinessID: "2", StrategyID: "4101"},
		StateGeneration: "generation",
	}
}

// The synthetic series a no-data round produces travel the same evaluation
// path as real ones, so they arrive here too - and they must not be added to
// the round's census. They exist because their groups did NOT report:
// counted beside the groups that did, a value carrying no series at all would
// enter the distribution a split is cut from, and the piece cut for it stays
// empty for as long as the roster remembers that group.
func TestASyntheticAbsenceSeriesIsCountedIntoTheRosterCensusAndNotTheRounds(t *testing.T) {
	builders := &censusBuilders{}
	identity := censusTestIdentity()

	builders.observeSeries(identity, execution.DimensionCensusFromRound, censusRecordView(t,
		map[string]json.RawMessage{"ip": json.RawMessage(`"192.0.2.1"`)}))
	// The tag as a string rather than as the boolean this repository's own
	// synthetic series happen to carry. A boolean is dropped by the value
	// guard whatever the tag filter does, so a fixture carrying one would
	// pass with the filter deleted - and the encoding of a dimension written
	// elsewhere is not this code's to assume.
	builders.observeSeries(identity, execution.DimensionCensusFromRoster, censusRecordView(t,
		map[string]json.RawMessage{
			"ip":                        json.RawMessage(`"192.0.2.9"`),
			contract.NoDataDimensionTag: json.RawMessage(`"missing"`),
		}))

	round, taken, err := builders.byPlan[identity.Plan].Build(600)
	if err != nil || !taken {
		t.Fatalf("the round census = %v, %v, want a census", taken, err)
	}
	if round.Series != 1 || len(round.Dimensions) != 1 || round.Dimensions[0].Values[0].Value != "192.0.2.1" {
		t.Fatalf("the round census counted the absence series too: %+v", round)
	}

	roster, taken, err := builders.rosterByPlan[identity.Plan].Build(600)
	if err != nil || !taken {
		t.Fatalf("the roster census = %v, %v, want a census", taken, err)
	}
	if roster.Source != execution.DimensionCensusFromRoster {
		t.Fatalf("the roster census reports source %q, want the fallback named: its values are an upper "+
			"bound and a reader has to be able to tell", roster.Source)
	}
	for _, entry := range roster.Dimensions {
		if entry.Dimension == contract.NoDataDimensionTag {
			t.Fatalf("the census names %q as a dimension; a piece cut by matching it would hold the "+
				"absence series and none of the series they are about", entry.Dimension)
		}
	}
}

type recordingCensusStore struct {
	written []execution.DimensionCensus
	outcome execution.CensusWriteOutcome
	err     error
}

func (store *recordingCensusStore) WriteCensus(
	_ context.Context, census execution.DimensionCensus,
) (execution.CensusWriteOutcome, error) {
	store.written = append(store.written, census)
	if store.outcome.Status == "" {
		return execution.CensusWriteOutcome{Status: execution.CensusWritten, Bytes: 128, Limit: 524288}, store.err
	}
	return store.outcome, store.err
}

func censusStream(t *testing.T, store execution.PlanCensusStore, output *bytes.Buffer) *streamedExecution {
	t.Helper()
	limiter, err := observability.NewWindowLogLimiter(observability.WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 10})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := observability.NewBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	coordinator := &SlotExecutionCoordinator{ports: Ports{
		Census:   store,
		Observer: observability.NewLoggingObserver(observability.New("alarmd", output), policy),
	}}
	stream := &streamedExecution{coordinator: coordinator, censusPeakBytes: 700 << 20, censusShareBytes: 512 << 20}
	stream.header.Contract.Slot.EvaluationTime = 600
	stream.header.Contract.Slot.QueryGroup = "qg-census"
	return stream
}

// A Plan the round saw series for takes the round's census and not its
// roster's. The roster is a memory; the round is a reading, and where both
// exist the reading is the one a split is planned from.
func TestThePlanTheRoundSawSeriesForTakesTheRoundsCensus(t *testing.T) {
	store := &recordingCensusStore{}
	var output bytes.Buffer
	stream := censusStream(t, store, &output)
	identity := censusTestIdentity()

	stream.censuses.observeSeries(identity, execution.DimensionCensusFromRound, censusRecordView(t,
		map[string]json.RawMessage{"ip": json.RawMessage(`"192.0.2.1"`)}))
	stream.censuses.observeSeries(identity, execution.DimensionCensusFromRoster, censusRecordView(t,
		map[string]json.RawMessage{"ip": json.RawMessage(`"192.0.2.9"`)}))

	stream.writeCensuses(context.Background())

	if len(store.written) != 1 {
		t.Fatalf("the Slot wrote %d censuses for one Plan, want the round's alone", len(store.written))
	}
	if store.written[0].Source != execution.DimensionCensusFromRound {
		t.Fatalf("the Slot wrote the %q census, want the round's", store.written[0].Source)
	}
}

// And a Plan whose round saw nothing falls back to its roster, under the
// roster's own name.
func TestAPlanWhoseRoundSawNothingFallsBackToItsRoster(t *testing.T) {
	store := &recordingCensusStore{}
	var output bytes.Buffer
	stream := censusStream(t, store, &output)
	identity := censusTestIdentity()

	stream.censuses.observeSeries(identity, execution.DimensionCensusFromRoster, censusRecordView(t,
		map[string]json.RawMessage{"ip": json.RawMessage(`"192.0.2.9"`)}))

	stream.writeCensuses(context.Background())

	if len(store.written) != 1 || store.written[0].Source != execution.DimensionCensusFromRoster {
		t.Fatalf("the Slot wrote %+v, want one census under the roster's name", store.written)
	}

	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode the census line: %v; log=%s", err, output.String())
	}
	if event["census_source"] != "roster" {
		t.Fatalf("census_source = %#v, want the fallback countable on the line as well as in the store: "+
			"a reading that is an upper bound and cannot be told from a current one is worse than none",
			event["census_source"])
	}
	if event["census_peak_bytes"] != float64(700<<20) || event["census_share_bytes"] != float64(512<<20) {
		t.Fatalf("the census line does not carry the gate's two numbers: %#v", event)
	}
}

// A refused census is reported as degraded under the store's own name. It is
// not a Slot failure: the census is a planning input, so a Plan whose census
// was refused goes on evaluating, and the refusal has to be countable or
// nothing says the planner is running on nothing.
func TestARefusedCensusIsReportedUnderItsOwnNameAndDoesNotStopTheSlot(t *testing.T) {
	store := &recordingCensusStore{outcome: execution.CensusWriteOutcome{
		Status: execution.CensusRejected, ReasonCode: execution.ReasonCode(contract.ReasonStateBudgetExceeded),
		Bytes: 900000, Limit: 524288,
	}}
	var output bytes.Buffer
	stream := censusStream(t, store, &output)
	stream.censuses.observeSeries(censusTestIdentity(), execution.DimensionCensusFromRound, censusRecordView(t,
		map[string]json.RawMessage{"ip": json.RawMessage(`"192.0.2.1"`)}))

	stream.writeCensuses(context.Background())

	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode the census line: %v; log=%s", err, output.String())
	}
	if event["census_status"] != "REJECTED" {
		t.Fatalf("census_status = %#v, want the refusal named", event["census_status"])
	}
	if event["reason_code"] != contract.ReasonStateBudgetExceeded {
		t.Fatalf("reason_code = %#v, want %q", event["reason_code"], contract.ReasonStateBudgetExceeded)
	}
	if event["census_bytes"] != float64(900000) || event["census_limit"] != float64(524288) {
		t.Fatalf("the line does not say how far over the cap the census was: %#v", event)
	}
}

// Which census a series lands in is decided from the series' kind, on the
// path the Slot actually calls. Asserted here rather than by handing the
// source in: a test that names the source itself proves the two builders are
// kept apart and proves nothing about the one line that chooses between them,
// and that line is the whole of what keeps a group that did not report out of
// the distribution a split is cut from.
func TestTheSeriesKindDecidesWhichCensusTheSeriesLandsIn(t *testing.T) {
	store := &recordingCensusStore{}
	var output bytes.Buffer
	stream := censusStream(t, store, &output)
	stream.censusCandidate = true
	due := execution.DuePlan{Identity: censusTestIdentity().Plan, StateGeneration: "generation"}

	count := func(kind execution.SeriesKind, value string) {
		stream.countSeriesForCensus(due, []execution.SeriesEvaluationInputRequest{{
			Kind: kind,
			Inputs: []execution.NamedInputBinding{{
				Role: execution.InputRolePrimary,
				View: censusDatasetView(t, map[string]json.RawMessage{"ip": json.RawMessage(`"` + value + `"`)}),
			}},
		}})
	}
	count(execution.SeriesKindReal, "192.0.2.1")
	count(execution.SeriesKindNoData, "192.0.2.9")

	round, taken, err := stream.censuses.byPlan[due.Identity].Build(600)
	if err != nil || !taken {
		t.Fatalf("the round census = %v, %v, want a census", taken, err)
	}
	if round.Series != 1 || round.Dimensions[0].Values[0].Value != "192.0.2.1" {
		t.Fatalf("the round census holds %+v, want only the series the round evaluated: a synthetic "+
			"absence series exists because its group did NOT report, and counted here it would put a "+
			"value carrying no series into the split's domain", round.Dimensions)
	}
	roster, taken, err := stream.censuses.rosterByPlan[due.Identity].Build(600)
	if err != nil || !taken {
		t.Fatalf("the roster census = %v, %v, want the absence series counted there", taken, err)
	}
	if roster.Series != 1 || roster.Dimensions[0].Values[0].Value != "192.0.2.9" {
		t.Fatalf("the roster census holds %+v, want the absence series", roster.Dimensions)
	}
}

// And a Slot whose Query Group is not a candidate counts nothing at all,
// which is what keeps the census off the ordinary Slot.
func TestASlotThatIsNotACandidateCountsNothing(t *testing.T) {
	store := &recordingCensusStore{}
	var output bytes.Buffer
	stream := censusStream(t, store, &output)
	due := execution.DuePlan{Identity: censusTestIdentity().Plan, StateGeneration: "generation"}

	stream.countSeriesForCensus(due, []execution.SeriesEvaluationInputRequest{{
		Kind: execution.SeriesKindReal,
		Inputs: []execution.NamedInputBinding{{
			Role: execution.InputRolePrimary,
			View: censusDatasetView(t, map[string]json.RawMessage{"ip": json.RawMessage(`"192.0.2.1"`)}),
		}},
	}})

	if len(stream.censuses.byPlan) != 0 || len(stream.censuses.rosterByPlan) != 0 {
		t.Fatalf("a Slot that is not a candidate built %d round censuses and %d roster ones, want none",
			len(stream.censuses.byPlan), len(stream.censuses.rosterByPlan))
	}
}
