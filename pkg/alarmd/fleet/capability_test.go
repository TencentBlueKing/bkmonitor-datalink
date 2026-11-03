// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"slices"
	"testing"
)

// Every reason the control plane withholds a strategy for, or notes on one
// it runs, is classified: one area and one outcome, from the closed lists,
// and none falls to the unknown default. The words table is the list of
// those reasons, held to the control plane's literals by its own test.
func TestEveryWithheldReasonHasOneCapabilityCell(t *testing.T) {
	for reason := range withheldReasonWords {
		if capabilityCostNotes[reason] {
			if class, known := ClassifyCapability(dispositionConfigNoted, reason, "PLAN"); !known || class != (CapabilityClass{}) {
				t.Fatalf("cost note %s classified %+v (known %t), want no capability fact", reason, class, known)
			}
			continue
		}
		for _, disposition := range []string{dispositionCapabilityUnsupported, dispositionConfigNoted} {
			class, known := ClassifyCapability(disposition, reason, "STRATEGY")
			if !known {
				t.Fatalf("%s under %s is not classified", reason, disposition)
			}
			if class.IgnoredByDesign {
				continue
			}
			if !slices.Contains(CapabilityAreas, class.Area) || !slices.Contains(CapabilityOutcomes, class.Outcome) || class.Outcome == CapabilitySupported {
				t.Fatalf("%s under %s classified %+v, want an area and a non-supported outcome from the closed lists", reason, disposition, class)
			}
		}
	}
	for reason := range capabilityRuntimeReasons {
		class, known := ClassifyCapability(capabilityRuntime, reason, "PLAN")
		if !known || !slices.Contains(CapabilityAreas, class.Area) || class.Outcome == CapabilitySupported {
			t.Fatalf("runtime reason %s classified %+v (known %t)", reason, class, known)
		}
	}
}

// The cells the capability boundary names: the outcome follows what the
// strategy still does, not only what was refused.
func TestCapabilityCellsFollowWhatTheStrategyStillDoes(t *testing.T) {
	for _, test := range []struct {
		disposition, reason, scope string
		area, outcome              string
		ignored                    bool
	}{
		{dispositionCapabilityUnsupported, "QUERY_SOURCE_NOT_MIGRATED", "STRATEGY", CapabilitySource, CapabilityNotYetSupported, false},
		{dispositionCapabilityUnsupported, "QUERY_FTA_UNSUPPORTED", "STRATEGY", CapabilitySource, CapabilityUnsupportedByDesign, false},
		{dispositionCapabilityUnsupported, "UNSUPPORTED_TARGET_SCOPE", "STRATEGY", CapabilityTarget, CapabilityNotYetSupported, false},
		{dispositionCapabilityUnsupported, "ALGORITHM_NOT_MIGRATED", "STRATEGY", CapabilityAlgorithm, CapabilityNotYetSupported, false},
		// One level refused, the strategy's other levels detect.
		{dispositionCapabilityUnsupported, "ALGORITHM_NOT_MIGRATED", "LEVEL", CapabilityAlgorithm, CapabilityPartial, false},
		{dispositionConfigRejected, "ALGORITHM_QUERY_INVALID", "LEVEL", CapabilityAlgorithm, CapabilityPartial, false},
		{dispositionConfigRejected, "OUTPUT_PROTOCOL_REQUIRES_STRATEGY_REVISION", "PLAN", CapabilityOutput, CapabilityNotYetSupported, false},
		{dispositionConfigRejected, "PLAN_INVALID", "PLAN", CapabilityConfig, CapabilityNotYetSupported, false},
		// A noted strategy runs: what was noted is at most a part of it.
		{dispositionConfigNoted, "EFFECTIVE_TIME_RANGE_INVALID", "STRATEGY", CapabilityConfig, CapabilityPartial, false},
		{dispositionConfigNoted, "PRIORITY_IGNORED", "STRATEGY", "", "", true},
		{dispositionCompatibilityIgnored, "ANY", "STRATEGY", "", "", true},
		{capabilityRuntime, "NO_DATA_ROSTER_UNSUPPORTED", "PLAN", CapabilityAlgorithm, CapabilityPartial, false},
	} {
		class, known := ClassifyCapability(test.disposition, test.reason, test.scope)
		if !known || class.Area != test.area || class.Outcome != test.outcome || class.IgnoredByDesign != test.ignored {
			t.Fatalf("%s/%s/%s = %+v (known %t), want %s/%s ignored=%t", test.disposition, test.reason, test.scope, class, known,
				test.area, test.outcome, test.ignored)
		}
	}
}

// A reason this build does not know is said to be unknown and lands where
// a writer's configuration would, never as supported; a disposition about
// the source's state, not the strategy, is not a capability at all.
func TestAnUnknownReasonIsNotSupportedAndSourceStateIsNotCapability(t *testing.T) {
	class, known := ClassifyCapability(dispositionCapabilityUnsupported, "A_REASON_FROM_A_LATER_BUILD", "STRATEGY")
	if known || class.Area != CapabilityConfig || class.Outcome != CapabilityNotYetSupported {
		t.Fatalf("unknown reason = %+v known=%t", class, known)
	}
	if class, known := ClassifyCapability(capabilityRuntime, "A_RUNTIME_REASON_FROM_A_LATER_BUILD", "PLAN"); known ||
		class.Area != CapabilityConfig || class.Outcome != CapabilityNotYetSupported {
		t.Fatalf("unknown runtime reason = %+v known=%t", class, known)
	}
	for _, disposition := range []string{"ACCEPTED", "SOURCE_INCOMPLETE", "STALE_CONFIG", "PENDING_REMOVAL", "REMOVED"} {
		if class, known := ClassifyCapability(disposition, "", "STRATEGY"); known || class != (CapabilityClass{}) {
			t.Fatalf("%s = %+v known=%t, want no capability fact", disposition, class, known)
		}
	}
}

// Within one area a strategy counts once, under its most severe outcome.
func TestTheMostSevereOutcomeWinsWithinAnArea(t *testing.T) {
	if got := MoreSevereCapability(CapabilityPartial, CapabilityNotYetSupported); got != CapabilityNotYetSupported {
		t.Fatalf("partial vs not yet = %s", got)
	}
	if got := MoreSevereCapability(CapabilityUnsupportedByDesign, CapabilityPartial); got != CapabilityUnsupportedByDesign {
		t.Fatalf("by design vs partial = %s", got)
	}
	if got := MoreSevereCapability(CapabilityNotYetSupported, CapabilityUnsupportedByDesign); got != CapabilityNotYetSupported {
		t.Fatalf("not yet vs by design = %s", got)
	}
}
