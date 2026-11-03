// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// fakeOpenAlertJudge answers Holds from a map; a fingerprint not in it is
// judged not held, and judged false says nothing is judged.
type fakeOpenAlertJudge struct {
	held   map[string]bool
	judged bool
}

func (judge fakeOpenAlertJudge) Holds(_, _, fingerprint string) (bool, bool) {
	return judge.held[fingerprint], judge.judged
}

func noDataEndEntryFor(series string, fingerprint string, last int64) execution.NoDataEndEntry {
	return execution.NoDataEndEntry{Series: execution.SeriesIdentityDigest(strings.Repeat(series, 64)), LevelID: 5,
		Fingerprint: fingerprint, LastRealPoint: last}
}

// One Plan's index for a round, decided without I/O: N = 1, R = 2 at a
// minute, so an absent series is due (R + T + 1) = 4 minutes after its last
// real point. Each rule both ways.
func TestDecideNoDataEnds(t *testing.T) {
	plan := internalCompiledPlanRecovering(t, "301", 1, 60, json.RawMessage(`{"enabled":true,"consecutive_windows":2,"missing_data":"evaluate_as_zero"}`))
	due := execution.DuePlan{Identity: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "301"}, CompiledPlan: plan}
	const now = int64(1_788_000_000)
	due3, early := noDataEndEntryFor("a", "fa", now-240), noDataEndEntryFor("b", "fb", now-180)
	absent := func(complete bool, entries ...execution.NoDataEndEntry) noDataEndPlan {
		return noDataEndPlan{loaded: execution.NoDataEndLoaded{Entries: entries}, complete: complete, present: map[execution.SeriesIdentityDigest]struct{}{}}
	}
	held := fakeOpenAlertJudge{held: map[string]bool{"fa": true, "fb": true}, judged: true}

	decided := decideNoDataEnds(due, absent(true, due3, early), nil, held, now, 10)
	if len(decided.ends) != 1 || decided.ends[due3.Series] == nil || len(decided.write.Deletes) != 0 {
		t.Fatalf("complete absent round: %+v, want only the series 4 minutes old ended", decided)
	}
	if partial := decideNoDataEnds(due, absent(false, due3), nil, held, now, 10); len(partial.ends) != 0 || len(partial.write.Deletes) != 0 {
		t.Fatalf("incomplete round decided %+v, want nothing", partial)
	}
	present := absent(true, due3)
	present.present[due3.Series] = struct{}{}
	if decided := decideNoDataEnds(due, present, nil, held, now, 10); len(decided.ends) != 0 {
		t.Fatalf("a present series was ended: %+v", decided)
	}
	if closed := decideNoDataEnds(due, absent(true, due3), nil, fakeOpenAlertJudge{judged: true}, now, 10); len(closed.ends) != 0 ||
		closed.notOpen != 1 || len(closed.write.Deletes) != 1 {
		t.Fatalf("an alert the set judges closed: %+v, want removed, counted not_open, nothing ended", closed)
	}
	if unjudged := decideNoDataEnds(due, absent(true, due3), nil, fakeOpenAlertJudge{}, now, 10); len(unjudged.ends) != 0 ||
		len(unjudged.write.Deletes) != 0 || unjudged.unjudged != 1 {
		t.Fatalf("a set not judged: %+v, want nothing ended or removed, counted unjudged", unjudged)
	}
	if none := decideNoDataEnds(due, absent(true, due3), nil, nil, now, 10); len(none.ends) != 0 || none.unjudged != 1 {
		t.Fatalf("no judge at all: %+v, want unjudged", none)
	}

	// The round's updates: an unjudged one refreshes an entry that is there
	// and creates none; a judged closed one removes; a new open one past the
	// capacity is not indexed and is counted.
	refreshed := due3
	refreshed.LastRealPoint = now
	newcomer := noDataEndEntryFor("c", "fc", now)
	updates := []execution.NoDataEndUpdate{{Entry: refreshed}, {Entry: newcomer}}
	decided = decideNoDataEnds(due, absent(false, due3), updates, held, now, 10)
	if len(decided.write.Puts) != 1 || decided.write.Puts[0].Key() != refreshed.Key() || decided.write.Puts[0].LastRealPoint != now {
		t.Fatalf("unjudged updates put %+v, want only the existing entry refreshed", decided.write.Puts)
	}
	decided = decideNoDataEnds(due, absent(false, due3), []execution.NoDataEndUpdate{{Entry: due3, Judged: true}}, held, now, 10)
	if len(decided.write.Deletes) != 1 || len(decided.write.Puts) != 0 {
		t.Fatalf("a judged closed update: %+v, want the entry removed", decided.write)
	}
	decided = decideNoDataEnds(due, absent(false, due3), []execution.NoDataEndUpdate{{Entry: newcomer, Judged: true, Open: true}}, held, now, 1)
	if decided.overflow != 1 || len(decided.write.Puts) != 0 {
		t.Fatalf("an open alert past the capacity: %+v, want counted overflow and not indexed", decided)
	}
	// A revision that moved the alert identity: the series' entry carries the
	// newest fingerprint, so an old one never ends the new alert.
	moved := due3
	moved.Fingerprint = "fnew"
	decided = decideNoDataEnds(due, absent(false, due3), []execution.NoDataEndUpdate{{Entry: moved, Judged: true, Open: true}}, held, now, 10)
	if len(decided.write.Puts) != 1 || decided.write.Puts[0].Fingerprint != "fnew" {
		t.Fatalf("an update under a new identity put %+v, want the entry replaced by it", decided.write.Puts)
	}
}

