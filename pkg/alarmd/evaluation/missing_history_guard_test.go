package evaluation

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestMissingHistoryFromFullPersistsCurrentFactAndConvergesAfterReplay(t *testing.T) {
	plan := compiledG4PlanWithTrigger(t, strategy.DetectorKindSimpleRingRatio, map[string]any{"floor": 20}, strategy.AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}}, 2, 2)
	history := []execution.StateHistoryPoint{}
	for _, timestamp := range []int64{600, 660} {
		record := g4Record(timestamp, "80", nil)
		history = append(history, execution.StateHistoryPoint{RecordID: record.RecordID, SourceTime: timestamp, Levels: []execution.StateLevelFact{{LevelID: 5, DetectFingerprint: plan.Levels()[0].Fingerprints().Detect, Result: execution.LevelFactAnomalous}}})
	}
	request := requestFixtureForPlan(t, plan, []contract.CanonicalRecordV2{g4Record(720, "80", nil)}, history)
	request.State.Items[0].Levels[0].LastProcessedEventTime = 660
	request.Inputs = []execution.SeriesEvaluationInputRequest{g4Input(t, request, map[string][]contract.CanonicalRecordV2{"primary": {g4Record(720, "80", nil)}})}
	evaluator := newEvaluator(t)
	evaluate := func(request execution.EvaluationRequest) execution.PlanEvaluationResult {
		t.Helper()
		result, err := evaluator.Evaluate(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if err = result.Validate(request); err != nil {
			t.Fatalf("result contract: %v", err)
		}
		return result.Plans[0]
	}
	result := evaluate(request)
	if len(result.StateResults) != 1 || result.LevelOutcomes[0].Outcome != execution.LevelOutcomeUnknown {
		t.Fatalf("missing durable gap %+v", result)
	}
	mutation := result.StateResults[0].Mutation
	if len(result.StateResults[0].Events) != 0 || len(result.GuardAfterState) != 0 || mutation.SeriesGuard != nil || len(mutation.Levels) != 1 || mutation.Levels[0].HistoryCompleteness != execution.HistoryGapped || mutation.Levels[0].GapReasonCode != execution.ReasonCode(contract.ReasonHistoryGapped) {
		t.Fatalf("gap scope or reason %+v", result)
	}
	if len(mutation.Points) != 2 || !reflect.DeepEqual(mutation.Points[0], history[1]) || mutation.Points[1].SourceTime != 720 || len(mutation.Points[1].Levels) != 1 || mutation.Points[1].Levels[0].Result != execution.LevelFactUnavailable {
		t.Fatalf("retained history/current unavailable %+v", mutation.Points)
	}
	if err := mutation.ValidateDigest(); err != nil {
		t.Fatal(err)
	}
	// Identical input before commit is deterministic; after commit the same
	// Slot is covered by its loaded guard without appending a duplicate point.
	repeated := evaluate(request)
	if repeated.StateResults[0].Mutation.MutationDigest != mutation.MutationDigest {
		t.Fatal("same-slot re-evaluation differs")
	}
	replay := request
	replay.State = execution.StatePreflightResult{Items: []execution.RuntimeStateView{stateViewFromMutation(mutation, execution.StateFoundGapped)}}
	replayResult := evaluate(replay)
	if len(replayResult.StateResults) != 0 || replayResult.LevelOutcomes[0].Outcome != execution.LevelOutcomeUnknown || replayResult.LevelOutcomes[0].ReasonCode != execution.ReasonCode(contract.ReasonHistoryGapped) {
		t.Fatalf("replay changed gap %+v", replayResult)
	}
	// A second series under the same Plan retains independent FULL state and
	// emits its normal business result despite the first series' missing input.
	siblingRecord := g4Record(720, "80", nil)
	siblingRecord.DimensionIdentity.Digest = strings.Repeat("b", 64)
	siblingPrevious := g4Record(660, "100", nil)
	siblingPrevious.DimensionIdentity.Digest = siblingRecord.DimensionIdentity.Digest
	sibling := requestFixtureForPlan(t, plan, []contract.CanonicalRecordV2{siblingRecord}, history)
	sibling.Inputs = []execution.SeriesEvaluationInputRequest{g4Input(t, sibling, map[string][]contract.CanonicalRecordV2{"primary": {siblingRecord}, "previous": {siblingPrevious}})}
	healthy := evaluate(sibling)
	if healthy.LevelOutcomes[0].Outcome != execution.LevelOutcomeAbnormal || len(healthy.StateResults) != 1 || healthy.StateResults[0].Mutation.Identity.SeriesIdentityDigest == mutation.Identity.SeriesIdentityDigest {
		t.Fatalf("sibling affected %+v", healthy)
	}
	// Existing guard convergence needs a complete loaded detection window. Two
	// good points refill it, and the following point clears the Level guard.
	view := replay.State.Items[0]
	for i, timestamp := range []int64{780, 840, 900} {
		record := g4Record(timestamp, "100", nil)
		next := requestFixtureForPlan(t, plan, []contract.CanonicalRecordV2{record}, nil)
		next.State = execution.StatePreflightResult{Items: []execution.RuntimeStateView{view}}
		next.Inputs = []execution.SeriesEvaluationInputRequest{g4Input(t, next, map[string][]contract.CanonicalRecordV2{"primary": {record}, "previous": {g4Record(timestamp-60, "100", nil)}})}
		current := evaluate(next)
		if len(current.StateResults) != 1 {
			t.Fatalf("restored input did not refill state %+v", current)
		}
		currentMutation := current.StateResults[0].Mutation
		if i < 2 {
			if current.LevelOutcomes[0].Outcome != execution.LevelOutcomeUnknown || currentMutation.Levels[0].HistoryCompleteness != execution.HistoryGapped {
				t.Fatalf("guard cleared before window refilled %+v", current)
			}
		} else if current.LevelOutcomes[0].Outcome == execution.LevelOutcomeUnknown || currentMutation.Levels[0].HistoryCompleteness != execution.HistoryFull || currentMutation.Levels[0].GapReasonCode != "" {
			t.Fatalf("guard failed to converge %+v", current)
		}
		view = stateViewFromMutation(currentMutation, execution.StateFoundGapped)
	}
}
