// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package comparator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestJoinerAlignsThreeStreamsByInputID(t *testing.T) {
	t.Parallel()

	inputPayload, input := testTriggerInput(t, "anomalous")
	decisionPayload := testDecisionBatch(t, input, contract.DecisionOutcomeTrigger)
	key := mustPartitionKey(t, input)
	inputID := input.DetectionOutcomes[0].InputID
	joiner := mustJoiner(t, "run-1", 10)

	if _, err := joiner.ObserveDecisionBatch("run-1", DecisionSideGo, key, decisionPayload); err != nil {
		t.Fatalf("ObserveDecisionBatch(go) error = %v", err)
	}
	pythonPayload := rewriteBatchID(t, decisionPayload, "python-batch")
	if _, err := joiner.ObserveDecisionBatch("run-1", DecisionSidePython, key, pythonPayload); err != nil {
		t.Fatalf("ObserveDecisionBatch(python) error = %v", err)
	}
	before, ok, err := joiner.Assess("run-1", inputID, Gates{StableEpoch: true, CoverageComplete: true})
	if err != nil || !ok || before.Join != JoinPendingInput || before.Verdict != VerdictNone {
		t.Fatalf("Assess(before input) = %#v, %v, %v", before, ok, err)
	}

	if _, err := joiner.ObserveTriggerInput("run-1", key, inputPayload); err != nil {
		t.Fatalf("ObserveTriggerInput() error = %v", err)
	}
	after, ok, err := joiner.Assess("run-1", inputID, Gates{
		StableEpoch:          true,
		CoverageComplete:     true,
		EpochStartSourceTime: int64Pointer(input.DetectionOutcomes[0].Record.SourceTime - 299),
	})
	if err != nil || !ok {
		t.Fatalf("Assess(after input) ok=%v error=%v", ok, err)
	}
	if after.Join != JoinComplete || after.Eligibility != EligibilityEligible || after.Verdict != VerdictMatch {
		t.Fatalf("Assessment = %#v, want complete eligible match", after)
	}
}

func TestJoinerProducesSameAssessmentForEveryArrivalOrder(t *testing.T) {
	t.Parallel()

	inputPayload, input := testTriggerInput(t, "normal")
	decisionPayload := testDecisionBatch(t, input, contract.DecisionOutcomeNoTrigger)
	key := mustPartitionKey(t, input)
	orders := [][]string{
		{"input", "go", "python"},
		{"input", "python", "go"},
		{"go", "input", "python"},
		{"go", "python", "input"},
		{"python", "input", "go"},
		{"python", "go", "input"},
	}
	for _, order := range orders {
		order := order
		t.Run(strings.Join(order, "-"), func(t *testing.T) {
			t.Parallel()
			joiner := mustJoiner(t, "run-1", 10)
			for _, stream := range order {
				switch stream {
				case "input":
					_, _ = joiner.ObserveTriggerInput("run-1", key, inputPayload)
				case "go":
					_, _ = joiner.ObserveDecisionBatch("run-1", DecisionSideGo, key, decisionPayload)
				case "python":
					_, _ = joiner.ObserveDecisionBatch("run-1", DecisionSidePython, key, decisionPayload)
				}
			}
			assessment, ok, err := joiner.Assess(
				"run-1",
				input.DetectionOutcomes[0].InputID,
				Gates{StableEpoch: true, CoverageComplete: true},
			)
			if err != nil || !ok || assessment.Join != JoinComplete || assessment.Verdict != VerdictMatch {
				t.Fatalf("Assessment = %#v, ok=%v, error=%v", assessment, ok, err)
			}
		})
	}
}

