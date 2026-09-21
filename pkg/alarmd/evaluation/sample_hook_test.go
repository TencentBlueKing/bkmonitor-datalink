// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package evaluation

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/trigger"
)

func evaluatorSampler(t testing.TB, req execution.EvaluationRequest) (*observability.SeriesSampler, observability.SeriesSampleSelection) {
	t.Helper()
	s, err := observability.NewSeriesSampler(observability.SeriesSampleLimits{RecordsPerMinute: 8, BytesPerMinute: 8 * observability.SeriesSampleMaxBytes, QueueCapacity: 1})
	if err != nil {
		t.Fatal(err)
	}
	due := req.Header.DuePlans[0]
	now := time.Now()
	v := observability.SeriesSampleSelection{QueryGroup: string(req.Header.Contract.Slot.QueryGroup), WindowID: "test-window", OpenedAt: now, ExpiresAt: now.Add(time.Minute), TenantID: due.Identity.TenantID, BusinessID: due.Identity.BusinessID, StrategyID: due.Identity.StrategyID, StateGeneration: string(due.StateGeneration), PlanScheduleRevision: string(due.ScheduleRevision), SeriesDigest: string(req.Inputs[0].SeriesIdentity), SeriesKind: string(req.Inputs[0].Kind)}
	if err := s.Select([]observability.SeriesSampleSelection{v}); err != nil {
		t.Fatal(err)
	}
	return s, v
}

func readEvaluatorSample(t testing.TB, s *observability.SeriesSampler) observability.SeriesSample {
	t.Helper()
	select {
	case r := <-s.Records():
		defer r.Release()
		var got observability.SeriesSample
		if err := json.Unmarshal(r.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	default:
		t.Fatalf("sample missing: %+v", s.Health())
		return observability.SeriesSample{}
	}
}

func equivalentSample(t *testing.T, req execution.EvaluationRequest) (execution.EvaluationResult, observability.SeriesSample) {
	t.Helper()
	e := newEvaluator(t)
	baseline, err := e.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := evaluatorSampler(t, req)
	e.SetSeriesSampler(s)
	got, err := e.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, baseline) {
		t.Fatal("sampling changed evaluator result/state/events")
	}
	// Stand in for the worker before validating: a round with an incomplete
	// input has its Level marker proposed by the worker after Evaluate, and
	// the contract accepts the outcome's reason against that marker. Without
	// this the harness refuses a shape production produces every time a
	// guarded series meets an empty dependency.
	proposeRoundGuard(t, req, &got)
	if err := got.Validate(req); err != nil {
		t.Fatal(err)
	}
	return got, readEvaluatorSample(t, s)
}

func TestSeriesSampleUsesActualEvaluatorFinalOutcome(t *testing.T) {
	for _, value := range []string{"80", "10", "-0.125"} {
		t.Run(value, func(t *testing.T) {
			req := requestFixture(t, json.RawMessage(value), nil)
			got, sample := equivalentSample(t, req)
			l := sample.Levels[0]
			want := got.Plans[0].LevelOutcomes[0]
			if l.Outcome != string(want.Outcome) || l.Reason != string(want.ReasonCode) || !sample.Provisional || sample.RecordID != want.Record.RecordID || sample.RunID != 0 || sample.ExecutionID != req.Header.ExecutionID {
				t.Fatalf("sample=%+v outcome=%+v", sample, want)
			}
			if l.ScalarStatus != "available" || l.NormalizedScalar == "" || l.TriggerObserved == nil || l.DecisionStatus != "evaluated" {
				t.Fatalf("scalar/decision absent: %+v", l)
			}
			if l.HistoryValid != 1 || l.HistoryRequired != 1 || l.TriggerRequired != 1 || l.TriggerWindow != 1 {
				t.Fatalf("history/window=%+v", l)
			}
		})
	}
}

