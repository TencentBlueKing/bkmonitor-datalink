package state

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A history point whose id the derivation cannot rebuild is carried, and comes
// back exactly as it was stored.
//
// The shape is the one production has: before the no-data producer derived its
// record ids, it stored the dimension digest itself as the id. Those points sit
// in state now, and the framed record rebuilds ids instead of storing them - so
// every write of such a record was refused, every round, for good. The record
// is rewritten whole each round, so the point never ages out of the refusal
// either: four strategies stopped writing state and one stopped emitting
// no-data alerts entirely.
//
// The envelope kept every id verbatim and checked none, so this is the
// representation being made able to hold what the one it replaced held.
func TestAHistoryPointThatCannotBeDerivedIsCarriedVerbatim(t *testing.T) {
	identity := packedIdentity()
	// The pre-derivation no-data id: the dimension digest itself.
	legacyID := string(identity.SeriesIdentityDigest)
	old := packedPoint(t, identity, 1758400000, execution.LevelFactNormal)
	if old.RecordID == legacyID {
		t.Fatal("the fixture's legacy id equals the derived one, so this case cannot tell them apart")
	}
	old.RecordID = legacyID
	fresh := packedPoint(t, identity, 1758400060, execution.LevelFactNormal)

	mutation := packedMutation(t, []execution.StateHistoryPoint{old, fresh}, 1)
	// Only the fresh point is this round's; the old one is inherited.
	mutation.AffectedRecords = []execution.RecordAnchor{{RecordID: fresh.RecordID, SourceTime: fresh.SourceTime}}

	raw, err := encodeRuntimePacked(mutation, 7)
	if err != nil {
		t.Fatalf("encode: %v; a record carrying an inherited point that does not derive must still be written", err)
	}
	if raw[4] != packedFrameSchemaV2 {
		t.Fatalf("frame schema = %d, want %d: a record that needs the table says so, and only such records do",
			raw[4], packedFrameSchemaV2)
	}
	view, err := decodeRuntimePacked(raw, identity)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(view.History) != 2 {
		t.Fatalf("read back %d points, wrote 2", len(view.History))
	}
	// Point for point, because carrying the id is worth nothing if anything
	// else about the point moved.
	for index, want := range []execution.StateHistoryPoint{old, fresh} {
		if got := view.History[index]; !reflect.DeepEqual(got, want) {
			t.Errorf("point %d came back as %+v, wrote %+v", index, got, want)
		}
	}
}

// A point this round produced is still refused when it does not derive.
//
// The carry is for what is already stored, not a licence for a producer to
// stop deriving: that producer would write records this build could read and
// an older one could not, and the refusal is what keeps the derivation the one
// way ids are made.
func TestAFreshPointThatDoesNotDeriveIsStillRefused(t *testing.T) {
	identity := packedIdentity()
	fresh := packedPoint(t, identity, 1758400000, execution.LevelFactNormal)
	fresh.RecordID = strings.Repeat("ff", 32)
	mutation := packedMutation(t, []execution.StateHistoryPoint{fresh}, 1)
	mutation.AffectedRecords = []execution.RecordAnchor{{RecordID: fresh.RecordID, SourceTime: fresh.SourceTime}}

	_, err := encodeRuntimePacked(mutation, 1)
	if !errors.Is(err, ErrPackedContract) || PackedRefusalRule(err) != PackedRuleRecordIDNotDerived {
		t.Fatalf("encode error = %v (rule %q), want the framed-record refusal naming %s",
			err, PackedRefusalRule(err), PackedRuleRecordIDNotDerived)
	}
}