func TestJoinerNeverMatchesEqualDecisionsThatContradictSource(t *testing.T) {
	t.Parallel()

	inputPayload, input := testTriggerInput(t, "normal")
	wrong := testDecisionBatch(t, input, contract.DecisionOutcomeTrigger)
	key := mustPartitionKey(t, input)
	joiner := mustJoiner(t, "run-1", 10)
	if _, err := joiner.ObserveDecisionBatch("run-1", DecisionSideGo, key, wrong); err != nil {
		t.Fatalf("ObserveDecisionBatch(go) error = %v", err)
	}
	if _, err := joiner.ObserveDecisionBatch("run-1", DecisionSidePython, key, wrong); err != nil {
		t.Fatalf("ObserveDecisionBatch(python) error = %v", err)
	}
	if _, err := joiner.ObserveTriggerInput("run-1", key, inputPayload); err != nil {
		t.Fatalf("ObserveTriggerInput() error = %v", err)
	}
	assessment, ok, err := joiner.Assess("run-1", input.DetectionOutcomes[0].InputID, Gates{StableEpoch: true, CoverageComplete: true})
	if err != nil || !ok {
		t.Fatalf("Assess() ok=%v error=%v", ok, err)
	}
	if assessment.Join != JoinInvalid || !assessment.GoInvalid || !assessment.PythonInvalid || assessment.Verdict != VerdictNone {
		t.Fatalf("Assessment = %#v, want sticky invalid without verdict", assessment)
	}
}

func TestJoinerSeparatesWarmupFromHardDiff(t *testing.T) {
	t.Parallel()

	inputPayload, input := testTriggerInput(t, "anomalous")
	key := mustPartitionKey(t, input)
	goPayload := testDecisionBatch(t, input, contract.DecisionOutcomeTrigger)
	pythonPayload := testDecisionBatch(t, input, contract.DecisionOutcomeNoTrigger)
	joiner := mustJoiner(t, "run-1", 10)
	_, _ = joiner.ObserveTriggerInput("run-1", key, inputPayload)
	_, _ = joiner.ObserveDecisionBatch("run-1", DecisionSideGo, key, goPayload)
	_, _ = joiner.ObserveDecisionBatch("run-1", DecisionSidePython, key, pythonPayload)

	inputID := input.DetectionOutcomes[0].InputID
	sourceTime := input.DetectionOutcomes[0].Record.SourceTime
	unstable, _, err := joiner.Assess("run-1", inputID, Gates{CoverageComplete: true})
	if err != nil || unstable.Eligibility != EligibilityEpochUnstable || unstable.Verdict != VerdictNone {
		t.Fatalf("unstable Assessment = %#v, error=%v", unstable, err)
	}
	coverageGap, _, err := joiner.Assess("run-1", inputID, Gates{StableEpoch: true})
	if err != nil || coverageGap.Eligibility != EligibilityCoverageGap || coverageGap.Verdict != VerdictNone {
		t.Fatalf("coverage-gap Assessment = %#v, error=%v", coverageGap, err)
	}
	if _, _, err := joiner.Assess("run-1", inputID, Gates{StableEpoch: true, CoverageComplete: true}); err == nil {
		t.Fatal("Assess() accepted anomalous input without an explicit epoch start")
	}
	warming, _, err := joiner.Assess("run-1", inputID, Gates{
		StableEpoch:          true,
		CoverageComplete:     true,
		EpochStartSourceTime: int64Pointer(sourceTime - 298),
	})
	if err != nil || warming.Eligibility != EligibilityWarmup || warming.Verdict != VerdictNone {
		t.Fatalf("warming Assessment = %#v, error=%v", warming, err)
	}
	eligible, _, err := joiner.Assess("run-1", inputID, Gates{
		StableEpoch:          true,
		CoverageComplete:     true,
		EpochStartSourceTime: int64Pointer(sourceTime - 299),
	})
	if err != nil || eligible.Eligibility != EligibilityEligible || eligible.Verdict != VerdictHardDiff {
		t.Fatalf("eligible Assessment = %#v, error=%v", eligible, err)
	}
	for _, epochStart := range []int64{-1, math.MaxInt64} {
		if _, _, err := joiner.Assess("run-1", inputID, Gates{
			StableEpoch:          true,
			CoverageComplete:     true,
			EpochStartSourceTime: int64Pointer(epochStart),
		}); err == nil {
			t.Fatalf("Assess() accepted unsafe epoch start %d", epochStart)
		}
	}
}

