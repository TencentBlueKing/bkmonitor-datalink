// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import "testing"

func TestQueryCooldownDefaultEnabled(t *testing.T) {
	cfg := Default().PhaseTwo.Scheduler
	if !cfg.RecoveryLimits().QueryUnavailableCooldown {
		t.Fatal("product default must enable cooldown")
	}
	cfg.QueryUnavailableCooldown = false
	if cfg.RecoveryLimits().QueryUnavailableCooldown {
		t.Fatal("explicit disable was not passed to scheduler")
	}
}

func TestQueryCooldownStrictYAML(t *testing.T) {
	for _, value := range []string{"", "true", "false"} {
		text := validGoAccessRuntimeConfigYAML("cooldown-worker")
		if value != "" {
			text += "  scheduler:\n    query_unavailable_cooldown: " + value + "\n"
		}
		cfg, err := Load(writeConfig(t, text))
		if err != nil || cfg.PhaseTwo.Scheduler.QueryUnavailableCooldown != (value != "false") {
			t.Fatalf("value=%s cfg=%+v error=%v", value, cfg.PhaseTwo.Scheduler, err)
		}
	}
}
