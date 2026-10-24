// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package fleet

import (
	"reflect"
	"testing"
)

func capabilityCell(capability *SourceCapability, area, outcome string) int {
	for _, cell := range capability.Cells {
		if cell.Area == area && cell.Outcome == outcome {
			return cell.Strategies
		}
	}
	return -1
}

// Strategies are counted, not records: once per area at their most severe
// outcome there, in every area they have a refusal in, and out of supported
// in each of those areas.
func TestCapabilityCountsDistinctStrategiesPerArea(t *testing.T) {
	records := []CapabilityRecord{
		// 1: two areas, source and target.
		{StrategyID: "1", Disposition: dispositionCapabilityUnsupported, Reason: "QUERY_FUNCTION_NOT_MIGRATED", Scope: "STRATEGY"},
		{StrategyID: "1", Disposition: dispositionCapabilityUnsupported, Reason: "UNSUPPORTED_TARGET_SCOPE", Scope: "STRATEGY"},
		// 2: one level refused and the strategy refused, both in algorithm:
		// not yet supported wins over partial.
		{StrategyID: "2", Disposition: dispositionCapabilityUnsupported, Reason: "ALGORITHM_NOT_MIGRATED", Scope: "LEVEL"},
		{StrategyID: "2", Disposition: dispositionCapabilityUnsupported, Reason: "ALGORITHM_UNSUPPORTED", Scope: "STRATEGY"},
		// 3: one level only: partial, twice on two levels, counted once.
		{StrategyID: "3", Disposition: dispositionCapabilityUnsupported, Reason: "ALGORITHM_NOT_MIGRATED", Scope: "LEVEL"},
		{StrategyID: "3", Disposition: dispositionCapabilityUnsupported, Reason: "ALGORITHM_NOT_MIGRATED", Scope: "LEVEL"},
		// 4: by design.
		{StrategyID: "4", Disposition: dispositionCapabilityUnsupported, Reason: "QUERY_FTA_UNSUPPORTED", Scope: "STRATEGY"},
		// 5: noted, runs: partial in config.
		{StrategyID: "5", Disposition: dispositionConfigNoted, Reason: "AGG_INTERVAL_DEFAULTED", Scope: "STRATEGY"},
		// 6: a reason the table does not know: the configuration's, not yet.
		{StrategyID: "6", Disposition: dispositionCapabilityUnsupported, Reason: "A_REASON_NOBODY_WROTE_YET", Scope: "STRATEGY"},
		// 7: the source's state, no capability fact.
		{StrategyID: "7", Disposition: "SOURCE_INCOMPLETE", Reason: "SOURCE_IDENTITY_UNAVAILABLE", Scope: "STRATEGY"},
		// 8: priority arbitration is left out by design, in no cell.
		{StrategyID: "8", Disposition: dispositionCapabilityUnsupported, Reason: "PRIORITY_IGNORED", Scope: "STRATEGY"},
	}
	got := CountCapability(records, 10)
	if len(got.Cells) != len(CapabilityAreas)*len(CapabilityOutcomes) {
		t.Fatalf("%d cells, want every area by every outcome", len(got.Cells))
	}
	for _, want := range []struct {
		area, outcome string
		strategies    int
	}{
		{CapabilitySource, CapabilityNotYetSupported, 1}, {CapabilitySource, CapabilityUnsupportedByDesign, 1}, {CapabilitySource, CapabilitySupported, 8},
		{CapabilityTarget, CapabilityNotYetSupported, 1}, {CapabilityTarget, CapabilitySupported, 9},
		{CapabilityAlgorithm, CapabilityNotYetSupported, 1}, {CapabilityAlgorithm, CapabilityPartial, 1}, {CapabilityAlgorithm, CapabilitySupported, 8},
		{CapabilityConfig, CapabilityPartial, 1}, {CapabilityConfig, CapabilityNotYetSupported, 1}, {CapabilityConfig, CapabilitySupported, 8},
		{CapabilityOutput, CapabilitySupported, 10}, {CapabilityOutput, CapabilityNotYetSupported, 0},
	} {
		if n := capabilityCell(got, want.area, want.outcome); n != want.strategies {
			t.Errorf("%s/%s = %d, want %d", want.area, want.outcome, n, want.strategies)
		}
	}
	var notMigrated *CapabilityReason
	for i := range got.Reasons {
		if got.Reasons[i].Code == "ALGORITHM_NOT_MIGRATED" {
			notMigrated = &got.Reasons[i]
		}
	}
	if notMigrated == nil || notMigrated.Strategies != 2 || notMigrated.Outcome != CapabilityPartial || !reflect.DeepEqual(notMigrated.SampleIDs, []string{"2", "3"}) {
		t.Errorf("ALGORITHM_NOT_MIGRATED = %+v, want 2 strategies, partial, samples 2 and 3", notMigrated)
	}
	if got.Reasons[0].Strategies != 2 {
		t.Errorf("reasons not largest first: %+v", got.Reasons)
	}
	for _, reason := range got.Reasons {
		if reason.Code == "SOURCE_IDENTITY_UNAVAILABLE" || reason.Code == "PRIORITY_IGNORED" {
			t.Errorf("reason %s counted as a capability refusal", reason.Code)
		}
	}
	if len(got.IgnoredByDesign) != 1 || got.IgnoredByDesign[0].Code != "PRIORITY_IGNORED" || got.IgnoredByDesign[0].Strategies != 1 {
		t.Errorf("ignored by design = %+v", got.IgnoredByDesign)
	}
}

// A sample is at most three strategies, in numeric order.
func TestCapabilityReasonSamplesAreFewAndNumeric(t *testing.T) {
	var records []CapabilityRecord
	for _, id := range []string{"81", "9", "100", "12", "9"} {
		records = append(records, CapabilityRecord{StrategyID: id, Disposition: dispositionCapabilityUnsupported, Reason: "UNSUPPORTED_TARGET_PLAN", Scope: "STRATEGY"})
	}
	got := CountCapability(records, 4)
	if len(got.Reasons) != 1 || got.Reasons[0].Strategies != 4 || !reflect.DeepEqual(got.Reasons[0].SampleIDs, []string{"9", "12", "81"}) {
		t.Fatalf("reasons = %+v", got.Reasons)
	}
	if n := capabilityCell(got, CapabilityTarget, CapabilitySupported); n != 0 {
		t.Errorf("target supported = %d, want 0", n)
	}
}

// A code met at one level of one strategy and on the whole of another is
// listed at its most severe outcome.
func TestACapabilityReasonTakesItsMostSevereOutcome(t *testing.T) {
	got := CountCapability([]CapabilityRecord{
		{StrategyID: "1", Disposition: dispositionCapabilityUnsupported, Reason: "UNSUPPORTED_TARGET_PLAN", Scope: "LEVEL"},
		{StrategyID: "2", Disposition: dispositionCapabilityUnsupported, Reason: "UNSUPPORTED_TARGET_PLAN", Scope: "STRATEGY"},
	}, 2)
	if len(got.Reasons) != 1 || got.Reasons[0].Outcome != CapabilityNotYetSupported {
		t.Fatalf("reasons = %+v, want UNSUPPORTED_TARGET_PLAN at not yet supported", got.Reasons)
	}
	if capabilityCell(got, CapabilityTarget, CapabilityPartial) != 1 || capabilityCell(got, CapabilityTarget, CapabilityNotYetSupported) != 1 {
		t.Errorf("target cells = %+v, want one partial and one not yet", got.Cells)
	}
}
