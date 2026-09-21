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
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
)

func TestExpiredRangeProfileTracksResolvedFlagOnly(t *testing.T) {
	// Range finalization is on by default; the switch is the rollback path,
	// so the profile is read with it off as the change.
	cfg := config.Default()
	enabled, err := phaseTwoRuntimeProfile(cfg, "cpu_quota", 8)
	if err != nil {
		t.Fatal(err)
	}
	if !enabled.Capacity.ExpiredRangeEnabled {
		t.Fatal("default range disabled")
	}
	cfg.PhaseTwo.Scheduler.ExpiredRangeEnabled = false
	disabled, err := phaseTwoRuntimeProfile(cfg, "cpu_quota", 8)
	if err != nil {
		t.Fatal(err)
	}
	if !enabled.Capacity.ExpiredRangeEnabled || enabled.Digest == disabled.Digest {
		t.Fatal("resolved switch absent from capacity digest")
	}
	wire, err := json.Marshal(enabled.Capacity)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(wire, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["expired_range_enabled"] != true {
		t.Fatalf("safe resolved flag not exported: %s", wire)
	}
	enabled.Capacity.ExpiredRangeEnabled = false
	if enabled.Capacity != disabled.Capacity {
		t.Fatal("the switch changed unrelated resource capacity")
	}
}
