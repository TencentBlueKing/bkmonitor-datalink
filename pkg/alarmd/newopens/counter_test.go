// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package newopens

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type clock struct{ at time.Time }

func (c *clock) now() time.Time { return c.at }

// hour is a clock hour; the counts start forty minutes into it.
var hour = time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC)

// opens is n first ABNORMALs of one strategy, each with its own span_name
// and one of three hosts.
func opens(n, from int) []contract.TriggerEventV1 {
	events := make([]contract.TriggerEventV1, 0, n)
	for i := from; i < from+n; i++ {
		events = append(events, contract.TriggerEventV1{EventKind: contract.TriggerEventAbnormal, TenantID: "system",
			PlanRef: contract.RuntimePlanRefV1{StrategyID: "801"}, DedupeMD5: fmt.Sprintf("fp-%d", i),
			RecordRef: contract.TriggerRecordRefV1{Dimensions: map[string]json.RawMessage{
				"span_name": json.RawMessage(fmt.Sprintf("%q", fmt.Sprintf("GET /item/%d", i))),
				"host":      json.RawMessage(fmt.Sprintf("%q", fmt.Sprintf("192.0.2.%d", i%3))),
			}}})
	}
	return events
}

var key801 = Key{TenantID: "system", StrategyID: "801"}

// Past the floor in a clock hour this replica reports its share, with the
// dimension that took a value per alert; at the floor it reports nothing.
// The fleet compares the replicas' sum with the threshold, not this.
func TestAShareOfAnHourPastTheFloorIsReportedWithItsWidestDimension(t *testing.T) {
	for _, test := range []struct {
		opened   int
		reported bool
	}{{opened: ReportFloor + 1, reported: true}, {opened: ReportFloor, reported: false}} {
		t.Run(fmt.Sprint(test.opened), func(t *testing.T) {
			counter := New((&clock{at: hour.Add(40 * time.Minute)}).now)
			counter.Note(opens(test.opened, 0))
			report, say := counter.Report(key801)
			if !test.reported {
				if say {
					t.Fatalf("%d opens in the hour reported %+v", test.opened, report)
				}
				return
			}
			want := Hour{Start: hour, Opened: test.opened, Dimension: "span_name", Distinct: test.opened}
			if !say || len(report.Hours) != 1 || report.Hours[0] != want {
				t.Fatalf("report %+v (say %t), want %+v", report, say, want)
			}
		})
	}
}

// Only an episode's first ABNORMAL is an open: a RECOVERY, an event with no
// fingerprint and a python-compatible one are not counted.
func TestOnlyAFirstAbnormalWithAFingerprintIsAnOpen(t *testing.T) {
	counter := New((&clock{at: hour}).now)
	events := opens(ReportFloor+1, 0)
	events[0].EventKind = contract.TriggerEventRecovery
	events[1].DedupeMD5 = ""
	events[2].LegacyOutput = &contract.LegacyEventContext{}
	counter.Note(events)
	if report, say := counter.Report(key801); say {
		t.Fatalf("three events that open nothing were counted: %+v", report)
	}
	counter.Note(opens(3, ReportFloor+1))
	if report, say := counter.Report(key801); !say || report.Hours[0].Opened != ReportFloor+1 {
		t.Fatalf("report %+v (say %t), want %d opened", report, say, ReportFloor+1)
	}
	if stats := counter.Stats(); stats[OutcomeCounted] != ReportFloor+1 || stats[OutcomeUntracked] != 0 {
		t.Fatalf("stats %v", stats)
	}
}

// The count is of the clock hour, wherever in it the first open fell: an
// open at 08:40 and one at 08:59 are the same hour, one at 09:01 the next.
// An hour past the floor is reported beside the next for the hour after it
// ends, then the strategy is reported clear once, then not at all.
func TestTheCountIsOfTheClockHourAndAnHourIsKeptAnHour(t *testing.T) {
	clk := &clock{at: hour.Add(40 * time.Minute)}
	counter := New(clk.now)
	counter.Note(opens(ReportFloor, 0))
	clk.at = hour.Add(59 * time.Minute)
	counter.Note(opens(1, ReportFloor))
	clk.at = hour.Add(61 * time.Minute)
	counter.Note(opens(ReportFloor+1, ReportFloor+1))
	report, say := counter.Report(key801)
	if !say || len(report.Hours) != 2 || !report.Hours[0].Start.Equal(hour) || report.Hours[0].Opened != ReportFloor+1 ||
		!report.Hours[1].Start.Equal(hour.Add(Window)) || report.Hours[1].Opened != ReportFloor+1 {
		t.Fatalf("09:01: %+v (say %t), want 08:00 and 09:00 each past the floor", report, say)
	}
	clk.at = hour.Add(2*Window + time.Minute)
	if report, say := counter.Report(key801); !say || len(report.Hours) != 1 || !report.Hours[0].Start.Equal(hour.Add(Window)) {
		t.Fatalf("10:01: %+v (say %t), want 09:00 kept, 08:00 let go", report, say)
	}
	clk.at = hour.Add(3*Window + time.Minute)
	if report, say := counter.Report(key801); !say || len(report.Hours) != 0 {
		t.Fatalf("11:01: %+v (say %t), want one report with no hour", report, say)
	}
	if report, say := counter.Report(key801); say {
		t.Fatalf("cleared twice: %+v", report)
	}
}

// A dimension that takes more values than MaxDistinct reads as capped, and
// still as the widest.
func TestADimensionPastMaxDistinctReadsCapped(t *testing.T) {
	counter := New((&clock{at: hour}).now)
	counter.Note(opens(MaxDistinct+10, 0))
	report, say := counter.Report(key801)
	if !say || report.Hours[0].Dimension != "span_name" || report.Hours[0].Distinct != MaxDistinct || !report.Hours[0].DistinctCapped {
		t.Fatalf("report %+v", report)
	}
}

// The sweep lets a quiet strategy go once its hour and the next have
// passed, and keeps one whose hour past the floor is still reported. An
// open past the strategy bound is counted untracked, and Note does not
// sweep to make room.
func TestTheSweepLetsQuietStrategiesGoAndNoteDoesNotSweep(t *testing.T) {
	clk := &clock{at: hour}
	counter := New(clk.now)
	counter.maxStrategies = 2
	quiet := opens(1, 0)
	quiet[0].PlanRef.StrategyID = "7"
	counter.Note(quiet)
	counter.Note(opens(ReportFloor+1, 1))
	clk.at = hour.Add(Window + time.Minute)
	stranger := opens(1, 0)
	stranger[0].PlanRef.StrategyID = "9"
	counter.Note(stranger)
	if counter.Tracked() != 2 || counter.Stats()[OutcomeUntracked] != 1 {
		t.Fatalf("at the bound: %d tracked, stats %v; want the stranger untracked and nothing swept", counter.Tracked(), counter.Stats())
	}
	counter.sweep(clk.now())
	if counter.Tracked() != 1 {
		t.Fatalf("after the sweep %d tracked, want 801 only (its 08:00 still kept)", counter.Tracked())
	}
	if _, say := counter.Report(key801); !say {
		t.Fatal("801 was swept with its hour still to report")
	}
	clk.at = hour.Add(2*Window + time.Minute)
	counter.sweep(clk.now())
	if counter.Tracked() != 0 {
		t.Fatalf("two hours on %d tracked, want none", counter.Tracked())
	}
}
