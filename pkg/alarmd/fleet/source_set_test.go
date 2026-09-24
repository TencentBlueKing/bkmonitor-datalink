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
	"fmt"
	"strings"
	"testing"
	"time"
)

// The platform's list drops 22 strategies at the top of the hour and lists
// them again six minutes later; one round's dispositions say PENDING_REMOVAL,
// then REMOVED, then nothing. The account says what happened: when each
// went absent, that they came back, in which hour and how long they were
// gone -- and a strategy that never comes back within the window is a
// strategy removed, not a flap.
func TestTheSourceSetAccountTellsAFlapFromARemoval(t *testing.T) {
	start := time.Date(2026, 9, 21, 18, 59, 0, 0, time.UTC)
	ledger := NewSourceSetLedger(func() time.Time { return start })
	names := func(prefix string, n int) []string {
		out := make([]string, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, fmt.Sprintf("%s%02d", prefix, i))
		}
		return out
	}
	flapping := names("s", 22)
	stayed := []string{"keep-1", "keep-2"}
	gone := []string{"deleted-1"}
	all := append(append([]string{}, stayed...), flapping...)
	absent := func(ids ...string) []AbsentStrategy {
		out := make([]AbsentStrategy, 0, len(ids))
		for _, id := range ids {
			out = append(out, AbsentStrategy{StrategyID: id})
		}
		return out
	}
	// 18:59: everything listed.
	ledger.NoteRound(SourceSetRound{At: start, Listed: append(append([]string{}, all...), gone...)})
	// 19:01: the list lost 22 and the deleted one; grace.
	at := start.Add(2 * time.Minute)
	ledger.NoteRound(SourceSetRound{At: at, Listed: stayed, PendingRemoval: absent(append(append([]string{}, flapping...), gone...)...)})
	facts := ledger.Facts(at)
	if facts.PendingRemoval != 23 || facts.Removed != 0 || len(facts.PendingRemovalSamples) != SourceSetSampleLimit ||
		!facts.PendingRemovalSamples[0].AbsentSince.Equal(at) {
		t.Fatalf("under grace: %+v", facts)
	}
	// 19:02: still under grace. The grace runs for minutes now, so the same
	// strategies are PENDING_REMOVAL round after round: absent since stays
	// the first round, and nothing is dropped twice.
	firstAbsent := at
	at = start.Add(3 * time.Minute)
	ledger.NoteRound(SourceSetRound{At: at, Listed: stayed, PendingRemoval: absent(append(append([]string{}, flapping...), gone...)...)})
	facts = ledger.Facts(at)
	if facts.PendingRemoval != 23 || !facts.PendingRemovalSamples[0].AbsentSince.Equal(firstAbsent) || facts.Hours[0].Dropped != 23 {
		t.Fatalf("a second round under grace moved absent_since or dropped again: %+v / %+v", facts.PendingRemovalSamples[0], facts.Hours[0])
	}
	// 19:03: still absent, and the grace is over for them.
	at = start.Add(4 * time.Minute)
	ledger.NoteRound(SourceSetRound{At: at, Listed: stayed, Removed: absent(append(append([]string{}, flapping...), gone...)...)})
	facts = ledger.Facts(at)
	if facts.PendingRemoval != 0 || facts.Removed != 23 {
		t.Fatalf("removed: %+v", facts)
	}
	// 19:07:45: the 22 are listed again; the deleted one is not.
	at = start.Add(8*time.Minute + 45*time.Second)
	if returned := ledger.NoteRound(SourceSetRound{At: at, Listed: all}); returned != 22 {
		t.Fatalf("returned after removal this round = %d, want the 22 whose Plans had left", returned)
	}
	facts = ledger.Facts(at)
	if facts.Hours[0].ReturnedAfterRemoval != 22 || len(facts.Hours[0].ReturnedAfterRemovalSamples) != SourceSetSampleLimit || facts.ReturnedAfterRemovalTotal != 22 {
		t.Fatalf("returned after removal: %+v total %d", facts.Hours[0], facts.ReturnedAfterRemovalTotal)
	}
	if facts.PendingRemoval != 0 || facts.Removed != 1 || facts.ReactivatedThisHour != 22 {
		t.Fatalf("after the return: %+v", facts)
	}
	if len(facts.Hours) != 1 {
		t.Fatalf("hours = %+v, want the one hour", facts.Hours)
	}
	hour := facts.Hours[0]
	if !hour.Hour.Equal(time.Date(2026, 9, 21, 19, 0, 0, 0, time.UTC)) || hour.Dropped != 23 || hour.Removed != 23 || hour.Reactivated != 22 ||
		hour.LongestAbsentSeconds != (6*time.Minute+45*time.Second).Seconds() || len(hour.Samples) != SourceSetSampleLimit {
		t.Fatalf("hour = %+v, want 23 dropped, 23 removed, 22 back after 6m45s, a bounded sample", hour)
	}
	// The same hour again next hour: a second hour on the account, and the
	// deleted strategy, absent past the return window, is forgotten rather
	// than reported as removed forever.
	at = start.Add(time.Hour + 2*time.Minute)
	ledger.NoteRound(SourceSetRound{At: at, Listed: stayed, PendingRemoval: absent(flapping...)})
	at = start.Add(time.Hour + 8*time.Minute)
	if returned := ledger.NoteRound(SourceSetRound{At: at, Listed: all}); returned != 0 {
		t.Fatalf("a return inside the grace counted as after removal: %d", returned)
	}
	facts = ledger.Facts(at)
	if facts.Hours[0].ReturnedAfterRemoval != 0 || facts.ReturnedAfterRemovalTotal != 22 {
		t.Fatalf("the hour of a grace return: %+v total %d", facts.Hours[0], facts.ReturnedAfterRemovalTotal)
	}
	if len(facts.Hours) != 2 || facts.Hours[0].Hour.Hour() != 20 || facts.Hours[0].Reactivated != 22 || facts.Hours[1].Hour.Hour() != 19 {
		t.Fatalf("two hours, newest first: %+v", facts.Hours)
	}
	if facts.Removed != 1 {
		t.Fatalf("the deleted strategy is still within the return window: %+v", facts)
	}
	at = start.Add(7 * time.Hour)
	ledger.NoteRound(SourceSetRound{At: at, Listed: all})
	if facts = ledger.Facts(at); facts.Removed != 0 || facts.PendingRemoval != 0 {
		t.Fatalf("past the return window the deleted strategy is forgotten: %+v", facts)
	}
	// Coming back after the window is not a flap.
	ledger.NoteRound(SourceSetRound{At: at.Add(time.Minute), Listed: append(append([]string{}, all...), gone...)})
	if facts = ledger.Facts(at.Add(time.Minute)); facts.ReactivatedThisHour != 0 {
		t.Fatalf("a return after the window counted as a reactivation: %+v", facts)
	}
	if !facts.Since.Equal(start) {
		t.Fatalf("since = %v, want the ledger's start %v", facts.Since, start)
	}
}

