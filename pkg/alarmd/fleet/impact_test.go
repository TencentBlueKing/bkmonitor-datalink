// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import "testing"

func withStrategies(queryGroup string, refs ...StrategyRef) Anomaly {
	item := anomaly(queryGroup)
	item.Strategies = refs
	return item
}

// A strategy with objects in two columns is one strategy.
//
// The page's lead sentence is how many strategies are getting no detection
// result, and adding the two columns overstates it -- by most when the
// deployment is worst, which is when the sentence is read.
func TestBlindImpactIsTheUnionOfTheTwoColumnsNotTheirSum(t *testing.T) {
	shared := StrategyRef{StrategyID: "100", BusinessID: "7"}
	view := View{
		Anomalies: []Anomaly{
			withStrategies("qg-a", shared),
			withStrategies("qg-b", StrategyRef{StrategyID: "200", BusinessID: "7"}),
		},
		AnomaliesTotal: 2,
		Demoted: []Anomaly{
			withStrategies("qg-c", shared),
			withStrategies("qg-d", StrategyRef{StrategyID: "300", BusinessID: "9"}),
		},
		DemotedTotal: 2,
	}
	impact := ImpactOf(view)
	if impact.Anomalies.Strategies != 2 || impact.Demoted.Strategies != 2 {
		t.Fatalf("columns = %d and %d strategies, want 2 and 2 -- the union below would be"+
			" comparing against the wrong thing", impact.Anomalies.Strategies, impact.Demoted.Strategies)
	}
	if impact.Blind.Strategies != 3 {
		t.Errorf("blind = %d strategies, want 3: strategy 100 has objects in both columns and"+
			" adding the two reports it twice", impact.Blind.Strategies)
	}
	if impact.Blind.Businesses != 2 {
		t.Errorf("blind = %d businesses, want 2", impact.Blind.Businesses)
	}
	if impact.Blind.Objects != 4 {
		t.Errorf("blind = %d objects, want 4: an object is in exactly one column, so those do add",
			impact.Blind.Objects)
	}
}

// An object that named no strategy is not an object affecting no strategy.
//
// A blocked round never got a Slot and the strategy references are read off the
// Slot, so those rows carry none by construction. Counted as zero, a column of
// them reports "0 条策略受影响", which reads as "nothing is affected" and means
// "we cannot say what is affected".
func TestObjectsThatNameNoStrategyAreReportedRatherThanCountedAsZero(t *testing.T) {
	blocked := anomaly("qg-blocked")
	blocked.Strategies = nil
	view := View{
		Anomalies:      []Anomaly{blocked, withStrategies("qg-a", StrategyRef{StrategyID: "100"})},
		AnomaliesTotal: 2,
	}
	impact := ImpactOf(view)
	if impact.NoStrategies != 1 {
		t.Errorf("no_strategies = %d, want 1", impact.NoStrategies)
	}
	if impact.Anomalies.Strategies != 1 {
		t.Errorf("strategies = %d, want 1", impact.Anomalies.Strategies)
	}
}

// A strategy count taken off a truncated list is a lower bound in the same
// shape as an exact one, on the line a reader uses to decide whether to act.
func TestImpactSaysWhenItsListWasCutBeforeItCounted(t *testing.T) {
	view := View{
		Anomalies:      []Anomaly{withStrategies("qg-a", StrategyRef{StrategyID: "100"})},
		AnomaliesTotal: 900,
	}
	impact := ImpactOf(view)
	if !impact.Anomalies.Partial {
		t.Error("the column does not report that its list was cut: 1 strategy off 900 objects" +
			" renders as the whole answer")
	}
	// The verdict's own subset inherits the limit, because it is counted off the
	// same cut list and is the number that decides whether anyone acts.
	if !impact.Ours.Partial {
		t.Error("the ours subset does not inherit the limit of the list it was counted from")
	}
}
