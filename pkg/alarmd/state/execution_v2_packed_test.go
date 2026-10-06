package state

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func packedIdentity() execution.StateKeyIdentity {
	return execution.StateKeyIdentity{
		Plan:                 execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"},
		StateGeneration:      execution.StateGeneration(strings.Repeat("c1", 32)),
		SeriesIdentityDigest: execution.SeriesIdentityDigest(strings.Repeat("ab", 32)),
	}
}

// packedPoint builds a point the way a producer does: the id is derived, never
// chosen, and the fingerprint is the Level's own.
func packedPoint(t *testing.T, identity execution.StateKeyIdentity, sourceTime int64, results ...execution.LevelFactResult) execution.StateHistoryPoint {
	t.Helper()
	recordID, err := contract.DeriveRecordIDV2(string(identity.SeriesIdentityDigest), sourceTime)
	if err != nil {
		t.Fatalf("derive record id: %v", err)
	}
	facts := make([]execution.StateLevelFact, 0, len(results))
	for index, result := range results {
		facts = append(facts, execution.StateLevelFact{LevelID: uint32(index + 1),
			DetectFingerprint: packedFingerprint(index), Result: result})
	}
	return execution.StateHistoryPoint{RecordID: recordID, SourceTime: sourceTime, Levels: facts}
}

func packedFingerprint(index int) string {
	return strings.Repeat([]string{"1a", "2b", "3c", "4d"}[index%4], 32)
}

func packedMutation(t *testing.T, points []execution.StateHistoryPoint, levelCount int) execution.StateMutation {
	t.Helper()
	levels := make([]execution.RuntimeLevelStateMutation, levelCount)
	for index := range levels {
		levels[index] = execution.RuntimeLevelStateMutation{LevelID: uint32(index + 1),
			LevelStateCompatibility: "COMPATIBLE", HistoryCompleteness: execution.HistoryFull,
			WarmupRequirementRef: strings.Repeat("9f", 32), LastProcessedEventTime: 1758400000}
	}
	// Every point counted as this round's, which is what the refusal cases
	// mean by "the producer sent this": a point the round did not produce is
	// history, and history that does not derive is stored rather than refused.
	affected := make([]execution.RecordAnchor, 0, len(points))
	for _, point := range points {
		affected = append(affected, execution.RecordAnchor{RecordID: point.RecordID, SourceTime: point.SourceTime})
	}
	return execution.StateMutation{Identity: packedIdentity(), AffectedRecords: affected, ApplyVersion: execution.ApplyVersion{
		StateApplyEpoch: 3, EvaluationTime: 1758400000, SlotDigest: execution.SlotIdentityDigest(strings.Repeat("7e", 32))},
		MutationDigest: execution.MutationDigest(strings.Repeat("5a", 32)), Levels: levels, Points: points}
}

// Every point goes in and comes back, field for field, including the two facts
// the packed window could not carry.
//
// Asserted over the whole StateHistoryPoint. A representation can agree on
// byte counts and point counts while dropping a field inside the point, and an
// assertion naming the fields it expects to survive cannot report the loss of
// one it does not name - which is how the detect fingerprint went missing from
// the first design of this record.
func TestAFramedRecordGivesBackEveryPointFieldForField(t *testing.T) {
	identity := packedIdentity()
	points := []execution.StateHistoryPoint{
		packedPoint(t, identity, 1758400000, execution.LevelFactNormal, execution.LevelFactAnomalous),
		packedPoint(t, identity, 1758400060, execution.LevelFactError, execution.LevelFactUnavailable),
		// No Level found this point usable. The packed window refuses to store
		// such a point at all; the result contract reads it back to justify
		// carrying an unknown outcome forward.
		packedPoint(t, identity, 1758400120, execution.LevelFactUnavailable, execution.LevelFactUnavailable),
		packedPoint(t, identity, 1758400180, execution.LevelFactAnomalous, execution.LevelFactError),
		// The fifth state: a point one Level has no fact for at all. The
		// present bitmap is the only thing that keeps it from coming back as
		// UNAVAILABLE, and a fixture where every Level has a fact at every
		// point never reads that bitmap.
		packedPoint(t, identity, 1758400240, execution.LevelFactNormal),
	}
	if last := points[len(points)-1]; len(last.Levels) != 1 {
		t.Fatalf("the fixture's last point carries %d Level facts, want one so a Level is absent", len(last.Levels))
	}
	mutation := packedMutation(t, points, 2)

	raw, err := encodeRuntimePacked(mutation, 9)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !packedFrame(raw) {
		t.Fatal("the encoded record is not recognised as a framed record, so a reader would try JSON on it")
	}
	view, err := decodeRuntimePacked(raw, identity)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(view.History) != len(points) {
		t.Fatalf("wrote %d points and read back %d", len(points), len(view.History))
	}
	for index, want := range points {
		if got := view.History[index]; !reflect.DeepEqual(got, want) {
			t.Errorf("point %d came back as %+v, wrote %+v", index, got, want)
		}
	}
	if view.BlobRevision != 9 || view.PersistedMutationDigest != mutation.MutationDigest ||
		view.PersistedApplyVersion != mutation.ApplyVersion {
		t.Errorf("header fields did not survive: revision=%d digest=%q version=%+v",
			view.BlobRevision, view.PersistedMutationDigest, view.PersistedApplyVersion)
	}
	if len(view.Levels) != 2 || view.Levels[0].WarmupRequirementRef != strings.Repeat("9f", 32) {
		t.Errorf("Level state did not survive: %+v", view.Levels)
	}
}

