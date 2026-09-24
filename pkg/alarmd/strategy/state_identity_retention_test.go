// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package strategy

import (
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func identityTestRequest() CompileRequest {
	return CompileRequest{
		Plan: contract.EvaluationPlanV2{StrategyIR: contract.StrategyIRV2{
			ExecutionSemantics: contract.ExecutionSemanticsV2{
				EvaluationScope: contract.EvaluationScopeSeries, QueryWindow: 300,
				AggregationInterval: 60, EvaluationInterval: 60, LatenessTolerance: 120,
			},
		}},
		DatasetContract: contract.DatasetContractV2{IdentityFields: []string{"host"}},
		StateSemantics: StateSemantics{
			StateSchemaVersion: "window-state-v1", CodecSemanticsVersion: "window-state-codec-v1",
			IdentitySchemaDigest: strings.Repeat("3", 64), SourceTimeSemanticsVersion: "source-time-seconds-v1",
			HistoryCellSemanticsVersion: "detect-history-cell-v1",
		},
	}
}

func identityTestLevel(required, retention uint32) CompiledLevel {
	return CompiledLevel{
		definition:       contract.LevelDefinitionV2{LevelID: 5, Priority: 1},
		fingerprints:     LevelFingerprints{Detect: strings.Repeat("a", 64), Trigger: strings.Repeat("b", 64)},
		stateRequirement: StateRequirement{RequiredDetectHistoryPoints: required, RetentionPoints: retention},
	}
}

// How much a Level retains is not part of what makes stored state compatible
// with the Plan that reads it, so raising it must not move the Plan's state
// generation.
//
// It did. A change that gave 674 Levels a few positions of recovery slack gave
// their Plans a new state compatibility hash, and with it a new state key: the
// records under the old key stopped being read, every series restarted from an
// empty window, and the Level contract stored on what could still be read no
// longer matched the compiled one, so it was refused as well. What it cost was
// every window those Plans had built, and it would have cost it again on every
// deployment that upgraded past the change.
func TestRetentionDoesNotMoveThePlansStateGeneration(t *testing.T) {
	request := identityTestRequest()
	lean, err := (&PlanCompiler{}).deriveStateCompatibilityHash(request, []CompiledLevel{identityTestLevel(30, 30)})
	if err != nil {
		t.Fatalf("derive without slack: %v", err)
	}
	slack, err := (&PlanCompiler{}).deriveStateCompatibilityHash(request, []CompiledLevel{identityTestLevel(30, 69)})
	if err != nil {
		t.Fatalf("derive with slack: %v", err)
	}
	if lean != slack {
		t.Fatalf("a Level retaining 69 positions hashes to %s and one retaining 30 to %s; the two read the "+
			"same stored record with the same detection, so a Plan that raises its retention would "+
			"abandon every window it has", slack, lean)
	}
	// And the half that has to keep moving: how many positions detection reads
	// is part of the contract, because a record written for a shorter window
	// cannot answer a longer one.
	longer, err := (&PlanCompiler{}).deriveStateCompatibilityHash(request, []CompiledLevel{identityTestLevel(31, 70)})
	if err != nil {
		t.Fatalf("derive with a longer window: %v", err)
	}
	if longer == lean {
		t.Fatal("a Level that reads one more position hashes the same as one that does not; the record " +
			"written for the shorter window would be read as if it answered the longer one")
	}
}

// The hash a deployment has already stored was derived over a requirement
// whose retention equalled its required count, because until this change the
// two could not differ. Filling the field rather than dropping it from the
// hashed shape is what makes the new derivation produce those same bytes.
func TestTheStateIdentityViewIsTheShapeEveryStoredHashWasDerivedOver(t *testing.T) {
	view := StateRequirement{RequiredDetectHistoryPoints: 30, RetentionPoints: 69}.StateIdentityView()
	if view != (StateRequirement{RequiredDetectHistoryPoints: 30, RetentionPoints: 30}) {
		t.Fatalf("the identity view is %+v; it has to be byte for byte the shape a pre-slack binary hashed, "+
			"or every Level's hash moves once more", view)
	}
	if lean := (StateRequirement{RequiredDetectHistoryPoints: 30, RetentionPoints: 30}).StateIdentityView(); lean !=
		(StateRequirement{RequiredDetectHistoryPoints: 30, RetentionPoints: 30}) {
		t.Fatalf("a requirement that never took slack came out as %+v; its hash must not move at all", lean)
	}
}
