package trigger

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// Every event of a Plan with strategy facts carries them, for the output to
// write; a Plan without them gives its events none.
func TestAnEventCarriesItsPlansStrategyFacts(t *testing.T) {
	facts := contract.StrategyFactsV2{Schema: "v2", MetricDigest: "3815df5a36678b1d738aa6f9a1522549", ConditionMD5: "d751713988987e9331980363e24189ce"}
	for _, withFacts := range []bool{true, false} {
		plan := compilePlanV2WithOutput(t, []contract.LevelIRV2{levelV2(1, 1, 1, 1, 1, nil)}, func(p *contract.EvaluationPlanV2) {
			p.WireFormat = contract.WireFormatStandardRawEvent
			if withFacts {
				p.StrategyFacts = &facts
			}
		})
		request := requestV2(t, plan, 300, []DetectionFact{factV2(plan.Levels().At(0), DetectionAnomalous)},
			[]LevelHistory{{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{300: true}}}}, activeFactsV2(t, plan, 300))
		result, err := EvaluateV2(request)
		if err != nil || result.TriggerEvent == nil {
			t.Fatalf("EvaluateV2() = %+v, %v", result, err)
		}
		got := result.TriggerEvent.StrategyFacts
		if withFacts && (got == nil || *got != facts) || !withFacts && got != nil {
			t.Fatalf("with facts %v: event facts %+v, want %+v", withFacts, got, facts)
		}
	}
}

// The no-data end built beside the evaluator carries them too.
func TestAnAbsentNoDataEndCarriesItsPlansStrategyFacts(t *testing.T) {
	facts := contract.StrategyFactsV2{Schema: "v2", MetricDigest: "3815df5a36678b1d738aa6f9a1522549", ConditionMD5: "d751713988987e9331980363e24189ce"}
	level := levelV2(5, 1, 2, 1, 3, nil)
	level.RecoveryPlan.Config = mustJSONV2(map[string]any{"enabled": true, "consecutive_windows": 3,
		"missing_data": contract.RecoveryMissingDataEvaluateAsZero, "end": contract.RecoveryEndClose})
	result := compileResultV2(t, []contract.LevelIRV2{level}, func(p *contract.EvaluationPlanV2) {
		p.StrategyRef.SnapshotRevision = 7
		p.StrategyIR.StrategyRef.SnapshotRevision = 7
		p.StrategyFacts = &facts
	})
	plan, ok := result.Plan()
	if !ok {
		t.Fatalf("compile terminal = %+v", result.PlanTerminal())
	}
	event, err := BuildAbsentNoDataEndV1(absentEnd(plan, 5))
	if err != nil {
		t.Fatal(err)
	}
	if event.StrategyFacts == nil || *event.StrategyFacts != facts {
		t.Fatalf("event facts %+v, want %+v", event.StrategyFacts, facts)
	}
}
