// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package evaluation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// The settled boundary of the recovery walk is the current Slot's own read:
// a position counts once that read, made under the group's hold, covered it,
// up to the evaluation time less the query delay - the primary
// requirement's EndOffsetSeconds, from which the query window's end is
// resolved. A series with state and a point three minutes before this one,
// under a three minute window and one recovery window, is read in the Slot a
// minute later: its two holes are covered and settled, and it recovers,
// whatever the hold the Slot was frozen with. With the requirement ending ten
// minutes earlier - a read that did not cover them - it does not.
//
// The earlier point is older than this Level retains, so the window holds
// only this record: the series is known to be past its first position from
// its state alone, which is what a sparse series' recovery depends on once
// retention has dropped every point before its oldest window.
func TestTheSlotsOwnReadSetsTheSettledBoundary(t *testing.T) {
	plan := compiledWindow(t, 3, 1)
	fingerprint := plan.Levels().At(0).Fingerprints().Detect
	const at = int64(6000)
	records := []contract.CanonicalRecordV2{{RecordID: strings.Repeat("b", 64), SourceTime: at, BusinessID: "2",
		DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)},
		Values:            map[string]json.RawMessage{"value": json.RawMessage(`10`)}, Dimensions: map[string]json.RawMessage{}, ReceivedTime: at}}
	history := []execution.StateHistoryPoint{{RecordID: strings.Repeat("d", 64), SourceTime: at - 3*60,
		Levels: []execution.StateLevelFact{{LevelID: 5, DetectFingerprint: fingerprint, Result: execution.LevelFactNormal}}}}
	outcome := func(endOffsetSeconds, holdMillis int64) execution.LevelOutcomeKind {
		t.Helper()
		request := atSlot(t, plan, requestFixtureForPlan(t, plan, records, history), at+60)
		request.Header.Requirements[0].RelativeWindow.StartOffsetSeconds = endOffsetSeconds - 60
		request.Header.Requirements[0].RelativeWindow.EndOffsetSeconds = endOffsetSeconds
		request.Header.Contract.ReadHoldMillis = holdMillis
		request.Inputs[0].Contract = request.Header.Contract
		result, err := newEvaluator(t).Evaluate(context.Background(), request)
		if err != nil {
			t.Fatalf("Evaluate(end %d, hold %d) = %v", endOffsetSeconds, holdMillis, err)
		}
		return result.Plans[0].LevelOutcomes[0].Outcome
	}
	for _, hold := range []int64{0, int64(10 * time.Minute / time.Millisecond)} {
		if got := outcome(0, hold); got != execution.LevelOutcomeRecovery {
			t.Fatalf("with the read covering the window and a %d ms hold the outcome is %s, want RECOVERY", hold, got)
		}
	}
	if got := outcome(-600, 0); got == execution.LevelOutcomeRecovery {
		t.Fatal("with the read ending ten minutes back the walk recovered: the read's end did not reach the trigger")
	}
}

// atSlot moves the fixture's Slot to evaluation time at, with the facts and
// window that go with it.
func atSlot(t *testing.T, plan *strategy.CompiledPlan, request execution.EvaluationRequest, at int64) execution.EvaluationRequest {
	t.Helper()
	request.Header.Contract.Slot.EvaluationTime = execution.EvaluationTime(at)
	request.Header.DecisionTime = at
	request.Header.Contract.ScheduleSegmentStart = execution.EvaluationTime(at)
	provider := strategy.NewStaticScheduleProvider(strategy.TimezoneResolverFunc(func(context.Context, string, string, string) (*time.Location, error) {
		return time.UTC, nil
	}))
	facts, err := provider.Resolve(context.Background(), []strategy.EffectiveTimeRequest{{TenantID: "tenant", BusinessID: "2",
		EvaluationTime: at, Requirement: plan.Levels().At(0).EffectiveTimeRequirement()}})
	if err != nil {
		t.Fatal(err)
	}
	request.Header.EffectiveTimeFacts[0].Fact = facts[0]
	request.Inputs[0].Contract = request.Header.Contract
	request.Inputs[0].Inputs[0].QueryWindow = execution.QueryWindow{Start: at - 60, End: at}
	return request
}

// A header that names no PRIMARY requirement for the Plan gives no read end,
// which the trigger reads as nothing settled. The production path cannot
// build one - every due Level has exactly one PRIMARY - so this is the
// function's own fail-safe, checked on its own.
func TestNoPrimaryRequirementGivesNoReadEnd(t *testing.T) {
	plan := compiledWindow(t, 3, 1)
	records := []contract.CanonicalRecordV2{{RecordID: strings.Repeat("b", 64), SourceTime: 6000, BusinessID: "2",
		DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)},
		Values:            map[string]json.RawMessage{"value": json.RawMessage(`10`)}, Dimensions: map[string]json.RawMessage{}, ReceivedTime: 6000}}
	request := atSlot(t, plan, requestFixtureForPlan(t, plan, records, nil), 6060)
	due := request.Header.DuePlans[0].Identity
	if end := primaryReadEnd(request.Header, due); end != 6060 {
		t.Fatalf("read end = %d, want the Slot's evaluation time less a zero delay", end)
	}
	request.Header.Requirements[0].Role = execution.InputRoleAlgorithmDependency
	if end := primaryReadEnd(request.Header, due); end != 0 {
		t.Fatalf("with no PRIMARY requirement the read end is %d, want 0: nothing settled", end)
	}
}
