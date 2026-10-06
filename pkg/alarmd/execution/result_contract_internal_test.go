package execution

import (
	"strings"
	"testing"
)

func TestAnomalousDetectFactAllowsAllValidTriggerOutcomes(t *testing.T) {
	for _, outcome := range []LevelOutcomeKind{LevelOutcomeNormal, LevelOutcomeAbnormal, LevelOutcomeRecovery} {
		if !levelFactMatchesOutcome(LevelFactAnomalous, outcome) {
			t.Fatalf("ANOMALOUS detect fact must allow %s trigger outcome", outcome)
		}
	}
}

func TestUnavailableAndErrorDetectFactsRemainConstrained(t *testing.T) {
	if !levelFactMatchesOutcome(LevelFactUnavailable, LevelOutcomeUnknown) ||
		levelFactMatchesOutcome(LevelFactUnavailable, LevelOutcomeNormal) {
		t.Fatal("UNAVAILABLE detect fact must only allow UNKNOWN")
	}
	if !levelFactMatchesOutcome(LevelFactError, LevelOutcomeTerminal) ||
		levelFactMatchesOutcome(LevelFactError, LevelOutcomeRecovery) {
		t.Fatal("ERROR detect fact must only allow TERMINAL")
	}
}

// The description renders what each comparison saw, per Level and per
// series: outcomes are counted for the outcome's own Level only, the fold
// is this round's and empty when none is proposed, the markers are shown as
// loaded and as final, and the State guard is the one after the round.
func TestDescribeMissingGuardRendersEachComparisonPerLevel(t *testing.T) {
	plan := PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}
	series := SeriesIdentityDigest("series-a")
	identity := StateKeyIdentity{Plan: plan, StateGeneration: "g", SeriesIdentityDigest: series}
	outcome := LevelOutcome{Plan: plan, LevelID: 5, SeriesIdentityDigest: series, Outcome: LevelOutcomeUnknown, ReasonCode: "QUERY_PARTIAL"}
	result := PlanEvaluationResult{
		Plan: plan,
		LevelOutcomes: []LevelOutcome{outcome, {Plan: plan, LevelID: 5, SeriesIdentityDigest: series, Outcome: LevelOutcomeUnknown, ReasonCode: "QUERY_PARTIAL"},
			{Plan: plan, LevelID: 6, SeriesIdentityDigest: series, Outcome: LevelOutcomeNormal},
			{Plan: plan, LevelID: 5, SeriesIdentityDigest: "series-b", Outcome: LevelOutcomeUnknown, ReasonCode: "QUERY_PARTIAL"}},
		StateResults: []StateEvaluation{{Mutation: StateMutation{Identity: identity}}},
	}
	input := InternalExecution{Inputs: []NamedInputBinding{{
		Consumer: ConsumerRef{Plan: plan, LevelID: 5, HasLevel: true}, Role: InputRolePrimary,
		Completeness: CompletenessPartial, DataState: DataStateData, Disposition: AccessDegraded, ReasonCode: "QUERY_PARTIAL",
	}}}
	loaded := map[GapScope]GapScopeState{{}: {Status: GapStatusGapped, ReasonCode: "GAP_SKIPPED"}}
	final := map[GapScope]GapScopeState{{}: {Status: GapStatusGapped, ReasonCode: "GAP_SKIPPED"},
		{HasLevel: true, LevelID: 5}: {Status: GapStatusWarming, ReasonCode: "QUERY_PARTIAL"}}
	states := map[StateKeyIdentity]RuntimeStateView{identity: {Identity: identity,
		SeriesGuard: &StateGuardFact{ReasonCode: "RECORD_INVALID"},
		Levels: []RuntimeLevelStateView{{LevelID: 5, HistoryCompleteness: HistoryGapped, GapReasonCode: "GAP_SKIPPED"},
			{LevelID: 6, HistoryCompleteness: HistoryFull}}}}

	got := describeMissingGuard(input, result, loaded, final, states, outcome)
	for _, want := range []string{
		"outcome UNKNOWN", "reason QUERY_PARTIAL", "level 5", "outcomes for level 2", "input full no",
		"inputs [level:PRIMARY:PARTIAL/DATA/DEGRADED] localized no", "round fold QUERY_PARTIAL", "state series guard RECORD_INVALID", "state level guard GAPPED/GAP_SKIPPED",
		"state written yes", "marker plan loaded GAPPED/GAP_SKIPPED", "marker level loaded none",
		"marker plan final GAPPED/GAP_SKIPPED", "marker level final WARMING/QUERY_PARTIAL", "guard proposed no",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("description %q does not say %q", got, want)
		}
	}
	// No fold when the round's inputs for the Level are all FULL, and
	// nothing found reads none, not empty.
	input.Inputs[0].Completeness, input.Inputs[0].Disposition = CompletenessFull, AccessAvailable
	bare := describeMissingGuard(input, PlanEvaluationResult{Plan: plan, LevelOutcomes: []LevelOutcome{outcome}}, nil, nil, nil, outcome)
	for _, want := range []string{"outcomes for level 1", "round fold none", "state series guard none", "state level guard none",
		"state written no", "marker plan loaded none", "marker level final none", "input full yes",
		"inputs [level:PRIMARY:FULL/DATA/AVAILABLE] localized no"} {
		if !strings.Contains(bare, want) {
			t.Fatalf("bare description %q does not say %q", bare, want)
		}
	}
}

