// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func factsPlan(tenant string, revision int64, fields []string, levels ...uint32) FrozenPlan {
	plan := FrozenPlan{Identity: execution.PlanIdentity{TenantID: tenant, BusinessID: "2", StrategyID: "7"}}
	plan.Plan.StrategyRef.SnapshotRevision = revision
	plan.Plan.OutputIdentity = &contract.MonitorOutputIdentity{DimensionFields: fields}
	for _, level := range levels {
		plan.Plan.StrategyIR.Levels = append(plan.Plan.StrategyIR.Levels, contract.LevelIRV2{Definition: contract.LevelDefinitionV2{LevelID: level}})
	}
	return plan
}

// The facts are the frozen Plan's: its snapshot revision, its output
// identity's dimensions, its Levels and its no-data grouping.
func TestStrategyPlanFactsAreTheFrozenPlans(t *testing.T) {
	plan := factsPlan("t", 9, []string{"ip", "device"}, 2, 1)
	plan.Plan.NoData = &contract.NoDataConfigV1{Continuous: 5, AggDimension: []string{"ip"}, Level: 2}
	plan.Plan.StrategyFacts = &contract.StrategyFactsV2{Schema: "v2", MetricDigest: "m", ConditionMD5: "c"}
	plan.Plan.InputProjection.DimensionFields = []string{"port", "ip"}
	index := buildStrategyIndex(SnapshotPublicationRef{SnapshotRevision: "rev"}, []QueryGroup{{Identity: "qg", Plans: []FrozenPlan{plan}}},
		[]ObjectDisposition{{SourceID: "7", Scope: "PLAN", Disposition: DispositionAccepted}})
	facts, found := index.planFacts("t", "7")
	want := StrategyPlanFacts{Revision: 9, DimensionFields: []string{"device", "ip"}, HasOutputIdentity: true,
		RecordDimensionFields: []string{"ip", "port"},
		Levels:                []contract.LevelDefinitionV2{{LevelID: 1}, {LevelID: 2}}, LevelsWhole: true,
		NoData: true, NoDataAggDimension: []string{"ip"}, Agree: true,
		StrategyFacts: &contract.StrategyFactsV2{Schema: "v2", MetricDigest: "m", ConditionMD5: "c"}}
	if !found || !reflect.DeepEqual(facts, want) {
		t.Fatalf("facts %+v found %v, want %+v", facts, found, want)
	}
	if _, found := index.planFacts("other", "7"); found {
		t.Fatal("another tenant's strategy was answered with this tenant's Plan")
	}
}

// What makes an answer partial is said: a Level refused, the Plans retained,
// Plans that disagree, a no-data half suspended.
func TestStrategyPlanFactsSayWhatIsPartial(t *testing.T) {
	refused := buildStrategyIndex(SnapshotPublicationRef{}, []QueryGroup{{Identity: "qg", Plans: []FrozenPlan{factsPlan("t", 9, nil, 1)}}},
		[]ObjectDisposition{{SourceID: "7", Scope: "LEVEL", LevelID: 2, Disposition: DispositionUnsupported, Reason: "ALGORITHM_NOT_MIGRATED"}})
	if facts, _ := refused.planFacts("t", "7"); facts.LevelsWhole {
		t.Fatal("a strategy with a refused Level read as detecting every Level it declares")
	}
	noted := buildStrategyIndex(SnapshotPublicationRef{}, []QueryGroup{{Identity: "qg", Plans: []FrozenPlan{factsPlan("t", 9, nil, 1)}}},
		[]ObjectDisposition{{SourceID: "7", Scope: "LEVEL", LevelID: 1, Disposition: DispositionConfigNoted}})
	if facts, _ := noted.planFacts("t", "7"); !facts.LevelsWhole {
		t.Fatal("a Level carrying only a note read as refused")
	}
	retained := buildStrategyIndex(SnapshotPublicationRef{}, []QueryGroup{{Identity: "qg", Plans: []FrozenPlan{factsPlan("t", 9, nil, 1)}}},
		[]ObjectDisposition{{SourceID: "7", Scope: "PLAN", Disposition: DispositionStaleConfig}})
	if facts, _ := retained.planFacts("t", "7"); !facts.Retained {
		t.Fatal("a retained Plan read as the current definition")
	}
	split := buildStrategyIndex(SnapshotPublicationRef{}, []QueryGroup{{Identity: "a", Plans: []FrozenPlan{factsPlan("t", 9, []string{"ip"}, 1)}},
		{Identity: "b", Plans: []FrozenPlan{factsPlan("t", 9, []string{"device"}, 1)}}}, nil)
	if facts, _ := split.planFacts("t", "7"); facts.Agree {
		t.Fatal("Plans with different dimensions read as agreeing")
	}
	suspended := factsPlan("t", 9, nil, 1)
	suspended.NoDataSuspended = contract.ReasonNoDataRosterUnsupported
	held := buildStrategyIndex(SnapshotPublicationRef{}, []QueryGroup{{Identity: "qg", Plans: []FrozenPlan{suspended}}}, nil)
	if facts, _ := held.planFacts("t", "7"); facts.NoData || !facts.NoDataSuspended {
		t.Fatalf("facts %+v, want no-data configured and suspended", facts)
	}
}

