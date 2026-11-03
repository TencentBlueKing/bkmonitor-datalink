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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// current is a strategy at revision 9 grouping by ip and device, with the
// three built-in levels and no-data over ip.
func current() Plan {
	return Plan{Revision: 9, DimensionFields: []string{"device", "ip"},
		Levels:      []contract.LevelDefinitionV2{{LevelID: 1}, {LevelID: 2}, {LevelID: 3}},
		LevelsWhole: true, NoData: true, NoDataDimensions: []string{"ip"}}
}

func threshold(revision int64, severity string, keys ...string) Alert {
	return Alert{Revision: revision, DimensionKeys: keys, DimensionsRead: true, EvaluationFamily: "metric_algorithm", Severity: severity}
}

func noData(revision int64, keys ...string) Alert {
	return Alert{Revision: revision, DimensionKeys: append(keys, "__NO_DATA_DIMENSION__"), DimensionsRead: true,
		EvaluationFamily: "no_data", Severity: "warning"}
}

func TestDecide(t *testing.T) {
	withoutDevice := current()
	withoutDevice.DimensionFields = []string{"ip"}
	withoutWarning := current()
	withoutWarning.Levels = []contract.LevelDefinitionV2{{LevelID: 1}, {LevelID: 3}}
	noDataOff := current()
	noDataOff.NoData, noDataOff.NoDataDimensions = false, nil
	suspended := noDataOff
	suspended.NoDataSuspended = true
	refusedLevel := withoutWarning
	refusedLevel.LevelsWhole = false
	customLevel := withoutWarning
	customLevel.Levels = append(customLevel.Levels, contract.LevelDefinitionV2{LevelID: 4, LevelCode: "emergency"})
	dynamic := withoutDevice
	dynamic.DynamicDimensions = true
	noDataRegrouped := current()
	noDataRegrouped.NoDataDimensions = nil
	// The threshold records carry ip; a no-data point carries only its own
	// grouping, so that is no reason to keep a regrouped no-data alert.
	noDataRegroupedCarried := noDataRegrouped
	noDataRegroupedCarried.RecordFields = []string{"device", "ip"}
	tagged := current()
	tagged.DimensionFields = []string{"tags.device", "ip"}
	// ProcPort: identified by target and process, its records also carry
	// the ports they found.
	procPort := Plan{Revision: 9, DimensionFields: []string{"bk_target_cloud_id", "bk_target_ip", "display_name"},
		RecordFields: []string{"bind_ip", "listen", "nonlisten", "not_accurate_listen", "protocol"},
		Levels:       []contract.LevelDefinitionV2{{LevelID: 1}}, LevelsWhole: true}
	carriedBeside := withoutDevice
	carriedBeside.RecordFields = []string{"ip", "port"}

	for name, test := range map[string]struct {
		plan  Plan
		alert Alert
		want  string
	}{
		// The candidate rule: only an alert older than the current revision.
		"raised under the current revision":   {withoutDevice, threshold(9, "warning", "ip", "device"), OutcomeCurrentRevision},
		"raised under a newer revision":       {withoutDevice, threshold(10, "warning", "ip", "device"), OutcomeRevisionAhead},
		"raised under no revision":            {withoutDevice, threshold(0, "warning", "ip", "device"), OutcomeRevisionUnknown},
		"an edit that changed nothing it has": {current(), threshold(8, "warning", "ip", "device"), OutcomeKept},
		// A target edit moves the revision and leaves the grouping alone.
		"a target edit": {current(), threshold(8, "critical", "ip", "device", "bk_target_ip", "bk_target_cloud_id"), OutcomeKept},
		"a host filled in by CMDB is not grouping": {withoutDevice, threshold(8, "warning", "ip", "bk_host_id"), OutcomeKept},

		// dimensions_changed.
		"a grouping dimension removed":     {withoutDevice, threshold(8, "warning", "ip", "device"), OutcomeDimensionsChanged},
		"a grouping dimension renamed":     {current(), threshold(8, "warning", "ip", "nic"), OutcomeDimensionsChanged},
		"a tags prefix reads as the field": {tagged, threshold(8, "warning", "ip", "device"), OutcomeKept},
		// What the records carry beyond the grouping was not dropped.
		"dimensions the records carry beyond the grouping": {procPort, threshold(8, "critical", "bind_ip", "bk_target_cloud_id",
			"bk_target_ip", "display_name", "listen", "nonlisten", "not_accurate_listen", "protocol"), OutcomeKept},
		"a removed grouping dimension beside carried ones": {carriedBeside, threshold(8, "warning", "ip", "port", "device"), OutcomeDimensionsChanged},
		// The record leaves out a grouping dimension its series did not
		// carry, so an alert lacking a current one is not decided.
		"a grouping dimension the alert lacks":                     {current(), threshold(8, "warning", "ip"), OutcomeKept},
		"dynamic dimensions are not compared":                      {dynamic, threshold(8, "warning", "ip", "device"), OutcomeKept},
		"a no-data group regrouped":                                {noDataRegrouped, noData(8, "ip"), OutcomeDimensionsChanged},
		"a no-data group regrouped, threshold records carrying it": {noDataRegroupedCarried, noData(8, "ip"), OutcomeDimensionsChanged},
		"a no-data group that survived":                            {withoutDevice, noData(8, "ip"), OutcomeKept},

		// level_removed.
		"its level removed":                      {withoutWarning, threshold(8, "warning", "ip", "device"), OutcomeLevelRemoved},
		"another level removed":                  {withoutWarning, threshold(8, "critical", "ip", "device"), OutcomeKept},
		"a refused level is not a removed one":   {refusedLevel, threshold(8, "warning", "ip", "device"), OutcomeLevelsPartial},
		"a level name the link may have mapped":  {customLevel, threshold(8, "warning", "ip", "device"), OutcomeSeverityUnmapped},
		"a severity this process does not write": {withoutWarning, threshold(8, "fatal", "ip", "device"), OutcomeSeverityUnmapped},
		"a no-data alert's level is not judged":  {withoutWarning, noData(8, "ip"), OutcomeKept},

		// no_data_disabled.
		"no-data switched off":                    {noDataOff, noData(8, "ip"), OutcomeNoDataDisabled},
		"threshold alerts stay when no-data goes": {noDataOff, threshold(8, "warning", "ip", "device"), OutcomeKept},
		"no-data configured and not attached":     {suspended, noData(8, "ip"), OutcomeNoDataSuspended},

		// What the record does not say is not guessed.
		"no family": {noDataOff, Alert{Revision: 8, DimensionKeys: []string{"ip"}, DimensionsRead: true, Severity: "warning"}, OutcomeFamilyUnknown},
		"no-data family without the tag": {noDataOff, Alert{Revision: 8, DimensionKeys: []string{"ip"}, DimensionsRead: true,
			EvaluationFamily: "no_data"}, OutcomeFamilyUnknown},
		"threshold family with the tag": {withoutWarning, threshold(8, "warning", "ip", "__NO_DATA_DIMENSION__"), OutcomeFamilyUnknown},
		"no dimensions object":          {withoutDevice, Alert{Revision: 8, EvaluationFamily: "metric_algorithm", Severity: "warning"}, OutcomeFamilyUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			if got, _ := Decide(test.plan, test.alert); got != test.want {
				t.Fatalf("Decide = %s, want %s", got, test.want)
			}
		})
	}
}