// The shape production produced on three Query Groups: every binding FULL,
// so the round folds nothing and "input full no" was all the line could
// say. The inputs term has to name the binding that closed the gate -- a
// dependency that completed FULL and holds no rows -- because that is the
// one fact the fold cannot see and the reader needs.
func TestDescribeMissingGuardNamesTheFullButEmptyBinding(t *testing.T) {
	plan := PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}
	series := SeriesIdentityDigest("series-a")
	outcome := LevelOutcome{Plan: plan, LevelID: 1, SeriesIdentityDigest: series, Outcome: LevelOutcomeUnknown, ReasonCode: "HISTORY_WARMING"}
	input := InternalExecution{Inputs: []NamedInputBinding{
		{Consumer: ConsumerRef{Plan: plan, LevelID: 1, HasLevel: true}, Role: InputRolePrimary,
			Completeness: CompletenessFull, DataState: DataStateData, Disposition: AccessAvailable},
		{Consumer: ConsumerRef{Plan: plan, LevelID: 1, HasLevel: true}, Role: InputRoleAlgorithmDependency,
			Completeness: CompletenessFull, DataState: DataStateEmpty, Disposition: AccessAvailable},
		{Consumer: ConsumerRef{Plan: plan, LevelID: 2, HasLevel: true}, Role: InputRolePrimary,
			Completeness: CompletenessUnavailable, Disposition: AccessUnavailable, ReasonCode: "QUERY_TIMEOUT"},
	}}
	got := describeMissingGuard(input, PlanEvaluationResult{Plan: plan, LevelOutcomes: []LevelOutcome{outcome}}, nil, nil, nil, outcome)
	// The fold now reads the same definition of incomplete the advance gate
	// does, so the line says which guard the round would propose for the
	// empty dependency. It read "round fold none" while the fold looked at
	// completeness alone, which is what production produced sixty-nine
	// times in eighteen minutes.
	for _, want := range []string{
		"input full no", "round fold QUERY_EMPTY",
		"inputs [level:PRIMARY:FULL/DATA/AVAILABLE level:ALGORITHM_DEPENDENCY:FULL/EMPTY/AVAILABLE] localized no",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("description %q does not say %q", got, want)
		}
	}
	// The other Level's binding is not this outcome's; listing it would
	// point the reader at a gate that was never consulted.
	if strings.Contains(got, "QUERY_TIMEOUT") || strings.Contains(got, "UNAVAILABLE/") {
		t.Fatalf("description %q lists a binding of another Level", got)
	}
	// A Plan-scope binding is consulted and says so; an absent data state
	// reads UNKNOWN rather than an empty slot between two slashes.
	input.Inputs = append(input.Inputs, NamedInputBinding{Consumer: ConsumerRef{Plan: plan}, Role: InputRolePrimary,
		Completeness: CompletenessFull, Disposition: AccessAvailable})
	got = describeMissingGuard(input, PlanEvaluationResult{Plan: plan, LevelOutcomes: []LevelOutcome{outcome}}, nil, nil, nil, outcome)
	if !strings.Contains(got, "plan:PRIMARY:FULL/UNKNOWN/AVAILABLE]") {
		t.Fatalf("description %q does not render the Plan-scope binding", got)
	}
	// A quality fact localized to this record is the fourth conjunct of
	// "input full", and the only one the other terms cannot show: every
	// binding reads FULL/DATA/AVAILABLE while the gate is closed.
	localizedOutcome := outcome
	localizedOutcome.ReasonCode, localizedOutcome.Record = "VALUE_INVALID", RecordAnchor{RecordID: "r1", SourceTime: 60}
	input.Inputs[0].QualityFacts = []InputQualityFact{{ReasonCode: "VALUE_INVALID", ImpactScope: ImpactSeries,
		RecordID: "r1", SourceTime: 60, SeriesIdentity: series}}
	got = describeMissingGuard(input, PlanEvaluationResult{Plan: plan, LevelOutcomes: []LevelOutcome{localizedOutcome}}, nil, nil, nil, localizedOutcome)
	if !strings.Contains(got, "] localized yes") {
		t.Fatalf("description %q does not say the outcome was localized", got)
	}
	input.Inputs[0].QualityFacts = nil
	// Past the bound the rest are counted, so a Level with many inputs
	// cannot turn the line into a page.
	for index := 0; index < maxDescribedInputs+2; index++ {
		input.Inputs = append(input.Inputs, NamedInputBinding{Consumer: ConsumerRef{Plan: plan, LevelID: 1, HasLevel: true},
			Role: InputRoleAlgorithmDependency, Completeness: CompletenessFull, DataState: DataStateData, Disposition: AccessAvailable})
	}
	got = describeMissingGuard(input, PlanEvaluationResult{Plan: plan, LevelOutcomes: []LevelOutcome{outcome}}, nil, nil, nil, outcome)
	if !strings.Contains(got, " +5]") || strings.Count(got, "ALGORITHM_DEPENDENCY") > maxDescribedInputs {
		t.Fatalf("description %q does not bound the inputs it lists", got)
	}
}

