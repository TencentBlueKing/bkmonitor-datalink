// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
)

type stubWakeSource struct {
	wakes []fleet.OverdueWake
	total int
	limit int
}

func (source *stubWakeSource) OverdueWakes(_ time.Time, limit int) ([]fleet.OverdueWake, int) {
	source.limit = limit
	return source.wakes, source.total
}

// A deployment with no due index reports nothing here, not zero. Zero is the
// good news -- "every object is being reached on time" -- and reporting it for
// a deployment that holds no wake times at all would be the page's most
// reassuring output in the case where it knows the least.
func TestADeploymentWithNoDueIndexReportsNoOverdueFactsRatherThanZero(t *testing.T) {
	at := time.Date(2026, 9, 11, 16, 0, 0, 0, time.UTC)

	anomalies, facts := publisherOverdue(nil, at, "pod-a", nil)

	if facts != nil {
		t.Fatalf("facts = %+v, want absence from a deployment with no index", facts)
	}
	if len(anomalies) != 0 {
		t.Fatalf("anomalies = %d, want none", len(anomalies))
	}
}

// An index that holds wake times and has nothing overdue reports a measured
// zero. Without this the fix above could be "never report anything" and still
// pass, and the page would lose the one reading that says objects are on time.
func TestAnIndexWithNothingOverdueReportsAMeasuredZero(t *testing.T) {
	at := time.Date(2026, 9, 11, 16, 0, 0, 0, time.UTC)

	_, facts := publisherOverdue(&stubWakeSource{}, at, "pod-a", nil)

	if facts == nil {
		t.Fatal("an index that answered was reported as absent")
	}
	if facts.Total != 0 {
		t.Fatalf("total = %d, want a measured zero", facts.Total)
	}
}

// The objects reach the list identified the way every other row is. An overdue
// object produced no round, so the tracker learned nothing about it from a
// trace; if the strategies were not attached here it would arrive as a bare
// hash beside rows that name a strategy, and nobody could act on it.
func TestAnOverdueObjectArrivesNamedByItsStrategies(t *testing.T) {
	at := time.Date(2026, 9, 11, 16, 0, 0, 0, time.UTC)
	source := &stubWakeSource{
		wakes: []fleet.OverdueWake{{
			QueryGroup: "qg-parked", WakeAt: at.Add(-10 * time.Minute), IntervalSeconds: 60,
		}},
		total: 1,
	}
	strategies := func(queryGroup string) []fleet.StrategyRef {
		if queryGroup != "qg-parked" {
			return nil
		}
		return []fleet.StrategyRef{{StrategyID: "4321", BusinessID: "7"}}
	}

	anomalies, facts := publisherOverdue(source, at, "pod-a", strategies)

	if len(anomalies) != 1 {
		t.Fatalf("anomalies = %d, want the parked object reported", len(anomalies))
	}
	if len(anomalies[0].Strategies) != 1 || anomalies[0].Strategies[0].StrategyID != "4321" {
		t.Fatalf("strategies = %+v, want the object named", anomalies[0].Strategies)
	}
	if facts.Total != 1 {
		t.Fatalf("total = %d, want the parked object counted", facts.Total)
	}
}

// The publish is a bounded write. After a fail-open tick every owned object is
// briefly past its wake time, and asking for all of them would make the worst
// moment for the deployment the largest write this replica performs.
func TestThePublishAsksForABoundedNumberOfParkedObjects(t *testing.T) {
	at := time.Date(2026, 9, 11, 16, 0, 0, 0, time.UTC)
	source := &stubWakeSource{}

	publisherOverdue(source, at, "pod-a", nil)

	if source.limit != fleetOverdueWakeCeiling || source.limit <= 0 {
		t.Fatalf("limit = %d, want the publish ceiling %d", source.limit, fleetOverdueWakeCeiling)
	}
}
