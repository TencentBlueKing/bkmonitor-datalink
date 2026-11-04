// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package editclose

import (
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// planWithFacts is current() with the facts its Plans state: metric m1,
// grouping device+ip, no-data grouping ip.
func planWithFacts() Plan {
	plan := current()
	plan.Facts = &contract.StrategyFactsV2{Schema: "v2", MetricDigest: "m1", ConditionMD5: "c1", ConditionBasis: "query",
		DimensionDigest: contract.GroupingDigest([]string{"device", "ip"}), NoDataDimensionDigest: contract.GroupingDigest([]string{"ip"})}
	return plan
}

// groupOf is the group the alerts of a version with these facts fall in:
// the facts its events carried (ForEvent) and the levels it detected.
func groupOf(revision int64, family string, plan Plan, levels ...string) Group {
	return Group{Revision: revision, Family: family, Facts: plan.Facts.ForEvent(family == "no_data"), Levels: levels, LevelsWhole: true}
}

// Wherever a group decides, its verdict is the one Decide gives each alert
// in it - the alert's record built from what its opening event wrote,
// dimensions and severity included - so closing the group closes exactly
// the alerts Decide would have closed one by one.
func TestAGroupDecidesAsEachOfItsAlertsWould(t *testing.T) {
	now := planWithFacts()
	metricMoved := planWithFacts()
	metricMoved.Facts.MetricDigest = "m2"
	deviceDropped := planWithFacts()
	deviceDropped.DimensionFields = []string{"ip"}
	deviceDropped.Facts.DimensionDigest = contract.GroupingDigest([]string{"ip"})
	noDataOff := planWithFacts()
	noDataOff.NoData, noDataOff.NoDataDimensions, noDataOff.Facts.NoDataDimensionDigest = false, nil, ""
	noDataRegrouped := planWithFacts()
	noDataRegrouped.NoDataDimensions = []string{"ip", "device"}
	noDataRegrouped.Facts.NoDataDimensionDigest = contract.GroupingDigest([]string{"ip", "device"})
	for name, test := range map[string]struct {
		was, now Plan
		family   string
		want     string
	}{
		"nothing it has changed":         {now, planWithFacts(), "metric_algorithm", OutcomeKept},
		"the metric changed":             {now, metricMoved, "metric_algorithm", OutcomeMetricChanged},
		"a grouping dimension dropped":   {now, deviceDropped, "metric_algorithm", OutcomeDimensionsChanged},
		"no-data switched off":           {now, noDataOff, "no_data", OutcomeNoDataDisabled},
		"no-data regrouped":              {now, noDataRegrouped, "no_data", OutcomeDimensionsChanged},
		"no-data metric changed":         {now, metricMoved, "no_data", OutcomeMetricChanged},
		"a no-data edit that changed it": {now, planWithFacts(), "no_data", OutcomeKept},
	} {
		facts := test.was.Facts.ForEvent(test.family == "no_data")
		raw, _ := json.Marshal(facts)
		alert := threshold(8, "warning", "ip", "device")
		if test.family == "no_data" {
			alert = noData(8, "ip")
		}
		alert.Facts = raw
		want, _ := Decide(test.now, alert)
		got, _ := DecideGroup(test.now, groupOf(8, test.family, test.was, "critical", "warning", "info"))
		if got != want || got != test.want {
			t.Fatalf("%s: the group decided %s, each alert %s, want %s", name, got, want, test.want)
		}
	}
}

// What a group cannot decide for all its alerts is left to each alert, and
// a version past the current one is not a candidate, as Decide has it.
func TestAGroupLeavesToEachAlertWhatOnlyItCanSay(t *testing.T) {
	now := planWithFacts()
	withoutWarning := planWithFacts()
	withoutWarning.Levels = []contract.LevelDefinitionV2{{LevelID: 1}, {LevelID: 3}}
	partial := planWithFacts()
	partial.LevelsWhole = false
	dynamic := planWithFacts()
	dynamic.DynamicDimensions, dynamic.Facts.DimensionDigest = true, ""
	for name, test := range map[string]struct {
		plan  Plan
		group Group
		want  string
	}{
		"a level the version detected was removed": {withoutWarning, groupOf(8, "metric_algorithm", now, "critical", "warning"), OutcomeGroupLevelUndecided},
		"the version's levels are all still there": {withoutWarning, groupOf(8, "metric_algorithm", now, "critical", "info"), OutcomeKept},
		"a level of the strategy refused now":      {partial, groupOf(8, "metric_algorithm", now, "critical"), OutcomeGroupLevelUndecided},
		"a dynamic grouping now":                   {dynamic, groupOf(8, "metric_algorithm", now, "critical"), OutcomeGroupDimensionsUndecided},
		"a dynamic grouping then":                  {now, groupOf(8, "metric_algorithm", dynamic, "critical"), OutcomeGroupDimensionsUndecided},
		"the current version":                      {now, groupOf(9, "metric_algorithm", now, "critical"), OutcomeCurrentRevision},
		// A version past the current one: the publication has not caught
		// up, so nothing is decided, as for each alert of it.
		"a larger version": {now, groupOf(10, "metric_algorithm", now, "critical"), OutcomeRevisionAhead},
		"no version":       {now, groupOf(0, "metric_algorithm", now), OutcomeRevisionUnknown},
		// A version of the publisher's earlier microsecond scheme
		// (synthetic): left to each alert's record.
		"a microsecond version": {now, groupOf(1700000000000000, "metric_algorithm", now, "critical"), OutcomeRevisionIncomparable},
		"an unknown family":     {now, Group{Revision: 8, Family: "other", Facts: *now.Facts}, OutcomeFamilyUnknown},
	} {
		if got, _ := DecideGroup(test.plan, test.group); got != test.want {
			t.Fatalf("%s: %s, want %s", name, got, test.want)
		}
	}
	metricMoved := planWithFacts()
	metricMoved.Facts.MetricDigest = "m2"
	if got, _ := DecideGroup(metricMoved, groupOf(10, "metric_algorithm", now)); got != OutcomeRevisionAhead {
		t.Fatalf("a version ahead whose metric changed: %s, want revision_ahead", got)
	}
	alert := threshold(10, "warning", "ip", "device")
	if got, _ := Decide(metricMoved, alert); got != OutcomeRevisionAhead {
		t.Fatalf("each alert of it: %s", got)
	}
}