// The account is a line: the hours of the last day in which strategies came
// back, newest first, each with its strategies, and the line's sentence
// says how often and what is under grace now. An account with no returns is
// no line, and a return older than a day is not on it.
func TestTheSourceSetLineFoldsByTheHourTheStrategiesCameBack(t *testing.T) {
	at := time.Date(2026, 9, 21, 19, 30, 0, 0, time.UTC)
	set := &SourceSetFacts{Since: at.Add(-30 * time.Hour), PendingRemoval: 3, ReactivatedThisHour: 22,
		PendingRemovalSamples: []AbsentSample{{StrategyID: "s01", AbsentSince: at.Add(-2 * time.Minute)}},
		Hours: []SourceSetHour{
			{Hour: at.Truncate(time.Hour), Dropped: 23, Removed: 23, Reactivated: 22, LongestAbsentSeconds: 405, Samples: []string{"s01", "s02"}},
			{Hour: at.Truncate(time.Hour).Add(-time.Hour), Dropped: 22, Removed: 22, Reactivated: 22, LongestAbsentSeconds: 390, Samples: []string{"s01"}},
			// Dropped and not yet back: not a fold of returns.
			{Hour: at.Truncate(time.Hour).Add(-2 * time.Hour), Dropped: 5},
			// Older than a day: history, off the line.
			{Hour: at.Truncate(time.Hour).Add(-26 * time.Hour), Dropped: 22, Reactivated: 22},
		}}
	view := &View{Source: sourceFactsWithSet(at, set), SourceReplica: "pod-leader"}
	reports := ReportChecks([][]Anomaly{nil, nil, nil, nil}, nil, view, at)
	var report *CheckReport
	for _, candidate := range reports {
		if candidate.Code == CheckSourceSetFlapping {
			report = &candidate
		}
	}
	if report == nil || report.Owner != OwnerPlatform || report.GroupBy != GroupByHour || report.Strategies != 44 || len(report.Groups) != 2 {
		t.Fatalf("report = %+v, want the platform's line over 44 strategy-returns in two hours", report)
	}
	if report.Groups[0].Key != "2026-09-21T19Z" || report.Groups[0].Strategies != 22 || len(report.Groups[0].Samples) != 2 ||
		report.Groups[1].Key != "2026-09-21T18Z" || report.Groups[0].Replicas[0] != "pod-leader" {
		t.Fatalf("groups = %+v, want newest hour first, each with its returns and samples", report.Groups)
	}
	if !strings.Contains(report.Line, "2 个小时") || !strings.Contains(report.Line, "44 条次") || !strings.Contains(report.Line, "3 条在宽限中") || !strings.Contains(report.Line, "本小时已回来 22 条") {
		t.Fatalf("line = %q", report.Line)
	}
	if !strings.Contains(report.Groups[0].Text, "22 条策略回到活动集") || !strings.Contains(report.Groups[0].Text, "7 分钟") {
		t.Fatalf("group text = %q", report.Groups[0].Text)
	}
	// No returns: no line.
	quiet := &View{Source: sourceFactsWithSet(at, &SourceSetFacts{Since: at, Hours: []SourceSetHour{{Hour: at.Truncate(time.Hour), Dropped: 2}}}), SourceReplica: "pod-leader"}
	for _, candidate := range ReportChecks([][]Anomaly{nil, nil, nil, nil}, nil, quiet, at) {
		if candidate.Code == CheckSourceSetFlapping {
			t.Fatalf("an account with no returns made a line: %+v", candidate)
		}
	}
}

