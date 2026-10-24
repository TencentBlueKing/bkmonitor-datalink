// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodata

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func closesNaming(t *testing.T, key string, at int64) *ManualCloses {
	t.Helper()
	identity := contract.MonitorOutputIdentity{DynamicDimensions: true}
	group, ok := ParseGroupKey(key)
	if !ok {
		t.Fatalf("key %q does not parse", key)
	}
	fingerprint, err := GroupFingerprint("7", "2", identity, group)
	if err != nil {
		t.Fatal(err)
	}
	return &ManualCloses{
		Fingerprint: func(group Group) (string, error) { return GroupFingerprint("7", "2", identity, group) },
		ClosedAt:    map[string]int64{fingerprint: at},
	}
}

func closeHostGroup(host string) Group {
	group, _ := Project(map[string]string{"host": host}, []string{"host"})
	return group
}

// A close stops the absence it names, whatever the roster, and nothing else.
func TestApplyManualClosesMarksOnlyTheAbsenceItNames(t *testing.T) {
	closed, other := closeHostGroup("a").Key(), closeHostGroup("b").Key()
	memory := map[string]GroupMemory{
		closed: {LastSeen: 100, FirstAbsent: 200},
		other:  {LastSeen: 100, FirstAbsent: 200},
	}
	applied, stopped := applyManualCloses(memory, closesNaming(t, closed, 250), 300)
	if stopped != 1 || applied[closed].SuppressedAt != 300 || applied[other].SuppressedAt != 0 {
		t.Fatalf("stopped = %d, memory = %+v; want only the closed group marked", stopped, applied)
	}
	if memory[closed].SuppressedAt != 0 {
		t.Fatal("the input memory was written to")
	}
	if _, stopped := applyManualCloses(memory, closesNaming(t, closed, 150), 300); stopped != 0 {
		t.Fatal("a close recorded before the absence began stopped it")
	}
}

// A closed group gets no verdict in the rounds after, and its data returning
// restarts it from nothing, so a later absence is not covered by the old close.
func TestAClosedAbsenceStaysStoppedUntilDataReturns(t *testing.T) {
	key := closeHostGroup("a").Key()
	roster := Roster{Source: RosterHistory, Groups: map[string]Group{key: closeHostGroup("a")}}
	closes := closesNaming(t, key, 250)
	memory, _ := applyManualCloses(map[string]GroupMemory{key: {LastSeen: 100, FirstAbsent: 200}}, closes, 300)
	for _, round := range []int64{360, 420, 480} {
		result := Evaluate(AbsenceInput{EvaluationTime: round, PeriodSeconds: 60, Completeness: execution.CompletenessFull,
			Roster: roster, Memory: memory})
		if verdict, judged := result.Verdicts[key]; judged {
			t.Fatalf("round %d judged the closed group %s", round, verdict)
		}
		if _, whole := result.Verdicts[WholeItemGroup().Key()]; whole {
			t.Fatalf("round %d opened the whole-item absence after the close", round)
		}
		memory = result.Memory
	}
	back := Evaluate(AbsenceInput{EvaluationTime: 540, PeriodSeconds: 60, Completeness: execution.CompletenessFull,
		Roster: roster, Memory: memory, Present: map[string]Group{key: closeHostGroup("a")}})
	if back.Memory[key].SuppressedAt != 0 || back.Memory[key].FirstAbsent != 0 {
		t.Fatalf("data returning did not restart the group: %+v", back.Memory[key])
	}
	later := Evaluate(AbsenceInput{EvaluationTime: 600, PeriodSeconds: 60, Completeness: execution.CompletenessFull,
		Roster: roster, Memory: back.Memory})
	if closes.Matches(mustFingerprint(t, closes, key), later.Memory[key].FirstAbsent) {
		t.Fatal("the old close covers an absence that began after the data returned")
	}
	if later.Verdicts[key] != VerdictAnomaly {
		t.Fatalf("a later absence was not tracked: %s", later.Verdicts[key])
	}
}

func mustFingerprint(t *testing.T, closes *ManualCloses, key string) string {
	t.Helper()
	group, _ := ParseGroupKey(key)
	fingerprint, err := closes.Fingerprint(group)
	if err != nil {
		t.Fatal(err)
	}
	return fingerprint
}

