// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"reflect"
	"strings"
	"testing"
)

// programFactsConfig is the go_access fixture with the four program facts
// either stated at the values every deployment states today, or absent.
func programFactsConfig(stated bool) string {
	text := validGoAccessRuntimeConfigYAML("facts-worker")
	if stated {
		return text + "  scheduler:\n    expired_range_enabled: true\n"
	}
	for _, line := range []string{
		"  state_prefix: alarmd:phase-two:g2:v1\n",
		"    timezone: Asia/Shanghai\n",
		"    query_source: alarmd\n",
	} {
		if !strings.Contains(text, line) {
			panic("fixture no longer states " + strings.TrimSpace(line))
		}
		text = strings.Replace(text, line, "", 1)
	}
	return text
}

// The state prefix, timezone, query source and range switch are program
// facts: a deployment that states them at the values every deployment
// states today loads to exactly what a deployment that omits them loads
// to, which is the proof a values file needs before those lines are removed.
// Stating another value still takes effect, and an unparseable timezone is
// still refused.
func TestProgramFactsLoadTheSameStatedOrAbsent(t *testing.T) {
	stated, err := Load(writeConfig(t, programFactsConfig(true)))
	if err != nil {
		t.Fatalf("stated: %v", err)
	}
	// The fixture states its own test prefix; the deployment value is what
	// the default has to equal, so it is compared on the absent side.
	stated.Redis.StatePrefix = DefaultStatePrefix
	absent, err := Load(writeConfig(t, programFactsConfig(false)))
	if err != nil {
		t.Fatalf("absent: %v", err)
	}
	if absent.Redis.StatePrefix != DefaultStatePrefix || absent.PhaseTwo.Control.Timezone != DefaultTimezone ||
		absent.PhaseTwo.Access.QuerySource != DefaultQuerySource || !absent.PhaseTwo.Scheduler.ExpiredRangeEnabled {
		t.Fatalf("absent facts = prefix %q tz %q source %q range %t", absent.Redis.StatePrefix, absent.PhaseTwo.Control.Timezone,
			absent.PhaseTwo.Access.QuerySource, absent.PhaseTwo.Scheduler.ExpiredRangeEnabled)
	}
	if !reflect.DeepEqual(stated, absent) {
		t.Fatalf("stated and absent program facts load differently:\nstated = %+v\nabsent = %+v", stated, absent)
	}
	// The override is spliced into the fixture's existing control block: a
	// second control block would be a duplicate key the strict decoder refuses.
	text := strings.Replace(programFactsConfig(false), "    strategy_cache_prefix: alarm-config\n", "    strategy_cache_prefix: alarm-config\n    timezone: UTC\n", 1)
	overridden, err := Load(writeConfig(t, text))
	if err != nil {
		t.Fatal(err)
	}
	if overridden.PhaseTwo.Control.Timezone != "UTC" {
		t.Fatalf("a stated timezone must take effect, got %q", overridden.PhaseTwo.Control.Timezone)
	}
	text = strings.Replace(programFactsConfig(false), "    strategy_cache_prefix: alarm-config\n", "    strategy_cache_prefix: alarm-config\n    timezone: Mars/Olympus\n", 1)
	if _, err := Load(writeConfig(t, text)); err == nil {
		t.Fatal("an unknown timezone must still be refused")
	}
}
