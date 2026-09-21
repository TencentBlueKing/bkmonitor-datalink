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
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

const stateAlreadyAppliedMetric = "bkmonitor_alarmd_state_already_applied_total"

func gatherStateAlreadyApplied(t *testing.T, observations ...observability.Observation) map[string]float64 {
	t.Helper()
	recorder := NewRecorder(BuildInfo{})
	for _, observation := range observations {
		recorder.Observe(context.Background(), observability.NormalizeObservation(observation))
	}
	families, err := recorder.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	gathered := map[string]float64{}
	for _, family := range families {
		if family.GetName() != stateAlreadyAppliedMetric {
			continue
		}
		for _, series := range family.Metric {
			// Keyed site/kind by name: Prometheus orders labels alphabetically,
			// which here is kind before site.
			labels := map[string]string{}
			for _, pair := range series.Label {
				labels[pair.GetName()] = pair.GetValue()
			}
			gathered[labels["site"]+"/"+labels["kind"]] = series.GetCounter().GetValue()
		}
	}
	return gathered
}

// revision_skew is expected to read zero on a healthy deployment, and a zero
// that only appears on the first increment cannot be told from a family that
// was never registered. Every site/kind pair is published from the first
// scrape, and an observation lands on exactly the pair it names.
func TestStateAlreadyAppliedPublishesEveryPairAndCountsBySiteAndKind(t *testing.T) {
	gathered := gatherStateAlreadyApplied(t)
	for _, site := range []string{"preflight", "apply"} {
		for _, kind := range []string{"stable", "revision_skew", "repeated_key", "other"} {
			if value, found := gathered[site+"/"+kind]; !found || value != 0 {
				t.Fatalf("%s/%s before any observation = %v (found=%v), want published at zero", site, kind, value, found)
			}
		}
	}
	var facts observability.StateAlreadyAppliedFacts
	facts.Record(observability.StateAlreadyAppliedAtApply, observability.StateAlreadyAppliedRevisionSkew, "series-1", 7, 8)
	facts.Record(observability.StateAlreadyAppliedAtApply, observability.StateAlreadyAppliedRevisionSkew, "series-2", 7, 8)
	facts.Record(observability.StateAlreadyAppliedAtPreflight, observability.StateAlreadyAppliedStable, "series-3", 9, 9)
	facts.Record(observability.StateAlreadyAppliedAtPreflight, observability.StateAlreadyAppliedKind("something_new"), "series-4", 1, 2)
	gathered = gatherStateAlreadyApplied(t, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageMutationCompared,
		Result: observability.ResultSuccess, StateAlreadyApplied: &facts,
	})
	want := map[string]float64{"apply/revision_skew": 2, "preflight/stable": 1, "preflight/other": 1,
		"apply/stable": 0, "apply/other": 0, "preflight/revision_skew": 0, "apply/repeated_key": 0, "preflight/repeated_key": 0}
	for key, value := range want {
		if gathered[key] != value {
			t.Fatalf("%s = %v, want %v; gathered=%v", key, gathered[key], value, gathered)
		}
	}
	if facts.Skew == nil || facts.Skew.SeriesIdentity != "series-1" || facts.Skew.ExpectedRevision != 7 || facts.Skew.StoredRevision != 8 {
		t.Fatalf("skew sample = %+v, want the first revision_skew recorded", facts.Skew)
	}
}
