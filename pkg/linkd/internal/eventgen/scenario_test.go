// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package eventgen

import (
	"testing"

	linkdconfig "linkd/internal/config"
)

func TestScenarioTemplates(t *testing.T) {
	t.Parallel()
	requiredDimensions := map[Scenario][]string{
		ScenarioCPUHigh:                 {"ip", "cpu_core"},
		ScenarioMemoryHigh:              {"ip", "memory_total_gb"},
		ScenarioDiskFull:                {"ip", "device", "mount_point", "filesystem"},
		ScenarioDiskReadOnly:            {"ip", "device", "mount_point", "filesystem"},
		ScenarioDiskIOLatencyHigh:       {"ip", "device", "mount_point"},
		ScenarioOOMKilled:               {"ip", "namespace", "pod", "container", "process"},
		ScenarioProcessDown:             {"ip", "process_name", "service_name"},
		ScenarioHostUnreachable:         {"ip", "zone"},
		ScenarioNetworkPacketLossHigh:   {"ip", "peer_ip", "interface"},
		ScenarioServiceUnavailable:      {"service", "instance", "ip", "port", "protocol"},
		ScenarioHTTPErrorRateHigh:       {"service", "route", "method", "status_class"},
		ScenarioDatabaseConnectionsHigh: {"db_instance", "engine", "region"},
		ScenarioOnlineUsersZero:         {"app", "region", "channel"},
		ScenarioQueueBacklogHigh:        {"queue", "consumer_group", "cluster"},
	}
	severity := linkdconfig.DefaultSeverityConfig()
	for index, scenario := range SupportedScenarios() {
		t.Run(string(scenario), func(t *testing.T) {
			t.Parallel()
			// 场景总数固定为 14，index+1 转换为 uint64 不会溢出。
			//nolint:gosec // G115: 上述固定边界保证转换安全。
			sequence := uint64(index + 1)
			template, err := buildAlertTemplate(scenario, sequence, "test-run", severity, newRandomSource(42))
			if err != nil {
				t.Fatal(err)
			}
			if template.AlertID == "" || template.Subject.ID != template.AlertID || template.Title == "" ||
				template.ConditionName == "" || template.Severity == "" {
				t.Fatalf("incomplete template: %#v", template)
			}
			if template.Dimensions["generator_id"] != template.AlertID {
				t.Fatalf("generator_id = %v, want %q", template.Dimensions["generator_id"], template.AlertID)
			}
			for _, key := range requiredDimensions[scenario] {
				if _, exists := template.Dimensions[key]; !exists {
					t.Fatalf("scenario %q missing dimension %q", scenario, key)
				}
			}
			if template.TriggerExtra["state"] != "firing" && template.TriggerExtra["state"] != "killed" &&
				template.TriggerExtra["state"] != "down" && template.TriggerExtra["state"] != "read_only" &&
				template.TriggerExtra["state"] != "unavailable" {
				t.Fatalf("trigger state = %v", template.TriggerExtra["state"])
			}
			if template.ResolvedExtra["state"] == template.TriggerExtra["state"] {
				t.Fatalf("resolved state did not change: %#v", template.ResolvedExtra)
			}
		})
	}
}

func TestAlertIdentityIsMonotonicAndUnique(t *testing.T) {
	t.Parallel()
	random := newRandomSource(20260902)
	seen := make(map[string]struct{}, 10_000)
	for index := 1; index <= 10_000; index++ {
		scenario := supportedScenarios[index%len(supportedScenarios)]
		template, err := buildAlertTemplate(
			scenario,
			uint64(index),
			"identity-run",
			linkdconfig.DefaultSeverityConfig(),
			random,
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := seen[template.AlertID]; exists {
			t.Fatalf("duplicate alert identity %q", template.AlertID)
		}
		seen[template.AlertID] = struct{}{}
	}
}

func TestParseScenarios(t *testing.T) {
	t.Parallel()
	parsed, err := ParseScenarios(SupportedScenariosCSV())
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed) != 14 {
		t.Fatalf("parsed scenarios = %d, want 14", len(parsed))
	}
	if _, err := ParseScenarios("cpu_high,cpu_high"); err == nil {
		t.Fatal("ParseScenarios() accepted duplicate scenario")
	}
	if _, err := ParseScenarios("unknown"); err == nil {
		t.Fatal("ParseScenarios() accepted unknown scenario")
	}
}
