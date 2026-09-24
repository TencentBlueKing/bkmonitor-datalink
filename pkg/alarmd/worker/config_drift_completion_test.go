package worker

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// TestConfigDriftCompletionDoesNotHideAnUnavailablePrimary pins that a Slot
// whose Plan activations drifted does not claim a partial gap when its PRIMARY
// was unavailable.
//
// The completion contract rejects that pair ("PARTIAL completion cannot hide
// unavailable PRIMARY"), and it rejects it at progress commit - so the Slot is
// never recorded and runs again the next minute, indefinitely. Both halves are
// ordinary on their own; a batch expiry of runtime state keys made the second
// common enough for the pair to appear across 46 Query Groups at once.
//
// This covers the decision and the shape of the completion, not the wiring:
// producing the pair end to end needs an activation change and an unavailable
// PRIMARY in the same Slot, which the coordinator fixture does not currently
// express. What makes the decision-level test enough is that there is now one
// constructor: the Plan drift branch and the activation-selection race both
// call it, where before the second was an inline literal that a fix to the
// first did not reach.
func TestConfigDriftCompletionDoesNotHideAnUnavailablePrimary(t *testing.T) {
	for _, one := range []struct {
		name         string
		completeness execution.Completeness
		want         execution.CompletionKind
		wantCause    execution.CompletionCause
	}{
		{
			name: "unavailable primary", completeness: execution.CompletenessUnavailable,
			want: execution.CompletionUnavailable, wantCause: execution.CausePrimaryInputUnavailable,
		},
		{
			name: "full primary", completeness: execution.CompletenessFull,
			want: execution.CompletionPartialGap, wantCause: execution.CauseConfigDrift,
		},
		{
			name: "partial primary", completeness: execution.CompletenessPartial,
			want: execution.CompletionPartialGap, wantCause: execution.CauseConfigDrift,
		},
	} {
		t.Run(one.name, func(t *testing.T) {
			primary := execution.PrimaryInputFact{Completeness: one.completeness}
			got, cause := configDriftCompletion(execution.FrozenExecutionContractRef{}, &primary)
			if got.Kind != one.want {
				t.Errorf("completion kind = %q, want %q", got.Kind, one.want)
			}
			// A completion that cannot say which condition it was is the shape
			// operators already learned to ignore, so the constructor answers
			// kind and cause from the same fact: the unavailable input when
			// there was one, the drift itself when the input was usable.
			if cause != one.wantCause {
				t.Errorf("cause = %q, want %q", cause, one.wantCause)
			}
			// Drift stays the reason and the result stays degraded whichever
			// kind it is: only the claim about the data moves.
			if got.Result != observability.ResultDegraded {
				t.Errorf("result = %q, want degraded", got.Result)
			}
			if got.ReasonCode != execution.ReasonCode(contract.ReasonConfigDrift) {
				t.Errorf("reason = %q, want the drift reason", got.ReasonCode)
			}
			if got.Primary != &primary {
				t.Error("the completion does not carry the PRIMARY it was judged on")
			}
		})
	}
}
