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
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
)

// oneHotReading is each one-hot family's series on one process: the series
// at 1, and every series it has (build_info's labels are the build's, so its
// set is not compared across processes).
func oneHotReading(t *testing.T, recorder *metric.Recorder) (ones, cells map[string][]string) {
	t.Helper()
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	ones, cells = map[string][]string{}, map[string][]string{}
	for _, name := range metric.OneHotFamilies {
		ones[name], cells[name] = nil, nil
	}
	for _, family := range families {
		if _, listed := ones[family.GetName()]; !listed {
			continue
		}
		for _, sample := range family.Metric {
			labels := ""
			for _, pair := range sample.Label {
				labels += pair.GetName() + "=" + pair.GetValue() + " "
			}
			cells[family.GetName()] = append(cells[family.GetName()], labels)
			if sample.GetGauge().GetValue() == 1 {
				ones[family.GetName()] = append(ones[family.GetName()], labels)
			}
		}
		sort.Strings(cells[family.GetName()])
	}
	return ones, cells
}

// Every process, whatever roles it runs, says exactly one state in each
// one-hot family. A family some role never sets reads all zero there - no
// answer - which is how alarmd_trace_export read on a control-only process.
// The processes are built the way production builds them: the role runtime
// every process opens, and the bundle for a process with control or worker.
func TestEveryProcessSaysOneStateInEachOneHotFamily(t *testing.T) {
	fixture := newElectionFixture(t, 1, "alarmd-one-hot-roles")
	processes := map[string]*metric.Recorder{
		"control-only": fixture.open("one-hot-control", roles.Set{roles.Control}, true).recorder,
		"worker-only":  fixture.open("one-hot-worker", roles.Set{roles.Worker}, true).recorder,
		"all-roles":    fixture.open("one-hot-all", roles.Set{roles.Control, roles.Worker, roles.Channel}, true).recorder,
	}
	// A channel-only process opens no bundle in production
	// (openProductionRoleBundle), only the role runtime.
	channel := metric.NewRecorder(metric.BuildInfo{})
	runtime, err := openPhaseTwoRoleRuntime(fixture.configFor("one-hot-channel", roles.Set{roles.Channel}), channel, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	runtime.Start(context.Background(), newPhaseTwoApplicationHealth())
	processes["channel-only"] = channel

	// And every process exposes the same series in each family, off_role
	// among them, so alerts and joins read alike whatever a Pod's roles.
	seen := map[string]map[string]string{}
	for shape, recorder := range processes {
		ones, cells := oneHotReading(t, recorder)
		for family, at := range ones {
			if len(at) != 1 {
				t.Errorf("%s process: %s has %d series at 1 %v, want exactly one", shape, family, len(at), fmt.Sprint(at))
			}
		}
		for family, set := range cells {
			if family == "alarmd_build_info" {
				continue
			}
			if seen[family] == nil {
				seen[family] = map[string]string{}
			}
			seen[family][shape] = strings.Join(set, "|")
		}
	}
	// A worker's 0/1 that means nothing elsewhere is absent there, not 0: a
	// control-only or channel-only Pod reading view_client_connected 0 would
	// read as a disconnected Worker.
	for shape, recorder := range processes {
		families, err := recorder.Gatherer().Gather()
		if err != nil {
			t.Fatal(err)
		}
		present := false
		for _, family := range families {
			present = present || family.GetName() == "alarmd_view_client_connected"
		}
		if worker := shape == "worker-only" || shape == "all-roles"; present != worker {
			t.Errorf("%s process: alarmd_view_client_connected present %v, want %v (only where the worker role runs)", shape, present, worker)
		}
	}
	for family, byShape := range seen {
		distinct := map[string][]string{}
		for shape, set := range byShape {
			distinct[set] = append(distinct[set], shape)
		}
		if len(distinct) != 1 {
			t.Errorf("%s exposes different series by role shape: %v", family, distinct)
		}
	}
}
