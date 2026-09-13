// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package observability

import "testing"

// Index facts keep their closed vocabularies: an unknown read result
// normalizes to invalid and an unknown shadow class to skipped, so a
// producer bug reads as "do not trust this" rather than as a new category.
func TestAssignmentIndexFactsKeepClosedVocabularies(t *testing.T) {
	got := NormalizeObservation(Observation{
		Component: ComponentOwnership, Stage: StageAssignmentIndexRead, Result: ResultSuccess, Operation: OperationLoad,
		AssignmentIndex: &AssignmentIndexFacts{Round: 3, Result: "sideways", Shadow: "maybe", StaleRounds: -4, Candidates: 2, Assigned: -1, Difference: -3},
	})
	if got.Stage != StageAssignmentIndexRead || got.Component != ComponentOwnership || got.AssignmentIndex == nil {
		t.Fatalf("observation = %+v, want the index read stage kept", got)
	}
	facts := got.AssignmentIndex
	if facts.Result != AssignmentIndexInvalid || facts.Shadow != AssignmentIndexShadowSkipped ||
		facts.StaleRounds != 0 || facts.Assigned != 0 || facts.Difference != 0 || facts.Candidates != 2 || facts.Round != 3 {
		t.Fatalf("facts were not normalized: %+v", facts)
	}
	kept := NormalizeObservation(Observation{
		Component: ComponentOwnership, Stage: StageAssignmentIndexWritten, Result: ResultSuccess, Operation: OperationWrite,
		AssignmentIndex: &AssignmentIndexFacts{Round: 9, Workers: 2, Rewritten: 1, Missing: 1},
	})
	if kept.Stage != StageAssignmentIndexWritten || kept.AssignmentIndex.Round != 9 || kept.AssignmentIndex.Rewritten != 1 || kept.AssignmentIndex.Result != "" {
		t.Fatalf("written facts were altered: %+v", kept.AssignmentIndex)
	}
}
