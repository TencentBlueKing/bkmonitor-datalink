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

// What the evaluator returns for a no-data series must pass the validation the
// Worker runs on it before anything is committed.
//
// The evaluator judges a synthetic series against the Plan's no-data view. The
// Worker then hands the result and the same request to
// EvaluationResult.Validate, which resolves the due Plan from the header - the
// full Plan - and until this test existed compared the no-data view's inputs,
// level outcomes and state mutation against the full Plan's level set. On the
// first production round that carried no-data content every one of those
// Plans failed with "evaluation inputs do not exactly cover one due Plan",
// "evaluation inputs are not one ordered Plan/series Level cover" or "State
// mutation Level contract differs from the compiled Plan", and the threshold
// rounds of the same Plans failed with them. No test had ever called Validate
// on a no-data result: the evaluator tests stopped at Evaluate.
func TestWhatTheEvaluatorReturnsForANoDataSeriesValidates(t *testing.T) {
	plan := noDataCompiled(t, 1)
	request := noDataRequestFixture(t, plan, 1)
	result, err := newEvaluator(t).Evaluate(context.Background(), request)
	if err != nil {
		t.Fatalf("Evaluate() on a no-data series: %v", err)
	}
	if err := result.Validate(request); err != nil {
		t.Fatalf("the Worker's validation refuses what the evaluator returned for a no-data series: %v", err)
	}
}

// A gap guard is the Plan's, not one view's. A real level's gap scope is
// loaded for the no-data series' round too, and a no-data level's scope is
// loaded for the real series' rounds; neither may read as a level the Plan
// does not have.
func TestAGapScopeOnEitherKindOfLevelValidatesForEitherKindOfSeries(t *testing.T) {
	plan := noDataCompiled(t, 1)
	var realLevel uint32
	for _, level := range plan.Levels() {
		realLevel = level.Definition().LevelID
	}
	noDataLevel := plan.NoDataLevel().Definition().LevelID
	if realLevel == 0 || realLevel == noDataLevel {
		t.Fatalf("fixture: real level %d, no-data level %d", realLevel, noDataLevel)
	}
	for name, scopedLevel := range map[string]uint32{"real level": realLevel, "no-data level": noDataLevel} {
		t.Run(name, func(t *testing.T) {
			request := noDataRequestFixture(t, plan, 1)
			identity := request.Header.DuePlans[0].Identity
			request.Gaps = execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{
				Identity: execution.PlanGapIdentity{Plan: identity, StateGeneration: "state-v1"},
				Status:   execution.GapFound, MarkerRevision: 1, LastScheduleRevision: "plan-schedule",
				PersistedMutationDigest: "gap-digest",
				Scopes: []execution.GapScopeState{{
					Scope:  execution.GapScope{HasLevel: true, LevelID: scopedLevel},
					Status: execution.GapStatusGapped, ReasonCode: execution.ReasonCode("QUERY_UNAVAILABLE"),
					RequiredFullSlots: 1,
				}},
			}}}
			result, err := newEvaluator(t).Evaluate(context.Background(), request)
			if err != nil {
				t.Fatalf("Evaluate(): %v", err)
			}
			if err := result.Validate(request); err != nil {
				t.Fatalf("a gap scope on the %s made the no-data series' round invalid: %v", name, err)
			}
		})
	}
}

// The other direction of the same rule: a real series' round loads the Plan's
// gap guard too, and a scope the no-data series wrote on the no-data level is
// not "a level the Plan does not have" -- the strategy's declared levels are
// not the whole Plan once it detects no-data.
func TestARealSeriesRoundAcceptsAGapScopeOnTheNoDataLevel(t *testing.T) {
	plan := noDataCompiled(t, 1)
	noDataLevel := plan.NoDataLevel().Definition().LevelID
	records := []contract.CanonicalRecordV2{{RecordID: strings.Repeat("b", 64), SourceTime: 100, BusinessID: "2",
		DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)},
		Values:            map[string]json.RawMessage{"value": json.RawMessage("1")}, Dimensions: map[string]json.RawMessage{}, ReceivedTime: 100}}
	request := requestFixtureForPlan(t, plan, records, nil)
	identity := request.Header.DuePlans[0].Identity
	request.Gaps = execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{
		Identity: execution.PlanGapIdentity{Plan: identity, StateGeneration: "state-v1"},
		Status:   execution.GapFound, MarkerRevision: 1, LastScheduleRevision: "plan-schedule",
		PersistedMutationDigest: "gap-digest",
		Scopes: []execution.GapScopeState{{
			Scope:  execution.GapScope{HasLevel: true, LevelID: noDataLevel},
			Status: execution.GapStatusGapped, ReasonCode: execution.ReasonCode("QUERY_UNAVAILABLE"),
			RequiredFullSlots: 1,
		}},
	}}}
	result, err := newEvaluator(t).Evaluate(context.Background(), request)
	if err != nil {
		t.Fatalf("Evaluate() on a real series: %v", err)
	}
	if err := result.Validate(request); err != nil {
		t.Fatalf("a gap scope on the no-data level made the real series' round invalid: %v", err)
	}
	// And the real series is still judged against the declared levels only.
	if outcomes := result.Plans[0].LevelOutcomes; len(outcomes) != 1 || outcomes[0].LevelID != 5 {
		t.Fatalf("real series outcomes = %+v, want the declared level 5 only", outcomes)
	}
}

// A level neither kind of the Plan has is still refused: the wider ownership is
// exactly the declared levels plus the no-data level, not "anything".
func TestAGapScopeOnALevelThePlanDoesNotHaveIsStillRefused(t *testing.T) {
	plan := noDataCompiled(t, 1)
	request := noDataRequestFixture(t, plan, 1)
	identity := request.Header.DuePlans[0].Identity
	request.Gaps = execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{
		Identity: execution.PlanGapIdentity{Plan: identity, StateGeneration: "state-v1"},
		Status:   execution.GapFound, MarkerRevision: 1, LastScheduleRevision: "plan-schedule",
		PersistedMutationDigest: "gap-digest",
		Scopes: []execution.GapScopeState{{
			Scope:  execution.GapScope{HasLevel: true, LevelID: 99},
			Status: execution.GapStatusGapped, ReasonCode: execution.ReasonCode("QUERY_UNAVAILABLE"),
			RequiredFullSlots: 1,
		}},
	}}}
	result, err := newEvaluator(t).Evaluate(context.Background(), request)
	if err != nil {
		t.Fatalf("Evaluate() refused before the validator saw the gap: %v", err)
	}
	err = result.Validate(request)
	if err == nil || !strings.Contains(err.Error(), "unknown compiled Level") {
		t.Fatalf("a gap scope on level 99, which the Plan has in no view: err = %v, want the unknown-level refusal", err)
	}
}