// The framed record is smaller than the envelope by the margin the decision
// was argued from, on the same window.
func TestAFramedRecordIsSmallerThanTheEnvelope(t *testing.T) {
	identity := packedIdentity()
	points := make([]execution.StateHistoryPoint, 1469)
	for index := range points {
		points[index] = packedPoint(t, identity, 1758400000+int64(index)*60, execution.LevelFactNormal)
	}
	mutation := packedMutation(t, points, 1)
	framed, err := encodeRuntimePacked(mutation, 4)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	envelope, err := encodeRuntime(mutation, 4)
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
	ratio := float64(len(envelope)) / float64(len(framed))
	t.Logf("1469 points, one Level: envelope %d bytes, framed %d bytes, %.1fx", len(envelope), len(framed), ratio)
	if ratio < 10 {
		t.Fatalf("the framed record is only %.1fx smaller; decision-021 rests on the per-point cost falling "+
			"from hundreds of bytes to single digits, and below this the change does not pay for its migration",
			ratio)
	}
	bound, err := PackedFrameUpperBoundV2(1, len(points))
	if err != nil {
		t.Fatalf("bound: %v", err)
	}
	if len(framed) > bound {
		t.Fatalf("the encoder produced %d bytes against an upper bound of %d; the bound is what the compile "+
			"time ceiling is derived from, so a record above it is admitted and then refused", len(framed), bound)
	}
}

// A producer that stops deriving the record id is refused where it writes.
func TestAFramedRecordRefusesARecordIDItsSourceTimeDoesNotDerive(t *testing.T) {
	identity := packedIdentity()
	point := packedPoint(t, identity, 1758400000, execution.LevelFactNormal)
	derived := point.RecordID
	point.RecordID = strings.Repeat("ff", 32)
	if point.RecordID == derived {
		t.Fatal("the fixture did not change the id, so both branches carry the same value")
	}
	if _, err := encodeRuntimePacked(packedMutation(t, []execution.StateHistoryPoint{point}, 1), 1); !errors.Is(err, ErrPackedContract) {
		t.Fatalf("encode error = %v, want the framed-record refusal: the id is not stored, so a point whose "+
			"id the series and source time do not derive would come back as a different record", err)
	}
}

// Two fingerprints for one Level cannot be represented, so the write is refused
// rather than the disagreement being silently resolved.
func TestAFramedRecordRefusesTwoFingerprintsForOneLevel(t *testing.T) {
	identity := packedIdentity()
	first := packedPoint(t, identity, 1758400000, execution.LevelFactNormal)
	second := packedPoint(t, identity, 1758400060, execution.LevelFactNormal)
	second.Levels[0].DetectFingerprint = strings.Repeat("0e", 32)
	if second.Levels[0].DetectFingerprint == first.Levels[0].DetectFingerprint {
		t.Fatal("the fixture left both points on one fingerprint, so this case cannot separate the branches")
	}
	_, err := encodeRuntimePacked(packedMutation(t, []execution.StateHistoryPoint{first, second}, 1), 1)
	if !errors.Is(err, ErrPackedContract) {
		t.Fatalf("encode error = %v, want the framed-record refusal", err)
	}
}

func versionedView(epoch execution.StateApplyEpoch, at execution.EvaluationTime) *execution.RuntimeStateView {
	return &execution.RuntimeStateView{PersistedApplyVersion: execution.ApplyVersion{
		StateApplyEpoch: epoch, EvaluationTime: at, SlotDigest: execution.SlotIdentityDigest(strings.Repeat("7e", 32))}}
}

// Which of the two records a round reads is decided by version, not by which
// key it came from.
func TestTheNewerOfTheTwoRecordsIsTheOneRead(t *testing.T) {
	for _, testCase := range []struct {
		name             string
		framed, envelope *execution.RuntimeStateView
		want             runtimeViewSource
	}{
		{name: "only the envelope exists, which is every series before the first framed write",
			framed: nil, envelope: versionedView(3, 1758400000), want: runtimeViewEnvelope},
		{name: "only the framed record exists, which is every series after it",
			framed: versionedView(3, 1758400000), envelope: nil, want: runtimeViewFramed},
		{name: "the framed record is newer",
			framed: versionedView(3, 1758400060), envelope: versionedView(3, 1758400000), want: runtimeViewFramed},
		// Ownership moves during a rollout: an old binary taking the Query
		// Group back writes the envelope after a new one wrote the framed
		// record, so the envelope is legitimately newer and preferring the
		// framed key on sight would discard those rounds.
		{name: "the envelope is newer because ownership bounced back",
			framed: versionedView(3, 1758400000), envelope: versionedView(3, 1758400060), want: runtimeViewEnvelope},
		{name: "an older epoch loses even with a later evaluation time",
			framed: versionedView(2, 1758400600), envelope: versionedView(3, 1758400000), want: runtimeViewEnvelope},
		// Either is correct to read; taking the envelope would re-derive the
		// framed record every round for as long as both exist, so the
		// migration would never converge while looking healthy.
		{name: "equal versions go to the framed record so the migration converges",
			framed: versionedView(3, 1758400000), envelope: versionedView(3, 1758400000), want: runtimeViewFramed},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, source := chooseRuntimeView(testCase.framed, testCase.envelope); source != testCase.want {
				t.Fatalf("read the %v record, want the %v one", source, testCase.want)
			}
		})
	}
}

// Neither key holding a record is its own answer, distinct from either of them
// holding one.
func TestNeitherRecordExistingIsItsOwnAnswer(t *testing.T) {
	view, source := chooseRuntimeView(nil, nil)
	if source != runtimeViewNone {
		t.Fatalf("source = %v, want none", source)
	}
	if view.BlobRevision != 0 {
		t.Fatalf("a view was returned for a series neither key holds: %+v", view)
	}
}
