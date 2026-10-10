package state

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Every way a framed write can be refused names its rule, and the name is one
// of the bounded set.
//
// The rule is what the admission line carries. Eight refusals shared one
// reason before it: a line saying STATE_CORRUPT sent a reader to read eight
// producers, and three Query Groups refusing inside one minute could not be
// told apart from each other or from the one case anybody had seen before.
//
// Driven through encodeRuntimePacked, the function the store actually calls,
// so a rule reachable only in a constructed error does not count.
func TestEveryFramedRefusalNamesItsRule(t *testing.T) {
	identity := packedIdentity()
	good := func() execution.StateMutation {
		return packedMutation(t, []execution.StateHistoryPoint{
			packedPoint(t, identity, 1758400000, execution.LevelFactNormal),
			packedPoint(t, identity, 1758400060, execution.LevelFactNormal),
		}, 1)
	}

	for _, testCase := range []struct {
		rule   string
		mutate func(execution.StateMutation) execution.StateMutation
	}{
		{rule: PackedRuleDuplicateLevel, mutate: func(m execution.StateMutation) execution.StateMutation {
			m.Levels = append(m.Levels, m.Levels[0])
			return m
		}},
		{rule: PackedRuleLevelNotInMutation, mutate: func(m execution.StateMutation) execution.StateMutation {
			m.Points[0].Levels[0].LevelID = 9
			return m
		}},
		{rule: PackedRuleNoDetectFingerprint, mutate: func(m execution.StateMutation) execution.StateMutation {
			m.Points[0].Levels[0].DetectFingerprint = ""
			return m
		}},
		{rule: PackedRuleTwoFingerprints, mutate: func(m execution.StateMutation) execution.StateMutation {
			m.Points[1].Levels[0].DetectFingerprint = strings.Repeat("0e", 32)
			return m
		}},
		{rule: PackedRuleSourceTimeNotRising, mutate: func(m execution.StateMutation) execution.StateMutation {
			m.Points[1] = m.Points[0]
			return m
		}},
		{rule: PackedRuleRecordIDNotDerived, mutate: func(m execution.StateMutation) execution.StateMutation {
			m.Points[0].RecordID = strings.Repeat("ff", 32)
			// The anchor moves with it, so the point stays this round's.
			m.AffectedRecords = []execution.RecordAnchor{{RecordID: m.Points[0].RecordID, SourceTime: m.Points[0].SourceTime}}
			return m
		}},
		{rule: PackedRuleRecordIDUnderivable, mutate: func(m execution.StateMutation) execution.StateMutation {
			// The derivation refuses the series digest itself, before it can
			// compare an id against anything.
			m.Identity.SeriesIdentityDigest = execution.SeriesIdentityDigest("not-a-digest")
			return m
		}},
		{rule: PackedRuleUnencodableFactState, mutate: func(m execution.StateMutation) execution.StateMutation {
			m.Points[0].Levels[0].Result = execution.LevelFactResult("SOMETHING_THIS_BUILD_CANNOT_STORE")
			return m
		}},
	} {
		t.Run(testCase.rule, func(t *testing.T) {
			_, err := encodeRuntimePacked(testCase.mutate(good()), 1)
			if !errors.Is(err, ErrPackedContract) {
				t.Fatalf("encode error = %v, want the framed-record refusal", err)
			}
			if rule := PackedRefusalRule(err); rule != testCase.rule {
				t.Fatalf("rule = %q, want %q: the line carries the rule, so a refusal that names the "+
					"wrong one sends a reader to the wrong producer", rule, testCase.rule)
			}
		})
	}

	// The control: the unmutated record frames. Without it every case above
	// would pass on an encoder that refused everything.
	if _, err := encodeRuntimePacked(good(), 1); err != nil {
		t.Fatalf("the unmutated record was refused (%v), so these cases prove nothing about which rule fired", err)
	}
}

