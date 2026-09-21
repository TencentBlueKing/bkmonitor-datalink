// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestFrozenLegacyOutputSharesWithoutExposingMutableStorage(t *testing.T) {
	source := &LegacyOutputContext{Strategy: json.RawMessage(`{"id":1,"bk_biz_id":2,"update_time":3}`), DimensionFields: []string{"host"}, ItemID: "4"}
	frozen := FreezeLegacyOutput(source)
	want := string(frozen.StrategyJSON())
	source.Strategy[0] = 'x'
	source.DimensionFields[0] = "mutated"
	raw := frozen.StrategyJSON()
	raw[0] = 'x'
	fields := frozen.DimensionFields()
	fields[0] = "mutated"
	if string(frozen.StrategyJSON()) != want || frozen.DimensionFields()[0] != "host" {
		t.Fatal("frozen context was mutated")
	}
	event := TriggerEventV1{LegacyOutput: &LegacyEventContext{Configuration: frozen, AnomalyTimestamps: []int64{100}}}
	wire, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(wire, []byte("update_time")) || bytes.Contains(wire, []byte("AnomalyTimestamps")) {
		t.Fatal("internal configuration leaked to wire")
	}
}