// A round with a read gap of the Plan open cannot say a group is absent,
// however whole its answer: the gap is a round nobody read, and the group
// may have reported in it. An absent series already past due is not ended
// while the gap is open, and is ended on the first whole round after it
// closes. A gap found with no scope open, or not found, closes nothing.
func TestAnOpenReadGapHoldsAnEndPastDueUntilTheGapCloses(t *testing.T) {
	plan := internalCompiledPlanRecovering(t, "302", 1, 60, json.RawMessage(`{"enabled":true,"consecutive_windows":2,"missing_data":"evaluate_as_zero"}`))
	due := execution.DuePlan{Identity: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "302"}, CompiledPlan: plan, StateGeneration: "generation-3"}
	const now = int64(1_788_000_000)
	overdue := noDataEndEntryFor("c", "fc", now-600)
	held := fakeOpenAlertJudge{held: map[string]bool{"fc": true}, judged: true}
	identity := execution.PlanGapIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration}
	round := func(gaps ...execution.GapGuardSnapshot) noDataEndPlan {
		stream := &streamedExecution{
			header:     execution.InternalExecutionHeader{DuePlans: []execution.DuePlan{due}},
			noDataEnds: map[execution.PlanIdentity]execution.NoDataEndLoaded{due.Identity: {Entries: []execution.NoDataEndEntry{overdue}}},
			gaps:       execution.GapLoadResult{Items: gaps},
		}
		return stream.noDataEndRound()[due.Identity]
	}
	open := execution.GapGuardSnapshot{Identity: identity, Status: execution.GapFound, Scopes: []execution.GapScopeState{{}}}
	if gapped := round(open); gapped.complete {
		t.Fatal("a round with the Plan's read gap open read as complete")
	} else if decided := decideNoDataEnds(due, gapped, nil, held, now, 10); len(decided.ends) != 0 || len(decided.write.Deletes) != 0 {
		t.Fatalf("a round with the read gap open decided %+v, want nothing", decided)
	}
	for name, gaps := range map[string][]execution.GapGuardSnapshot{
		"closed":          nil,
		"found, no scope": {{Identity: identity, Status: execution.GapFound}},
		"another Plan's":  {{Identity: execution.PlanGapIdentity{Plan: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "303"}}, Status: execution.GapFound, Scopes: []execution.GapScopeState{{}}}},
	} {
		whole := round(gaps...)
		if !whole.complete {
			t.Fatalf("%s: a whole round read as incomplete", name)
		}
		if decided := decideNoDataEnds(due, whole, nil, held, now, 10); len(decided.ends) != 1 || decided.ends[overdue.Series] == nil {
			t.Fatalf("%s: the first whole round decided %+v, want the overdue series ended", name, decided)
		}
	}
}