// The interval is the longest of the strategy's Plans, how often it sends
// at the slowest; Plans that differ only in it still agree.
func TestStrategyPlanFactsCarryTheLongestInterval(t *testing.T) {
	minute, fiveMinutes := factsPlan("t", 9, []string{"ip"}, 1), factsPlan("t", 9, []string{"ip"}, 1)
	minute.Plan.StrategyIR.ExecutionSemantics.EvaluationInterval = 60
	fiveMinutes.Plan.StrategyIR.ExecutionSemantics.EvaluationInterval = 300
	for _, order := range [][]FrozenPlan{{minute, fiveMinutes}, {fiveMinutes, minute}} {
		index := buildStrategyIndex(SnapshotPublicationRef{}, []QueryGroup{{Identity: "a", Plans: order[:1]}, {Identity: "b", Plans: order[1:]}}, nil)
		if facts, _ := index.planFacts("t", "7"); facts.Interval != 5*time.Minute || !facts.Agree {
			t.Fatalf("interval %s agree %v, want 5m0s and agreeing", facts.Interval, facts.Agree)
		}
	}
}

// A process without a publication of its own answers unavailable, not "no
// such strategy".
func TestStrategyPlanFactsWithoutAPublicationAreUnavailable(t *testing.T) {
	reconciler := &SourceReconciler{}
	if _, available, found := reconciler.StrategyPlanFacts("t", "7"); available || found {
		t.Fatal("a process without a publication answered")
	}
	reconciler.strategies.replace(buildStrategyIndex(SnapshotPublicationRef{}, nil, nil))
	if _, available, found := reconciler.StrategyPlanFacts("t", "7"); !available || found {
		t.Fatalf("available %v found %v, want a publication without the strategy", available, found)
	}
}

// A strategy's target is its Plans' target plan, with the revision and the
// evaluation period; Plans with different target plans disagree; a Plan
// without one answers found with no plan; another tenant is not found.
func TestStrategyTargetIsThePlansTargetPlan(t *testing.T) {
	targetPlan := &contract.TargetPlanV1{SchemaVersion: 1, Rule: contract.TargetPlanRuleIPCloud,
		Identity:   contract.TargetPlanIdentityV1{Dimensions: []string{"bk_target_ip", "bk_target_cloud_id"}, Address: true},
		StaticKeys: []string{"192.0.2.1|0"}}
	plan := factsPlan("t", 9, nil, 1)
	plan.Plan.TargetPlan = targetPlan
	plan.Plan.StrategyIR.ExecutionSemantics.EvaluationInterval = 60
	index := buildStrategyIndex(SnapshotPublicationRef{}, []QueryGroup{{Identity: "qg", Plans: []FrozenPlan{plan}}}, nil)
	target, found := index.target("t", "7")
	if !found || target.Plan != targetPlan || target.Revision != 9 || target.Interval.Seconds() != 60 || !target.Agree {
		t.Fatalf("target %+v found %v", target, found)
	}
	if _, found := index.target("other", "7"); found {
		t.Fatal("another tenant's strategy was answered")
	}
	other := factsPlan("t", 9, nil, 1)
	other.Plan.TargetPlan = &contract.TargetPlanV1{SchemaVersion: 1, Rule: contract.TargetPlanRuleIPCloud,
		Identity: targetPlan.Identity, StaticKeys: []string{"192.0.2.2|0"}}
	split := buildStrategyIndex(SnapshotPublicationRef{}, []QueryGroup{{Identity: "a", Plans: []FrozenPlan{plan}}, {Identity: "b", Plans: []FrozenPlan{other}}}, nil)
	if target, _ := split.target("t", "7"); target.Agree {
		t.Fatal("Plans with different target plans read as agreeing")
	}
	whole := buildStrategyIndex(SnapshotPublicationRef{}, []QueryGroup{{Identity: "qg", Plans: []FrozenPlan{factsPlan("t", 9, nil, 1)}}}, nil)
	if target, found := whole.target("t", "7"); !found || target.Plan != nil {
		t.Fatalf("a whole-business strategy: %+v found %v", target, found)
	}
}