func TestJoinerReplayConflictCapacityAndEpochAreFailClosed(t *testing.T) {
	t.Parallel()

	inputPayload, input := testTriggerInput(t, "anomalous")
	key := mustPartitionKey(t, input)
	triggerPayload := testDecisionBatch(t, input, contract.DecisionOutcomeTrigger)
	noTriggerPayload := testDecisionBatch(t, input, contract.DecisionOutcomeNoTrigger)
	joiner := mustJoiner(t, "run-1", 1)

	updates, err := joiner.ObserveDecisionBatch("run-1", DecisionSideGo, key, triggerPayload)
	if err != nil || len(updates) != 1 || updates[0].Disposition != DispositionAccepted {
		t.Fatalf("first observation = %#v, error=%v", updates, err)
	}
	updates, err = joiner.ObserveDecisionBatch("run-1", DecisionSideGo, key, rewriteBatchID(t, triggerPayload, "replay-batch"))
	if err != nil || updates[0].Disposition != DispositionReplay {
		t.Fatalf("replay observation = %#v, error=%v", updates, err)
	}
	updates, err = joiner.ObserveDecisionBatch("run-1", DecisionSideGo, key, noTriggerPayload)
	if err != nil || updates[0].Disposition != DispositionConflict {
		t.Fatalf("conflict observation = %#v, error=%v", updates, err)
	}
	if _, err := joiner.ObserveTriggerInput("wrong-run", key, inputPayload); err == nil {
		t.Fatal("ObserveTriggerInput() accepted a different run epoch")
	}
	assessment, ok, _ := joiner.Assess("run-1", input.DetectionOutcomes[0].InputID, Gates{})
	if !ok || assessment.Join != JoinConflict || !assessment.GoConflict {
		t.Fatalf("Assessment = %#v, want sticky Go conflict", assessment)
	}

	_, second := testTriggerInput(t, "error-partial")
	updates, err = joiner.ObserveDecisionBatch(
		"run-1", DecisionSidePython, mustPartitionKey(t, second), testDecisionBatch(t, second, contract.OutcomeError),
	)
	if err != nil || len(updates) != 1 || updates[0].Disposition != DispositionCapacityDropped {
		t.Fatalf("capacity observation = %#v, error=%v, want one explicit drop", updates, err)
	}
	nextRun := mustJoiner(t, "run-2", 1)
	if _, err := nextRun.ObserveTriggerInput("run-1", key, inputPayload); err == nil {
		t.Fatal("new run accepted a delayed observation from the old epoch")
	}
	if _, ok, err := nextRun.Assess("run-2", input.DetectionOutcomes[0].InputID, Gates{}); err != nil || ok {
		t.Fatalf("Assess(new run) ok=%v error=%v", ok, err)
	}
}

func TestJoinerMarksConflictsSymmetricallyForBothDecisionSides(t *testing.T) {
	t.Parallel()

	_, input := testTriggerInput(t, "anomalous")
	key := mustPartitionKey(t, input)
	triggerPayload := testDecisionBatch(t, input, contract.DecisionOutcomeTrigger)
	noTriggerPayload := testDecisionBatch(t, input, contract.DecisionOutcomeNoTrigger)
	for _, side := range []DecisionSide{DecisionSideGo, DecisionSidePython} {
		side := side
		t.Run(fmt.Sprintf("side-%d", side), func(t *testing.T) {
			t.Parallel()
			joiner := mustJoiner(t, "run-1", 10)
			_, _ = joiner.ObserveDecisionBatch("run-1", side, key, triggerPayload)
			updates, err := joiner.ObserveDecisionBatch("run-1", side, key, noTriggerPayload)
			if err != nil || updates[0].Disposition != DispositionConflict {
				t.Fatalf("conflict observation = %#v, error=%v", updates, err)
			}
			assessment, ok, err := joiner.Assess("run-1", input.DetectionOutcomes[0].InputID, Gates{})
			if err != nil || !ok || assessment.Join != JoinConflict {
				t.Fatalf("Assessment = %#v, ok=%v, error=%v", assessment, ok, err)
			}
			if side == DecisionSideGo && !assessment.GoConflict {
				t.Fatal("Go conflict was not retained")
			}
			if side == DecisionSidePython && !assessment.PythonConflict {
				t.Fatal("Python conflict was not retained")
			}
		})
	}
}

