// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package fleet

import (
	"testing"
	"time"
)

// A record is read as still happening for three of its object's periods,
// at least ten minutes: a minute object keeps the ten minutes, an hourly one
// three hours, an unknown period ten minutes.
func TestTheRecentWindowIsThreeOfTheObjectsPeriods(t *testing.T) {
	for interval, want := range map[int64]time.Duration{0: 10 * time.Minute, 60: 10 * time.Minute, 200: 10 * time.Minute, 201: 603 * time.Second, 3600: 3 * time.Hour} {
		if got := RecentWindow(interval); got != want {
			t.Errorf("period %d s: window %v, want %v", interval, got, want)
		}
	}
}

// An hourly object's skip two hours old is still a loss in progress, where
// the fixed ten minutes made it history; past three hours it is history. A
// minute object's skip is history past ten minutes, as before.
func TestAnHourlyObjectsSkipIsNotHistoryAfterTenMinutes(t *testing.T) {
	at := now.Add(-2 * time.Hour)
	if loss := lossOf(false, false, false, at, now, 3600); loss != LossOngoing {
		t.Fatalf("hourly skip 2 h ago: %s, want ongoing", loss)
	}
	if loss := lossOf(false, false, false, now.Add(-3*time.Hour-time.Second), now, 3600); loss != LossHistorical {
		t.Fatalf("hourly skip 3 h 1 s ago: %s, want historical", loss)
	}
	if loss := lossOf(false, false, false, now.Add(-10*time.Minute-time.Second), now, 60); loss != LossHistorical {
		t.Fatalf("minute skip 10 min 1 s ago: %s, want historical", loss)
	}
	var consequence Consequence
	consequence.note(at, now, 3600)
	consequence.note(at, now, 60)
	if consequence.SkippedRecent != 1 {
		t.Fatalf("recent consequences %d, want the hourly one", consequence.SkippedRecent)
	}
}

// A daily Plan's zero series count from two days before the row's latest
// round is still read, where ten minutes hid it most of every day; past
// three days it is not.
func TestADailyPlansZeroSeriesIsReadForThreeDays(t *testing.T) {
	row := Anomaly{ReasonLastAt: now, Wake: &WakeFacts{Known: true, IntervalSeconds: 86400}}
	if !recentZero(row, PlanSeriesMatched{LastSeenAt: now.Add(-48 * time.Hour)}) {
		t.Fatal("a daily Plan's zero two days old was not read")
	}
	if recentZero(row, PlanSeriesMatched{LastSeenAt: now.Add(-72*time.Hour - time.Second)}) {
		t.Fatal("a daily Plan's zero three days and a second old was read")
	}
	minute := Anomaly{ReasonLastAt: now, Wake: &WakeFacts{Known: true, IntervalSeconds: 60}}
	if recentZero(minute, PlanSeriesMatched{LastSeenAt: now.Add(-10*time.Minute - time.Second)}) {
		t.Fatal("a minute Plan's zero past ten minutes was read")
	}
}
