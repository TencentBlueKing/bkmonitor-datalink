// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import (
	"errors"
	"fmt"
	"testing"
)

func mergePoint(recordID string, sourceTime int64, result LevelFactResult) StateHistoryPoint {
	return StateHistoryPoint{RecordID: recordID, SourceTime: sourceTime,
		Levels: []StateLevelFact{{LevelID: 5, DetectFingerprint: "detect-v1", Result: result}}}
}

func walkedHistory(t testing.TB, base, delta []StateHistoryPoint, retention uint32) []StateHistoryPoint {
	t.Helper()
	var walked []StateHistoryPoint
	if err := WalkMergedHistory(base, delta, retention, func(point StateHistoryPoint) error {
		walked = append(walked, point)
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	return walked
}

// The count and the walk have to answer the same question, and the only place
// they can disagree is where a fresh point lands on a source time the history
// already holds. Everywhere else a count derived from its own comparison
// agrees, which is why a table that leaves the coincident row out proves
// nothing: it is the row the disagreement lives on.
func TestTheMergedCountIsWhatTheWalkEmits(t *testing.T) {
	for _, test := range []struct {
		name        string
		base, delta []StateHistoryPoint
	}{
		{"nothing loaded", nil, []StateHistoryPoint{mergePoint("a", 10, LevelFactNormal)}},
		{"nothing added", []StateHistoryPoint{mergePoint("a", 10, LevelFactNormal)}, nil},
		{"appended after", []StateHistoryPoint{mergePoint("a", 10, LevelFactNormal)},
			[]StateHistoryPoint{mergePoint("b", 20, LevelFactNormal)}},
		{"placed before", []StateHistoryPoint{mergePoint("b", 20, LevelFactNormal)},
			[]StateHistoryPoint{mergePoint("a", 10, LevelFactNormal)}},
		{"interleaved", []StateHistoryPoint{mergePoint("a", 10, LevelFactNormal), mergePoint("c", 30, LevelFactNormal)},
			[]StateHistoryPoint{mergePoint("b", 20, LevelFactNormal), mergePoint("d", 40, LevelFactNormal)}},
		{"landing on a held position", []StateHistoryPoint{mergePoint("a", 10, LevelFactNormal), mergePoint("b", 20, LevelFactNormal)},
			[]StateHistoryPoint{mergePoint("b", 20, LevelFactAnomalous)}},
		{"every position held", []StateHistoryPoint{mergePoint("a", 10, LevelFactNormal), mergePoint("b", 20, LevelFactNormal)},
			[]StateHistoryPoint{mergePoint("a", 10, LevelFactNormal), mergePoint("b", 20, LevelFactNormal)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			counted := MergedHistoryPointCount(test.base, test.delta)
			walked := walkedHistory(t, test.base, test.delta, 0)
			if counted != len(walked) {
				t.Fatalf("the pre-scan counted %d and the walk emitted %d; truncation reads the count and "+
					"emits from the walk, so a record that merges at one source time would lose or keep "+
					"one point too many", counted, len(walked))
			}
		})
	}
}

// A point this round adds at a source time the record already holds replaces
// it rather than appearing beside it. Without that the record grows by one
// every time a Slot re-evaluates a record it has already written, which is
// what a restart produces.
func TestAPointLandingOnAHeldPositionReplacesItInsteadOfAppearing(t *testing.T) {
	base := []StateHistoryPoint{mergePoint("a", 10, LevelFactNormal), mergePoint("b", 20, LevelFactNormal)}
	delta := []StateHistoryPoint{mergePoint("b", 20, LevelFactAnomalous)}
	walked := walkedHistory(t, base, delta, 0)
	if len(walked) != 2 {
		t.Fatalf("the record holds %d points, want 2: a re-evaluated record must not appear twice", len(walked))
	}
	if walked[1].Levels[0].Result != LevelFactAnomalous {
		t.Fatalf("the held position kept the stored fact %q; this round's point is the merged one and is "+
			"the one to store", walked[1].Levels[0].Result)
	}
	// Applying the same addition again changes nothing, which is what makes a
	// skipped write and a repeated write the same outcome.
	again, err := MergedHistory(walked, delta, 0)
	if err != nil {
		t.Fatalf("merge again: %v", err)
	}
	if len(again) != len(walked) {
		t.Fatalf("applying one addition twice left %d points and once left %d", len(again), len(walked))
	}
}

func TestTheRetentionBoundEvictsTheOldestOfTheMergedRecord(t *testing.T) {
	base := []StateHistoryPoint{mergePoint("a", 10, LevelFactNormal), mergePoint("b", 20, LevelFactNormal)}
	delta := []StateHistoryPoint{mergePoint("c", 30, LevelFactNormal)}
	walked := walkedHistory(t, base, delta, 2)
	if len(walked) != 2 || walked[0].RecordID != "b" || walked[1].RecordID != "c" {
		t.Fatalf("the bound kept %+v; it must drop the oldest, not the newest", walked)
	}
	if kept := walkedHistory(t, base, delta, 0); len(kept) != 3 {
		t.Fatalf("no bound kept %d points, want every one of them", len(kept))
	}
	if whole := walkedHistory(t, base, delta, 9); len(whole) != 3 {
		t.Fatalf("a bound above the record kept %d points, want every one of them", len(whole))
	}
}

// The walk is what replaced rebuilding the window at the producer, so what it
// must not do is allocate per point. Asserted as independence from the base
// length rather than as an exact count: the property is that a longer record
// costs no more, and an exact number would move with unrelated compiler
// decisions while that property stayed true or stopped being true unnoticed.
func TestTheMergeWalkDoesNotAllocatePerPoint(t *testing.T) {
	measure := func(points int) float64 {
		base := make([]StateHistoryPoint, 0, points)
		for index := 0; index < points; index++ {
			base = append(base, mergePoint(fmt.Sprintf("r%04d", index), int64(index)*60, LevelFactNormal))
		}
		delta := []StateHistoryPoint{mergePoint("fresh", int64(points)*60, LevelFactAnomalous)}
		var seen int
		return testing.AllocsPerRun(50, func() {
			_ = WalkMergedHistory(base, delta, uint32(points), func(StateHistoryPoint) error {
				seen++
				return nil
			})
		})
	}
	short, long := measure(8), measure(2048)
	if long > short {
		t.Fatalf("walking 2048 points allocated %v and walking 8 allocated %v; the walk holds two indices "+
			"and must not grow with the record", long, short)
	}
}

func TestTwoRecordsAtOneSourceTimeAreNamedRatherThanPicked(t *testing.T) {
	base := []StateHistoryPoint{mergePoint("stored", 10, LevelFactNormal)}
	delta := []StateHistoryPoint{mergePoint("fresh", 10, LevelFactNormal)}
	err := WalkMergedHistory(base, delta, 0, func(StateHistoryPoint) error { return nil })
	var conflict *HistoryRecordIdentityConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("two records at one source time gave %v; picking either one would decide a record's past "+
			"by which side of the merge it arrived on", err)
	}
	if conflict.SourceTime != 10 || conflict.Stored != "stored" || conflict.Fresh != "fresh" {
		t.Fatalf("the conflict does not say which two records and when: %+v", conflict)
	}
}

// The order is source time and nothing else. A tie broken by record id would
// make two records at one source time two positions, and the frame writer
// would then refuse the pair for not rising - a rule about encoding standing
// in for a rule about identity.
func TestTheHistoryOrderIsSourceTimeAlone(t *testing.T) {
	early, late := mergePoint("z", 10, LevelFactNormal), mergePoint("a", 20, LevelFactNormal)
	if StateHistoryOrder(early, late) >= 0 || StateHistoryOrder(late, early) <= 0 {
		t.Fatal("source time must decide the order")
	}
	if StateHistoryOrder(mergePoint("z", 10, LevelFactNormal), mergePoint("a", 10, LevelFactAnomalous)) != 0 {
		t.Fatal("two points at one source time are one position whatever their record ids")
	}
}
