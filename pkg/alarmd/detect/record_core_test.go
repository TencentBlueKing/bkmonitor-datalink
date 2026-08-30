package detect

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestPreparedRecordCoreMatchesPhaseOnePlanView(t *testing.T) {
	algorithms := []contract.AlgorithmIRV2{
		fixtureThresholdAlgorithmFor("value", "GTE", "50", "percent", ""), fixtureThresholdAlgorithmFor("value", "GTE", "80", "percent", ""),
	}
	for _, connector := range []string{contract.LevelConnectorAND, contract.LevelConnectorOR} {
		plan := fixturePlan("1001", []contract.LevelIRV2{
			fixtureLevel(5, 1, connector, algorithms...),
			fixtureLevel(9, 2, contract.LevelConnectorAND, fixtureThresholdAlgorithmFor("value", "GTE", "60", "percent", "")),
		})
		envelope := fixtureEnvelope(t, []contract.EvaluationPlanV2{plan}, []fixtureRecord{{host: "host", sourceTime: 100, value: json.RawMessage(`70`)}}, contract.QueryCompletenessFull)
		input, executions, digest := fixtureExecutions(t, envelope)
		evaluator := newTestEvaluator(t)
		phaseOne, err := evaluator.Evaluate(context.Background(), EvaluateRequest{Completeness: input.Execution().Completeness, DatasetContractDigest: digest, Plans: executions, Limits: generousLimits()})
		if err != nil {
			t.Fatalf("phase-one Evaluate() error = %v", err)
		}
		prepared, err := evaluator.PreparePlan(executions[0].Plan)
		if err != nil {
			t.Fatalf("PreparePlan() error = %v", err)
		}
		facts, values, _, err := evaluator.EvaluatePreparedRecord(context.Background(), prepared, recordValueMap{"value": json.RawMessage(`70`)})
		if err != nil {
			t.Fatalf("EvaluatePreparedRecord() error = %v", err)
		}
		got := RecordDetection{ProjectedValues: values, LevelFacts: facts}
		want := phaseOne.Series[0].Records[0]
		want.RecordOrdinal, want.RecordID, want.SourceTime = 0, "", 0
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("prepared result = %#v, phase-one = %#v", got, want)
		}
	}
}

func TestPreparedRecordCoreKeepsNormalizationFailuresUnavailable(t *testing.T) {
	for name, value := range map[string]json.RawMessage{"absent": nil, "null": json.RawMessage(`null`), "type": json.RawMessage(`"70"`), "overflow": json.RawMessage(`1e100`)} {
		t.Run(name, func(t *testing.T) {
			fact := evaluateSingleFact(t, []contract.AlgorithmIRV2{fixtureThresholdAlgorithmFor("value", "GTE", "50", "percent", "")}, contract.LevelConnectorAND, value, "percent")
			if fact.Result != FactResultUnavailable {
				t.Fatalf("fact result = %s, want UNAVAILABLE", fact.Result)
			}
		})
	}
}

func TestCombineAlgorithmTruthUsesStrongThreeValueLogic(t *testing.T) {
	tests := []struct {
		name      string
		connector string
		truths    []algorithmTruth
		want      bool
		decided   bool
	}{
		{name: "AND false dominates unknown", connector: contract.LevelConnectorAND, truths: []algorithmTruth{algorithmTruthUnknown, algorithmTruthFalse}, want: false, decided: true},
		{name: "AND true cannot dominate unknown", connector: contract.LevelConnectorAND, truths: []algorithmTruth{algorithmTruthUnknown, algorithmTruthTrue}, decided: false},
		{name: "OR true dominates unknown", connector: contract.LevelConnectorOR, truths: []algorithmTruth{algorithmTruthUnknown, algorithmTruthTrue}, want: true, decided: true},
		{name: "OR false cannot dominate unknown", connector: contract.LevelConnectorOR, truths: []algorithmTruth{algorithmTruthUnknown, algorithmTruthFalse}, decided: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, decided := combineAlgorithmTruth(test.connector, test.truths)
			if got != test.want || decided != test.decided {
				t.Fatalf("combineAlgorithmTruth()=(%t,%t), want (%t,%t)", got, decided, test.want, test.decided)
			}
		})
	}
}
