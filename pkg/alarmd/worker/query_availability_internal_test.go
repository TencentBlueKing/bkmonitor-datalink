package worker

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestQueryAvailabilityEvidence(t *testing.T) {
	type input struct {
		role         execution.InputRole
		completeness execution.Completeness
		streamed     bool
	}
	p := execution.InputRolePrimary
	u, f, partial := execution.CompletenessUnavailable, execution.CompletenessFull, execution.CompletenessPartial
	for _, test := range []struct {
		name   string
		inputs []input
		want   execution.QueryAvailability
	}{
		{"no queries", nil, execution.QueryAvailabilityUnknown},
		{"all unavailable shared consumers", []input{{p, u, false}, {p, u, false}}, execution.QueryAvailabilityUnavailable},
		{"full empty", []input{{p, f, false}}, execution.QueryAvailabilityAvailable},
		{"mixed primary", []input{{p, u, false}, {p, f, false}}, execution.QueryAvailabilityAvailable},
		{"dependency availability cannot mask primary failure", []input{{p, u, false}, {execution.InputRoleAlgorithmDependency, f, true}}, execution.QueryAvailabilityUnavailable},
		{"partial empty", []input{{p, partial, false}}, execution.QueryAvailabilityUnknown},
		{"partial usable stream", []input{{p, partial, true}, {p, u, false}}, execution.QueryAvailabilityAvailable},
		{"unavailable invalidates provisional stream", []input{{p, u, true}}, execution.QueryAvailabilityUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			var evidence queryAvailabilityEvidence
			for _, item := range test.inputs {
				evidence.observe(execution.NamedInputBinding{Role: item.role, Disposition: execution.AccessAvailable}, execution.PhysicalQueryCompletion{Completeness: item.completeness}, item.streamed, true)
			}
			if got := evidence.availability(); got != test.want {
				t.Fatalf("availability=%v, want %v", got, test.want)
			}
		})
	}
}

func TestMixedBackendAndLocalFailureDoesNotCooldown(t *testing.T) {
	var evidence queryAvailabilityEvidence
	binding := execution.NamedInputBinding{Role: execution.InputRolePrimary}
	physical := execution.PhysicalQueryCompletion{Completeness: execution.CompletenessUnavailable}
	evidence.observe(binding, physical, false, true)
	evidence.observe(binding, physical, false, false)
	if got := evidence.availability(); got != execution.QueryAvailabilityUnknown {
		t.Fatalf("mixed backend/local availability=%v", got)
	}
	physical.Completeness = execution.CompletenessFull
	evidence.observe(binding, physical, false, false)
	if got := evidence.availability(); got != execution.QueryAvailabilityAvailable {
		t.Fatalf("healthy sibling availability=%v", got)
	}
}

// The reason handed out is the first unavailable primary's own, as its
// binding attributes it; a dependency's never, and none when the query was
// not unavailable.
func TestTheUnavailableReasonIsThePrimarysOwn(t *testing.T) {
	unavailable := execution.PhysicalQueryCompletion{Completeness: execution.CompletenessUnavailable}
	var evidence queryAvailabilityEvidence
	evidence.observe(execution.NamedInputBinding{Role: execution.InputRoleAlgorithmDependency, ReasonCode: contract.ReasonQueryTimeout}, unavailable, false, true)
	evidence.observe(execution.NamedInputBinding{Role: execution.InputRolePrimary, ReasonCode: contract.ReasonQueryTargetMissing}, unavailable, false, true)
	evidence.observe(execution.NamedInputBinding{Role: execution.InputRolePrimary, ReasonCode: contract.ReasonQueryUnavailable}, unavailable, false, true)
	if got := evidence.unavailableReason(); got != contract.ReasonQueryTargetMissing {
		t.Fatalf("reason = %q, want the first primary's %s", got, contract.ReasonQueryTargetMissing)
	}
	var unattributed queryAvailabilityEvidence
	unattributed.observe(execution.NamedInputBinding{Role: execution.InputRolePrimary, ReasonCode: contract.ReasonQueryUnavailable,
		UnavailableAttribution: execution.UnavailableNoAttemptReason}, unavailable, false, true)
	if got := unattributed.unavailableReason(); got != execution.ReasonQueryReasonUnrecorded {
		t.Fatalf("reason = %q, want %s for a code no attempt named", got, execution.ReasonQueryReasonUnrecorded)
	}
	unattributed.observe(execution.NamedInputBinding{Role: execution.InputRolePrimary}, execution.PhysicalQueryCompletion{Completeness: execution.CompletenessFull}, false, true)
	if got := unattributed.unavailableReason(); got != "" {
		t.Fatalf("reason = %q for an available query, want none", got)
	}
}
