package worker

import (
	"testing"

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
