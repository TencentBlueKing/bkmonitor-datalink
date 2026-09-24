// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// deltaMutation is a round's own write: the record it was built against, the
// points it adds, and the bound in force.
func deltaMutation(t *testing.T, base, delta []execution.StateHistoryPoint, retention uint32, levels int) execution.StateMutation {
	t.Helper()
	mutation := packedMutation(t, delta, levels)
	mutation.BaseHistory = base
	mutation.RetentionPoints = retention
	return mutation
}

// The record the frame holds is the loaded history with this round's points
// merged in, and the write is the only place that assembly happens. Before
// this the producer sent the whole window and the frame wrote what it was
// given, which is what made every round rebuild and re-digest 1469 points.
func TestTheFramedRecordIsTheLoadedHistoryWithThisRoundsPointsMergedIn(t *testing.T) {
	identity := packedIdentity()
	base := []execution.StateHistoryPoint{
		packedPoint(t, identity, 1758400000, execution.LevelFactNormal),
		packedPoint(t, identity, 1758400060, execution.LevelFactNormal),
	}
	delta := []execution.StateHistoryPoint{packedPoint(t, identity, 1758400120, execution.LevelFactAnomalous)}
	raw, err := encodeRuntimePacked(deltaMutation(t, base, delta, 0, 1), 9)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	view, err := decodeRuntimePacked(raw, identity)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(view.History) != 3 {
		t.Fatalf("the record holds %d points, want the two loaded and the one added", len(view.History))
	}
	for index, want := range append(append([]execution.StateHistoryPoint(nil), base...), delta...) {
		if view.History[index].SourceTime != want.SourceTime || view.History[index].RecordID != want.RecordID {
			t.Fatalf("point %d is %+v, want %+v", index, view.History[index], want)
		}
	}
	if view.History[2].Levels[0].Result != execution.LevelFactAnomalous {
		t.Fatalf("the added point reads back as %q", view.History[2].Levels[0].Result)
	}
}

// The header's point count is what the frame emitted, not how many points the
// mutation named. A count taken from the addition would say one while the
// frame held the whole record, and the reader trusts the count.
func TestTheHeaderCountsThePointsTheFrameHoldsNotThePointsTheRoundAdded(t *testing.T) {
	identity := packedIdentity()
	base := []execution.StateHistoryPoint{
		packedPoint(t, identity, 1758400000, execution.LevelFactNormal),
		packedPoint(t, identity, 1758400060, execution.LevelFactNormal),
		packedPoint(t, identity, 1758400120, execution.LevelFactNormal),
	}
	delta := []execution.StateHistoryPoint{packedPoint(t, identity, 1758400180, execution.LevelFactNormal)}
	raw, err := encodeRuntimePacked(deltaMutation(t, base, delta, 0, 1), 9)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	view, err := decodeRuntimePacked(raw, identity)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(view.History) != 4 {
		t.Fatalf("the reader found %d points in a record of four; the header's count is what it reads",
			len(view.History))
	}
}

// A round re-evaluating a record the history already holds writes the same
// record back, not one point longer. This is the shape a restart produces -
// the Slot writes, the process stops before the Slot is recorded, the record
// is evaluated again on the way back - and under the whole-window mutation it
// was the producer's merge that kept the count still. It is the store's now.
func TestARetryCarryingAnAlreadyStoredPointLeavesTheRecordTheSameLength(t *testing.T) {
	identity := packedIdentity()
	base := []execution.StateHistoryPoint{
		packedPoint(t, identity, 1758400000, execution.LevelFactNormal),
		packedPoint(t, identity, 1758400060, execution.LevelFactAnomalous),
	}
	delta := []execution.StateHistoryPoint{packedPoint(t, identity, 1758400060, execution.LevelFactAnomalous)}
	raw, err := encodeRuntimePacked(deltaMutation(t, base, delta, 0, 1), 9)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	view, err := decodeRuntimePacked(raw, identity)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(view.History) != 2 {
		t.Fatalf("the record grew to %d points, want 2: a record evaluated twice is one point, and a "+
			"writer that appends instead of merging grows it once per restart", len(view.History))
	}
	if view.History[1].SourceTime != 1758400060 {
		t.Fatalf("the merged position is at %d", view.History[1].SourceTime)
	}
}

// The bound applies to the merged record and evicts its oldest, which is what
// moving truncation from the producer to the writer has to preserve.
func TestTheWriterBoundsTheMergedRecordByEvictingItsOldest(t *testing.T) {
	identity := packedIdentity()
	base := []execution.StateHistoryPoint{
		packedPoint(t, identity, 1758400000, execution.LevelFactNormal),
		packedPoint(t, identity, 1758400060, execution.LevelFactNormal),
	}
	delta := []execution.StateHistoryPoint{packedPoint(t, identity, 1758400120, execution.LevelFactAnomalous)}
	raw, err := encodeRuntimePacked(deltaMutation(t, base, delta, 2, 1), 9)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	view, err := decodeRuntimePacked(raw, identity)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(view.History) != 2 {
		t.Fatalf("a bound of two kept %d points", len(view.History))
	}
	if view.History[0].SourceTime != 1758400060 || view.History[1].SourceTime != 1758400120 {
		t.Fatalf("the bound kept %d and %d; it must drop the oldest",
			view.History[0].SourceTime, view.History[1].SourceTime)
	}
}

