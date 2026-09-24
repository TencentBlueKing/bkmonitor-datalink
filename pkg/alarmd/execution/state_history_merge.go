// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import "fmt"

// A mutation names the points one round adds; the record it leaves behind is
// those points merged into the history the round loaded, oldest evicted to the
// retention bound. This file is the single definition of that merge: the order
// two points sit in, how many points the merge yields, and the walk that emits
// them.
//
// One definition because the count and the walk have to agree. The count is
// needed before the walk starts - truncation drops the oldest, so a forward
// walk cannot know which point is the first to emit until it knows how many
// there will be - and a count derived from its own comparison disagrees with
// the walk on exactly one family of records: the ones where a fresh point
// lands on a source time the history already holds. Everywhere else the two
// agree, which is what makes the disagreement expensive to find.

// StateHistoryOrder reports how left sits against right in a record's history:
// negative before, zero the same position, positive after.
//
// Position is source time and nothing else. Two points at one source time are
// the same position whatever record ids they carry - one of them has to give
// way, and which one is not this comparison's business. A tie broken by record
// id would make them two positions, and the writer would then refuse the pair
// for not rising rather than naming what is actually wrong with it.
func StateHistoryOrder(left, right StateHistoryPoint) int {
	switch {
	case left.SourceTime < right.SourceTime:
		return -1
	case left.SourceTime > right.SourceTime:
		return 1
	default:
		return 0
	}
}

// HistoryRecordIdentityConflict is two different records claiming one source
// time in a record's history. It is returned rather than refused in place so
// each caller can name it in its own vocabulary; nothing else about the merge
// can fail.
type HistoryRecordIdentityConflict struct {
	SourceTime int64
	Stored     string
	Fresh      string
}

func (conflict *HistoryRecordIdentityConflict) Error() string {
	return fmt.Sprintf("alarmd execution: two records claim source time %d: %s and %s",
		conflict.SourceTime, conflict.Stored, conflict.Fresh)
}

// MergedHistoryPointCount reports how many points base and delta leave behind
// once merged, before retention evicts anything. One pass over both, and it
// allocates nothing: the whole reason it exists is that the walk needs the
// total up front and must not get it by materializing the merge.
//
// Both slices are assumed ordered and internally unique by source time, which
// is what the loaded record and the contract check each guarantee.
func MergedHistoryPointCount(base, delta []StateHistoryPoint) int {
	total, left, right := 0, 0, 0
	for left < len(base) && right < len(delta) {
		switch StateHistoryOrder(base[left], delta[right]) {
		case -1:
			left++
		case 1:
			right++
		default:
			left++
			right++
		}
		total++
	}
	return total + (len(base) - left) + (len(delta) - right)
}

// WalkMergedHistory calls visit for every point the write stores, oldest
// first, with the oldest evicted so that no more than retention remain. A
// retention of zero keeps every point.
//
// Where a delta point shares a source time with a loaded one, the delta point
// is the one emitted: the producer has already merged that position's Level
// facts against the loaded point and refused the pair if they disagreed, so
// the delta point is the loaded point plus this round's facts.
//
// Nothing is materialized. The caller sees each point once, in order, and the
// merge holds two indices.
func WalkMergedHistory(base, delta []StateHistoryPoint, retention uint32, visit func(StateHistoryPoint) error) error {
	skip := 0
	if retention > 0 {
		if total := MergedHistoryPointCount(base, delta); total > int(retention) {
			skip = total - int(retention)
		}
	}
	emitted := 0
	emit := func(point StateHistoryPoint) error {
		emitted++
		if emitted <= skip {
			return nil
		}
		return visit(point)
	}
	left, right := 0, 0
	for left < len(base) && right < len(delta) {
		switch StateHistoryOrder(base[left], delta[right]) {
		case -1:
			if err := emit(base[left]); err != nil {
				return err
			}
			left++
		case 1:
			if err := emit(delta[right]); err != nil {
				return err
			}
			right++
		default:
			if base[left].RecordID != delta[right].RecordID {
				return &HistoryRecordIdentityConflict{SourceTime: base[left].SourceTime,
					Stored: base[left].RecordID, Fresh: delta[right].RecordID}
			}
			if err := emit(delta[right]); err != nil {
				return err
			}
			left++
			right++
		}
	}
	for ; left < len(base); left++ {
		if err := emit(base[left]); err != nil {
			return err
		}
	}
	for ; right < len(delta); right++ {
		if err := emit(delta[right]); err != nil {
			return err
		}
	}
	return nil
}

// MergedHistory materializes what WalkMergedHistory emits. Only for callers
// that genuinely need the window in hand - the evaluator reading it back for
// the next record of the same Slot, and tests. The write path must not use it:
// materializing here moves the per-round allocation from the producer to the
// store rather than removing it.
func MergedHistory(base, delta []StateHistoryPoint, retention uint32) ([]StateHistoryPoint, error) {
	total := MergedHistoryPointCount(base, delta)
	if retention > 0 && total > int(retention) {
		total = int(retention)
	}
	merged := make([]StateHistoryPoint, 0, total)
	if err := WalkMergedHistory(base, delta, retention, func(point StateHistoryPoint) error {
		merged = append(merged, point)
		return nil
	}); err != nil {
		return nil, err
	}
	return merged, nil
}