// Only the three changes close.
func TestOnlyTheThreeChangesClose(t *testing.T) {
	closing := 0
	for _, decision := range Decisions {
		if Closes(decision) {
			closing++
		}
	}
	if closing != 5 || !Closes("metric_changed") || !Closes("condition_changed") || !Closes("dimensions_changed") ||
		!Closes("level_removed") || !Closes("no_data_disabled") || Closes("kept") {
		t.Fatalf("%d closing decisions, want metric_changed, condition_changed, dimensions_changed, level_removed and no_data_disabled", closing)
	}
}

// The key moves with every fact a decision reads.
func TestThePlanKeyMovesWithEveryFact(t *testing.T) {
	base := current()
	changes := []func(*Plan){
		func(p *Plan) { p.Revision++ },
		func(p *Plan) { p.DimensionFields = []string{"ip"} },
		func(p *Plan) { p.DynamicDimensions = true },
		func(p *Plan) { p.Levels = p.Levels[:2] },
		func(p *Plan) {
			p.Levels = []contract.LevelDefinitionV2{{LevelID: 1, LevelCode: "x"}, {LevelID: 2}, {LevelID: 3}}
		},
		func(p *Plan) { p.LevelsWhole = false },
		func(p *Plan) { p.NoData = false },
		func(p *Plan) { p.NoDataDimensions = nil },
		func(p *Plan) { p.NoDataSuspended = true },
		func(p *Plan) { p.Facts = &contract.StrategyFactsV2{Schema: "v2", MetricDigest: "m", ConditionMD5: "c"} },
		func(p *Plan) {
			p.Facts = &contract.StrategyFactsV2{Schema: "v2", MetricDigest: "m", ConditionMD5: "c", ConditionBasis: "query"}
		},
	}
	for index, change := range changes {
		changed := current()
		change(&changed)
		if changed.Key() == base.Key() {
			t.Fatalf("change %d left the key as it was", index)
		}
	}
}

// strategyFacts is the opening event's strategy facts as the link stores
// them.
func strategyFacts(metric, condition string) []byte {
	return []byte(`{"schema":"v2","metric_digest":"` + metric + `","condition_md5":"` + condition + `","condition_basis":"query"}`)
}