// A strategy back in the list is back whether or not it compiled a Plan this
// round: one that returns under STALE_CONFIG is a reactivation and leaves
// the pending count, where reading only the accepted set kept it there as a
// running strategy "waiting to be removed" for six hours. And a strategy the
// source really dropped -- graced, removed, never listed again -- stays
// removed and is never read as back.
func TestAStrategyBackInTheListIsBackWhateverBecameOfItThisRound(t *testing.T) {
	start := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	ledger := NewSourceSetLedger(func() time.Time { return start })
	// Round 1: two strategies dropped.
	ledger.NoteRound(SourceSetRound{At: start, Listed: []string{"keep"}, PendingRemoval: []AbsentStrategy{{StrategyID: "stale-back"}, {StrategyID: "gone"}}})
	// Round 2, six minutes later: stale-back is listed again but compiles
	// no Plan (its round-level word would be STALE_CONFIG); gone is still
	// absent and past the grace.
	at := start.Add(6 * time.Minute)
	ledger.NoteRound(SourceSetRound{At: at, Listed: []string{"keep", "stale-back"}, Removed: []AbsentStrategy{{StrategyID: "gone"}}})
	facts := ledger.Facts(at)
	if facts.PendingRemoval != 0 || facts.Removed != 1 || facts.ReactivatedThisHour != 1 {
		t.Fatalf("after the return under stale config: %+v, want nothing pending, one removed, one reactivated", facts)
	}
	for _, sample := range facts.PendingRemovalSamples {
		if sample.StrategyID == "stale-back" {
			t.Fatalf("a strategy back in the list is still named as waiting to be removed: %+v", sample)
		}
	}
	// Rounds later, gone is still not listed: removed, not back.
	at = start.Add(30 * time.Minute)
	ledger.NoteRound(SourceSetRound{At: at, Listed: []string{"keep", "stale-back"}})
	if facts = ledger.Facts(at); facts.Removed != 1 || facts.Hours[0].Reactivated != 1 {
		t.Fatalf("a strategy never listed again was read as back, or the one return was lost: %+v", facts)
	}
}

