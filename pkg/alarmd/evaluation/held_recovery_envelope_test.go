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
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// withEmptyDependency binds one more input to a Level: a dependency that came
// back complete and empty, which makes the Level's inputs this round
// incomplete for a recovery.
func withEmptyDependency(request *execution.EvaluationRequest, levelIndex int) {
	input := &request.Inputs[levelIndex]
	dependency := input.Inputs[0]
	empty := execution.NewDataset(nil)
	view, _ := execution.NewDatasetView(empty, nil)
	dependency.RequirementID, dependency.DatasetName = "dependency", "dependency"
	dependency.Role = execution.InputRoleAlgorithmDependency
	dependency.Dataset, dependency.View, dependency.DataState = empty, view, execution.DataStateEmpty
	input.Inputs = append(input.Inputs, dependency)
	input.RequirementIDs = append(input.RequirementIDs, "dependency")
}

// A recovery reached on a round whose own inputs for the Level were
// incomplete is held (decision-022 §9): the Level reads UNKNOWN with the
// guard's reason. The envelope its record carries agrees with that -- it says
// nothing of the held Level -- so the worker's result contract accepts the
// evaluation, whether the sibling Level recovers or alerts beside it. Before,
// the RECOVERY was rewritten after the envelope was built, the envelope
// still said RECOVERY for the held Level, and the contract refused the whole
// Slot of the Query Group on every retry.
func TestAHeldRecoveryLeavesNoTraceInTheRecordsEnvelope(t *testing.T) {
	for _, arm := range []struct {
		name       string
		threshold5 string
		sibling    string
	}{
		{"sibling recovers", "50", contract.LevelResultRecovery},
		{"sibling alerts", "5", contract.LevelResultAbnormal},
	} {
		t.Run(arm.name, func(t *testing.T) {
			plan := compiledTwoLevelsWithThresholds(t, arm.threshold5, "50")
			history := []execution.StateHistoryPoint{{RecordID: strings.Repeat("a", 64), SourceTime: 40, Levels: []execution.StateLevelFact{
				{LevelID: 5, DetectFingerprint: plan.Levels().At(0).Fingerprints().Detect, Result: execution.LevelFactAnomalous},
				{LevelID: 6, DetectFingerprint: plan.Levels().At(1).Fingerprints().Detect, Result: execution.LevelFactAnomalous},
			}}}
			request := requestFixtureTwoLevels(t, plan, json.RawMessage(`10`), history)
			withEmptyDependency(&request, 1)
			result, err := newEvaluator(t).Evaluate(context.Background(), request)
			if err != nil {
				t.Fatalf("Evaluate() = %v", err)
			}
			proposeRoundGuard(t, request, &result)
			if err := result.Validate(request); err != nil {
				t.Fatalf("the worker's result contract refused the evaluator's own result: %v", err)
			}
			byLevel := map[uint32]execution.LevelOutcome{}
			for _, outcome := range result.Plans[0].LevelOutcomes {
				byLevel[outcome.LevelID] = outcome
			}
			if held := byLevel[6]; held.Outcome != execution.LevelOutcomeUnknown || held.ReasonCode == "" {
				t.Fatalf("held Level = %+v, want UNKNOWN with the guard's reason", held)
			}
			events := 0
			for _, state := range result.Plans[0].StateResults {
				for _, event := range state.Events {
					events++
					for _, level := range event.LevelResults {
						if level.LevelID == 6 {
							t.Fatalf("envelope %s carries the held Level: %+v", event.EventKind, event.LevelResults)
						}
					}
					if len(event.LevelResults) != 1 || event.LevelResults[0].Result != arm.sibling {
						t.Fatalf("envelope %s = %+v, want the sibling's %s alone", event.EventKind, event.LevelResults, arm.sibling)
					}
				}
			}
			if arm.sibling == contract.LevelResultAbnormal && events != 1 {
				t.Fatalf("%d envelopes, want the sibling's ABNORMAL", events)
			}
		})
	}
}

// An ABNORMAL on the same incomplete inputs is not held: a degraded input may
// still have crossed a threshold, and refusing to say so is the one direction
// of the rule that loses an alert. The Level alerts and its envelope says so.
func TestAnAlertOnIncompleteInputsIsNotHeld(t *testing.T) {
	plan := compiledTwoLevelsWithThresholds(t, "50", "5")
	history := []execution.StateHistoryPoint{{RecordID: strings.Repeat("a", 64), SourceTime: 40, Levels: []execution.StateLevelFact{
		{LevelID: 5, DetectFingerprint: plan.Levels().At(0).Fingerprints().Detect, Result: execution.LevelFactNormal},
		{LevelID: 6, DetectFingerprint: plan.Levels().At(1).Fingerprints().Detect, Result: execution.LevelFactNormal},
	}}}
	request := requestFixtureTwoLevels(t, plan, json.RawMessage(`10`), history)
	withEmptyDependency(&request, 1)
	result, err := newEvaluator(t).Evaluate(context.Background(), request)
	if err != nil {
		t.Fatalf("Evaluate() = %v", err)
	}
	alerted := false
	for _, state := range result.Plans[0].StateResults {
		for _, event := range state.Events {
			for _, level := range event.LevelResults {
				if level.LevelID == 6 && level.Result == contract.LevelResultAbnormal {
					alerted = true
				}
			}
		}
	}
	if !alerted {
		t.Fatalf("plan result = %+v, want Level 6's ABNORMAL in the envelope", result.Plans[0])
	}
}