func TestSeriesSampleExplainsNMAndMissingHistory(t *testing.T) {
	plan := compiledWindow(t, 5, 2)
	req := requestFixtureForPlan(t, plan, []contract.CanonicalRecordV2{{RecordID: strings.Repeat("b", 64), SourceTime: 400, DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)}, Values: map[string]json.RawMessage{"value": json.RawMessage(`80`)}, Dimensions: map[string]json.RawMessage{}}}, nil)
	_, sample := equivalentSample(t, req)
	l := sample.Levels[0]
	if l.TriggerRequired != 2 || l.TriggerWindow != 5 || l.HistoryValid != 1 || l.HistoryRequired != 5 || l.DecisionStatus != "not_evaluated" || l.TriggerObserved != nil || l.RecoveryObserved != nil {
		t.Fatalf("short history fabricated a decision: %+v", l)
	}
}

func TestSeriesSampleExplainsRecoveryGateAndTwoLevels(t *testing.T) {
	plan := compiledTwoLevelsShaped(t, "50", "50", func(p *contract.EvaluationPlanV2) {
		p.WireFormat = contract.WireFormatStandardRawEvent
		p.StrategyRef.SnapshotRevision = 7
		p.StrategyIR.StrategyRef.SnapshotRevision = 7
		p.OutputIdentity = &contract.MonitorOutputIdentity{DimensionFields: []string{"host"}}
	})
	history := []execution.StateHistoryPoint{{RecordID: strings.Repeat("a", 64), SourceTime: 40, Levels: []execution.StateLevelFact{{LevelID: 5, DetectFingerprint: plan.Levels()[0].Fingerprints().Detect, Result: execution.LevelFactAnomalous}, {LevelID: 6, DetectFingerprint: plan.Levels()[1].Fingerprints().Detect, Result: execution.LevelFactAnomalous}}}}
	req := requestFixtureTwoLevels(t, plan, json.RawMessage(`10`), history)
	req.OpenAlerts = openAlertSetStub{}
	_, sample := equivalentSample(t, req)
	if len(sample.Levels) != 2 || !sample.RecoveryHeld || sample.RecoveryCause != trigger.RecoveryHeldNoOpenAlert || sample.OpenAlertGate != trigger.OpenAlertGateHeldNoOpenAlert || sample.EventID != "" {
		t.Fatalf("gate=%+v", sample)
	}
	for _, l := range sample.Levels {
		if l.Outcome != "RECOVERY" || l.RecoveryObserved == nil || *l.RecoveryObserved != 1 || l.StateDisposition != trigger.StateAdvance {
			t.Fatalf("level=%+v", l)
		}
	}
}

func TestSharedQueryGroupSamplesOnlyRequestedPlanAndVersion(t *testing.T) {
	req := requestFixture(t, json.RawMessage(`80`), nil)
	baseline, err := newEvaluator(t).Evaluate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"tenant", "business", "strategy", "generation", "schedule", "series", "kind", "expired"} {
		t.Run(field, func(t *testing.T) {
			s, v := evaluatorSampler(t, req)
			switch field {
			case "tenant":
				v.TenantID = "sibling"
			case "business":
				v.BusinessID = "sibling"
			case "strategy":
				v.StrategyID = "sibling"
			case "generation":
				v.StateGeneration = "sibling"
			case "schedule":
				v.PlanScheduleRevision = "sibling"
			case "series":
				v.SeriesDigest = "sibling"
			case "kind":
				v.SeriesKind = "NO_DATA"
			case "expired":
				v.OpenedAt = time.Now().Add(-2 * time.Minute)
				v.ExpiresAt = time.Now().Add(-time.Minute)
			}
			if err := s.Select([]observability.SeriesSampleSelection{v}); err != nil {
				t.Fatal(err)
			}
			e := newEvaluator(t)
			e.SetSeriesSampler(s)
			got, err := e.Evaluate(context.Background(), req)
			if err != nil || !reflect.DeepEqual(got, baseline) {
				t.Fatalf("sibling selection changed result: %v", err)
			}
			if s.Health().Selected != 0 {
				t.Fatal("sibling identity admitted materialization")
			}
		})
	}
}

