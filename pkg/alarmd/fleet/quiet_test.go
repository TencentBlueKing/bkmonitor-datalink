// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// quietRound is one empty completion of an event count of groups answered
// with no group.
func quietRound(queryGroup string, slot time.Time) observability.Observation {
	observed := emptyAt(queryGroup, "4101", slot)
	observed.PrimaryInput = &observability.PrimaryInputFacts{Completeness: "FULL", DataState: "EMPTY", QuietWhenEmpty: true}
	return observed
}

// An event count of groups that answers with no group is quiet from its
// first such round: detecting, nothing to do, and never on the data's
// no-data line or the strategy's empty line - not after an hour, and not
// after it once had data. A round with records takes it off the line, and
// an empty round that is not quiet is the ordinary empty run again.
func TestAnEventCountAtRestIsQuietNotNoData(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	quietOf := func() (Anomaly, bool) {
		row, listed := rowsOfKind(tracker.NoData(), KindQuiet)["qg-events"]
		return row, listed
	}
	tracker.Observe(context.Background(), quietRound("qg-events", at.at))
	if _, listed := quietOf(); !listed {
		t.Fatalf("the first quiet round is not on the quiet line: %+v", tracker.NoData())
	}
	tracker.Observe(context.Background(), dataAt("qg-events", "4101", at.at.Add(time.Minute)))
	if _, listed := quietOf(); listed {
		t.Fatal("a round with records left the object quiet")
	}
	at.at = at.at.Add(2 * time.Minute)
	for elapsed := time.Duration(0); elapsed <= 2*time.Hour; elapsed += time.Minute {
		tracker.Observe(context.Background(), quietRound("qg-events", at.at))
		at.at = at.at.Add(time.Minute)
	}
	row, listed := quietOf()
	if !listed {
		t.Fatalf("two hours at rest after data are not quiet: %+v", tracker.NoData())
	}
	if others := tracker.NoData(); len(others) != 1 {
		t.Fatalf("no-data rows %+v, want the quiet one alone: not NO_DATA_PERSISTENT, not EMPTY_EVERY_ROUND", others)
	}
	list := []Anomaly{row}
	Attribute(list, now)
	if list[0].Finding.Check != CheckQuiet {
		t.Fatalf("check %s, want QUIET", list[0].Finding.Check)
	}
	if words := checkWords[CheckQuiet]; words.State != StateDetecting || words.Action != ActionNone {
		t.Fatalf("QUIET reads %+v, want detecting with nothing to do", words)
	}
	tracker.Observe(context.Background(), emptyAt("qg-events", "4101", at.at))
	if _, listed := quietOf(); listed {
		t.Fatal("an empty round that is not quiet kept the object on the quiet line")
	}
}

// Quiet is a claim about a whole, empty primary and nothing else.
func TestOnlyAWholeEmptyPrimaryCanBeQuiet(t *testing.T) {
	for _, facts := range []observability.PrimaryInputFacts{
		{Completeness: "FULL", DataState: "DATA", QuietWhenEmpty: true},
		{Completeness: "PARTIAL", DataState: "EMPTY", QuietWhenEmpty: true},
	} {
		observed := observability.NormalizeObservation(observability.Observation{PrimaryInput: &facts})
		if observed.PrimaryInput.QuietWhenEmpty {
			t.Fatalf("%s %s kept the quiet claim", facts.Completeness, facts.DataState)
		}
	}
}
