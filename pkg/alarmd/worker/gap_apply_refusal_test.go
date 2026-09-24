// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

// Each of the gap marker store's refusals reports as itself, through the Slot's
// error wrappers.
//
// Three names rather than one, because they answer different questions: the
// marker moved under this write, this Slot's version is behind, or the write
// did not land at all. All three arrived as internal_unknown - the word for a
// site that looked at a failure and could not name it - so a Query Group
// refusing on every other Slot for half an hour was indistinguishable from a
// site that had simply given up classifying, and the same-Slot retry then
// cleared it.
func TestEachGapApplyRefusalReportsAsItself(t *testing.T) {
	for _, testCase := range []struct {
		status execution.GapGuardApplyStatus
		want   string
	}{
		{execution.GapGuardConflict, contract.ReasonGapApplyConflict},
		{execution.GapGuardStale, contract.ReasonGapApplyStaleVersion},
		{execution.GapGuardRetryable, contract.ReasonGapWriteRetryable},
	} {
		t.Run(string(testCase.status), func(t *testing.T) {
			cause := &worker.GapApplyRefusal{
				Stage: "gap guard did not complete", Status: testCase.status,
				Plan:            execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"},
				StateGeneration: "state-v1", ExpectedRevision: 9,
			}
			wrapped := fmt.Errorf("execute Slot: %w", fmt.Errorf("alarmd worker: apply gap guard: %w", cause))
			got, named := worker.GapApplyReason(wrapped)
			if !named || string(got) != testCase.want {
				t.Fatalf("reason = %q/%v, want %s/true", got, named, testCase.want)
			}
			if !errors.Is(wrapped, cause) {
				t.Fatalf("the error chain no longer carries the refusal: %v", wrapped)
			}
			// The evidence a reader needs to go find the second writer: which
			// Plan, and which revision this Slot thought it was writing
			// against. A line that says only the status sends them looking
			// with nothing to match on.
			text := cause.Error()
			for _, want := range []string{string(testCase.status), "strategy 7", "state-v1", "revision 9"} {
				if !strings.Contains(text, want) {
					t.Fatalf("refusal text %q does not carry %q", text, want)
				}
			}
		})
	}
}

// The three names stay three names.
//
// A status this build does not name reports nothing rather than borrowing one
// of the three. The point of separating them is that a reader can act on which
// one happened, and a fourth status folded into an existing name would make
// every reading of that name wrong, silently, starting with the deployment
// that introduced it.
func TestAnUnnamedGapApplyStatusIsNotGivenSomebodyElsesName(t *testing.T) {
	for _, err := range []error{
		nil,
		errors.New("CONFLICT"),
		&worker.GapApplyRefusal{Status: execution.GapGuardApplied},
		&worker.GapApplyRefusal{Status: execution.GapGuardAlreadyApplied},
		&worker.GapApplyRefusal{Status: execution.GapGuardApplyStatus("SOMETHING_THIS_BUILD_DOES_NOT_KNOW")},
	} {
		if got, named := worker.GapApplyReason(err); named || got != "" {
			t.Fatalf("unnamed error %v was reported as %q", err, got)
		}
	}
}