// Two different records at one source time are named where the merge brings
// them together. The producer refuses the same pair first, so reaching this is
// already a bug elsewhere; a merge that silently picked one would decide a
// record's past by which side it arrived on.
func TestTheWriterNamesTwoRecordsClaimingOneSourceTime(t *testing.T) {
	identity := packedIdentity()
	stored := packedPoint(t, identity, 1758400000, execution.LevelFactNormal)
	fresh := stored
	fresh.RecordID = "0" + stored.RecordID[1:]
	_, err := encodeRuntimePacked(deltaMutation(t,
		[]execution.StateHistoryPoint{stored}, []execution.StateHistoryPoint{fresh}, 0, 1), 9)
	if rule := PackedRefusalRule(err); rule != PackedRuleTwoRecordsOneSourceTime {
		t.Fatalf("the writer refused with %q (%v), want %q", rule, err, PackedRuleTwoRecordsOneSourceTime)
	}
}

// The header carries one fingerprint per Level for the whole record, so the
// disagreement it refuses has to be looked for across the whole record - an
// inherited point against a fresh one, which is the only way the two can
// differ. Reading only the round's own points would let a record be written
// whose stored points disagree with its header.
func TestTheHeadersFingerprintIsCheckedAgainstTheInheritedPointsToo(t *testing.T) {
	identity := packedIdentity()
	stale := packedPoint(t, identity, 1758400000, execution.LevelFactNormal)
	stale.Levels[0].DetectFingerprint = packedFingerprint(3)
	fresh := packedPoint(t, identity, 1758400060, execution.LevelFactNormal)
	_, err := encodeRuntimePacked(deltaMutation(t,
		[]execution.StateHistoryPoint{stale}, []execution.StateHistoryPoint{fresh}, 0, 1), 9)
	if rule := PackedRefusalRule(err); rule != PackedRuleTwoFingerprints {
		t.Fatalf("the writer refused with %q (%v), want %q", rule, err, PackedRuleTwoFingerprints)
	}
}

// An inherited id the derivation cannot rebuild is stored rather than refused,
// and a point this round produced with the same id is refused. The difference
// is the affected-record set, and it has to keep working now that the two
// kinds of point reach the encoder from different fields.
func TestAnInheritedUndervivedIDIsCarriedWhileThisRoundsIsRefused(t *testing.T) {
	identity := packedIdentity()
	inherited := packedPoint(t, identity, 1758400000, execution.LevelFactNormal)
	inherited.RecordID = "legacy-dimension-digest"
	fresh := packedPoint(t, identity, 1758400060, execution.LevelFactNormal)
	carried := deltaMutation(t, []execution.StateHistoryPoint{inherited}, []execution.StateHistoryPoint{fresh}, 0, 1)
	raw, legacy, err := encodeRuntimePackedCounted(carried, 9)
	if err != nil {
		t.Fatalf("an inherited id the derivation cannot rebuild must be carried, not refused: %v", err)
	}
	if legacy != 1 {
		t.Fatalf("the writer counted %d carried ids, want the one inherited point", legacy)
	}
	view, err := decodeRuntimePacked(raw, identity)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.History[0].RecordID != inherited.RecordID {
		t.Fatalf("the carried id came back as %q", view.History[0].RecordID)
	}

	own := packedPoint(t, identity, 1758400060, execution.LevelFactNormal)
	own.RecordID = "not-derived-from-this-series"
	refused := deltaMutation(t, []execution.StateHistoryPoint{inherited}, []execution.StateHistoryPoint{own}, 0, 1)
	_, _, err = encodeRuntimePackedCounted(refused, 9)
	if rule := PackedRefusalRule(err); rule != PackedRuleRecordIDNotDerived {
		t.Fatalf("this round's undeliverable id was refused with %q (%v), want %q", rule, err, PackedRuleRecordIDNotDerived)
	}
}

// The bound and a merged position in the same record, which is the only shape
// where the pre-scan and the walk can disagree. The pre-scan decides how many
// of the oldest points the walk skips, so a count that reads a merged position
// as two positions drops one point too many - and only from records a restart
// or a late arrival produced, which is to say from the records that matter.
func TestABoundedRecordWithAMergedPositionKeepsTheRightPoints(t *testing.T) {
	identity := packedIdentity()
	base := []execution.StateHistoryPoint{
		packedPoint(t, identity, 1758400000, execution.LevelFactNormal),
		packedPoint(t, identity, 1758400060, execution.LevelFactNormal),
		packedPoint(t, identity, 1758400120, execution.LevelFactNormal),
	}
	// One point lands on a position the record holds, one is new: four
	// positions, not five.
	delta := []execution.StateHistoryPoint{
		packedPoint(t, identity, 1758400120, execution.LevelFactAnomalous),
		packedPoint(t, identity, 1758400180, execution.LevelFactAnomalous),
	}
	raw, err := encodeRuntimePacked(deltaMutation(t, base, delta, 3, 1), 9)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	view, err := decodeRuntimePacked(raw, identity)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(view.History) != 3 {
		t.Fatalf("the record holds %d points under a bound of three", len(view.History))
	}
	want := []int64{1758400060, 1758400120, 1758400180}
	for index, sourceTime := range want {
		if view.History[index].SourceTime != sourceTime {
			t.Fatalf("the record holds %d at position %d, want %d: four positions merge to four, and a "+
				"count that reads the merged one as two evicts one point too many",
				view.History[index].SourceTime, index, sourceTime)
		}
	}
}
