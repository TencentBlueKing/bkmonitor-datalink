// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/newopens"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

// The worker asks the port for the count by this interface; a port that
// stopped satisfying it would leave the line unwritten with nothing failing.
var _ worker.NewOpensReporter = openAlertCopyPort{}

// The port counts what the copy says opened, not every ABNORMAL it is told
// of: an episode's repeated ABNORMALs, sent every round while it stays
// open, are not new alerts. Past the report floor of first ABNORMALs the
// strategy's hour is reported with the dimension that took a value per
// alert; the same number of ABNORMALs, half of them repeats, is not past it.
func TestThePortCountsWhatTheCopySaysOpened(t *testing.T) {
	abnormals := func(from, n int) []contract.TriggerEventV1 {
		events := make([]contract.TriggerEventV1, 0, n)
		for i := from; i < from+n; i++ {
			events = append(events, contract.TriggerEventV1{EventKind: contract.TriggerEventAbnormal, TenantID: "default",
				PlanRef: contract.RuntimePlanRefV1{StrategyID: "801"}, StrategyRef: &contract.StrategySnapshotRef{Revision: 1},
				DedupeMD5: fmt.Sprintf("%032x", i), RecordRef: contract.TriggerRecordRefV1{Dimensions: map[string]json.RawMessage{
					"span_name": json.RawMessage(fmt.Sprintf(`"GET /item/%d"`, i)), "service": json.RawMessage(`"shop"`)}}})
		}
		return events
	}
	for _, test := range []struct {
		name     string
		batches  [][]contract.TriggerEventV1
		reported bool
	}{
		{name: "past the threshold of first ABNORMALs", batches: [][]contract.TriggerEventV1{abnormals(0, newopens.ReportFloor+1)}, reported: true},
		{name: "as many ABNORMALs, half of them repeats", batches: [][]contract.TriggerEventV1{
			abnormals(0, (newopens.ReportFloor+2)/2), abnormals(0, (newopens.ReportFloor+2)/2)}, reported: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			copy, _ := activeCopyAgainst(t, 200, func(string) string { return `{"items":[]}` })
			port := openAlertCopyPort{copy: copy, opens: newopens.New(nil)}
			port.TrackPlans("qg-test", []execution.PlanIdentity{{TenantID: "default", StrategyID: "801"}})
			for _, batch := range test.batches {
				port.Acknowledged(batch)
			}
			report, say := port.NewOpens(newopens.Key{TenantID: "default", StrategyID: "801"})
			if !test.reported {
				if say {
					t.Fatalf("reported %+v", report)
				}
				return
			}
			if !say || len(report.Hours) != 1 || report.Hours[0].Opened != newopens.ReportFloor+1 ||
				report.Hours[0].Dimension != "span_name" || report.Hours[0].Distinct != newopens.ReportFloor+1 {
				t.Fatalf("report %+v (say %t), want span_name with a value per alert", report, say)
			}
		})
	}
}

// A series that moves between Levels is one alert at the link, so one open:
// opened at info, raised to warning, info recovered while the alert is at
// warning, info raised again - the second info ABNORMAL is not a new alert.
// Before the copy kept each series' open Levels, the info RECOVERY ended
// the episode and the next ABNORMAL counted as a new open, inflating
// new_opens_total and NEW_ALERTS_BURST. The RECOVERY of the
// Level the alert is at does end it, as the link resolves the alert: an
// ABNORMAL after it is an open.
func TestASeriesMovingBetweenLevelsIsOneOpen(t *testing.T) {
	const warning, info = 2, 3
	envelope := func(kind string, results ...contract.LevelResultV1) contract.TriggerEventV1 {
		return contract.TriggerEventV1{EventKind: kind, TenantID: "default", PlanRef: contract.RuntimePlanRefV1{StrategyID: "803"},
			StrategyRef: &contract.StrategySnapshotRef{Revision: 1}, DedupeMD5: fmt.Sprintf("%032x", 1), LevelResults: results}
	}
	level := func(id uint32, result string) contract.LevelResultV1 {
		return contract.LevelResultV1{LevelID: id, Priority: id, Result: result}
	}
	copy, _ := activeCopyAgainst(t, 200, func(string) string { return `{"items":[]}` })
	port := openAlertCopyPort{copy: copy, opens: newopens.New(nil)}
	port.TrackPlans("qg-test", []execution.PlanIdentity{{TenantID: "default", StrategyID: "803"}})
	for _, event := range []contract.TriggerEventV1{
		envelope(contract.TriggerEventAbnormal, level(info, contract.LevelResultAbnormal)),
		envelope(contract.TriggerEventAbnormal, level(warning, contract.LevelResultAbnormal), level(info, contract.LevelResultAbnormal)),
		envelope(contract.TriggerEventRecovery, level(info, contract.LevelResultRecovery)),
		envelope(contract.TriggerEventAbnormal, level(info, contract.LevelResultAbnormal)),
	} {
		port.Acknowledged([]contract.TriggerEventV1{event})
	}
	if counted := port.opens.Stats()[newopens.OutcomeCounted]; counted != 1 {
		t.Fatalf("a series moving between Levels counted %d opens, want 1", counted)
	}
	port.Acknowledged([]contract.TriggerEventV1{envelope(contract.TriggerEventRecovery, level(warning, contract.LevelResultRecovery))})
	port.Acknowledged([]contract.TriggerEventV1{envelope(contract.TriggerEventAbnormal, level(info, contract.LevelResultAbnormal))})
	if counted := port.opens.Stats()[newopens.OutcomeCounted]; counted != 2 {
		t.Fatalf("after the alert's Level recovered beside open info an ABNORMAL counted %d opens in all, want 2", counted)
	}
	// Info triggered while the alert is at warning changes nothing at the
	// link: no open.
	port.Acknowledged([]contract.TriggerEventV1{envelope(contract.TriggerEventAbnormal, level(warning, contract.LevelResultAbnormal))})
	port.Acknowledged([]contract.TriggerEventV1{envelope(contract.TriggerEventAbnormal, level(info, contract.LevelResultAbnormal))})
	if counted := port.opens.Stats()[newopens.OutcomeCounted]; counted != 2 {
		t.Fatalf("info triggered under warning counted %d opens in all, want 2", counted)
	}
	// One envelope that recovers warning and triggers info: the link ends
	// the warning alert and opens an info one, an open.
	port.Acknowledged([]contract.TriggerEventV1{envelope(contract.TriggerEventAbnormal,
		level(warning, contract.LevelResultRecovery), level(info, contract.LevelResultAbnormal))})
	if counted := port.opens.Stats()[newopens.OutcomeCounted]; counted != 3 {
		t.Fatalf("warning recovered beside info triggered in one envelope counted %d opens in all, want 3", counted)
	}
}

// The evaluator asks the production port which Levels of a series' alert are
// open by this interface; a port that stopped satisfying it would let every
// Level's RECOVERY through with nothing failing.
var _ contract.OpenLevelSet = openAlertCopyPort{}
