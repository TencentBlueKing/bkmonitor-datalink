// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The published count is read out of the bytes, not off the struct they came
// from.
//
// That distinction is the whole reason this count exists. The leader's own
// gauge already says how many Plans it compiled with a no-data section; what
// nobody could say was how many of them survive into what every other process
// reads. Counting the struct again would report the leader's belief a second
// time under a new name.
func TestThePublishedNoDataCountIsReadOutOfThePayload(t *testing.T) {
	payload, err := contract.CanonicalJSONV2(BuildQueryGroupObject(QueryGroup{
		Plans: []FrozenPlan{
			{Plan: contract.EvaluationPlanV2{PlanID: "1", NoData: &contract.NoDataConfigV1{Continuous: 1, Level: 2}}},
			{Plan: contract.EvaluationPlanV2{PlanID: "2"}},
			{Plan: contract.EvaluationPlanV2{PlanID: "3", NoData: &contract.NoDataConfigV1{Continuous: 5, Level: 1}}},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}

	count, err := noDataPlansInPayload(payload)
	if err != nil {
		t.Fatal(err)
	}

	if count != 2 {
		t.Fatalf("published count = %d, want the 2 Plans whose section is in the bytes", count)
	}
}

// A publication carrying none reports none rather than nothing.
func TestThePublishedNoDataCountIsZeroWhenTheBytesCarryNone(t *testing.T) {
	payload, err := contract.CanonicalJSONV2(BuildQueryGroupObject(QueryGroup{
		Plans: []FrozenPlan{{Plan: contract.EvaluationPlanV2{PlanID: "1"}}},
	}))
	if err != nil {
		t.Fatal(err)
	}

	count, err := noDataPlansInPayload(payload)
	if err != nil {
		t.Fatal(err)
	}

	if count != 0 {
		t.Fatalf("published count = %d on bytes carrying no such Plan, want 0", count)
	}
}

// The count sums over every Query Group of the publication.
//
// A per-group count would be comparable with nothing: the leader's gauge is
// over the whole Catalog, and the question is whether the Catalog's Plans
// reached the bytes.
func TestThePublishedNoDataCountCoversTheWholePublication(t *testing.T) {
	content, err := buildObjectCatalogContent(Catalog{QueryGroups: []QueryGroup{
		{
			Identity: "qg-a",
			Plans: []FrozenPlan{
				{Plan: contract.EvaluationPlanV2{PlanID: "1", NoData: &contract.NoDataConfigV1{Continuous: 1, Level: 2}}},
				{Plan: contract.EvaluationPlanV2{PlanID: "2"}},
			},
		},
		{
			Identity: "qg-b",
			Plans: []FrozenPlan{
				{Plan: contract.EvaluationPlanV2{PlanID: "3", NoData: &contract.NoDataConfigV1{Continuous: 3, Level: 1}}},
			},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	if content.noDataPlans != 2 {
		t.Fatalf("published count = %d over two Query Groups, want the 2 Plans across both",
			content.noDataPlans)
	}
}
