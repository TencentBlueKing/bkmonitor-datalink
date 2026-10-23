// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package access

import (
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// withheldCompletion delivers one series to an adapter whose Plans read
// contexts and restates a DATA completion for what it forwarded.
func withheldCompletion(t *testing.T, chain *admission.Chain, dimensions map[string]json.RawMessage, contexts ...admission.PlanContext) execution.ProviderCompletion {
	t.Helper()
	adapter, plan := scopeDropAdapter(t, chain, contexts[0], &recordingSink{}, scopeOutput)
	for index, context := range contexts[1:] {
		other := execution.PlanIdentity{TenantID: plan.TenantID, BusinessID: plan.BusinessID, StrategyID: plan.StrategyID + "-" + string(rune('a'+index))}
		requirement := adapter.query.Requirements[0]
		consumer := requirement.Consumers[0]
		consumer.Consumer.Plan = other
		requirement.Consumers = append(append([]execution.DataRequirementConsumer(nil), requirement.Consumers...), consumer)
		adapter.query.Requirements = []execution.DataRequirement{requirement}
		context.StrategyID = other.StrategyID
		adapter.scopes[other] = context
		adapter.outputs[other] = scopeOutput
	}
	deliverSeries(t, adapter, dimensions)
	return adapter.reconcileCompletion(execution.ProviderCompletion{
		Completeness: execution.CompletenessFull, DataState: execution.DataStateData,
	})
}

// A series every Plan refuses as outside its target, on facts that were all
// there, is data the target selected none of; the completion is EMPTY, as
// before, and now says so. A refusal the target could not decide is not that
// claim.
func TestASeriesEveryTargetRefusesOnFullFactsIsCountedAsOutsideTheTarget(t *testing.T) {
	outside := withheldCompletion(t, targetChain(), hostDims("202"), hostPlanContext(true))
	if outside.DataState != execution.DataStateEmpty || outside.Withheld != 1 || outside.WithheldOutsideTarget != 1 {
		t.Fatalf("completion %+v, want EMPTY with the one series withheld as outside the target", outside)
	}
	undecided := withheldCompletion(t, targetChain(), hostDims("202"), hostPlanContext(false))
	if undecided.DataState != execution.DataStateEmpty || undecided.Withheld != 1 || undecided.WithheldOutsideTarget != 0 {
		t.Fatalf("completion %+v, want the series withheld and not claimed outside: the resolution was not definitive", undecided)
	}
}

// A refusal for a reason other than the target - a host whose monitoring is
// off - is withheld data, not data outside the target.
func TestAHostStatusRefusalIsWithheldButNotOutsideTheTarget(t *testing.T) {
	filter := admission.NewHostStatusFilter([]string{"spare"})
	chain := admission.NewChain([]admission.Fuller{admission.IdentityFuller{}, stateFuller{state: "spare"}},
		[]admission.Filter{filter, admission.TargetScopeFilter{}})
	got := withheldCompletion(t, chain, map[string]json.RawMessage{"bk_host_id": json.RawMessage(`"7"`)},
		admission.PlanContext{TargetScope: hostScope("7")})
	if got.Withheld != 1 || got.WithheldOutsideTarget != 0 {
		t.Fatalf("completion %+v, want the series withheld and not counted outside the target", got)
	}
}

// Outside the target is every Plan's answer, not any one's: a second Plan
// that cannot decide keeps the series off the claim, and one that admits it
// keeps it from being withheld at all.
func TestOutsideTheTargetIsEveryPlansAnswer(t *testing.T) {
	both := withheldCompletion(t, targetChain(), hostDims("202"), hostPlanContext(true), hostPlanContext(true))
	if both.Withheld != 1 || both.WithheldOutsideTarget != 1 {
		t.Fatalf("both Plans definitive: %+v, want withheld outside the target", both)
	}
	for _, contexts := range [][]admission.PlanContext{
		{hostPlanContext(true), hostPlanContext(false)}, {hostPlanContext(false), hostPlanContext(true)},
	} {
		split := withheldCompletion(t, targetChain(), hostDims("202"), contexts...)
		if split.Withheld != 1 || split.WithheldOutsideTarget != 0 {
			t.Fatalf("one Plan not definitive: %+v, want withheld but not claimed outside, whichever Plan answers last", split)
		}
	}
	admitted := withheldCompletion(t, targetChain(), hostDims("101"), hostPlanContext(true), hostPlanContext(true))
	if admitted.DataState != execution.DataStateData || admitted.Withheld != 0 || admitted.WithheldOutsideTarget != 0 {
		t.Fatalf("a member of the target: %+v, want the series forwarded and nothing withheld", admitted)
	}
}

// Through the source: the counts reach the physical completion the worker
// reads. This chain has no host cache, so its scope refusals are not
// definitive and nothing is claimed outside the target.
func TestTheWithheldCountsReachTheQueryCompletion(t *testing.T) {
	completion, _, _ := executeScoped(t, hostScopeContract("192.0.2.10|0"), "192.0.2.98", "192.0.2.99")
	physical := completion.PhysicalQueries[0]
	if physical.DataState != execution.DataStateEmpty || physical.Withheld != 2 || physical.WithheldOutsideTarget != 0 {
		t.Fatalf("completion %+v, want EMPTY with both series withheld and none claimed outside", physical)
	}
}

// Through the source with a host cache that resolves every host: the
// target's refusals are its own answer, and both counts reach the physical
// completion the worker reads. Without the second one the worker would read
// every withheld series as withheld for another reason and never say the
// target emptied the query.
func TestTheOutsideTargetCountReachesTheQueryCompletion(t *testing.T) {
	chain := admission.NewChain([]admission.Fuller{admission.IdentityFuller{}, stateFuller{state: "running"}},
		[]admission.Filter{admission.TargetScopeFilter{}})
	completion, _, _ := executeScopedThrough(t, chain, hostScopeContract("192.0.2.10|0"), "192.0.2.98", "192.0.2.99")
	physical := completion.PhysicalQueries[0]
	if physical.DataState != execution.DataStateEmpty || physical.Withheld != 2 || physical.WithheldOutsideTarget != 2 {
		t.Fatalf("completion %+v, want EMPTY with both series withheld as outside the target", physical)
	}
}