func TestJoinerDropsOnlyNewEntriesBeyondCapacity(t *testing.T) {
	t.Parallel()

	payload, input := testTriggerInputMany(t, "anomalous", "normal")
	joiner := mustJoiner(t, "run-1", 1)
	updates, err := joiner.ObserveTriggerInput("run-1", mustPartitionKey(t, input), payload)
	if err != nil {
		t.Fatalf("ObserveTriggerInput() error = %v", err)
	}
	if len(updates) != 2 || updates[0].Disposition != DispositionAccepted || updates[1].Disposition != DispositionCapacityDropped {
		t.Fatalf("updates = %#v, want accepted then capacity dropped", updates)
	}
	if _, ok, err := joiner.Assess("run-1", input.DetectionOutcomes[0].InputID, Gates{}); err != nil || !ok {
		t.Fatalf("Assess(admitted) ok=%v error=%v", ok, err)
	}
	if _, ok, err := joiner.Assess("run-1", input.DetectionOutcomes[1].InputID, Gates{}); err != nil || ok {
		t.Fatalf("Assess(dropped) ok=%v error=%v, want no retained entry", ok, err)
	}
}

func TestJoinerAlignsDifferentDecisionBatchShapes(t *testing.T) {
	t.Parallel()

	inputPayload, input := testTriggerInputMany(t, "anomalous", "normal")
	key := mustPartitionKey(t, input)
	fullBatch := testFullDecisionBatch(t, input)
	fullPayload, err := contract.EncodeTriggerDecisionBatch(fullBatch)
	if err != nil {
		t.Fatalf("EncodeTriggerDecisionBatch(full) error = %v", err)
	}
	joiner := mustJoiner(t, "run-1", 10)
	_, _ = joiner.ObserveTriggerInput("run-1", key, inputPayload)
	if _, err := joiner.ObserveDecisionBatch("run-1", DecisionSideGo, key, fullPayload); err != nil {
		t.Fatalf("ObserveDecisionBatch(go full) error = %v", err)
	}
	for index, decision := range fullBatch.Decisions {
		partial := *fullBatch
		partial.BatchID = fmt.Sprintf("python-batch-%d", index)
		partial.Decisions = []contract.TriggerDecision{decision}
		payload, err := contract.EncodeTriggerDecisionBatch(&partial)
		if err != nil {
			t.Fatalf("EncodeTriggerDecisionBatch(partial) error = %v", err)
		}
		if _, err := joiner.ObserveDecisionBatch("run-1", DecisionSidePython, key, payload); err != nil {
			t.Fatalf("ObserveDecisionBatch(python partial) error = %v", err)
		}
	}
	for _, outcome := range input.DetectionOutcomes {
		gates := Gates{StableEpoch: true, CoverageComplete: true}
		if outcome.Outcome == contract.OutcomeAnomalous {
			gates.EpochStartSourceTime = int64Pointer(outcome.Record.SourceTime - 299)
		}
		assessment, ok, err := joiner.Assess("run-1", outcome.InputID, gates)
		if err != nil || !ok || assessment.Join != JoinComplete || assessment.Verdict != VerdictMatch {
			t.Fatalf("Assessment(%s) = %#v, ok=%v, error=%v", outcome.InputID, assessment, ok, err)
		}
	}
}

