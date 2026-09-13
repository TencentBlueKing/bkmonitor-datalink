package execution

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestStateHistoryReplacementPreservesLoadedPrefixAndAppendsAffectedPoint(t *testing.T) {
	loaded := []StateHistoryPoint{stateHistoryPoint("old", 10, LevelFactNormal)}
	mutation := StateMutation{
		AffectedRecords: []RecordAnchor{{RecordID: "current", SourceTime: 20}},
		Points: []StateHistoryPoint{
			stateHistoryPoint("old", 10, LevelFactNormal),
			stateHistoryPoint("current", 20, LevelFactAnomalous),
		},
	}
	if err := validateStateHistoryReplacement(loaded, mutation, 3); err != nil {
		t.Fatalf("valid replacement rejected: %v", err)
	}
}

func TestStateHistoryReplacementAllowsOnlyBoundedOldestEviction(t *testing.T) {
	loaded := []StateHistoryPoint{
		stateHistoryPoint("oldest", 10, LevelFactNormal),
		stateHistoryPoint("old", 20, LevelFactAnomalous),
	}
	mutation := StateMutation{
		AffectedRecords: []RecordAnchor{{RecordID: "current", SourceTime: 30}},
		Points: []StateHistoryPoint{
			stateHistoryPoint("old", 20, LevelFactAnomalous),
			stateHistoryPoint("current", 30, LevelFactNormal),
		},
	}
	if err := validateStateHistoryReplacement(loaded, mutation, 2); err != nil {
		t.Fatalf("bounded oldest eviction rejected: %v", err)
	}
}

func TestStateHistoryReplacementAllowsOlderAffectedPointEvictedInSameBatch(t *testing.T) {
	mutation := StateMutation{
		AffectedRecords: []RecordAnchor{
			{RecordID: "first", SourceTime: 10},
			{RecordID: "second", SourceTime: 20},
		},
		Points: []StateHistoryPoint{stateHistoryPoint("second", 20, LevelFactAnomalous)},
	}
	if err := validateStateHistoryReplacement(nil, mutation, 1); err != nil {
		t.Fatalf("bounded same-batch eviction rejected: %v", err)
	}
}

func TestStateHistoryReplacementRejectsLoadedPrefixTamperingOrUnneededLoss(t *testing.T) {
	loaded := []StateHistoryPoint{stateHistoryPoint("old", 10, LevelFactNormal)}
	base := StateMutation{
		AffectedRecords: []RecordAnchor{{RecordID: "current", SourceTime: 20}},
		Points: []StateHistoryPoint{
			stateHistoryPoint("old", 10, LevelFactNormal),
			stateHistoryPoint("current", 20, LevelFactAnomalous),
		},
	}

	tampered := base
	tampered.Points = append([]StateHistoryPoint(nil), base.Points...)
	tampered.Points[0] = stateHistoryPoint("old", 10, LevelFactAnomalous)
	if err := validateStateHistoryReplacement(loaded, tampered, 3); err == nil {
		t.Fatal("tampered loaded history must be rejected")
	}

	dropped := base
	dropped.Points = []StateHistoryPoint{stateHistoryPoint("current", 20, LevelFactAnomalous)}
	if err := validateStateHistoryReplacement(loaded, dropped, 3); err == nil {
		t.Fatal("loaded history must not be dropped before the retention bound")
	}
}

func TestStateHistoryReplacementRejectsLoadedHistoryClearedByEmptySnapshot(t *testing.T) {
	loaded := []StateHistoryPoint{stateHistoryPoint("old", 10, LevelFactNormal)}
	mutation := StateMutation{AffectedRecords: []RecordAnchor{{RecordID: "current", SourceTime: 20}}}
	if err := validateStateHistoryReplacement(loaded, mutation, 3); err == nil {
		t.Fatal("empty replacement must not clear loaded history")
	}
}

func TestStateHistoryReplacementRejectsOverlappingAnchorFactTampering(t *testing.T) {
	loaded := []StateHistoryPoint{stateHistoryPoint("same", 10, LevelFactNormal)}
	mutation := StateMutation{
		AffectedRecords: []RecordAnchor{{RecordID: "same", SourceTime: 10}},
		Points:          []StateHistoryPoint{stateHistoryPoint("same", 10, LevelFactAnomalous)},
	}
	if err := validateStateHistoryReplacement(loaded, mutation, 3); err == nil {
		t.Fatal("replayed anchor must not rewrite its loaded detect fact")
	}
}

