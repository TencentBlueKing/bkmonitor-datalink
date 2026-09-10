package execution

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func memoTestMutation(points int) StateMutation {
	history := make([]StateHistoryPoint, 0, points)
	for index := 0; index < points; index++ {
		history = append(history, StateHistoryPoint{
			RecordID: "9f2a7c1e5b3d8f0a4c6e2b9d7f1a3c5e.175746240", SourceTime: int64(1757462400 + index*60),
			Levels: []StateLevelFact{
				{LevelID: 1, DetectFingerprint: "2f6c1d9e4a7b0c3d5e8f1a2b3c4d5e6f", Result: LevelFactResult("NORMAL")},
				{LevelID: 2, DetectFingerprint: "3a7d2e0f5b8c1d4e7f0a3b6c9d2e5f8a", Result: LevelFactResult("NORMAL")},
			},
		})
	}
	return StateMutation{
		Identity: StateKeyIdentity{
			Plan:                 PlanIdentity{TenantID: "system", BusinessID: "2", StrategyID: "104857"},
			StateGeneration:      "2f6c1d9e4a7b0c3d5e8f1a2b3c4d5e6f",
			SeriesIdentityDigest: "9f2a7c1e5b3d8f0a4c6e2b9d7f1a3c5e",
		},
		ApplyVersion:    ApplyVersion{StateApplyEpoch: 7, EvaluationTime: 1757462400, SlotDigest: "5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f"},
		AffectedRecords: []RecordAnchor{{RecordID: "9f2a7c1e5b3d8f0a4c6e2b9d7f1a3c5e.1757462400", SourceTime: 1757462400}},
		Levels: []RuntimeLevelStateMutation{
			{LevelID: 1, LevelStateCompatibility: "2f6c1d9e4a7b0c3d5e8f1a2b3c4d5e6f", WarmupRequirementRef: "8b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e", HistoryCompleteness: HistoryFull, LastProcessedEventTime: 1757462400},
			{LevelID: 2, LevelStateCompatibility: "3a7d2e0f5b8c1d4e7f0a3b6c9d2e5f8a", WarmupRequirementRef: "9c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f", HistoryCompleteness: HistoryFull, LastProcessedEventTime: 1757462400},
		},
		Points: history,
	}
}

// TestStateMutationDigestIsStable pins the persisted digest of a fixed
// mutation. The digest travels into stored state and decides ALREADY_APPLIED
// on replay, so it must survive refactoring of how it is derived or cached.
//
// The expected value is stated twice on purpose: once as a literal, which
// catches a change in the canonical encoder, and once by digesting the wire
// shape written out in full here, which catches a change in the payload type
// the production path now shares with its memo key.
func TestStateMutationDigestIsStable(t *testing.T) {
	mutation := normalizeStateMutation(memoTestMutation(3))
	independent, err := contract.DeriveCanonicalDigestV2("alarmd-runtime-state-mutation-v1", struct {
		Identity        StateKeyIdentity            `json:"identity"`
		ApplyVersion    ApplyVersion                `json:"apply_version"`
		AffectedRecords []RecordAnchor              `json:"affected_records"`
		SeriesGuard     *StateGuardFact             `json:"series_guard,omitempty"`
		Levels          []RuntimeLevelStateMutation `json:"levels"`
		Points          []StateHistoryPoint         `json:"points"`
	}{mutation.Identity, mutation.ApplyVersion, mutation.AffectedRecords, mutation.SeriesGuard, mutation.Levels, mutation.Points})
	if err != nil {
		t.Fatalf("derive from the declared wire shape: %v", err)
	}
	built, err := BuildStateMutation(memoTestMutation(3))
	if err != nil {
		t.Fatalf("build mutation: %v", err)
	}
	const pinned = MutationDigest("8ccd6c96a1b02afef2e1b124b73e37c705a813277de2e6d8b7fd181fb13adc79")
	if built.MutationDigest != MutationDigest(independent) {
		t.Fatalf("state mutation digest left its declared wire shape:\n got %s\nwant %s", built.MutationDigest, independent)
	}
	if built.MutationDigest != pinned {
		t.Fatalf("state mutation digest changed:\n got %s\nwant %s", built.MutationDigest, pinned)
	}
}

// TestStateMutationDigestMemoServesOnlyItsOwnContent proves the memo never
// answers for content it did not digest: one changed history point must yield
// a different digest, not the remembered one.
func TestStateMutationDigestMemoServesOnlyItsOwnContent(t *testing.T) {
	first, err := BuildStateMutation(memoTestMutation(3))
	if err != nil {
		t.Fatalf("build first mutation: %v", err)
	}
	repeat, err := BuildStateMutation(memoTestMutation(3))
	if err != nil {
		t.Fatalf("build repeat mutation: %v", err)
	}
	if first.MutationDigest != repeat.MutationDigest {
		t.Fatalf("identical content produced different digests: %s and %s", first.MutationDigest, repeat.MutationDigest)
	}
	if err := first.ValidateDigest(); err != nil {
		t.Fatalf("validate remembered digest: %v", err)
	}
	changed := memoTestMutation(3)
	changed.Points[1].Levels[0].Result = LevelFactResult("ANOMALOUS")
	rebuilt, err := BuildStateMutation(changed)
	if err != nil {
		t.Fatalf("build changed mutation: %v", err)
	}
	if rebuilt.MutationDigest == first.MutationDigest {
		t.Fatal("changed content was served the remembered digest")
	}
	// The remembered digest must also not validate against the changed content.
	forged := changed
	forged.MutationDigest = first.MutationDigest
	if err := forged.ValidateDigest(); err == nil {
		t.Fatal("changed content validated against a digest it did not produce")
	}
}

// TestProvisionalStateMutationCarriesNoDigest pins that the per-record builder
// leaves the digest to the mutation that survives the series.
func TestProvisionalStateMutationCarriesNoDigest(t *testing.T) {
	provisional, err := BuildProvisionalStateMutation(memoTestMutation(3))
	if err != nil {
		t.Fatalf("build provisional mutation: %v", err)
	}
	if provisional.MutationDigest != "" {
		t.Fatalf("provisional mutation carries a digest: %s", provisional.MutationDigest)
	}
	if err := provisional.ValidateDigest(); err == nil {
		t.Fatal("a provisional mutation passed the digest contract")
	}
	sealed, err := BuildStateMutation(provisional)
	if err != nil {
		t.Fatalf("seal provisional mutation: %v", err)
	}
	if err := sealed.ValidateDigest(); err != nil {
		t.Fatalf("sealed mutation failed the digest contract: %v", err)
	}
}
