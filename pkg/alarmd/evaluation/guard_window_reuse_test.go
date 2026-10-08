// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func reuseHistoryPoint(plan *strategy.CompiledPlan, id string, sourceTime int64, result execution.LevelFactResult) execution.StateHistoryPoint {
	return execution.StateHistoryPoint{RecordID: strings.Repeat(id, 64), SourceTime: sourceTime,
		Levels: []execution.StateLevelFact{{LevelID: 5, DetectFingerprint: plan.Levels().At(0).Fingerprints().Detect, Result: result}}}
}

func reuseRecord(id string, sourceTime int64, value string) contract.CanonicalRecordV2 {
	return contract.CanonicalRecordV2{RecordID: strings.Repeat(id, 64), SourceTime: sourceTime, BusinessID: "2",
		DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)},
		Values:            map[string]json.RawMessage{"value": json.RawMessage(value)},
		Dimensions:        map[string]json.RawMessage{}, ReceivedTime: sourceTime}
}

// The requests the first record's window is read under: a plain loaded
// history, a late point, a guard that converges and one that does not, a Plan
// gap marker, loads that hold the whole Plan, a cold series, and a Slot of
// several records, whose later records build their own window.
func guardWindowReuseCases(t *testing.T) map[string]execution.EvaluationRequest {
	t.Helper()
	cases := map[string]execution.EvaluationRequest{}

	plain := compiledWindow(t, 3, 2)
	cases["plain loaded history"] = requestFixtureForPlan(t, plain, []contract.CanonicalRecordV2{reuseRecord("f", 300, `80`)},
		[]execution.StateHistoryPoint{reuseHistoryPoint(plain, "d", 180, execution.LevelFactAnomalous), reuseHistoryPoint(plain, "e", 240, execution.LevelFactNormal)})

	late := compiledWindow(t, 3, 2)
	cases["late point"] = requestFixtureForPlan(t, late, []contract.CanonicalRecordV2{reuseRecord("f", 240, `80`)},
		[]execution.StateHistoryPoint{reuseHistoryPoint(late, "d", 180, execution.LevelFactAnomalous), reuseHistoryPoint(late, "e", 300, execution.LevelFactNormal)})

	for name, history := range map[string][]int64{"converging guard": {180, 240}, "guard over a hole": {120, 240}} {
		plan := compiledWindow(t, 2, 2)
		points := make([]execution.StateHistoryPoint, 0, len(history))
		for index, sourceTime := range history {
			points = append(points, reuseHistoryPoint(plan, string(rune('d'+index)), sourceTime, execution.LevelFactNormal))
		}
		request := requestFixtureForPlan(t, plan, []contract.CanonicalRecordV2{reuseRecord("f", 300, `10`)}, points)
		request.State.Items[0].Status = execution.StateFoundGapped
		request.State.Items[0].Levels[0].HistoryCompleteness = execution.HistoryGapped
		request.State.Items[0].Levels[0].GapReasonCode = execution.ReasonCode(contract.ReasonGapSkipped)
		request.State.Items[0].Levels[0].LastProcessedEventTime = history[len(history)-1]
		cases[name] = request
	}

	gapPlan := compiledWindow(t, 3, 2)
	gapped := requestFixtureForPlan(t, gapPlan, []contract.CanonicalRecordV2{reuseRecord("f", 300, `10`)},
		[]execution.StateHistoryPoint{reuseHistoryPoint(gapPlan, "d", 240, execution.LevelFactNormal)})
	cases["plan gap marker"] = planGapMarkerFixtureOn(t, gapped,
		execution.GapScope{}, contract.ReasonGapSkipped, 2, 1, "")

	for name, marker := range map[string]struct {
		status execution.GapLoadStatus
		reason string
	}{"gap marker unreadable": {execution.GapUnavailable, contract.ReasonRedisUnavailable}, "gap marker terminal": {execution.GapTerminal, contract.ReasonStateCorrupt}} {
		plan := compiledWindow(t, 3, 2)
		request := requestFixtureForPlan(t, plan, []contract.CanonicalRecordV2{reuseRecord("f", 300, `80`)},
			[]execution.StateHistoryPoint{reuseHistoryPoint(plan, "d", 240, execution.LevelFactNormal)})
		due := request.Header.DuePlans[0]
		request.Gaps.Items[0] = execution.GapGuardSnapshot{
			Identity: execution.PlanGapIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration},
			Status:   marker.status, ReasonCode: execution.ReasonCode(marker.reason),
		}
		cases[name] = request
	}

	cold := requestFixtureForPlan(t, compiledWindow(t, 2, 2), []contract.CanonicalRecordV2{reuseRecord("f", 300, `80`)}, nil)
	cold.State.Items[0].Status = execution.StateMissingWarming
	cold.State.Items[0].BlobRevision = 0
	cold.State.Items[0].Levels = nil
	cases["cold series"] = cold

	batch := compiledWindow(t, 2, 2)
	cases["several records"] = requestFixtureForPlan(t, batch,
		[]contract.CanonicalRecordV2{reuseRecord("7", 300, `80`), reuseRecord("8", 360, `10`), reuseRecord("9", 420, `80`)},
		[]execution.StateHistoryPoint{reuseHistoryPoint(batch, "d", 180, execution.LevelFactNormal), reuseHistoryPoint(batch, "e", 240, execution.LevelFactAnomalous)})
	trimmed := compiledWindow(t, 2, 2)
	long := make([]execution.StateHistoryPoint, 0, 8)
	for index := 0; index < 8; index++ {
		result := execution.LevelFactNormal
		if index%3 == 0 {
			result = execution.LevelFactAnomalous
		}
		point := reuseHistoryPoint(trimmed, "0", int64(60+index*60), result)
		point.RecordID = fmt.Sprintf("%064x", index+1)
		long = append(long, point)
	}
	cases["several records past the retention"] = requestFixtureForPlan(t, trimmed,
		[]contract.CanonicalRecordV2{reuseRecord("7", 540, `80`), reuseRecord("8", 600, `10`), reuseRecord("9", 660, `80`)}, long)
	return cases
}

