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

// unmatchedIdentity is the evidence of strategy 136's rounds: the gate wants
// cw_object_model_id = "8", the data carries the field's own name, and the
// canonical fields are there.
func unmatchedIdentity() *observability.IdentityFacts {
	return &observability.IdentityFacts{Withheld: 2, KeyMissing: 2, Samples: []observability.IdentitySample{{
		Fields: []observability.IdentityField{
			{Name: "cw_object_model_inst_id", Actual: "cw_object_model_inst_id", Present: true},
			{Name: "cw_object_model_id", Expected: "8", Actual: "cw_object_model_id", Present: true},
			{Name: "model_id", Actual: "cw-MySQL", Present: true, Canonical: true},
			{Name: "model_inst_id", Actual: "22", Present: true, Canonical: true},
		},
	}}}
}

// identityRound is one empty completion whose primary was, or was not,
// emptied by the target's identity.
func identityRound(queryGroup string, slot time.Time, unmatched bool) observability.Observation {
	observed := emptyAt(queryGroup, "136", slot)
	observed.PrimaryInput = &observability.PrimaryInputFacts{Completeness: "FULL", DataState: "EMPTY"}
	if unmatched {
		observed.PrimaryInput.Identity = unmatchedIdentity()
	}
	return observed
}

func identityRow(tracker *Tracker, queryGroup string) (Anomaly, bool) {
	row, listed := rowsOfKind(tracker.NoData(), KindIdentityUnmatched)[queryGroup]
	return row, listed
}

// The data is there and its identity reads none of it: that is positive
// evidence, so the row is named after the rounds that rule out a one-off,
// not after the hour an absent source needs.
func TestAnObjectWhoseIdentityReadsNoneOfItsDataIsNamedOnItsThirdRound(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for round := 1; round < DefaultDegradedRounds; round++ {
		tracker.Observe(context.Background(), identityRound("qg-136", at.at, true))
		at.at = at.at.Add(time.Minute)
		if row, listed := identityRow(tracker, "qg-136"); listed {
			t.Fatalf("named after %d rounds, want %d: %+v", round, DefaultDegradedRounds, row)
		}
	}
	tracker.Observe(context.Background(), identityRound("qg-136", at.at, true))
	row, listed := identityRow(tracker, "qg-136")
	if !listed || row.IdentityUnmatched == nil {
		t.Fatalf("not named after %d rounds: %+v", DefaultDegradedRounds, tracker.NoData())
	}
	if row.IdentityUnmatched.Rounds != DefaultDegradedRounds || row.IdentityUnmatched.KeyMissing != 2 {
		t.Errorf("facts %+v, want %d rounds and 2 key-missing series", row.IdentityUnmatched, DefaultDegradedRounds)
	}
	if want := "数据在，但身份标签对不上：策略要 cw_object_model_id=8，数据里是 cw_object_model_id=cw_object_model_id"; row.IdentityUnmatched.Line != want {
		t.Errorf("line %q, want %q", row.IdentityUnmatched.Line, want)
	}
	list := []Anomaly{row}
	Attribute(list, now)
	if list[0].Finding.Check != CheckIdentityUnmatched {
		t.Errorf("check %s, want %s", list[0].Finding.Check, CheckIdentityUnmatched)
	}
}

// Any round that is not identity-emptied ends the run: one whose series the
// target admitted, and one whose query returned nothing (that is the empty
// line's, not this one's).
func TestARoundNotEmptiedByIdentityStartsTheCountAgain(t *testing.T) {
	for _, between := range []struct {
		name  string
		round func(slot time.Time) observability.Observation
	}{
		{"data", func(slot time.Time) observability.Observation { return dataAt("qg-136", "136", slot) }},
		{"query empty", func(slot time.Time) observability.Observation { return identityRound("qg-136", slot, false) }},
	} {
		t.Run(between.name, func(t *testing.T) {
			at := &clock{at: now}
			tracker := newTracker(t, at)
			step := func(observed observability.Observation) {
				tracker.Observe(context.Background(), observed)
				at.at = at.at.Add(time.Minute)
			}
			for round := 1; round < DefaultDegradedRounds; round++ {
				step(identityRound("qg-136", at.at, true))
			}
			step(between.round(at.at))
			for round := 1; round < DefaultDegradedRounds; round++ {
				step(identityRound("qg-136", at.at, true))
				if row, listed := identityRow(tracker, "qg-136"); listed {
					t.Fatalf("named %d rounds after a %s round: %+v", round, between.name, row)
				}
			}
			step(identityRound("qg-136", at.at, true))
			if _, listed := identityRow(tracker, "qg-136"); !listed {
				t.Fatalf("not named %d rounds after a %s round", DefaultDegradedRounds, between.name)
			}
		})
	}
}

// A restart restores the run from the record: a record that already holds
// the rounds is named on the first round after the restart that is still
// identity-emptied; a record with one round waits for the rest. As with the
// record's other words, the count is trusted only under the content the
// owner runs now: after an edit the run starts again.
func TestARestoredIdentityRunCountsTheRoundsItsRecordHeld(t *testing.T) {
	lastSlot := now.Add(-time.Minute)
	for _, restored := range []struct {
		rounds     int
		contentNow string
		namedAfter int
	}{
		{DefaultDegradedRounds, "content-a", 1},
		{1, "content-a", DefaultDegradedRounds - 1},
		{DefaultDegradedRounds, "content-b", DefaultDegradedRounds},
	} {
		at := &clock{at: now}
		tracker := newTracker(t, at)
		if !tracker.Restore("qg-136", RestoredState{
			LastCompletion: "FULL_EMPTY_COMPLETED", NextSlot: now,
			LastRound: &RestoredRound{Slot: lastSlot, CompletedAt: lastSlot.Add(2 * time.Second), Kind: "FULL_EMPTY_COMPLETED",
				Empty: &RestoredEmptyRound{IdentityRounds: restored.rounds, ContentScope: "content-a"}},
			EmptyRunSince: lastSlot, ContentScopeNow: restored.contentNow,
		}, now, 0) {
			t.Fatalf("not restored")
		}
		if row, listed := identityRow(tracker, "qg-136"); listed {
			t.Fatalf("restored with %d rounds and named before any round of this process: %+v", restored.rounds, row)
		}
		for round := 1; round <= restored.namedAfter; round++ {
			tracker.Observe(context.Background(), identityRound("qg-136", at.at, true))
			at.at = at.at.Add(time.Minute)
			if _, listed := identityRow(tracker, "qg-136"); listed != (round == restored.namedAfter) {
				t.Fatalf("restored with %d rounds under %s: named %t after %d rounds, want named after %d", restored.rounds, restored.contentNow, listed, round, restored.namedAfter)
			}
		}
	}
}
