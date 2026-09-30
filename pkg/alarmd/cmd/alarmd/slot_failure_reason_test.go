// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/trigger"
)

// Two failures that named themselves and were not asked. Both reached the
// completion line as internal_unknown - the word for a site that looked at a
// failure and could not name it - so the deployment's most common failure was
// reported as the deployment's inability to report.
//
// Each case is stated against internal_unknown as well as against its own
// word, because the word alone does not say the classification changed
// anything: a mapping that never fires and a mapping that fires give the same
// answer once the expected word is the only thing asserted.
func TestASlotFailureThatNamesItselfIsNotReportedAsUnnamed(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want observability.ReasonCode
	}{
		{
			name: "loaded Level contract is not the compiled Plan's",
			err:  &execution.StateContractMismatchError{What: "Level contract"},
			want: observability.ReasonCode(contract.ReasonStateLevelContractMismatch),
		},
		{
			name: "the trigger evaluator refused its own state",
			err:  &trigger.InternalErrorV2{Operation: "window", LevelID: 5, Err: errors.New("no window")},
			want: observability.ReasonCode(contract.ReasonTriggerInvariant),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			reason, site := slotFailureReason(test.err)
			if reason == observability.ReasonInternalUnknown {
				t.Fatalf("%v is reported as %s: the failure carries its own name and the line says nobody "+
					"could name it", test.err, reason)
			}
			if reason != test.want {
				t.Fatalf("%v is reported as %s, want %s", test.err, reason, test.want)
			}
			if site != "" {
				t.Fatalf("an evaluation failure named the gap apply site %q; no apply refused it", site)
			}
			// Wrapped the way the Slot wraps it on the way out, which is how it
			// actually arrives at the classification.
			wrapped := fmt.Errorf("alarmd worker: complete Slot: %w", test.err)
			if reason, _ := slotFailureReason(wrapped); reason != test.want {
				t.Fatalf("wrapped, %v is reported as %s, want %s", test.err, reason, test.want)
			}
		})
	}
}

// And the word that has to stay reachable: a failure nothing recognises is
// still internal_unknown, which is what makes the word mean "nobody looked at
// this yet" rather than "this site does not classify".
func TestASlotFailureNothingRecognisesIsStillUnnamed(t *testing.T) {
	reason, site := slotFailureReason(errors.New("something nobody has classified"))
	if reason != observability.ReasonInternalUnknown {
		t.Fatalf("an unrecognised failure is reported as %s; the word for an unnamed failure has to stay "+
			"reachable or a new one arrives wearing an old name", reason)
	}
	if site != "" {
		t.Fatalf("an unrecognised failure named the gap apply site %q", site)
	}
}

// Both words have to be words the observation vocabulary admits. A reason the
// catalogue does not hold normalizes to "other" on its way to the line, so the
// classification above would be undone one layer down with nothing to say so.
func TestTheTwoNamedFailuresSurviveReasonNormalization(t *testing.T) {
	for _, reason := range []observability.ReasonCode{
		observability.ReasonCode(contract.ReasonStateLevelContractMismatch),
		observability.ReasonCode(contract.ReasonTriggerInvariant),
	} {
		if got := observability.NormalizeReason(reason, observability.ResultFailed); got != reason {
			t.Fatalf("%s normalizes to %s: a word the catalogue does not hold is replaced on the line, "+
				"and the classification that produced it leaves no trace", reason, got)
		}
	}
}