// Every rule the encoder can name is in the published list, and every name in
// the list is one a reader can meet.
//
// The list is what a reader groups by. A rule added to the encoder and not to
// the list reaches the line as a word nothing documents; a name in the list no
// refusal produces is a row on a dashboard that is always empty, which reads
// as "this never happens" rather than "nothing can report it".
func TestThePublishedRuleListIsExactlyWhatCanBeReported(t *testing.T) {
	published := map[string]bool{}
	for _, rule := range PackedRuleNames {
		if published[rule] {
			t.Fatalf("%q is listed twice", rule)
		}
		published[rule] = true
	}
	if len(PackedRuleNames) != 12 {
		t.Fatalf("the list holds %d rules; every refusal that reaches the line under STATE_CORRUPT needs "+
			"exactly one, so a change to either has to change this number on purpose", len(PackedRuleNames))
	}
	// A refusal this build does not name reports nothing rather than a
	// neighbouring rule, so an unnamed rule shows up as a reason with no rule
	// beside it instead of being counted as one that was named.
	if rule := PackedRefusalRule(errors.New(contract.ReasonStateCorrupt)); rule != "" {
		t.Fatalf("a plain error was given the rule %q", rule)
	}
	if rule := PackedRefusalRule(nil); rule != "" {
		t.Fatalf("a nil error was given the rule %q", rule)
	}
}

// The rule reaches the admission result, which is the path the line reads.
//
// encodeRuntimePacked naming its rule is not enough: encodeForWrite had the
// error in hand and returned only the reason, so the rule was discarded one
// call above the line that needed it and every refusal still arrived as a bare
// STATE_CORRUPT. What this asserts is the seam that was broken, not the
// function that was already right.
func TestTheRefusalRuleReachesTheAdmissionResult(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	router, _ := NewFixedRouter("state-01", backend)
	store, _ := NewExecutionStore(ExecutionStoreOptions{Prefix: "alarmd", Router: router,
		MaxValueBytes: 1 << 20, MaxItemsPerCall: 4, MinTTL: time.Minute, MaxTTL: time.Hour, RestartMargin: time.Minute})

	// A record id the series identity and source time do not derive. Applied
	// before the digest is computed, so the record is internally consistent
	// and reaches the framing rules: mutating afterwards breaks the digest and
	// the store refuses it one branch earlier, under a different rule.
	//
	// This rule rather than one of the structural ones because
	// BuildStateMutation refuses those itself - a point naming a Level the
	// mutation does not carry never gets as far as the encoder, so it could
	// not exercise the seam this case is about.
	point := derivedPoint(t, stateIdentityV2(), 60, "detect", execution.LevelFactNormal)
	point.RecordID = strings.Repeat("ff", 32)
	// Anchored as this round's, which is what makes it the producer's to
	// answer for: an unanchored point is history, and history that does not
	// derive is carried rather than refused.
	thisRound := execution.RecordAnchor{RecordID: point.RecordID, SourceTime: point.SourceTime}
	mutation, err := execution.BuildStateMutation(execution.StateMutation{
		Identity: stateIdentityV2(), ApplyVersion: applyVersion(),
		AffectedRecords: []execution.RecordAnchor{thisRound},
		Levels: []execution.RuntimeLevelStateMutation{{LevelID: 1, LevelStateCompatibility: "compat",
			HistoryCompleteness: execution.HistoryFull, WarmupRequirementRef: "warm", LastProcessedEventTime: 60}},
		Points: []execution.StateHistoryPoint{point},
	})
	if err != nil {
		t.Fatal(err)
	}

	admission, err := store.AdmitRuntime(context.Background(),
		execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(),
			Items: []execution.StateMutation{mutation}})
	if err != nil {
		t.Fatal(err)
	}
	item := admission.Items[0]
	if item.Status != execution.StateAdmissionDeterministicInvalid ||
		item.ReasonCode != execution.ReasonCode(contract.ReasonStateCorrupt) {
		t.Fatalf("admission = %+v, want a deterministic invalid STATE_CORRUPT", item)
	}
	if item.RefusalRule != PackedRuleRecordIDNotDerived {
		t.Fatalf("rule = %q, want %q: the reason alone sends a reader to read every producer",
			item.RefusalRule, PackedRuleRecordIDNotDerived)
	}
	// The control: a record that frames carries no rule, so the field's
	// presence means a refusal rather than meaning this build sets it.
	good, err := execution.BuildStateMutation(execution.StateMutation{
		Identity: stateIdentityV2(), ApplyVersion: applyVersion(),
		AffectedRecords: []execution.RecordAnchor{derivedAnchor(t, stateIdentityV2(), 60)},
		Levels: []execution.RuntimeLevelStateMutation{{LevelID: 1, LevelStateCompatibility: "compat",
			HistoryCompleteness: execution.HistoryFull, WarmupRequirementRef: "warm", LastProcessedEventTime: 60}},
		Points: []execution.StateHistoryPoint{derivedPoint(t, stateIdentityV2(), 60, "detect", execution.LevelFactNormal)},
	})
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := store.AdmitRuntime(context.Background(),
		execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(),
			Items: []execution.StateMutation{good}})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Items[0].Status != execution.StateAdmissionAccepted || accepted.Items[0].RefusalRule != "" {
		t.Fatalf("an admitted record carries %+v, want no rule", accepted.Items[0])
	}
}

