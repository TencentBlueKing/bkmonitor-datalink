// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/platformsettings"
	"gopkg.in/yaml.v3"
)

func TestFTAStorageRoutingYAMLAndFrozenRuntimeFacts(t *testing.T) {
	var cfg config.Config
	err := yaml.Unmarshal([]byte(`phase_two:
  control:
    legacy_query_runtime:
      fta_event_storage:
        table_id: fta.events
        storage_id: "17"
        storage_type: elasticsearch
        db: bkfta_event_*_read
        measurement: __default__
        need_add_time: false
        time_field:
          name: time
          type: date
          unit: millisecond
        source_type: event
`), &cfg)
	if err != nil {
		t.Fatal(err)
	}
	route := cfg.PhaseTwo.Control.LegacyQueryRuntime.FTAEventStorage
	if route == nil || route.StorageID != "17" || route.TimeField.Unit != "millisecond" || route.DB != "bkfta_event_*_read" || route.TimeField.Name != "time" {
		t.Fatalf("route=%+v", route)
	}
	facts := legacyQueryRuntimeFacts(cfg, platformsettings.CodeDefaults())
	route.DB = "mutated"
	if facts.FTAEventStorage == route || facts.FTAEventStorage.DB != "bkfta_event_*_read" {
		t.Fatal("runtime facts retain mutable config pointer")
	}
}