// The two loaded-record exemptions are pinned here, at the predicate, rather
// than through Validate.
//
// Not a preference. The outcome-kind condition cannot be told apart through
// the real contract: a non-TERMINAL outcome beside a broken record is already
// refused by codeOutcomeInvalidSeriesNotTerminal, which requires a
// DeterministicInvalid series to produce a TERMINAL outcome carrying the view's
// own reason, so a case built to widen the exemption past TERMINAL never
// reaches it. The exemption still has to hold that line -- that is a separate
// rule, and relaxing it later must not silently let a business UNKNOWN through
// with no guard at all -- so the condition is nailed where it can be seen.
//
// The load-status condition is not repeated here: the contract can express it,
// and evaluation's TestATerminalOutcomeWithAReadableRecordIsStillRefused does.
func TestTheLoadedRecordExemptionsReadKindStatusAndReason(t *testing.T) {
	plan := PlanIdentity{TenantID: "t1", BusinessID: "b1", StrategyID: "s1"}
	const series SeriesIdentityDigest = "series-1"
	outcomeOf := func(kind LevelOutcomeKind, reason ReasonCode) LevelOutcome {
		return LevelOutcome{Plan: plan, LevelID: 1, SeriesIdentityDigest: series, Outcome: kind, ReasonCode: reason}
	}
	states := func(status StateLoadStatus, reason ReasonCode) StatePreflightResult {
		return StatePreflightResult{Items: []RuntimeStateView{{
			Identity: StateKeyIdentity{Plan: plan, StateGeneration: "g1", SeriesIdentityDigest: series},
			Status:   status, ReasonCode: reason,
		}}}
	}
	gaps := func(status GapLoadStatus, reason ReasonCode) GapLoadResult {
		return GapLoadResult{Items: []GapGuardSnapshot{{
			Identity: PlanGapIdentity{Plan: plan, StateGeneration: "g1"},
			Status:   status, ReasonCode: reason,
		}}}
	}

	stateCells := []struct {
		name    string
		outcome LevelOutcome
		loaded  StatePreflightResult
		want    bool
	}{
		{"an UNKNOWN outcome is not exempt even beside the record that names its reason",
			outcomeOf(LevelOutcomeUnknown, "STATE_CORRUPT"), states(StateDeterministicInvalid, "STATE_CORRUPT"), false},
		{"a TERMINAL outcome naming another cause is not covered by this record",
			outcomeOf(LevelOutcomeTerminal, "RECORD_INVALID"), states(StateDeterministicInvalid, "STATE_CORRUPT"), false},
		{"a TERMINAL outcome naming the record's own cause is covered",
			outcomeOf(LevelOutcomeTerminal, "STATE_CORRUPT"), states(StateDeterministicInvalid, "STATE_CORRUPT"), true},
	}
	for _, cell := range stateCells {
		if got := loadedStateGuardsTerminalOutcome(cell.loaded, cell.outcome); got != cell.want {
			t.Fatalf("loadedStateGuardsTerminalOutcome = %v, want %v: %s", got, cell.want, cell.name)
		}
	}

	gapCells := []struct {
		name    string
		outcome LevelOutcome
		loaded  GapLoadResult
		want    bool
	}{
		{"an UNKNOWN outcome is not exempt even beside the marker that names its reason",
			outcomeOf(LevelOutcomeUnknown, "STATE_CORRUPT"), gaps(GapTerminal, "STATE_CORRUPT"), false},
		{"a TERMINAL outcome naming another cause is not covered by this marker",
			outcomeOf(LevelOutcomeTerminal, "RECORD_INVALID"), gaps(GapTerminal, "STATE_CORRUPT"), false},
		{"a TERMINAL outcome naming the marker's own cause is covered",
			outcomeOf(LevelOutcomeTerminal, "STATE_CORRUPT"), gaps(GapTerminal, "STATE_CORRUPT"), true},
	}
	for _, cell := range gapCells {
		if got := loadedGapGuardsTerminalOutcome(cell.loaded, cell.outcome); got != cell.want {
			t.Fatalf("loadedGapGuardsTerminalOutcome = %v, want %v: %s", got, cell.want, cell.name)
		}
	}
}
