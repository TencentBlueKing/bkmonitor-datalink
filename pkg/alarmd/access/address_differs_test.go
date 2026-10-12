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
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// placementFuller stands for CMDB placing the record's host: by id at
// another address than the record's own, by id at the record's address, or
// by the address itself.
type placementFuller struct{ byID, differs bool }

func (placementFuller) Name() string { return "placement" }
func (fuller placementFuller) Fill(_ map[string]json.RawMessage, facts *admission.Facts) {
	facts.PlacedByHostID, facts.ReportedAddressDiffers = fuller.byID, fuller.differs
}

// placement is one observed series: the Plan's grouping, and whether its
// address differed from its host's.
type placement struct{ grouped, differs bool }

// A series whose host CMDB placed by id or agent is reported once for every
// Plan that admits it - the count a differing address is read against - with
// whether its address differs, under whether the query's grouping holds
// bk_target_ip: the grouping every Plan of the query builds its alert
// identity from, and the one Python's rewrite of that dimension reaches. A
// Plan that turns the series away raises no alert for it and is not
// counted, and a series placed by its address is not on that branch at all.
func TestAPlacementByIDIsCountedPerAdmittingPlanByItsGrouping(t *testing.T) {
	count := func(identityFields []string, fuller placementFuller) []placement {
		t.Helper()
		_, frozen := frozenExecution(t)
		requirement := frozen.Requirements[0]
		admitted := requirement.Consumers[0].Consumer.Plan
		another, outside := admitted, admitted
		another.StrategyID, outside.StrategyID = admitted.StrategyID+"-another", admitted.StrategyID+"-outside"
		for _, plan := range []execution.PlanIdentity{another, outside} {
			consumer := requirement.Consumers[0]
			consumer.Consumer.Plan = plan
			requirement.Consumers = append(requirement.Consumers, consumer)
		}
		query := plannedQueryForTest(requirement)
		query.Spec.PlanFacts.Normalization.DatasetContract.IdentityFields = identityFields
		var counted []placement
		adapter := &seriesAdapter{
			consumer:  &admissionConsumer{},
			query:     query,
			attemptNo: 1,
			admission: admission.NewChain([]admission.Fuller{admission.IdentityFuller{}, fuller},
				[]admission.Filter{admission.TargetScopeFilter{}}),
			hostPlacement: func(grouped, differs bool) { counted = append(counted, placement{grouped, differs}) },
			scopes: planScopes{
				admitted: {StrategyID: admitted.StrategyID},
				another:  {StrategyID: another.StrategyID},
				outside:  {StrategyID: outside.StrategyID, TargetScope: hostScope("192.0.2.2|0")},
			},
		}
		err := adapter.ConsumeProviderSeries(context.Background(), execution.ProviderSeriesBatch{
			PhysicalQuery: adapter.query.Spec.Digest, CompletionRef: "result", Dataset: hostSeries(t, "192.0.2.1"),
			Delivery: execution.SeriesDelivery{PhysicalQuery: adapter.query.Spec.Digest,
				QueryRevision: adapter.query.Spec.PlanFacts.QueryRevision, Series: 1, Records: 1, Digest: "digest"},
		})
		if err != nil {
			t.Fatalf("consume: %v", err)
		}
		return counted
	}
	byIP := []string{"bk_target_cloud_id", "bk_target_ip"}
	if got := count(byIP, placementFuller{byID: true, differs: true}); !reflect.DeepEqual(got, []placement{{true, true}, {true, true}}) {
		t.Fatalf("grouped by bk_target_ip, another address: counted %v, want the two admitting Plans, differing, and the outside one not at all", got)
	}
	if got := count(byIP, placementFuller{byID: true}); !reflect.DeepEqual(got, []placement{{true, false}, {true, false}}) {
		t.Fatalf("grouped by bk_target_ip, the host's address: counted %v, want the two admitting Plans, not differing", got)
	}
	if got := count([]string{"bk_agent_id"}, placementFuller{byID: true, differs: true}); !reflect.DeepEqual(got, []placement{{false, true}, {false, true}}) {
		t.Fatalf("grouped by bk_agent_id: counted %v, want the two admitting Plans under false", got)
	}
	if got := count(byIP, placementFuller{}); len(got) != 0 {
		t.Fatalf("placed by its address: counted %v, want nothing", got)
	}
}