func TestJoinerSourceReplayIgnoresBatchIDAndConflictsOnExecutionIdentity(t *testing.T) {
	t.Parallel()

	inputPayload, input := testTriggerInput(t, "anomalous")
	key := mustPartitionKey(t, input)
	joiner := mustJoiner(t, "run-1", 10)
	updates, err := joiner.ObserveTriggerInput("run-1", key, inputPayload)
	if err != nil || updates[0].Disposition != DispositionAccepted {
		t.Fatalf("first source = %#v, error=%v", updates, err)
	}
	pending, ok, err := joiner.Assess("run-1", input.DetectionOutcomes[0].InputID, Gates{})
	if err != nil || !ok || pending.Join != JoinPendingBoth {
		t.Fatalf("source-only Assessment = %#v, ok=%v, error=%v", pending, ok, err)
	}
	updates, err = joiner.ObserveTriggerInput("run-1", key, rewriteInputIdentity(t, inputPayload, "retry-batch", ""))
	if err != nil || updates[0].Disposition != DispositionReplay {
		t.Fatalf("source replay = %#v, error=%v", updates, err)
	}
	updates, err = joiner.ObserveTriggerInput("run-1", key, rewriteInputIdentity(t, inputPayload, "retry-batch", "next-generation"))
	if err != nil || updates[0].Disposition != DispositionConflict {
		t.Fatalf("source conflict = %#v, error=%v", updates, err)
	}
	assessment, ok, err := joiner.Assess("run-1", input.DetectionOutcomes[0].InputID, Gates{})
	if err != nil || !ok || assessment.Join != JoinConflict || !assessment.SourceConflict {
		t.Fatalf("Assessment = %#v, ok=%v, error=%v", assessment, ok, err)
	}
}

func TestJoinerClassifiesNonBusinessSourcesWithoutVerdict(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fixture     string
		outcome     string
		eligibility Eligibility
	}{
		{fixture: "error-partial", outcome: contract.OutcomeError, eligibility: EligibilitySourceError},
		{fixture: "unsupported-empty", outcome: contract.OutcomeUnsupported, eligibility: EligibilityUnsupported},
	}
	for _, test := range tests {
		test := test
		t.Run(test.fixture, func(t *testing.T) {
			t.Parallel()
			inputPayload, input := testTriggerInput(t, test.fixture)
			key := mustPartitionKey(t, input)
			decisionPayload := testDecisionBatch(t, input, test.outcome)
			joiner := mustJoiner(t, "run-1", 10)
			_, _ = joiner.ObserveTriggerInput("run-1", key, inputPayload)
			_, _ = joiner.ObserveDecisionBatch("run-1", DecisionSideGo, key, decisionPayload)
			_, _ = joiner.ObserveDecisionBatch("run-1", DecisionSidePython, key, decisionPayload)
			assessment, _, err := joiner.Assess("run-1", input.DetectionOutcomes[0].InputID, Gates{StableEpoch: true, CoverageComplete: true})
			if err != nil || assessment.Join != JoinComplete || assessment.Eligibility != test.eligibility || assessment.Verdict != VerdictNone {
				t.Fatalf("Assessment = %#v, error=%v", assessment, err)
			}
		})
	}
}