// The apply path names its refusals too.
//
// The admission case alone was not enough, and the gap it left was the one
// that matters: a Slot reaches apply only after admission accepted it, so a
// producer that changes the record between the two steps is refused here, and
// that is the shape a rollout produces. Both refusals on this path reported
// STATE_CORRUPT with no rule, and the batch branch folded two causes into one
// condition so neither could have been named without splitting it.
func TestTheApplyPathNamesItsRefusals(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		rule     string
		corrupt  func(execution.StateMutation) execution.StateMutation
		identity func() execution.StateKeyIdentity
	}{
		{name: "the digest does not cover what was sent", rule: PackedRuleMutationDigestMismatch,
			corrupt: func(m execution.StateMutation) execution.StateMutation {
				// Edited after the digest was computed, which is exactly what a
				// producer changing the record between admission and apply does.
				m.Points[0].SourceTime += 30
				return m
			}},
		{name: "the identity does not produce a key", rule: PackedRuleIdentityKeyUnderivable,
			// Built with this identity rather than edited into it, so the
			// digest covers it and the record reaches key derivation instead
			// of being refused by the digest one branch earlier. A business
			// identity the key scheme will not accept but the mutation
			// builder will: the two validate different things, which is the
			// gap this refusal exists for.
			identity: func() execution.StateKeyIdentity {
				identity := seriesIdentity(0)
				identity.Plan.BusinessID = "007"
				return identity
			}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			backend := &casMemoryBackend{values: make(map[string][]byte)}
			store := newBatchStore(t, backend, nil)
			identity := seriesIdentity(0)
			if testCase.identity != nil {
				identity = testCase.identity()
			}
			mutation := seriesMutation(t, identity, applyVersion(), 0, "")
			if testCase.corrupt != nil {
				mutation = testCase.corrupt(mutation)
			}

			result, err := store.ApplyRuntimeFenced(context.Background(),
				execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(),
					Items: []execution.StateMutation{mutation}}, testApplyFence())
			if err != nil {
				t.Fatal(err)
			}
			item := result.Items[0]
			if item.Status != execution.StateApplyDeterministicInvalid ||
				item.ReasonCode != execution.ReasonCode(contract.ReasonStateCorrupt) {
				t.Fatalf("apply = %+v, want a deterministic invalid STATE_CORRUPT", item)
			}
			if item.RefusalRule != testCase.rule {
				t.Fatalf("rule = %q, want %q: on this path the reason alone leaves a reader with nothing "+
					"to tell the two causes apart", item.RefusalRule, testCase.rule)
			}
		})
	}

	// The control: an uncorrupted record applies and carries no rule, so the
	// field's presence means a refusal rather than meaning this build sets it.
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	store := newBatchStore(t, backend, nil)
	result, err := store.ApplyRuntimeFenced(context.Background(),
		execution.StateApplyRequest{Contract: frozenRef(), Retention: testRetention(),
			Items: seriesMutations(t, 1, applyVersion(), 0)}, testApplyFence())
	if err != nil {
		t.Fatal(err)
	}
	if result.Items[0].Status != execution.StateApplied || result.Items[0].RefusalRule != "" {
		t.Fatalf("an applied record carries %+v, want no rule", result.Items[0])
	}
}