func TestSeriesSampleFullQueueDoesNotChangeEvaluator(t *testing.T) {
	req := requestFixture(t, json.RawMessage(`80`), nil)
	s, v := evaluatorSampler(t, req)
	e := newEvaluator(t)
	e.SetSeriesSampler(s)
	baseline, err := e.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		v.WindowID = "another-window" // first iteration replaces Slot guard; queue stays full
		if err := s.Select([]observability.SeriesSampleSelection{v}); err != nil {
			t.Fatal(err)
		}
		got, err := e.Evaluate(context.Background(), req)
		if err != nil || !reflect.DeepEqual(got, baseline) {
			t.Fatalf("full queue changed evaluation: %v", err)
		}
	}
	if s.Health().QueueDropped != 100 {
		t.Fatalf("full queue was not rejected before sample construction: %+v", s.Health())
	}
	readEvaluatorSample(t, s)
}

func TestSeriesSampleOnlyFirstRecordDoesNotChangeProvisionalFold(t *testing.T) {
	records := []contract.CanonicalRecordV2{
		{RecordID: strings.Repeat("a", 64), SourceTime: 400, DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)}, Values: map[string]json.RawMessage{"value": json.RawMessage(`80`)}, Dimensions: map[string]json.RawMessage{}},
		{RecordID: strings.Repeat("b", 64), SourceTime: 460, DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)}, Values: map[string]json.RawMessage{"value": json.RawMessage(`10`)}, Dimensions: map[string]json.RawMessage{}},
	}
	got, sample := equivalentSample(t, requestFixtureForPlan(t, compiledWindow(t, 2, 1), records, nil))
	if len(got.Plans[0].LevelOutcomes) != 2 || sample.RecordID != records[0].RecordID || sample.Levels[0].Outcome != string(got.Plans[0].LevelOutcomes[0].Outcome) {
		t.Fatalf("provisional sample=%+v", sample)
	}
}

func TestSeriesSampleUsesFinalGuardAndFreeze(t *testing.T) {
	plan := compiledG4Plan(t, strategy.DetectorKindSimpleRingRatio, map[string]any{"floor": 20, "ceil": nil}, strategy.AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}})
	record := g4Record(99, `80`, nil)
	// The ring ratio's dependency is supplied too, so the only reason on the
	// Level is the state's guard. Left empty, the dependency's QUERY_EMPTY
	// competes with the guard for the outcome's reason, and what this test
	// is about -- the sample copying the final guard rather than the
	// preliminary detection -- is no longer the only thing the fixture asks.
	previous := g4Record(39, `40`, nil)
	for _, completeness := range []execution.HistoryCompleteness{execution.HistoryWarming, execution.HistoryGapped} {
		req := requestFixtureForPlan(t, plan, []contract.CanonicalRecordV2{record}, nil)
		req.State.Items[0].Status = execution.StateFoundWarming
		if completeness == execution.HistoryGapped {
			req.State.Items[0].Status = execution.StateFoundGapped
		}
		req.State.Items[0].Levels[0].HistoryCompleteness = completeness
		req.State.Items[0].Levels[0].GapReasonCode = execution.ReasonCode(contract.ReasonSnapshotUnavailable)
		req.Inputs = []execution.SeriesEvaluationInputRequest{g4Input(t, req, map[string][]contract.CanonicalRecordV2{"primary": {record}, "previous": {previous}})}
		got, sample := equivalentSample(t, req)
		l := sample.Levels[0]
		// The guard holds the outcome UNKNOWN with its own reason while the
		// detection underneath read NORMAL, and the history window still
		// advances by the point it was given: the sample has to carry the
		// final guard and the final disposition, not the preliminary
		// detection. (A frozen state is the detection-unavailable path, which
		// is the empty dependency this fixture no longer supplies.)
		if l.Outcome != "UNKNOWN" || l.Reason != string(got.Plans[0].LevelOutcomes[0].ReasonCode) || l.Reason == l.DetectReason ||
			l.Reason != string(contract.ReasonSnapshotUnavailable) || l.DetectResult != "NORMAL" ||
			l.StateDisposition != trigger.StateAdvance || l.DecisionStatus != "not_evaluated" || l.TriggerObserved != nil || !l.HistoryForced {
			t.Fatalf("sample copied preliminary detection instead of final guard: %+v", l)
		}
	}
}