func TestJoinerIsSafeForConcurrentStreamObservation(t *testing.T) {
	t.Parallel()

	inputPayload, input := testTriggerInput(t, "normal")
	decisionPayload := testDecisionBatch(t, input, contract.DecisionOutcomeNoTrigger)
	key := mustPartitionKey(t, input)
	joiner := mustJoiner(t, "run-1", 10)
	var wait sync.WaitGroup
	errors := make(chan error, 60)
	for index := 0; index < 20; index++ {
		wait.Add(3)
		go func() {
			defer wait.Done()
			_, err := joiner.ObserveTriggerInput("run-1", key, inputPayload)
			errors <- err
		}()
		go func() {
			defer wait.Done()
			_, err := joiner.ObserveDecisionBatch("run-1", DecisionSideGo, key, decisionPayload)
			errors <- err
		}()
		go func() {
			defer wait.Done()
			_, err := joiner.ObserveDecisionBatch("run-1", DecisionSidePython, key, decisionPayload)
			errors <- err
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent Observe error = %v", err)
		}
	}
	assessment, ok, err := joiner.Assess("run-1", input.DetectionOutcomes[0].InputID, Gates{StableEpoch: true, CoverageComplete: true})
	if err != nil || !ok || assessment.Verdict != VerdictMatch || assessment.Join != JoinComplete {
		t.Fatalf("Assessment = %#v, ok=%v, error=%v", assessment, ok, err)
	}
}

type goldenFixture struct {
	Name       string          `json:"name"`
	StrategyIR json.RawMessage `json:"strategy_ir"`
	Outcome    json.RawMessage `json:"outcome"`
}

func testTriggerInput(t *testing.T, name string) ([]byte, *contract.TriggerInput) {
	return testTriggerInputMany(t, name)
}

func testTriggerInputMany(t *testing.T, names ...string) ([]byte, *contract.TriggerInput) {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("..", "contract", "testdata", "python-v1", "detection_outcome_v1.json"))
	if err != nil {
		t.Fatalf("ReadFile(golden) error = %v", err)
	}
	var document struct {
		Fixtures []goldenFixture `json:"fixtures"`
	}
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatalf("json.Unmarshal(golden) error = %v", err)
	}
	selected := make([]goldenFixture, 0, len(names))
	for _, name := range names {
		found := false
		for _, fixture := range document.Fixtures {
			if fixture.Name == name {
				selected = append(selected, fixture)
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("fixture %q not found", name)
		}
	}
	outcomes := make([]json.RawMessage, 0, len(selected))
	for _, fixture := range selected {
		outcomes = append(outcomes, fixture.Outcome)
	}
	inputPayload, err := json.Marshal(map[string]any{
		"schema":                 map[string]any{"name": "trigger-input", "major": 1, "minor": 0},
		"required_features":      []string{},
		"partition_hash_version": contract.PartitionHashVersionV1,
		"strategy_ir":            selected[0].StrategyIR,
		"detection_outcomes":     outcomes,
	})
	if err != nil {
		t.Fatalf("json.Marshal(input) error = %v", err)
	}
	input, err := contract.DecodeTriggerInput(inputPayload)
	if err != nil {
		t.Fatalf("DecodeTriggerInput() error = %v", err)
	}
	return inputPayload, input
}

func testFullDecisionBatch(t *testing.T, input *contract.TriggerInput) *contract.TriggerDecisionBatch {
	t.Helper()
	decisions := make([]contract.TriggerDecision, 0, len(input.DetectionOutcomes))
	for _, source := range input.DetectionOutcomes {
		decisionID, err := contract.DeriveTriggerDecisionID(source.InputID)
		if err != nil {
			t.Fatalf("DeriveTriggerDecisionID() error = %v", err)
		}
		decision := contract.TriggerDecision{
			DecisionID:        decisionID,
			InputID:           source.InputID,
			RecordID:          source.Record.RecordID,
			Outcome:           contract.DecisionOutcomeNoTrigger,
			ReasonCode:        contract.DecisionReasonInputNormal,
			AnomalyTimestamps: []int64{},
		}
		if source.Outcome == contract.OutcomeAnomalous {
			decision.Outcome = contract.DecisionOutcomeTrigger
			decision.ReasonCode = contract.DecisionReasonTriggerConditionMet
			decision.Level = intPointer(3)
			decision.AnomalyTimestamps = []int64{source.Record.SourceTime}
		}
		decisions = append(decisions, decision)
	}
	batch, err := input.BuildTriggerDecisionBatch(decisions)
	if err != nil {
		t.Fatalf("BuildTriggerDecisionBatch() error = %v", err)
	}
	return batch
}

