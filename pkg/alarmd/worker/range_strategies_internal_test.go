// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A range's strategies are named once each, in order, however many of the
// strategy's shards the due set holds.
func TestARangeNamesEachStrategyOnce(t *testing.T) {
	plan := func(strategy string, shard int) execution.PlanKey {
		return execution.PlanKey{PlanIdentity: execution.PlanIdentity{TenantID: "tenant-a", BusinessID: "2", StrategyID: strategy}, ShardIndex: shard}
	}
	got := rangeStrategies(execution.FrozenDuePlanTargets{Plans: []execution.PlanKey{plan("7", 0), plan("7", 1), plan("8", 0)}})
	want := []observability.ExpiredRangeStrategy{{BusinessID: "2", StrategyID: "7"}, {BusinessID: "2", StrategyID: "8"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("strategies = %+v, want %+v", got, want)
	}
}