func withFacts(alert Alert, facts []byte) Alert {
	alert.Facts = facts
	return alert
}

// The metric and condition rules, from the facts the opening event carried
// against the current Plans'.
func TestDecideTheStrategyFacts(t *testing.T) {
	plan := current()
	plan.Facts = &contract.StrategyFactsV2{Schema: "v2", MetricDigest: "metric-b", ConditionMD5: "condition-b", ConditionBasis: "query"}
	noDataOff := plan
	noDataOff.NoData, noDataOff.NoDataDimensions = false, nil
	for name, test := range map[string]struct {
		plan       Plan
		alert      Alert
		want, note string
	}{
		"metric changed closes a threshold alert": {plan, withFacts(threshold(8, "warning", "ip", "device"), strategyFacts("metric-a", "condition-b")),
			OutcomeMetricChanged, ""},
		"metric changed closes a no-data alert": {plan, withFacts(noData(8, "ip"), strategyFacts("metric-a", "condition-b")), OutcomeMetricChanged, ""},
		// The condition rule is held: counted, nothing closed.
		"condition changed is held for a no-data alert": {plan, withFacts(noData(8, "ip"), strategyFacts("metric-b", "condition-a")),
			OutcomeKept, OutcomeConditionRuleHeld},
		"condition changed leaves a threshold alert": {plan, withFacts(threshold(8, "warning", "ip", "device"), strategyFacts("metric-b", "condition-a")),
			OutcomeKept, ""},
		// The connector of the first condition is not in the digest (the
		// platform's rule), so the same digest on both sides is no change.
		"unchanged facts keep": {plan, withFacts(noData(8, "ip"), strategyFacts("metric-b", "condition-b")), OutcomeKept, ""},
		"a snapshot that could not be read": {plan, withFacts(noData(8, "ip"),
			[]byte(`{"schema":"v2","metric_digest":"metric-b","condition_md5":"condition-a","condition_basis":"snapshot_unreadable"}`)), OutcomeKept, OutcomeSnapshotUnreadable},
		"no condition basis": {plan, withFacts(noData(8, "ip"), []byte(`{"schema":"v2","metric_digest":"metric-b","condition_md5":"condition-b"}`)),
			OutcomeKept, OutcomeFactsUnknownSchema},
		"condition held, then no-data off": {noDataOff, withFacts(noData(8, "ip"), strategyFacts("metric-b", "condition-a")),
			OutcomeNoDataDisabled, OutcomeConditionRuleHeld},
		"an alert at the current revision is not compared": {plan, withFacts(threshold(9, "warning", "ip", "device"), strategyFacts("metric-a", "condition-a")),
			OutcomeCurrentRevision, ""},

		// Missing or unreadable facts decide neither rule; the others still run.
		"no facts":              {plan, noData(8, "ip"), OutcomeKept, OutcomeFactsMissing},
		"no facts, no-data off": {noDataOff, noData(8, "ip"), OutcomeNoDataDisabled, OutcomeFactsMissing},
		"another schema": {plan, withFacts(threshold(8, "warning", "ip", "device"),
			[]byte(`{"schema":"v3","metric_digest":"metric-a","condition_md5":"condition-a"}`)), OutcomeKept, OutcomeFactsUnknownSchema},
		"no schema": {plan, withFacts(threshold(8, "warning", "ip", "device"), []byte(`{"metric_digest":"metric-a"}`)),
			OutcomeKept, OutcomeFactsUnknownSchema},
		"facts that are not an object": {plan, withFacts(threshold(8, "warning", "ip", "device"), []byte(`"v1"`)), OutcomeKept, OutcomeFactsUnknownSchema},
		"v2 without a digest": {plan, withFacts(threshold(8, "warning", "ip", "device"), []byte(`{"schema":"v2","metric_digest":"metric-a"}`)),
			OutcomeKept, OutcomeFactsUnknownSchema},
		"Plans without facts": {current(), withFacts(threshold(8, "warning", "ip", "device"), strategyFacts("metric-a", "condition-a")),
			OutcomeKept, OutcomePlanFactsMissing},
		"Plans with facts of another schema": {func() Plan {
			p := current()
			p.Facts = &contract.StrategyFactsV2{Schema: "v3", MetricDigest: "metric-b", ConditionMD5: "condition-b"}
			return p
		}(), withFacts(threshold(8, "warning", "ip", "device"), strategyFacts("metric-a", "condition-a")), OutcomeKept, OutcomePlanFactsMissing},
	} {
		t.Run(name, func(t *testing.T) {
			if got, note := Decide(test.plan, test.alert); got != test.want || note != test.note {
				t.Fatalf("Decide = %s, %q; want %s, %q", got, note, test.want, test.note)
			}
		})
	}
}

