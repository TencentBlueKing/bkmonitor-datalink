// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package metric

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// Every site and refusal pair exists at zero from startup, so a refusal that
// never happened reads as zero and not as an absent series; and each
// refusing observation lands on exactly one pair, by where it was met. The
// admission check is met on the fence_checked relay and not on the state-side
// admission line it is relayed from, so one refusal is counted once.
func TestOwnershipRefusalsArePreRegisteredAndCountedOncePerRefusalBySite(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	if got, want := testutil.CollectAndCount(recorder.phaseTwo.ownershipRefusals), len(ownershipRefusalSites)*len(ownership.RefusalReasons); got != want {
		t.Fatalf("pre-registered refusal series = %d, want %d (%d sites x %d refusals)", got, want, len(ownershipRefusalSites), len(ownership.RefusalReasons))
	}
	observe := func(component observability.Component, stage observability.Stage, reason string) {
		recorder.Observe(context.Background(), observability.Observation{
			Component: component, Stage: stage, Result: observability.ResultFailed,
			ReasonCode: observability.ReasonCode(reason),
		})
	}
	// The same refusal through the state-side admission line and its
	// ownership relay: one count, on the admission site.
	observe(observability.ComponentState, observability.StageSideEffectAdmission, contract.ReasonOwnershipStaleFence)
	observe(observability.ComponentOwnership, observability.StageFenceChecked, contract.ReasonOwnershipStaleFence)
	// A fenced write refused because the scope moved, a lease another owner
	// holds at takeover, a renewal that met a stale fence, and a control
	// leader write the assignment fence refused.
	observe(observability.ComponentState, observability.StageStateApplied, contract.ReasonContentScopeMoved)
	observe(observability.ComponentOwnership, observability.StageTakeoverCompleted, contract.ReasonOwnershipLeaseBusy)
	observe(observability.ComponentOwnership, observability.StageLeaseRenewed, contract.ReasonOwnershipStaleFence)
	observe(observability.ComponentOwnership, observability.StageAssignmentIndexWritten, contract.ReasonOwnershipNotDesired)
	// Failures that are not refusals, and a refusal word on a component that
	// does not meet the store, count nowhere.
	observe(observability.ComponentOwnership, observability.StageTakeoverCompleted, string(observability.ReasonInternalUnknown))
	observe(observability.ComponentScheduler, observability.StageSlotCompleted, contract.ReasonOwnershipStaleFence)
	for _, want := range []struct {
		site, refusal string
		count         float64
	}{
		{"admission", contract.ReasonOwnershipStaleFence, 1},
		{"state_apply", contract.ReasonContentScopeMoved, 1},
		{"lease", contract.ReasonOwnershipLeaseBusy, 1},
		{"renewal", contract.ReasonOwnershipStaleFence, 1},
		{"control", contract.ReasonOwnershipNotDesired, 1},
		{"admission", contract.ReasonContentScopeMoved, 0},
		{"lease", contract.ReasonOwnershipStaleFence, 0},
	} {
		if got := testutil.ToFloat64(recorder.phaseTwo.ownershipRefusals.WithLabelValues(want.site, want.refusal)); got != want.count {
			t.Errorf("ownership_refusals_total{site=%s, refusal=%s} = %v, want %v", want.site, want.refusal, got, want.count)
		}
	}
	// Still exactly the pre-registered pairs: no observation created a series.
	if got, want := testutil.CollectAndCount(recorder.phaseTwo.ownershipRefusals), len(ownershipRefusalSites)*len(ownership.RefusalReasons); got != want {
		t.Fatalf("refusal series after observing = %d, want %d", got, want)
	}
}

// The four words are observation-domain reasons the catalog knows, so the
// structured line keeps them exact rather than collapsing to _other; and the
// class each maps to for the generic metric is the one the catalog says.
func TestOwnershipRefusalReasonsAreInTheCatalog(t *testing.T) {
	for reason, class := range map[string]observability.ReasonCode{
		contract.ReasonOwnershipStaleFence: observability.ReasonContractDeterministic,
		contract.ReasonOwnershipNotDesired: observability.ReasonContractDeterministic,
		contract.ReasonOwnershipLeaseBusy:  observability.ReasonContractRetryable,
		contract.ReasonContentScopeMoved:   observability.ReasonContractDeterministic,
	} {
		if got := observability.NormalizeReason(observability.ReasonCode(reason), observability.ResultFailed); string(got) != reason {
			t.Errorf("NormalizeReason(%s) = %q, want the word kept", reason, got)
		}
		if got := observability.NormalizeMetricReason(observability.ComponentOwnership, observability.ReasonCode(reason), observability.ResultFailed); got != class {
			t.Errorf("NormalizeMetricReason(%s) = %q, want %q", reason, got, class)
		}
	}
}
