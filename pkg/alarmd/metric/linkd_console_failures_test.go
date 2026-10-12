// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import "testing"

// Each operation's failures are counted under every class the source was
// bound with, a class with none at zero, so a zero is a reading; the classes
// of one operation add up to its failed cell of the calls family.
func TestTheConsoleFailuresAreCountedUnderEveryBoundClass(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	ops, classes := []string{"roster", "reconcile"}, []string{"timeout", "connection_reset", "status", "other"}
	recorder.SetLinkdConsoleSource([]string{"reachable"}, ops, classes, func() LinkdConsoleReading {
		return LinkdConsoleReading{State: "reachable", Calls: map[string]LinkdConsoleCalls{
			"reconcile": {Calls: 9, Failures: 5, FailuresByClass: map[string]uint64{"timeout": 2, "connection_reset": 3}},
		}}
	})
	families, err := recorder.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	cells := map[string]float64{}
	failed := map[string]float64{}
	for _, family := range families {
		for _, series := range family.Metric {
			labels := map[string]string{}
			for _, pair := range series.Label {
				labels[pair.GetName()] = pair.GetValue()
			}
			switch family.GetName() {
			case "bkmonitor_alarmd_linkd_console_failures_total":
				cells[labels["op"]+"/"+labels["class"]] = series.GetCounter().GetValue()
			case "bkmonitor_alarmd_linkd_console_calls_total":
				if labels["result"] == "failed" {
					failed[labels["op"]] = series.GetCounter().GetValue()
				}
			}
		}
	}
	if len(cells) != len(ops)*len(classes) {
		t.Fatalf("%d cells, want every operation by every class: %v", len(cells), cells)
	}
	if cells["reconcile/timeout"] != 2 || cells["reconcile/connection_reset"] != 3 || cells["reconcile/status"] != 0 || cells["roster/timeout"] != 0 {
		t.Fatalf("cells %v", cells)
	}
	for _, op := range ops {
		sum := 0.0
		for _, class := range classes {
			sum += cells[op+"/"+class]
		}
		if sum != failed[op] {
			t.Fatalf("%s: classes add up to %v, the failed cell says %v", op, sum, failed[op])
		}
	}
}
