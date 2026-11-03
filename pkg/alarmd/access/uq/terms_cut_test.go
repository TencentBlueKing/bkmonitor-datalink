// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uq

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func seriesOf(groups map[string]string) responseSeries {
	series := responseSeries{}
	for key, value := range groups {
		series.GroupKeys = append(series.GroupKeys, key)
		series.GroupValues = append(series.GroupValues, json.RawMessage(fmt.Sprintf("%q", value)))
	}
	return series
}

// A level is suspected cut when some group of series agreeing on every other
// dimension holds exactly the cap of distinct values: one dimension at the
// cap is, one below is not; with two dimensions, an outer value whose inner
// level holds the cap is, and the same series spread so no outer value
// reaches it is not.
func TestALevelAtExactlyTheCapIsSuspectedCut(t *testing.T) {
	single := func(n int) *termsCutCounter {
		counter := &termsCutCounter{source: "custom/event"}
		for index := 0; index < n; index++ {
			counter.add(seriesOf(map[string]string{"host": fmt.Sprintf("h-%d", index)}))
		}
		return counter
	}
	if dimension, cut := single(esTermsCap).suspected(); !cut || dimension != "host" {
		t.Fatalf("one dimension at the cap: %q %v, want host suspected", dimension, cut)
	}
	if _, cut := single(esTermsCap - 1).suspected(); cut {
		t.Fatal("one dimension below the cap was suspected")
	}
	nested := &termsCutCounter{source: "bk_log_search/log"}
	for index := 0; index < esTermsCap; index++ {
		nested.add(seriesOf(map[string]string{"cluster": "c-1", "pod": fmt.Sprintf("p-%d", index)}))
	}
	nested.add(seriesOf(map[string]string{"cluster": "c-2", "pod": "p-0"}))
	if dimension, cut := nested.suspected(); !cut || dimension != "pod" {
		t.Fatalf("an inner level at the cap under one outer value: %q %v, want pod suspected", dimension, cut)
	}
	spread := &termsCutCounter{source: "bk_log_search/log"}
	for index := 0; index < esTermsCap; index++ {
		spread.add(seriesOf(map[string]string{"cluster": fmt.Sprintf("c-%d", index%2), "pod": fmt.Sprintf("p-%d", index)}))
	}
	if dimension, cut := spread.suspected(); cut {
		t.Fatalf("no outer value reaches the cap, yet %s was suspected", dimension)
	}
}

// Only an answer from a source answered through Elasticsearch terms, grouped
// by something, is counted at all.
func TestOnlyAnElasticsearchFamilyGroupedAnswerIsCounted(t *testing.T) {
	spec := validAttempt(t).Spec
	if newTermsCutCounter(spec) != nil {
		t.Fatal("an answer without source semantics is counted")
	}
	spec.PlanFacts.SourceSemantics = []string{"bk_monitor/time_series"}
	if newTermsCutCounter(spec) != nil {
		t.Fatal("a time series answer, not answered through Elasticsearch terms, is counted")
	}
	spec.PlanFacts.SourceSemantics = []string{"custom/event"}
	if newTermsCutCounter(spec) == nil {
		t.Fatal("a grouped event count is not counted")
	}
	spec.PlanFacts.Normalization.DatasetContract.IdentityFields = nil
	if newTermsCutCounter(spec) != nil {
		t.Fatal("an ungrouped answer is counted: it has no terms level")
	}
}

// Through the client: an event count answered with exactly the cap of
// groups is completed with the suspected cut on its route, and its series
// are delivered as they are; one group fewer carries nothing.
func TestAnAnswerAtTheCapCarriesTheSuspectedCut(t *testing.T) {
	answer := func(groups int) string {
		series := make([]string, 0, groups)
		for index := 0; index < groups; index++ {
			series = append(series, fmt.Sprintf(`{"name":"_result0","columns":["_time","_value"],"types":["float","float"],`+
				`"group_keys":["bk_target_ip"],"group_values":["h-%d"],"values":[[1700123940000,1]]}`, index))
		}
		return `{"series":[` + strings.Join(series, ",") + `],"is_partial":false}`
	}
	attempt := validAttempt(t)
	facts := attempt.Spec.PlanFacts
	facts.QueryRevision, facts.SourceSemantics = "", []string{"custom/event"}
	var err error
	if facts, err = execution.BuildQueryPlanFacts(facts); err != nil {
		t.Fatal(err)
	}
	if attempt.Spec, err = execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{PlanFacts: facts,
		LogicalWindow: attempt.Spec.LogicalWindow, ProviderRange: attempt.Spec.ProviderRange,
		AcceptedRange: attempt.Spec.AcceptedRange, RequiredColumns: attempt.Spec.RequiredColumns}); err != nil {
		t.Fatal(err)
	}
	for groups, want := range map[int]bool{esTermsCap: true, esTermsCap - 1: false} {
		client := fixtureClient(t, http.StatusOK, answer(groups), DefaultLimits())
		sink := &collectingSink{}
		completion, err := client.Execute(context.Background(), attempt, sink)
		if err != nil {
			t.Fatal(err)
		}
		cut := completion.RouteFacts.Truncation
		if (cut != nil) != want {
			t.Fatalf("%d groups: truncation %+v, want suspected %v", groups, cut, want)
		}
		if want && (cut.Kind != execution.TruncationTermsCut || cut.Dimension != "bk_target_ip" || cut.Cap != esTermsCap ||
			cut.SourceSemantics != "custom/event") {
			t.Fatalf("truncation %+v, want the terms cut on bk_target_ip at the cap from custom/event", cut)
		}
		if completion.Completeness != execution.CompletenessFull || len(sink.batches) != groups {
			t.Fatalf("%d groups: completeness %s, %d series delivered: the answer is read as it is", groups, completion.Completeness, len(sink.batches))
		}
	}
}