func testDecisionBatch(t *testing.T, input *contract.TriggerInput, outcome string) []byte {
	t.Helper()
	source := input.DetectionOutcomes[0]
	decisionID, err := contract.DeriveTriggerDecisionID(source.InputID)
	if err != nil {
		t.Fatalf("DeriveTriggerDecisionID() error = %v", err)
	}
	decision := contract.TriggerDecision{
		DecisionID:        decisionID,
		InputID:           source.InputID,
		RecordID:          source.Record.RecordID,
		Outcome:           outcome,
		AnomalyTimestamps: []int64{},
	}
	switch outcome {
	case contract.DecisionOutcomeTrigger:
		decision.ReasonCode = contract.DecisionReasonTriggerConditionMet
		decision.Level = intPointer(3)
		decision.AnomalyTimestamps = []int64{source.Record.SourceTime}
	case contract.DecisionOutcomeNoTrigger:
		if source.Outcome == contract.OutcomeNormal {
			decision.ReasonCode = contract.DecisionReasonInputNormal
		} else {
			decision.ReasonCode = contract.DecisionReasonTriggerConditionNotMet
		}
	case contract.OutcomeError, contract.OutcomeUnsupported:
		if err := json.Unmarshal(source.ErrorCode, &decision.ReasonCode); err != nil {
			t.Fatalf("json.Unmarshal(error_code) error = %v", err)
		}
	default:
		t.Fatalf("unsupported decision outcome %q", outcome)
	}
	batch := &contract.TriggerDecisionBatch{
		Schema:               contract.Schema{Name: "trigger-decision-batch", Major: 1, Minor: 0},
		RequiredFeatures:     []string{},
		PartitionHashVersion: input.PartitionHashVersion,
		BatchID:              "decision-batch",
		TenantID:             input.StrategyIR.TenantID,
		Purpose:              input.StrategyIR.Purpose,
		StrategyRef:          input.StrategyIR.StrategyRef,
		DecisionAlgorithm:    contract.DecisionAlgorithmV1,
		Decisions:            []contract.TriggerDecision{decision},
	}
	payload, err := contract.EncodeTriggerDecisionBatch(batch)
	if err != nil {
		t.Fatalf("EncodeTriggerDecisionBatch() error = %v", err)
	}
	return payload
}

func rewriteBatchID(t *testing.T, payload []byte, batchID string) []byte {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatalf("json.Unmarshal(batch) error = %v", err)
	}
	document["batch_id"] = batchID
	rewritten, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("json.Marshal(batch) error = %v", err)
	}
	return rewritten
}

func rewriteInputIdentity(t *testing.T, payload []byte, batchID, generation string) []byte {
	t.Helper()
	var document map[string]any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("json.Unmarshal(input) error = %v", err)
	}
	outcomes := document["detection_outcomes"].([]any)
	for _, rawOutcome := range outcomes {
		outcome := rawOutcome.(map[string]any)
		outcome["batch_id"] = batchID
		if generation != "" {
			outcome["strategy_ref"].(map[string]any)["generation"] = generation
		}
	}
	if generation != "" {
		document["strategy_ir"].(map[string]any)["strategy_ref"].(map[string]any)["generation"] = generation
	}
	rewritten, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("json.Marshal(input) error = %v", err)
	}
	return rewritten
}

func mustPartitionKey(t *testing.T, input *contract.TriggerInput) []byte {
	t.Helper()
	key, err := input.PartitionKey()
	if err != nil {
		t.Fatalf("PartitionKey() error = %v", err)
	}
	return key
}

func mustJoiner(t *testing.T, epoch string, capacity int) *Joiner {
	t.Helper()
	joiner, err := NewJoiner(epoch, capacity)
	if err != nil {
		t.Fatalf("NewJoiner() error = %v", err)
	}
	return joiner
}

func intPointer(value int) *int { return &value }

func int64Pointer(value int64) *int64 { return &value }
