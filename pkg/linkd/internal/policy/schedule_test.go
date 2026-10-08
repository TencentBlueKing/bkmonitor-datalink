// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package policy

import (
	"testing"
	"time"
)

func TestSchedulePreservesKACSecondsAndCalendar(t *testing.T) {
	schedule, err := CompileSchedule("", []ActiveTime{{Period: "every_week", OpenClock: "10:00:00", CloseClock: "11:00:00", DaysOfWeek: "1,3"}, {Period: "every_month", OpenClock: "12:00:00", CloseClock: "12:00:00", DaysOfMonth: "31"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		at   string
		want bool
	}{{"2026-09-30T10:00:00+08:00", true}, {"2026-09-30T11:00:00.999+08:00", true}, {"2026-09-30T11:00:01+08:00", false}, {"2026-10-01T10:00:00+08:00", false}, {"2026-10-31T12:00:00+08:00", true}, {"2026-09-30T12:00:00+08:00", false}} {
		at, err := time.Parse(time.RFC3339Nano, tc.at)
		if err != nil {
			t.Fatal(err)
		}
		if schedule.Active(at) != tc.want {
			t.Errorf("time=%s", tc.at)
		}
	}
	once, err := CompileSchedule("Asia/Shanghai", []ActiveTime{{Period: "once", OpenOnce: "2026-09-30 10:00:00", CloseOnce: "2026-09-30 11:00:00"}})
	if err != nil {
		t.Fatal(err)
	}
	at, _ := time.Parse(time.RFC3339, "2026-09-30T03:00:00Z")
	if !once.Active(at.Add(999*time.Millisecond)) || once.Active(at.Add(time.Second)) {
		t.Fatal("once second boundary differs")
	}
	empty, _ := CompileSchedule("", nil)
	if empty.Active(at) {
		t.Fatal("empty schedule became active")
	}
}

func TestScheduleRejectsInvalidAndAmbiguousTime(t *testing.T) {
	for _, rule := range []ActiveTime{{Period: "everyday", OpenClock: "23:00:00", CloseClock: "01:00:00"}, {Period: "everyday", OpenClock: "1:00:00", CloseClock: "11:00:00"}, {Period: "every_week", OpenClock: "10:00:00", CloseClock: "11:00:00", DaysOfWeek: "0"}, {Period: "every_month", OpenClock: "10:00:00", CloseClock: "11:00:00", DaysOfMonth: "32"}, {Period: "once", OpenOnce: "2026-02-30 10:00:00", CloseOnce: "2026-03-01 10:00:00"}, {Period: ""}} {
		if _, err := CompileSchedule("", []ActiveTime{rule}); err == nil {
			t.Errorf("invalid schedule accepted: %+v", rule)
		}
	}
	for _, text := range []string{"2026-11-01 01:30:00", "2026-03-08 02:30:00"} {
		if _, err := CompileSchedule("America/New_York", []ActiveTime{{Period: "once", OpenOnce: text, CloseOnce: text}}); err == nil {
			t.Fatalf("ambiguous/nonexistent local time accepted: %s", text)
		}
	}
	if _, err := CompileSchedule("America/New_York", []ActiveTime{{Period: "once", OpenOnce: "2026-11-01T01:30:00-04:00", CloseOnce: "2026-11-01T01:30:00-05:00"}}); err != nil {
		t.Fatal(err)
	}
}

func TestScheduleOccurrenceUsesConfiguredRecurrence(t *testing.T) {
	schedule, err := CompileSchedule("America/New_York", []ActiveTime{{Period: "everyday", OpenClock: "01:00:00", CloseClock: "01:45:00"}})
	if err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 11, 1, 5, 15, 0, 0, time.UTC)
	same := first.Add(time.Minute)
	fold := first.Add(time.Hour)
	a, err := schedule.Occurrence(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := schedule.Occurrence(same)
	if err != nil || a != b {
		t.Fatal("identity depends on arbitrary evaluation time", err)
	}
	c, err := schedule.Occurrence(fold)
	if err != nil || a == c {
		t.Fatal("folded hour reused an ended activation", err)
	}
	if _, err := schedule.Occurrence(first.Add(3 * time.Hour)); err == nil {
		t.Fatal("inactive schedule has an activation")
	}
}
