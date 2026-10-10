// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package evaluation

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// History carried across a moved state generation reaches the evaluator as
// the history of a series with no record of its own. Evaluated so, it gives
// exactly what the same history gives when the new contract wrote it all
// along; with nothing carried it never concludes NORMAL; and a retried
// record at a time the carried history already holds adds nothing.
func TestCarriedHistoryEvaluatesAsIfTheNewContractHadWrittenIt(t *testing.T) {
	plan := compiledWindow(t, 3, 2)
	fingerprint := plan.Levels()[0].Fingerprints().Detect
	record := func(sourceTime int64, value string) []contract.CanonicalRecordV2 {
		return []contract.CanonicalRecordV2{{RecordID: strings.Repeat("b", 64), SourceTime: sourceTime, BusinessID: "2",
			DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)},
			Values:            map[string]json.RawMessage{"value": json.RawMessage(value)}, Dimensions: map[string]json.RawMessage{}, ReceivedTime: sourceTime}}
	}
	point := func(id string, sourceTime int64, result execution.LevelFactResult) execution.StateHistoryPoint {
		return execution.StateHistoryPoint{RecordID: strings.Repeat(id, 64), SourceTime: sourceTime,
			Levels: []execution.StateLevelFact{{LevelID: 5, DetectFingerprint: fingerprint, Result: result}}}
	}
	// As carryHistory hands it over: the missing record, with history.
	carried := func(req *execution.EvaluationRequest) {
		view := &req.State.Items[0]
		view.Status, view.BlobRevision, view.Levels = execution.StateMissingWarming, 0, nil
		view.VersionComparison = execution.ApplyVersionPersistedOlder
	}
	evaluate := func(t *testing.T, req execution.EvaluationRequest) execution.PlanEvaluationResult {
		t.Helper()
		result, err := newEvaluator(t).Evaluate(context.Background(), req)
		if err != nil {
			t.Fatalf("Evaluate()=%v", err)
		}
		return result.Plans[0]
	}
	history := []execution.StateHistoryPoint{point("d", 280, execution.LevelFactAnomalous), point("e", 340, execution.LevelFactAnomalous)}

	t.Run("the same outcome as the new contract all along", func(t *testing.T) {
		native := requestFixtureForPlan(t, plan, record(400, `60`), history)
		moved := requestFixtureForPlan(t, plan, record(400, `60`), history)
		carried(&moved)
		want, got := evaluate(t, native), evaluate(t, moved)
		if want.LevelOutcomes[0].Outcome != got.LevelOutcomes[0].Outcome {
			t.Fatalf("carried outcome %s, native %s", got.LevelOutcomes[0].Outcome, want.LevelOutcomes[0].Outcome)
		}
		if got.LevelOutcomes[0].Outcome == execution.LevelOutcomeNormal {
			t.Fatalf("three anomalous positions of three read as NORMAL")
		}
		if len(want.StateResults) != 1 || len(got.StateResults) != 1 ||
			!reflect.DeepEqual(want.StateResults[0].Mutation.Points, got.StateResults[0].Mutation.Points) ||
			!reflect.DeepEqual(got.StateResults[0].Mutation.BaseHistory, history) {
			t.Fatalf("carried mutation %+v differs from native %+v", got.StateResults, want.StateResults)
		}
	})

	t.Run("nothing carried never concludes NORMAL", func(t *testing.T) {
		moved := requestFixtureForPlan(t, plan, record(400, `10`), nil)
		carried(&moved)
		if outcome := evaluate(t, moved).LevelOutcomes[0].Outcome; outcome == execution.LevelOutcomeNormal {
			t.Fatalf("a window of one position of three concluded NORMAL")
		}
	})

	t.Run("a retried record the carried history holds adds nothing", func(t *testing.T) {
		retried := append([]execution.StateHistoryPoint(nil), history...)
		retried[1] = point("b", 340, execution.LevelFactAnomalous)
		moved := requestFixtureForPlan(t, plan, record(340, `60`), retried)
		carried(&moved)
		result := evaluate(t, moved)
		for _, state := range result.StateResults {
			for _, added := range state.Mutation.Points {
				if added.SourceTime == 340 && !reflect.DeepEqual(added, retried[1]) {
					t.Fatalf("the retried record rewrote the carried point: %+v", added)
				}
			}
		}
	})
}