func evaluateBothWays(t *testing.T, request execution.EvaluationRequest) (reused, rebuilt execution.EvaluationResult, reusedErr, rebuiltErr error) {
	t.Helper()
	reusing := newEvaluator(t)
	rebuilding := newEvaluator(t)
	rebuilding.rebuildGuardWindow = true
	reused, reusedErr = reusing.Evaluate(context.Background(), request)
	rebuilt, rebuiltErr = rebuilding.Evaluate(context.Background(), request)
	return reused, rebuilt, reusedErr, rebuiltErr
}

// The first record takes the window the guard built from the same loaded view
// instead of building it again. Whatever the request, the evaluation is the
// one the second build gave: every outcome, State mutation, event and gap
// mutation, and the result still validates against its request.
func TestTheFirstRecordReadsTheGuardsWindowAsItReadItsOwn(t *testing.T) {
	for name, request := range guardWindowReuseCases(t) {
		t.Run(name, func(t *testing.T) {
			reused, rebuilt, reusedErr, rebuiltErr := evaluateBothWays(t, request)
			if reusedErr != nil || rebuiltErr != nil {
				t.Fatalf("Evaluate() error: reused %v, rebuilt %v", reusedErr, rebuiltErr)
			}
			if !reflect.DeepEqual(reused, rebuilt) {
				t.Fatalf("the reused window evaluated differently:\nreused  %+v\nrebuilt %+v", reused.Plans, rebuilt.Plans)
			}
			if err := reused.Validate(request); err != nil {
				t.Fatalf("result did not validate: %v", err)
			}
		})
	}
}

// A loaded history the window refuses fails the Plan with the same error
// either way, and on every path: the guard builds the window before anything
// else reads it, so taking its window moves no error, the paths where the
// loads hold the whole Plan and no record is evaluated included.
func TestALoadedHistoryTheWindowRefusesFailsTheSameWayOnEveryPath(t *testing.T) {
	for name, request := range guardWindowReuseCases(t) {
		if len(request.State.Items[0].History) == 0 {
			continue
		}
		t.Run(name, func(t *testing.T) {
			request.State.Items[0].History = append([]execution.StateHistoryPoint(nil), request.State.Items[0].History...)
			request.State.Items[0].History[0].Levels = []execution.StateLevelFact{{LevelID: 5,
				DetectFingerprint: strings.Repeat("0", 64), Result: execution.LevelFactNormal}}
			_, _, reusedErr, rebuiltErr := evaluateBothWays(t, request)
			if rebuiltErr == nil || !strings.Contains(rebuiltErr.Error(), "detect fingerprint mismatch") {
				t.Fatalf("setup: the rebuilt path gave %v, want the window's own refusal", rebuiltErr)
			}
			if reusedErr == nil || reusedErr.Error() != rebuiltErr.Error() {
				t.Fatalf("error = %v, want the rebuilt path's %v", reusedErr, rebuiltErr)
			}
		})
	}
}

// What taking the window saves: one build of it, per series per round. The
// evaluation allocates less than the rebuilding one by at least nine tenths of
// what building that window alone allocates.
func TestTheFirstRecordDoesNotBuildTheWindowAgain(t *testing.T) {
	if raceEnabled {
		// Plain runs read the same counts every time (reused 1176, rebuilt
		// 1275: 99 saved against 87 wanted). Under the race detector a pooled
		// buffer is dropped at random and grown again, the saving swings by
		// about a dozen either way, and 3 runs in 60 read it below the bar.
		t.Skip("allocation counts are not stable through pooled buffers under the race detector")
	}
	const points = 16
	plan := compiledWindow(t, points, 2)
	history := make([]execution.StateHistoryPoint, 0, points)
	for index := 0; index < points; index++ {
		point := reuseHistoryPoint(plan, "0", int64(60+index*60), execution.LevelFactNormal)
		point.RecordID = fmt.Sprintf("%064x", index+1)
		history = append(history, point)
	}
	request := requestFixtureForPlan(t, plan, []contract.CanonicalRecordV2{reuseRecord("f", 60+points*60, `10`)}, history)
	requirements, err := planLevelRequirements(plan)
	if err != nil {
		t.Fatal(err)
	}
	oneBuild := testing.AllocsPerRun(20, func() {
		window, err := state.NewWindow(requirements)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := window.Apply(toStatePoints(history)); err != nil {
			t.Fatal(err)
		}
	})
	reusing := newEvaluator(t)
	rebuilding := newEvaluator(t)
	rebuilding.rebuildGuardWindow = true
	measure := func(evaluator *Evaluator) float64 {
		return testing.AllocsPerRun(20, func() {
			if _, err := evaluator.Evaluate(context.Background(), request); err != nil {
				t.Fatal(err)
			}
		})
	}
	reused, rebuilt := measure(reusing), measure(rebuilding)
	if oneBuild < 1 || rebuilt-reused < 0.9*oneBuild {
		t.Fatalf("allocations per evaluation: reused %.0f, rebuilt %.0f, one window build %.0f; want the reuse to save that build",
			reused, rebuilt, oneBuild)
	}
}
