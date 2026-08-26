// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v2

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestDatasetContractViewSnapshotIsDeeplyIsolated(t *testing.T) {
	source := contract.DatasetContractV2{
		SchemaDigest: "schema", NormalizationDigest: "normalization", IdentityFields: []string{"host"},
		SourceTimeField: "time", CollectionTimeField: "collection_time", ReceivedTimeField: "received_time",
	}
	view := newDatasetContractView(source)
	source.IdentityFields[0] = "mutated-source"

	snapshot := view.Snapshot()
	if snapshot.SchemaDigest != "schema" || snapshot.NormalizationDigest != "normalization" ||
		snapshot.SourceTimeField != "time" || snapshot.CollectionTimeField != "collection_time" ||
		snapshot.ReceivedTimeField != "received_time" || len(snapshot.IdentityFields) != 1 || snapshot.IdentityFields[0] != "host" {
		t.Fatalf("Snapshot() = %#v, want complete original DatasetContract", snapshot)
	}
	snapshot.IdentityFields[0] = "mutated-snapshot"
	if got := view.Snapshot().IdentityFields[0]; got != "host" {
		t.Fatalf("Snapshot() leaked mutable identity fields: %q", got)
	}
}

func TestAdapterPlanViewSnapshotPreservesSourceCompatibility(t *testing.T) {
	envelope := validEnvelope(t, 1)
	envelope.PlanSet.EvaluationPlans[0].SourceCompatibility = &contract.SourceCompatibilityV2{ItemID: "item-1"}

	result, err := New(readerLimits()).Decode(context.Background(), encodeEnvelope(t, envelope))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if result.Rejected || result.Input == nil || len(result.Input.PlanViews()) != 1 {
		t.Fatalf("Decode() = %#v, want one accepted Plan", result)
	}
	snapshot := result.Input.PlanViews()[0].Snapshot()
	if snapshot.SourceCompatibility == nil || snapshot.SourceCompatibility.ItemID != "item-1" {
		t.Fatalf("Snapshot().SourceCompatibility = %#v, want item-1", snapshot.SourceCompatibility)
	}
}

func TestPlanViewSnapshotPreservesCompletePlanAndIsDeeplyIsolated(t *testing.T) {
	source := validPlan("1001", 5)
	source.SourceCompatibility = &contract.SourceCompatibilityV2{ItemID: "item-1"}
	source.StrategyIR.RequiredFeatures = []string{"strategy-feature"}
	view := newPlanView(0, source, source.StrategyIR, contract.SelectorIndexViewV2{}, nil)

	source.InputProjection.ValueFields[0] = "mutated-source"
	source.SourceCompatibility.ItemID = "mutated-source"
	source.StrategyIR.RequiredFeatures[0] = "mutated-source"
	source.StrategyIR.InputProjection.DimensionFields[0] = "mutated-source"
	source.StrategyIR.Levels[0].DetectPlan.Algorithms[0].Config[0] = 'x'
	source.StrategyIR.Levels[0].TriggerPlan.Config[0] = 'x'
	source.StrategyIR.Levels[0].RecoveryPlan.Config[0] = 'x'

	snapshot := view.Snapshot()
	assertPlanSnapshot(t, snapshot)
	snapshot.InputProjection.ValueFields[0] = "mutated-snapshot"
	snapshot.SourceCompatibility.ItemID = "mutated-snapshot"
	snapshot.StrategyIR.RequiredFeatures[0] = "mutated-snapshot"
	snapshot.StrategyIR.InputProjection.DimensionFields[0] = "mutated-snapshot"
	snapshot.StrategyIR.Levels[0].DetectPlan.Algorithms[0].Config[0] = 'y'
	snapshot.StrategyIR.Levels[0].TriggerPlan.Config[0] = 'y'
	snapshot.StrategyIR.Levels[0].RecoveryPlan.Config[0] = 'y'

	assertPlanSnapshot(t, view.Snapshot())
	strategy := view.Strategy().Snapshot()
	if strategy.RequiredFeatures[0] != "strategy-feature" ||
		string(strategy.Levels[0].DetectPlan.Algorithms[0].Config) != `{"method":"gt","threshold":40}` {
		t.Fatalf("Strategy() leaked Snapshot mutation: %#v", strategy)
	}
}

func assertPlanSnapshot(t *testing.T, snapshot contract.EvaluationPlanV2) {
	t.Helper()
	if snapshot.PlanID != "1001" || snapshot.StrategyRef.StrategyID != "1001" ||
		snapshot.SourceCompatibility == nil || snapshot.SourceCompatibility.ItemID != "item-1" ||
		len(snapshot.InputProjection.ValueFields) != 1 || snapshot.InputProjection.ValueFields[0] != "value" ||
		len(snapshot.StrategyIR.RequiredFeatures) != 1 || snapshot.StrategyIR.RequiredFeatures[0] != "strategy-feature" ||
		snapshot.StrategyIR.InputProjection.DimensionFields[0] != "host" ||
		string(snapshot.StrategyIR.Levels[0].DetectPlan.Algorithms[0].Config) != `{"method":"gt","threshold":40}` ||
		string(snapshot.StrategyIR.Levels[0].TriggerPlan.Config) != `{"m":1,"n":1}` ||
		string(snapshot.StrategyIR.Levels[0].RecoveryPlan.Config) != `{"m":1,"n":1}` {
		encoded, _ := json.Marshal(snapshot)
		t.Fatalf("Snapshot() = %s, want complete original EvaluationPlan", encoded)
	}
}