// The start of an absence is the catalog's word when it has one: a leader
// that took over mid-grace learns from the disposition that the strategy
// went absent before its own account began, and the first screen says so
// rather than "since I first saw it". A start the catalog names later than
// what the ledger already holds never moves it later, and a disposition
// without the word leaves this round as the lower bound.
func TestTheAbsenceStartsWhenTheCatalogSaysNotWhenTheLedgerFirstSaw(t *testing.T) {
	ledgerStart := time.Date(2026, 9, 22, 10, 5, 0, 0, time.UTC)
	trueStart := ledgerStart.Add(-8 * time.Minute)
	ledger := NewSourceSetLedger(func() time.Time { return ledgerStart })
	// A new leader's first round: the strategy is already under grace, and
	// the disposition carries when the old leader first found it absent.
	ledger.NoteRound(SourceSetRound{At: ledgerStart, Listed: []string{"keep"},
		PendingRemoval: []AbsentStrategy{{StrategyID: "graced", AbsentSince: trueStart}, {StrategyID: "unsaid"}}})
	facts := ledger.Facts(ledgerStart)
	if facts.PendingRemoval != 2 {
		t.Fatalf("pending = %+v", facts)
	}
	since := map[string]time.Time{}
	for _, sample := range facts.PendingRemovalSamples {
		since[sample.StrategyID] = sample.AbsentSince
	}
	if !since["graced"].Equal(trueStart) {
		t.Fatalf("graced absent since %v, want the catalog's %v, not the ledger's first sight", since["graced"], trueStart)
	}
	if !since["unsaid"].Equal(ledgerStart) {
		t.Fatalf("a disposition without the word: absent since %v, want this round %v as the lower bound", since["unsaid"], ledgerStart)
	}
	// The drop is counted in the hour the absence began -- the hour before
	// this account did -- not the hour this process heard of it.
	dropped := map[time.Time]int{}
	for _, hour := range facts.Hours {
		dropped[hour.Hour] = hour.Dropped
	}
	if trueStart.Truncate(time.Hour).Equal(ledgerStart.Truncate(time.Hour)) {
		t.Fatal("fixture: the catalog's start has to fall in the hour before the account's")
	}
	if dropped[trueStart.Truncate(time.Hour)] != 1 || dropped[ledgerStart.Truncate(time.Hour)] != 1 {
		t.Fatalf("dropped by hour = %v, want the graced one in the hour of its start (%v) and the unsaid one in this round's", dropped, trueStart.Truncate(time.Hour))
	}
	// The next round carries the word for the one that lacked it -- earlier
	// than the ledger's sight -- and a later moment for the other; the
	// earlier is taken, the later ignored.
	at := ledgerStart.Add(time.Minute)
	ledger.NoteRound(SourceSetRound{At: at, Listed: []string{"keep"},
		PendingRemoval: []AbsentStrategy{{StrategyID: "graced", AbsentSince: trueStart.Add(time.Minute)}, {StrategyID: "unsaid", AbsentSince: trueStart}}})
	facts = ledger.Facts(at)
	since = map[string]time.Time{}
	for _, sample := range facts.PendingRemovalSamples {
		since[sample.StrategyID] = sample.AbsentSince
	}
	if !since["graced"].Equal(trueStart) || !since["unsaid"].Equal(trueStart) {
		t.Fatalf("after the second round: graced %v unsaid %v, want both at the earliest start %v", since["graced"], since["unsaid"], trueStart)
	}
	// And the return measures against the true start.
	at = ledgerStart.Add(3 * time.Minute)
	ledger.NoteRound(SourceSetRound{At: at, Listed: []string{"keep", "graced", "unsaid"}})
	if facts = ledger.Facts(at); facts.Hours[0].LongestAbsentSeconds != (11 * time.Minute).Seconds() {
		t.Fatalf("longest absence = %v s, want 11 minutes from the catalog's start", facts.Hours[0].LongestAbsentSeconds)
	}
}

// The hour's sentence says "moved back after removal" for the returns after
// removal, not for the removals: an hour that removed 23 and brought none
// back does not read as 23 put back.
func TestTheSourceSetHourSaysReturnsAfterRemovalNotRemovals(t *testing.T) {
	removedOnly := sourceSetHourText(SourceSetHour{Removed: 23, Reactivated: 0})
	if strings.Contains(removedOnly, "重新放置") {
		t.Errorf("removals read as returns: %q", removedOnly)
	}
	back := sourceSetHourText(SourceSetHour{Removed: 23, Reactivated: 22, ReturnedAfterRemoval: 22})
	if !strings.Contains(back, "22 条是已被移除后重新放置") {
		t.Errorf("returns after removal not said: %q", back)
	}
}