// A guarded series whose dependency came back empty this round: the outcome
// names the round's own reason, not the stored guard's. The marker the round
// proposes is the guard that will cover this outcome, and the sample copies
// that final reason. The stored guard stays in durable state untouched.
func TestSeriesSampleUnderAGuardNamesTheRoundsOwnReason(t *testing.T) {
	plan := compiledG4Plan(t, strategy.DetectorKindSimpleRingRatio, map[string]any{"floor": 20, "ceil": nil}, strategy.AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}})
	record := g4Record(99, `80`, nil)
	req := requestFixtureForPlan(t, plan, []contract.CanonicalRecordV2{record}, nil)
	req.State.Items[0].Status = execution.StateFoundWarming
	req.State.Items[0].Levels[0].HistoryCompleteness = execution.HistoryWarming
	req.State.Items[0].Levels[0].GapReasonCode = execution.ReasonCode(contract.ReasonSnapshotUnavailable)
	req.Inputs = []execution.SeriesEvaluationInputRequest{g4Input(t, req, map[string][]contract.CanonicalRecordV2{"primary": {record}, "previous": {}})}
	got, sample := equivalentSample(t, req)
	want := got.Plans[0].LevelOutcomes[0]
	l := sample.Levels[0]
	if want.Outcome != execution.LevelOutcomeUnknown || want.ReasonCode != execution.ReasonCode(contract.ReasonQueryEmpty) {
		t.Fatalf("outcome %s/%s, want UNKNOWN/QUERY_EMPTY: the round's empty dependency is the guard covering it", want.Outcome, want.ReasonCode)
	}
	if l.Outcome != "UNKNOWN" || l.Reason != string(contract.ReasonQueryEmpty) || l.Reason == string(contract.ReasonSnapshotUnavailable) {
		t.Fatalf("sample copied the stored guard instead of the round's own reason: %+v", l)
	}
	if len(got.Plans[0].StateResults) != 0 {
		t.Fatalf("a guarded series with an empty dependency wrote state: %+v", got.Plans[0].StateResults)
	}
}

// Compare the complete evaluator on identical inputs after a scarce diagnostic
// allocation is exhausted. Dimensions remain expensive for execution itself;
// the sampling guard must add no copies as their size or record count grows.
func BenchmarkSeriesSampleEvaluator(b *testing.B) {
	for _, count := range []int{1, 30} {
		for _, dimensionBytes := range []int{16, 4096} {
			b.Run(fmt.Sprintf("records_%d/dimension_%d", count, dimensionBytes), func(b *testing.B) {
				records := make([]contract.CanonicalRecordV2, count)
				for i := range records {
					records[i] = contract.CanonicalRecordV2{RecordID: fmt.Sprintf("%064d", i+1), SourceTime: int64(100 + i*60), DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)}, Values: map[string]json.RawMessage{"value": json.RawMessage(`80`)}, Dimensions: map[string]json.RawMessage{"host": json.RawMessage(`"` + strings.Repeat("x", dimensionBytes) + `"`)}}
				}
				req := requestFixtureForPlan(b, compiled(b), records, nil)
				for _, mode := range []string{"disabled", "sibling", "exhausted"} {
					b.Run(mode, func(b *testing.B) {
						e := newEvaluator(b)
						e.limits.MaxRecords = uint64(count)
						if mode != "disabled" {
							s, v := evaluatorSampler(b, req)
							if mode == "sibling" {
								v.StrategyID = "sibling"
								if err := s.Select([]observability.SeriesSampleSelection{v}); err != nil {
									b.Fatal(err)
								}
							} else {
								r := s.TryReserve(context.Background(), observability.SeriesSampleCandidate{QueryGroup: v.QueryGroup, TenantID: v.TenantID, BusinessID: v.BusinessID, StrategyID: v.StrategyID, StateGeneration: v.StateGeneration, PlanScheduleRevision: v.PlanScheduleRevision, SeriesDigest: v.SeriesDigest, Slot: 1})
								r.AddLevel(5)
								r.Commit()
							}
							e.SetSeriesSampler(s)
						}
						b.ReportAllocs()
						b.ResetTimer()
						for i := 0; i < b.N; i++ {
							if _, err := e.Evaluate(context.Background(), req); err != nil {
								b.Fatal(err)
							}
						}
					})
				}
			})
		}
	}
}