func TestGuardedOrInactiveUnknownMayPersistValidDetectFact(t *testing.T) {
	outcome := LevelOutcome{LevelID: 5, Outcome: LevelOutcomeUnknown, ReasonCode: ReasonCode(contract.ReasonHistoryWarming)}
	guarded := StateMutation{Levels: []RuntimeLevelStateMutation{{
		LevelID: 5, HistoryCompleteness: HistoryWarming, GapReasonCode: outcome.ReasonCode,
	}}}
	for _, fact := range []LevelFactResult{LevelFactNormal, LevelFactAnomalous} {
		if !stateFactMayAdvanceUnknown(fact, outcome, guarded, strategy.EffectiveTimeActive, true) {
			t.Fatalf("guarded %s fact must advance", fact)
		}
	}

	inactive := outcome
	inactive.ReasonCode = ReasonCode(contract.ReasonEffectiveTimeInactive)
	if !stateFactMayAdvanceUnknown(LevelFactNormal, inactive, StateMutation{}, strategy.EffectiveTimeInactive, true) {
		t.Fatal("INACTIVE must preserve a valid detect fact while suppressing business outcome")
	}
}

func TestUnknownWithoutExactAdvanceAuthorityIsRejected(t *testing.T) {
	outcome := LevelOutcome{LevelID: 5, Outcome: LevelOutcomeUnknown, ReasonCode: ReasonCode(contract.ReasonHistoryWarming)}
	if stateFactMayAdvanceUnknown(LevelFactNormal, outcome, StateMutation{}, strategy.EffectiveTimeActive, true) {
		t.Fatal("unguarded UNKNOWN must not advance state")
	}
	guarded := StateMutation{Levels: []RuntimeLevelStateMutation{{
		LevelID: 5, HistoryCompleteness: HistoryWarming, GapReasonCode: outcome.ReasonCode,
	}}}
	if stateFactMayAdvanceUnknown(LevelFactUnavailable, outcome, guarded, strategy.EffectiveTimeActive, true) ||
		stateFactMayAdvanceUnknown(LevelFactError, outcome, guarded, strategy.EffectiveTimeActive, true) {
		t.Fatal("UNAVAILABLE/ERROR facts must never advance history")
	}
	if stateFactMayAdvanceUnknown(LevelFactNormal, outcome, guarded, strategy.EffectiveTimeActive, false) {
		t.Fatal("PARTIAL/UNAVAILABLE input must never advance guarded UNKNOWN history")
	}
}

func TestUnavailableSeriesQualityFactDoesNotAuthorizeStateAdvance(t *testing.T) {
	plan := PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}
	series := SeriesIdentityDigest("series")
	outcome := LevelOutcome{
		Plan: plan, LevelID: 5, SeriesIdentityDigest: series,
		Record:  RecordAnchor{RecordID: "record", SourceTime: 10},
		Outcome: LevelOutcomeUnknown, ReasonCode: ReasonCode(contract.ReasonQueryUnavailable),
	}
	input := InternalExecution{Inputs: []NamedInputBinding{{
		Consumer: ConsumerRef{Plan: plan}, Role: InputRolePrimary,
		Completeness: CompletenessUnavailable, DataState: DataStateUnknown,
		Disposition: AccessUnavailable, ReasonCode: outcome.ReasonCode,
		Dataset: nil, View: nil,
		QualityFacts: []InputQualityFact{{
			ReasonCode: outcome.ReasonCode, ImpactScope: ImpactSeries,
			RecordID: outcome.Record.RecordID, SourceTime: outcome.Record.SourceTime, SeriesIdentity: series,
		}},
	}}}
	localized, err := validateLocalizedInputOutcome(input, plan, outcome)
	if err != nil || !localized {
		t.Fatalf("legal UNAVAILABLE series fact must localize its UNKNOWN outcome, localized=%v err=%v", localized, err)
	}
	if stateInputAllowsAdvance(input, outcome) {
		t.Fatal("legal UNAVAILABLE input must not authorize State advance")
	}
	guarded := StateMutation{Levels: []RuntimeLevelStateMutation{{
		LevelID: 5, HistoryCompleteness: HistoryWarming, GapReasonCode: outcome.ReasonCode,
	}}}
	if stateFactMayAdvanceUnknown(LevelFactNormal, outcome, guarded, strategy.EffectiveTimeActive, stateInputAllowsAdvance(input, outcome)) {
		t.Fatal("UNAVAILABLE input must be rejected by the State advance rule even with an exact guard")
	}
}

func stateHistoryPoint(recordID string, sourceTime int64, result LevelFactResult) StateHistoryPoint {
	return StateHistoryPoint{RecordID: recordID, SourceTime: sourceTime, Levels: []StateLevelFact{{
		LevelID: 5, DetectFingerprint: "detect-v1", Result: result,
	}}}
}