// A record whose points all derive is unchanged: still the older schema, still
// the same bytes.
//
// The table is optional so the population that does not need it pays nothing,
// and so a build that reads only the older schema goes on reading all of them.
// Without this the change would be a fleet-wide format bump wearing the shape
// of a fix for four strategies.
func TestARecordWithoutLegacyIDsIsUnchanged(t *testing.T) {
	identity := packedIdentity()
	points := []execution.StateHistoryPoint{
		packedPoint(t, identity, 1758400000, execution.LevelFactNormal),
		packedPoint(t, identity, 1758400060, execution.LevelFactAnomalous),
	}
	raw, err := encodeRuntimePacked(packedMutation(t, points, 1), 3)
	if err != nil {
		t.Fatal(err)
	}
	if raw[4] != packedFrameSchemaV1 {
		t.Fatalf("frame schema = %d, want %d: a record needing no table must not move the population",
			raw[4], packedFrameSchemaV1)
	}
	if strings.Contains(string(raw), "legacy_record_ids") {
		t.Fatal("the header carries the table key for a record with nothing to carry")
	}
}

// The carried id survives the store, not just the encoder.
func TestALegacyRecordIDSurvivesApplyAndLoad(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	store := newBatchStore(t, backend, nil)
	identity := seriesIdentity(0)

	// A real round: the old point is carried, a fresh one is appended. Every
	// mutation must affect at least one record, so "wholly inherited" is not a
	// shape production can produce either.
	legacyID := string(identity.SeriesIdentityDigest)
	old := derivedPoint(t, identity, 60, "detect", execution.LevelFactNormal)
	old.RecordID = legacyID
	fresh := derivedPoint(t, identity, 120, "detect", execution.LevelFactNormal)
	mutation, err := execution.BuildStateMutation(execution.StateMutation{
		Identity: identity, ApplyVersion: applyVersion(),
		AffectedRecords: []execution.RecordAnchor{{RecordID: fresh.RecordID, SourceTime: fresh.SourceTime}},
		Levels: []execution.RuntimeLevelStateMutation{{LevelID: 1, LevelStateCompatibility: "compat",
			HistoryCompleteness: execution.HistoryFull, WarmupRequirementRef: "warm", LastProcessedEventTime: 120}},
		Points: []execution.StateHistoryPoint{old, fresh},
	})
	if err != nil {
		t.Fatal(err)
	}

	applied, err := store.ApplyRuntimeFenced(context.Background(), execution.StateApplyRequest{
		Contract: frozenRef(), Retention: testRetention(), Items: []execution.StateMutation{mutation}}, testApplyFence())
	if err != nil {
		t.Fatal(err)
	}
	if applied.Items[0].Status != execution.StateApplied {
		t.Fatalf("apply = %+v, want it written", applied.Items[0])
	}
	loaded, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(),
		Items: []execution.StatePreflightItem{{Identity: identity,
			ApplyVersion: execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 120, SlotDigest: "slot-2"}}}})
	if err != nil {
		t.Fatal(err)
	}
	history := loaded.Items[0].History
	if len(history) != 2 || history[0].RecordID != legacyID {
		t.Fatalf("history = %+v, want the stored id %q back on the carried point", history, legacyID)
	}
	if history[1].RecordID != fresh.RecordID {
		t.Fatalf("the fresh point came back as %q, want %q", history[1].RecordID, fresh.RecordID)
	}
}

// An inherited id wider than the bound costs it at is refused by name.
//
// The bound prices every carried id at MaxLegacyRecordIDLength, and that is the
// whole basis of the compile-time ceiling. If the encoder carried a wider one
// the bound would stop being a bound the moment it appeared - the same silent
// shape as before it counted the table at all, except arriving one stored id at
// a time instead of all at once. So the width is enforced where the carry
// happens, and the refusal says what was over and by how much rather than
// leaving the next reader to measure it.
//
// It refuses rather than truncating because a truncated id is a different
// record: the point would read back as one that was never written.
func TestAnInheritedRecordIDWiderThanTheBoundIsRefusedByName(t *testing.T) {
	identity := packedIdentity()
	old := packedPoint(t, identity, 1758400000, execution.LevelFactNormal)
	// One character past the width, so the case pins the boundary and not
	// merely "something very long".
	old.RecordID = strings.Repeat("a", MaxLegacyRecordIDLength+1)
	fresh := packedPoint(t, identity, 1758400060, execution.LevelFactNormal)

	mutation := packedMutation(t, []execution.StateHistoryPoint{old, fresh}, 1)
	// Inherited, not this round's - otherwise the not-derived rule refuses it
	// first and this case never reaches the width at all.
	mutation.AffectedRecords = []execution.RecordAnchor{{RecordID: fresh.RecordID, SourceTime: fresh.SourceTime}}

	_, err := encodeRuntimePacked(mutation, 7)
	if !errors.Is(err, ErrPackedContract) || PackedRefusalRule(err) != PackedRuleLegacyRecordIDTooLong {
		t.Fatalf("encode error = %v (rule %q), want the framed-record refusal naming %s",
			err, PackedRefusalRule(err), PackedRuleLegacyRecordIDTooLong)
	}
}

