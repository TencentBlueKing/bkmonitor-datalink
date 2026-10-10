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

const stateVersionConflictMetric = "bkmonitor_alarmd_state_version_conflict_total"

func gatherStateVersionConflict(t *testing.T, observations ...observability.Observation) map[string]float64 {
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
		if family.GetName() != stateVersionConflictMetric {
			continue
		}
		for _, series := range family.Metric {
			labels := map[string]string{}
			for _, pair := range series.Label {
				labels[pair.GetName()] = pair.GetValue()
			}
			gathered[labels["site"]+"/"+labels["kind"]] = series.GetCounter().GetValue()
		}
	}
	return gathered
}

// The kind is what separates a key that expired from a key another writer
// moved, and a healthy deployment reads zero on all of them: every site/kind
// pair is published from the first scrape so absent and zero read apart, an
// observation lands on exactly the pair it names, and a kind this build does
// not name lands on other rather than on a label of its own.
func TestStateVersionConflictPublishesEveryPairAndCountsBySiteAndKind(t *testing.T) {
	kinds := []string{"missing", "revision_moved", "revision_reset", "same_version_other_statement", "version_incomparable", "other"}
	gathered := gatherStateVersionConflict(t)
	for _, site := range []string{"preflight", "apply"} {
		for _, kind := range kinds {
			if value, found := gathered[site+"/"+kind]; !found || value != 0 {
				t.Fatalf("%s/%s before any observation = %v (found=%v), want published at zero", site, kind, value, found)
			}
		}
	}
	if len(gathered) != 2*len(kinds) {
		t.Fatalf("published pairs = %d, want %d; gathered=%v", len(gathered), 2*len(kinds), gathered)
	}
	var facts observability.StateVersionConflictFacts
	facts.Record(observability.StateAlreadyAppliedAtApply, observability.StateVersionConflictMissing, "series-1", 7, 0, "", false)
	facts.Record(observability.StateAlreadyAppliedAtApply, observability.StateVersionConflictMissing, "series-2", 7, 0, "", false)
	facts.Record(observability.StateAlreadyAppliedAtApply, observability.StateVersionConflictRevisionMoved, "series-3", 7, 9, "PERSISTED_NEWER", false)
	facts.Record(observability.StateAlreadyAppliedAtPreflight, observability.StateVersionConflictSameVersionOtherStatement, "series-4", 3, 3, "PERSISTED_EQUAL", false)
	facts.Record(observability.StateAlreadyAppliedAtApply, observability.StateVersionConflictKind("something_new"), "series-5", 1, 2, "", false)
	facts.Record(observability.StateAlreadyAppliedAtApply, observability.StateVersionConflictKind(""), "series-6", 1, 2, "", false)
	gathered = gatherStateVersionConflict(t, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageStateApplied,
		Result: observability.ResultFailed, ReasonCode: "STATE_VERSION_CONFLICT", StateVersionConflict: &facts,
	})
	want := map[string]float64{"apply/missing": 2, "apply/revision_moved": 1, "preflight/same_version_other_statement": 1, "apply/other": 2}
	for key, value := range gathered {
		if want[key] != value {
			t.Fatalf("%s = %v, want %v; gathered=%v", key, value, want[key], gathered)
		}
	}
	if len(facts.Samples) != 4 || facts.Samples[0].SeriesIdentity != "series-1" || facts.Samples[1].StoredRevision != 9 ||
		facts.Samples[2].Site != observability.StateAlreadyAppliedAtPreflight || facts.Samples[3].Kind != "other" {
		t.Fatalf("samples = %+v, want the first of each kind in first-seen order, the unnamed ones folded into one other", facts.Samples)
	}
}
