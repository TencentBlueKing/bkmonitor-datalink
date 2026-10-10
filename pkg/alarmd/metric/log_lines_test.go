// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// log_lines_total reads the log observer's counts at scrape time: nothing
// before the observer is bound, then every stage it counts, written and
// limited, with the observer's numbers.
func TestLogLinesReadTheObserverAtScrape(t *testing.T) {
	read := func(recorder *Recorder) map[string]float64 {
		t.Helper()
		families, err := recorder.Gatherer().Gather()
		if err != nil {
			t.Fatal(err)
		}
		values := map[string]float64{}
		for _, family := range families {
			if family.GetName() != "bkmonitor_alarmd_log_lines_total" {
				continue
			}
			for _, sample := range family.GetMetric() {
				labels := map[string]string{}
				for _, label := range sample.GetLabel() {
					labels[label.GetName()] = label.GetValue()
				}
				values[labels["stage"]+"/"+labels["admission"]] = sample.GetCounter().GetValue()
			}
		}
		return values
	}
	recorder := NewRecorder(BuildInfo{})
	if values := read(recorder); len(values) != 0 {
		t.Fatalf("unbound: %v, want nothing before the observer is bound", values)
	}
	recorder.SetLogLineSource(func() observability.LogLineCounts {
		counts := observability.LogLineCounts{Written: map[observability.Stage]uint64{}, Limited: map[observability.Stage]uint64{}}
		for _, stage := range observability.AllStages() {
			counts.Written[stage], counts.Limited[stage] = 0, 0
		}
		counts.Written[observability.StageResourceHard], counts.Limited[observability.StageResourceHard] = 2, 3
		counts.Written[observability.StageOther] = 1
		return counts
	})
	values := read(recorder)
	if len(values) != 2*len(observability.AllStages()) || values["resource_hard/written"] != 2 ||
		values["resource_hard/limited"] != 3 || values["_other/written"] != 1 || values["startup/written"] != 0 {
		t.Fatalf("bound: %d series, resource_hard %v/%v, _other %v; want every stage both ways and the observer's numbers",
			len(values), values["resource_hard/written"], values["resource_hard/limited"], values["_other/written"])
	}
}
