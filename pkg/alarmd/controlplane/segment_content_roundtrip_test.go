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
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// What a publication names is what comes back out of it.
//
// The manifest's digest is computed from the Catalog the leader built; the
// group a Segment is cut from has been through the object store and back. The
// two are only ever equal because publish and assemble are inverses, and when
// they stopped being inverses -- assembly dropping the no-data section -- every
// Segment cut during it named an object the manifest did not, the cutover
// refused the mismatch on every later round, and the fleet stopped taking up
// published content for eleven hours with nothing else reporting a problem.
//
// This is the equality itself, stated on the two functions rather than on a
// deployment. It complements the reflection guard on the same round trip: that
// one holds every field to coming back, this one holds the whole object to
// hashing the same. A field could in principle come back changed rather than
// missing, and only this side would see it.
func TestWhatAPublicationNamesIsWhatComesBackOutOfIt(t *testing.T) {
	scope := &contract.TargetScopeV2{}
	identity := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "1001"}
	strategy := contract.StrategyRefV2{TenantID: "tenant", StrategyID: "1001", Revision: "r1", SnapshotRevision: 7}

	for name, plan := range map[string]contract.EvaluationPlanV2{
		"a Plan that does not detect no-data": {PlanID: "1", StrategyRef: strategy},
		"a Plan that does": {PlanID: "1", StrategyRef: strategy,
			NoData: &contract.NoDataConfigV1{Continuous: 1, Level: 2, AggDimension: []string{"bk_target_ip"}}},
		"a Plan with an empty aggregation": {PlanID: "1", StrategyRef: strategy,
			NoData: &contract.NoDataConfigV1{Continuous: 3, Level: 1, AggDimension: []string{}}},
		"a Plan with no target scope": {PlanID: "1", StrategyRef: strategy, TargetScope: nil},
		"a Plan with a target scope":  {PlanID: "1", StrategyRef: strategy, TargetScope: scope},
		"a Plan with both": {PlanID: "1", StrategyRef: strategy, TargetScope: scope,
			NoData: &contract.NoDataConfigV1{Continuous: 2, Level: 3}},
	} {
		t.Run(name, func(t *testing.T) {
			// The IR's reference is the Plan's, which is what a compiled Plan
			// has. The publisher stores it stripped to the identity and the
			// assembly restores the revisions from the output context, so the
			// round trip preserves it only when the two are the same reference
			// -- a fixture that leaves them different asks this equality to
			// hold for a state no compiled Plan is in.
			plan.StrategyIR.StrategyRef = plan.StrategyRef
			group := QueryGroup{Identity: "qg-a", Plans: []FrozenPlan{{Identity: identity, Plan: plan}}}

			// What the publication writes and names it by.
			payload, err := contract.CanonicalJSONV2(BuildQueryGroupObject(group))
			if err != nil {
				t.Fatal(err)
			}
			named, err := DeriveQueryGroupObjectDigest(group)
			if err != nil {
				t.Fatal(err)
			}
			contextPayload, err := contract.CanonicalJSONV2(BuildOutputContext(group.Plans[0]))
			if err != nil {
				t.Fatal(err)
			}
			namedContext, err := DeriveOutputContextDigest(group.Plans[0])
			if err != nil {
				t.Fatal(err)
			}

			// What a reader gets back for it.
			var object QueryGroupObject
			if err := json.Unmarshal(payload, &object); err != nil {
				t.Fatal(err)
			}
			var outputContext OutputContextObject
			if err := json.Unmarshal(contextPayload, &outputContext); err != nil {
				t.Fatal(err)
			}
			assembled, err := AssembleQueryGroup(object,
				map[execution.PlanIdentity]OutputContextObject{identity: outputContext})
			if err != nil {
				t.Fatal(err)
			}

			back, err := DeriveQueryGroupObjectDigest(assembled)
			if err != nil {
				t.Fatal(err)
			}
			if back != named {
				t.Fatalf("the publication names %s and what comes back hashes to %s. Publish and assemble "+
					"are no longer inverses, so every Segment cut from an assembled group will name an "+
					"object the manifest does not", named, back)
			}
			backContext, err := DeriveOutputContextDigest(assembled.Plans[0])
			if err != nil {
				t.Fatal(err)
			}
			if backContext != namedContext {
				t.Fatalf("the publication names output context %s and what comes back hashes to %s",
					namedContext, backContext)
			}
		})
	}
}