// One fingerprint names one group: two groups that differ only in a no-data
// dimension the query's identity fields do not name are two alerts under the
// no-data view's identity, and would be one under the Plan's.
func TestGroupsDifferingInANoDataOnlyFieldAreTwoFingerprints(t *testing.T) {
	east, _ := Project(map[string]string{"host": "a", "zone": "east"}, []string{"host", "zone"})
	west, _ := Project(map[string]string{"host": "a", "zone": "west"}, []string{"host", "zone"})
	view := contract.MonitorOutputIdentity{DynamicDimensions: true}
	left, err := GroupFingerprint("7", "2", view, east)
	if err != nil {
		t.Fatal(err)
	}
	right, err := GroupFingerprint("7", "2", view, west)
	if err != nil {
		t.Fatal(err)
	}
	if left == right {
		t.Fatal("two groups got one fingerprint under the no-data view's identity")
	}
	plan := contract.MonitorOutputIdentity{DimensionFields: []string{"host"}}
	planLeft, _ := GroupFingerprint("7", "2", plan, east)
	planRight, _ := GroupFingerprint("7", "2", plan, west)
	if planLeft != planRight {
		t.Fatal("the contrast no longer holds: the Plan's identity now tells the two apart, update the reason above")
	}
}

// A closed history group is still forgotten at the horizon, with nothing
// said; the last one sets the Plan-level exhausted mark, so the empty roster
// does not open the whole-item absence; before the horizon it stays marked.
func TestTheHorizonStillForgetsAClosedHistoryGroup(t *testing.T) {
	const horizon = int64(600)
	keyA, keyB := closeHostGroup("a").Key(), closeHostGroup("b").Key()
	roster := Roster{Source: RosterHistory, Groups: map[string]Group{keyA: closeHostGroup("a"), keyB: closeHostGroup("b")}}
	closedA := GroupMemory{LastSeen: 100, FirstAbsent: 200, SuppressedAt: 300}
	closedB := GroupMemory{LastSeen: 100, FirstAbsent: 200, SuppressedAt: 300}

	before := Evaluate(AbsenceInput{EvaluationTime: 200 + horizon - 60, PeriodSeconds: 60, Completeness: execution.CompletenessFull,
		Roster: roster, Memory: map[string]GroupMemory{keyA: closedA, keyB: closedB}, TrackingHorizonSeconds: horizon})
	if before.Memory[keyA].SuppressedAt == 0 || len(before.Verdicts) != 0 {
		t.Fatalf("before the horizon: memory = %+v, verdicts = %+v; want it kept marked and silent", before.Memory, before.Verdicts)
	}

	one := Roster{Source: RosterHistory, Groups: map[string]Group{keyA: closeHostGroup("a")}}
	past := Evaluate(AbsenceInput{EvaluationTime: 200 + horizon, PeriodSeconds: 60, Completeness: execution.CompletenessFull,
		Roster: one, Memory: map[string]GroupMemory{keyA: closedA, keyB: {LastSeen: 790}}, TrackingHorizonSeconds: horizon})
	if _, kept := past.Memory[keyA]; kept || len(past.Verdicts) != 0 || past.Facts.Expired != 1 {
		t.Fatalf("past the horizon: memory = %+v, verdicts = %+v, expired = %d; want it forgotten with nothing said",
			past.Memory, past.Verdicts, past.Facts.Expired)
	}

	last := Evaluate(AbsenceInput{EvaluationTime: 200 + horizon, PeriodSeconds: 60, Completeness: execution.CompletenessFull,
		Roster: one, Memory: map[string]GroupMemory{keyA: closedA}, TrackingHorizonSeconds: horizon})
	if len(last.Memory) != 0 || last.TrackingExhaustedAt != 200+horizon || len(last.Verdicts) != 0 {
		t.Fatalf("last group past the horizon: memory = %+v, exhausted = %d, verdicts = %+v", last.Memory, last.TrackingExhaustedAt, last.Verdicts)
	}
	next := Evaluate(AbsenceInput{EvaluationTime: 200 + horizon + 60, PeriodSeconds: 60, Completeness: execution.CompletenessFull,
		Roster: Roster{Source: RosterHistory}, Memory: last.Memory, TrackingExhaustedAt: last.TrackingExhaustedAt,
		TrackingHorizonSeconds: horizon})
	if verdict, opened := next.Verdicts[WholeItemGroup().Key()]; opened {
		t.Fatalf("the round after opened the whole-item absence: %s", verdict)
	}
}