// An inherited id at exactly the width is carried.
//
// The pair of this case and the one above is what makes the check a boundary
// rather than a direction: every id production has written is exactly this
// wide, so a check that refused at the width would refuse the entire population
// the carry exists for, and no case asserting "wide ids are refused" would
// notice.
func TestAnInheritedRecordIDAtExactlyTheBoundIsCarried(t *testing.T) {
	identity := packedIdentity()
	old := packedPoint(t, identity, 1758400000, execution.LevelFactNormal)
	old.RecordID = strings.Repeat("a", MaxLegacyRecordIDLength)
	if old.RecordID == packedPoint(t, identity, 1758400000, execution.LevelFactNormal).RecordID {
		t.Fatal("the fixture's id equals the derived one, so it would never reach the carry")
	}
	fresh := packedPoint(t, identity, 1758400060, execution.LevelFactNormal)

	mutation := packedMutation(t, []execution.StateHistoryPoint{old, fresh}, 1)
	mutation.AffectedRecords = []execution.RecordAnchor{{RecordID: fresh.RecordID, SourceTime: fresh.SourceTime}}

	raw, legacy, err := encodeRuntimePackedCounted(mutation, 7)
	if err != nil {
		t.Fatalf("encode: %v; an id at the width the bound prices is the population the carry is for", err)
	}
	if legacy != 1 {
		t.Fatalf("carried %d ids, want 1: the case has to reach the carry to say anything about it", legacy)
	}
	view, err := decodeRuntimePacked(raw, identity)
	if err != nil {
		t.Fatal(err)
	}
	if view.History[0].RecordID != old.RecordID {
		t.Fatalf("carried id came back as %q, want %q", view.History[0].RecordID, old.RecordID)
	}
}

// The upper bound covers a record whose history is entirely ids the decode
// cannot rebuild.
//
// It is the bound the compile-time ceiling is derived from, so a shape it
// under-reports is a Plan admitted at a size the write then refuses - the
// silent per-series refusal the ceiling exists to prevent. Before the header
// table was counted the bound reported a seventh of what such a record
// actually takes.
func TestTheUpperBoundCoversARecordOfNothingButLegacyIDs(t *testing.T) {
	identity := packedIdentity()
	const count = 4096
	points := make([]execution.StateHistoryPoint, count)
	for index := range points {
		points[index] = packedPoint(t, identity, 1758400000+int64(index)*60, execution.LevelFactNormal)
		if index < count-1 {
			points[index].RecordID = string(identity.SeriesIdentityDigest)
		}
	}
	fresh := points[count-1]
	mutation := packedMutation(t, points, 1)
	mutation.AffectedRecords = []execution.RecordAnchor{{RecordID: fresh.RecordID, SourceTime: fresh.SourceTime}}

	raw, legacy, err := encodeRuntimePackedCounted(mutation, 1)
	if err != nil {
		t.Fatal(err)
	}
	if legacy != count-1 {
		t.Fatalf("carried %d ids, want %d: without them this case does not reach the shape it is about",
			legacy, count-1)
	}
	bound, err := PackedFrameUpperBoundV2(1, count)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > bound {
		t.Fatalf("the record is %d bytes against an upper bound of %d; a bound below the real size admits "+
			"a Plan whose every write is then refused", len(raw), bound)
	}
}
