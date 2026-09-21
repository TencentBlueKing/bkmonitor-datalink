// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"context"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestQueryCooldownMetricHasOnlyBoundedEvent(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	for _, event := range []string{"entered", "extended", "recovered", "config_changed", "disabled", "qg-secret"} {
		r.Observe(context.Background(), observability.Observation{Component: observability.ComponentScheduler, Stage: observability.StageQueryCooldown, Trace: observability.TraceFields{QueryGroupKey: "qg-secret"}, QueryCooldown: &observability.QueryCooldownFacts{Event: event}})
	}
	families, err := r.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, family := range families {
		if family.GetName() != "bkmonitor_alarmd_query_cooldown_events_total" {
			continue
		}
		found = true
		if len(family.Metric) != 6 {
			t.Fatalf("unexpected vocabulary: %v", family)
		}
		for _, m := range family.Metric {
			if len(m.Label) != 1 || m.Label[0].GetName() != "event" || m.Label[0].GetValue() == "qg-secret" {
				t.Fatalf("unbounded labels: %v", m)
			}
		}
	}
	if !found {
		t.Fatal("cooldown metric missing")
	}
}

func TestQueryCooldownDispatchIsNotNotDue(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	r.RecordDispatchSkipped("query_cooldown")
	if got := testutil.ToFloat64(r.phaseTwo.dueIndex.skipped.WithLabelValues("query_cooldown")); got != 1 {
		t.Fatalf("cooldown count %v", got)
	}
	if got := testutil.ToFloat64(r.phaseTwo.dueIndex.skipped.WithLabelValues("not_due")); got != 0 {
		t.Fatalf("not_due count %v", got)
	}
}

func TestFleetCooldownCountRequiresEvidence(t *testing.T) {
	const name = "bkmonitor_alarmd_fleet_query_cooldown_objects"
	if _, ok := gatherFleet(t, FleetVerdict{Health: "UNKNOWN"})[name]; ok {
		t.Fatal("unmeasured cooldown published as zero")
	}
	count := 7
	if got := gatherFleet(t, FleetVerdict{Health: "DEGRADED", QueryCooldown: &count})[name][""]; got != 7 {
		t.Fatalf("cooldown population %v", got)
	}
}
