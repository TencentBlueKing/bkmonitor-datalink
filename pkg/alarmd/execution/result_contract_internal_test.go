package execution

import "testing"

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