// The grouping rule reads the digests where both sides state one, and the
// alert's dimension names otherwise; schema v1 is not one this build reads.
func TestDecideTheGroupingDigest(t *testing.T) {
	facts := func(dimension string) []byte {
		return []byte(`{"schema":"v2","metric_digest":"metric-b","condition_md5":"condition-b","condition_basis":"query","dimension_digest":"` + dimension + `"}`)
	}
	plan := current()
	plan.Facts = &contract.StrategyFactsV2{Schema: "v2", MetricDigest: "metric-b", ConditionMD5: "condition-b", ConditionBasis: "query",
		DimensionDigest: "grouping-b", NoDataDimensionDigest: "no-data-b"}
	undigested := plan
	undigested.Facts = &contract.StrategyFactsV2{Schema: "v2", MetricDigest: "metric-b", ConditionMD5: "condition-b", ConditionBasis: "query"}
	dynamic := undigested
	dynamic.DynamicDimensions = true
	for name, test := range map[string]struct {
		plan       Plan
		alert      Alert
		want, note string
	}{
		"grouping changed":                       {plan, withFacts(threshold(8, "warning", "ip", "device"), facts("grouping-a")), OutcomeDimensionsChanged, ""},
		"grouping unchanged":                     {plan, withFacts(threshold(8, "warning", "ip", "nic"), facts("grouping-b")), OutcomeKept, ""},
		"no-data grouping changed":               {plan, withFacts(noData(8, "ip"), facts("no-data-a")), OutcomeDimensionsChanged, ""},
		"no-data grouping unchanged":             {plan, withFacts(noData(8, "ip"), facts("no-data-b")), OutcomeKept, ""},
		"alert states no grouping":               {plan, withFacts(threshold(8, "warning", "ip", "nic"), facts("")), OutcomeDimensionsChanged, ""},
		"alert states no grouping, none dropped": {plan, withFacts(threshold(8, "warning", "ip"), facts("")), OutcomeKept, ""},
		"Plans state no grouping":                {undigested, withFacts(threshold(8, "warning", "ip", "nic"), facts("grouping-a")), OutcomeDimensionsChanged, ""},
		"dynamic Plans state no grouping":        {dynamic, withFacts(threshold(8, "warning", "ip", "nic"), facts("grouping-a")), OutcomeKept, ""},
		"schema v1": {plan, withFacts(threshold(8, "warning", "ip", "device"),
			[]byte(`{"schema":"v1","metric_digest":"metric-a","condition_md5":"condition-b","condition_basis":"query","dimension_digest":"grouping-a"}`)), OutcomeKept, OutcomeFactsUnknownSchema},
	} {
		t.Run(name, func(t *testing.T) {
			if got, note := Decide(test.plan, test.alert); got != test.want || note != test.note {
				t.Fatalf("Decide = %s, %q; want %s, %q", got, note, test.want, test.note)
			}
		})
	}
}

// The condition rule as it will run once released: a close only between
// digests of the same basis; held while it is not released; an unreadable
// snapshot on either side decides nothing.
func TestConditionVerdict(t *testing.T) {
	facts := func(md5, basis string) *contract.StrategyFactsV2 {
		return &contract.StrategyFactsV2{Schema: "v2", MetricDigest: "m", ConditionMD5: md5, ConditionBasis: basis}
	}
	for name, test := range map[string]struct {
		alert, current *contract.StrategyFactsV2
		released       bool
		decision, note string
	}{
		"held":                        {facts("a", "query"), facts("b", "query"), false, "", OutcomeConditionRuleHeld},
		"held, unchanged":             {facts("a", "query"), facts("a", "query"), false, "", ""},
		"released, same basis":        {facts("a", "snapshot_user"), facts("b", "snapshot_user"), true, OutcomeConditionChanged, ""},
		"released, unchanged":         {facts("a", "snapshot_user"), facts("a", "snapshot_user"), true, "", ""},
		"released, other basis":       {facts("a", "snapshot_user"), facts("b", "query"), true, "", OutcomeConditionRuleHeld},
		"alert snapshot unreadable":   {facts("a", "snapshot_unreadable"), facts("b", "query"), true, "", OutcomeSnapshotUnreadable},
		"current snapshot unreadable": {facts("a", "query"), facts("a", "snapshot_unreadable"), true, "", OutcomeSnapshotUnreadable},
	} {
		t.Run(name, func(t *testing.T) {
			if decision, note := conditionVerdict(test.alert, test.current, test.released); decision != test.decision || note != test.note {
				t.Fatalf("conditionVerdict = %q, %q; want %q, %q", decision, note, test.decision, test.note)
			}
		})
	}
	if conditionRuleReleased {
		t.Fatal("condition_changed is released before the publisher states what a snapshot's conditions are")
	}
}