// The rule reaches the apply result on both apply paths - the pipelined path
// a preflight witness sends a write down, and the sequential path a write
// without one takes. Production refuses on apply, not admission (admission
// passed the same bytes moments earlier), so a rule that only reaches the
// admission result is one the operator never sees.
func TestTheRefusalRuleReachesTheApplyResultOnBothPaths(t *testing.T) {
	underived := func(t *testing.T, identity execution.StateKeyIdentity) execution.StateMutation {
		t.Helper()
		point := derivedPoint(t, identity, 60, "detect", execution.LevelFactNormal)
		point.RecordID = strings.Repeat("ff", 32)
		mutation, err := execution.BuildStateMutation(execution.StateMutation{
			Identity: identity, ApplyVersion: applyVersion(),
			// Anchored as this round's: the refusal is about a producer that
			// stopped deriving, and only a point this round produced is the
			// producer's to answer for.
			AffectedRecords: []execution.RecordAnchor{{RecordID: point.RecordID, SourceTime: point.SourceTime}},
			Levels: []execution.RuntimeLevelStateMutation{{LevelID: 1, LevelStateCompatibility: "compat",
				HistoryCompleteness: execution.HistoryFull, WarmupRequirementRef: "warm", LastProcessedEventTime: 60}},
			Points: []execution.StateHistoryPoint{point},
		})
		if err != nil {
			t.Fatal(err)
		}
		return mutation
	}
	requireRule := func(t *testing.T, item execution.StateApplyItemResult) {
		t.Helper()
		if item.Status != execution.StateApplyDeterministicInvalid || item.ReasonCode != execution.ReasonCode(contract.ReasonStateCorrupt) {
			t.Fatalf("apply = %+v, want a deterministic invalid STATE_CORRUPT", item)
		}
		if item.RefusalRule != PackedRuleRecordIDNotDerived {
			t.Fatalf("rule = %q, want %q on the apply result: the path production refuses on", item.RefusalRule, PackedRuleRecordIDNotDerived)
		}
	}
	t.Run("pipelined, with a preflight witness", func(t *testing.T) {
		backend := newPipelineMemoryBackend()
		store := newBatchStore(t, backend, fixedFenceKeys{testFenceKeys()})
		mutation := underived(t, seriesIdentity(0))
		if _, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(),
			Items: preflightItems([]execution.StateMutation{mutation})}); err != nil {
			t.Fatal(err)
		}
		result, err := store.ApplyRuntimeFenced(context.Background(), execution.StateApplyRequest{Contract: frozenRef(),
			Retention: testRetention(), Items: []execution.StateMutation{mutation}}, testApplyFence())
		if err != nil {
			t.Fatal(err)
		}
		requireRule(t, result.Items[0])
	})
	t.Run("sequential, without a witness", func(t *testing.T) {
		backend := newPipelineMemoryBackend()
		store := newBatchStore(t, backend, nil)
		mutation := underived(t, seriesIdentity(1))
		result, err := store.ApplyRuntime(context.Background(), execution.StateApplyRequest{Contract: frozenRef(),
			Retention: testRetention(), Items: []execution.StateMutation{mutation}})
		if err != nil {
			t.Fatal(err)
		}
		requireRule(t, result.Items[0])
	})
}
