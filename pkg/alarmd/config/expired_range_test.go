// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestExpiredRangeStrictLoadDefaultAndExplicit(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		want        bool
	}{
		{"omitted", "", true}, {"disabled", "false", false}, {"enabled", "true", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := validGoAccessRuntimeConfigYAML("range-worker")
			if tc.value != "" {
				text += "  scheduler:\n    expired_range_enabled: " + tc.value + "\n"
			}
			cfg, err := Load(writeConfig(t, text))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.PhaseTwo.Scheduler.ExpiredRangeEnabled != tc.want {
				t.Fatalf("enabled=%v want=%v", cfg.PhaseTwo.Scheduler.ExpiredRangeEnabled, tc.want)
			}
		})
	}
	// Range finalization is the product's behaviour; the key is kept as the
	// rollback switch (decision-002), which "disabled" above exercises.
	if !Default().PhaseTwo.Scheduler.ExpiredRangeEnabled {
		t.Fatal("product default disables range creation")
	}
}

func TestExpiredRangeStrictLoadRejectsWrongType(t *testing.T) {
	for _, value := range []string{"42", `"true"`, "[]", "{}", "not-a-boolean"} {
		t.Run(value, func(t *testing.T) {
			text := validGoAccessRuntimeConfigYAML("range-worker") + "  scheduler:\n    expired_range_enabled: " + value + "\n"
			if _, err := Load(writeConfig(t, text)); err == nil {
				t.Fatal("wrong type accepted", value)
			}
		})
	}
}

func TestExpiredRangeOldStrictShapeRequiresFieldRemoval(t *testing.T) {
	// A deterministic decoder-shape simulation, not an executed old binary.
	// Removing the new key matters even when its value was explicitly false.
	type oldScheduler struct {
		TickInterval string `yaml:"tick_interval"`
	}
	for _, value := range []string{"true", "false"} {
		var old oldScheduler
		decoder := yaml.NewDecoder(strings.NewReader("tick_interval: 1s\nexpired_range_enabled: " + value + "\n"))
		decoder.KnownFields(true)
		if err := decoder.Decode(&old); err == nil || !strings.Contains(err.Error(), "expired_range_enabled") {
			t.Fatalf("old shape accepted field: %v", err)
		}
	}
	var old oldScheduler
	decoder := yaml.NewDecoder(strings.NewReader("tick_interval: 1s\n"))
	decoder.KnownFields(true)
	if err := decoder.Decode(&old); err != nil {
		t.Fatal(err)
	}
}
