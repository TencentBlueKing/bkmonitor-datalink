// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package trigger

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// openAlertSetFixture answers membership from a fixed set and records what it
// was asked, so a test can assert both the verdict and that the gate asked
// with the identity the consumer keys alerts by.
type openAlertSetFixture struct {
	members map[string]bool
	asked   []string
	// oneTime marks the set a one-time close is asked against
	// (contract.OneTimeCloseSet).
	oneTime bool
}

func (set *openAlertSetFixture) OneTimeClose() bool { return set.oneTime }

func (set *openAlertSetFixture) Contains(tenantID, strategyID, fingerprint string) bool {
	key := tenantID + "/" + strategyID + "/" + fingerprint
	set.asked = append(set.asked, key)
	return set.members[key]
}

func nativePlanV2(t *testing.T, levels []contract.LevelIRV2, identity *contract.MonitorOutputIdentity) *strategy.CompiledPlan {
	t.Helper()
	return compilePlanV2WithOutput(t, levels, func(p *contract.EvaluationPlanV2) {
		p.WireFormat = contract.WireFormatStandardRawEvent
		p.StrategyRef.SnapshotRevision = 7
		p.StrategyIR.StrategyRef.SnapshotRevision = 7
		p.OutputIdentity = identity
	})
}

// The consumer resolves whatever alert it holds on a RECOVERY envelope and
// closes the envelope as an orphan when it holds none. So once every Level
// has agreed, the envelope goes only if the consumer holds an open alert on
// the series. The set answers membership only: the Level results and the
// first gate are unchanged by it.
func TestRecoveryEnvelopeGoesOnlyToAnOpenAlert(t *testing.T) {
	const source = int64(300)
	recovered := []contract.LevelIRV2{levelV2(1, 1, 1, 1, 1, nil), levelV2(2, 2, 1, 1, 1, nil)}
	identity := &contract.MonitorOutputIdentity{DimensionFields: []string{"host"}}
	fingerprint := func(t *testing.T) string {
		t.Helper()
		// The same dimensions requestV2 puts on the record, so the fixture
		// member is the fingerprint the gate will ask about.
		md5, err := contract.MonitorDedupeMD5("1001", "2", map[string]json.RawMessage{"host": json.RawMessage(`"127.0.0.1"`)}, *identity)
		if err != nil {
			t.Fatal(err)
		}
		return md5
	}
	tests := []struct {
		name     string
		plan     func(t *testing.T) *strategy.CompiledPlan
		set      func(t *testing.T) *openAlertSetFixture
		wantGate RecoveryGateV2
		envelope bool
		asked    int
	}{
		{
			name: "the consumer holds an open alert: the envelope goes",
			plan: func(t *testing.T) *strategy.CompiledPlan { return nativePlanV2(t, recovered, identity) },
			set: func(t *testing.T) *openAlertSetFixture {
				return &openAlertSetFixture{members: map[string]bool{"default/1001/" + fingerprint(t): true}}
			},
			wantGate: RecoveryGateV2{OpenAlertGate: OpenAlertGatePassed},
			envelope: true, asked: 1,
		},
		{
			// The one close of a group the target dropped is let through by
			// the set it is asked against, and named apart from a pass the
			// consumer's copy vouched for: some of these are orphans at the
			// consumer, and only a name of their own counts them.
			name: "the one close of a dropped group: let through, named as such",
			plan: func(t *testing.T) *strategy.CompiledPlan { return nativePlanV2(t, recovered, identity) },
			set: func(t *testing.T) *openAlertSetFixture {
				return &openAlertSetFixture{members: map[string]bool{"default/1001/" + fingerprint(t): true}, oneTime: true}
			},
			wantGate: RecoveryGateV2{OpenAlertGate: OpenAlertGatePassedOneTimeClose},
			envelope: true, asked: 1,
		},
		{
			name:     "the consumer holds no alert on the series: held, nothing to resolve",
			plan:     func(t *testing.T) *strategy.CompiledPlan { return nativePlanV2(t, recovered, identity) },
			set:      func(t *testing.T) *openAlertSetFixture { return &openAlertSetFixture{} },
			wantGate: RecoveryGateV2{Held: true, Cause: RecoveryHeldNoOpenAlert, OpenAlertGate: OpenAlertGateHeldNoOpenAlert},
			envelope: false, asked: 1,
		},
		{
			name:     "no fingerprint can be built: held as unknown, the set is not asked",
			plan:     func(t *testing.T) *strategy.CompiledPlan { return nativePlanV2(t, recovered, nil) },
			set:      func(t *testing.T) *openAlertSetFixture { return &openAlertSetFixture{} },
			wantGate: RecoveryGateV2{Held: true, Cause: RecoveryHeldFingerprintUnknown, OpenAlertGate: OpenAlertGateHeldFingerprintUnknown},
			envelope: false, asked: 0,
		},
		{
			name:     "no set was passed: the envelope goes as before the gate, and says so",
			plan:     func(t *testing.T) *strategy.CompiledPlan { return nativePlanV2(t, recovered, identity) },
			set:      func(t *testing.T) *openAlertSetFixture { return nil },
			wantGate: RecoveryGateV2{OpenAlertGate: OpenAlertGateNotConfigured},
			envelope: true, asked: 0,
		},
		{
			name:     "a Plan on the compatibility protocol: the set is not asked",
			plan:     func(t *testing.T) *strategy.CompiledPlan { return compilePlanV2(t, recovered) },
			set:      func(t *testing.T) *openAlertSetFixture { return &openAlertSetFixture{} },
			wantGate: RecoveryGateV2{OpenAlertGate: OpenAlertGateProtocolNotGated},
			envelope: true, asked: 0,
		},
		{
			name: "a historical decision-event Plan now uses the consumer recovery gate",
			plan: func(t *testing.T) *strategy.CompiledPlan {
				return compilePlanV2WithOutput(t, recovered, func(p *contract.EvaluationPlanV2) {
					p.WireFormat = contract.WireFormatTriggerEvent
					p.StrategyRef.SnapshotRevision = 7
					p.StrategyIR.StrategyRef.SnapshotRevision = 7
					p.OutputIdentity = identity
				})
			},
			set:      func(t *testing.T) *openAlertSetFixture { return &openAlertSetFixture{} },
			wantGate: RecoveryGateV2{Held: true, Cause: RecoveryHeldNoOpenAlert, OpenAlertGate: OpenAlertGateHeldNoOpenAlert},
			envelope: false, asked: 1,
		}, {
			name: "a historical empty-format Plan with revision uses the consumer recovery gate",
			plan: func(t *testing.T) *strategy.CompiledPlan {
				return compilePlanV2WithOutput(t, recovered, func(p *contract.EvaluationPlanV2) {
					p.WireFormat = ""
					p.StrategyRef.SnapshotRevision = 7
					p.StrategyIR.StrategyRef.SnapshotRevision = 7
					p.OutputIdentity = identity
				})
			},
			set:      func(t *testing.T) *openAlertSetFixture { return &openAlertSetFixture{} },
			wantGate: RecoveryGateV2{Held: true, Cause: RecoveryHeldNoOpenAlert, OpenAlertGate: OpenAlertGateHeldNoOpenAlert},
			envelope: false, asked: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := test.plan(t)
			levels := plan.Levels().Copy()
			request := requestV2(t, plan, source,
				[]DetectionFact{factV2(levels[0], DetectionNormal), factV2(levels[1], DetectionNormal)},
				[]LevelHistory{
					{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
					{LevelID: 2, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
				}, activeFactsV2(t, plan, source))
			set := test.set(t)
			if set != nil {
				request.OpenAlerts = set
			}
			result, err := EvaluateV2(request)
			if err != nil {
				t.Fatalf("EvaluateV2() error = %v", err)
			}
			if result.RecordResult != contract.LevelResultRecovery || result.LevelOutcomes[0].Result != contract.LevelResultRecovery {
				t.Fatalf("record = %q / Level 1 = %q, want RECOVERY: the fixture does not reach the second gate", result.RecordResult, result.LevelOutcomes[0].Result)
			}
			if result.RecoveryGate != test.wantGate {
				t.Fatalf("gate = %+v, want %+v", result.RecoveryGate, test.wantGate)
			}
			if (result.TriggerEvent != nil) != test.envelope {
				t.Fatalf("envelope = %+v, want present=%v", result.TriggerEvent, test.envelope)
			}
			if set != nil && len(set.asked) != test.asked {
				t.Fatalf("the set was asked %d times %v, want %d", len(set.asked), set.asked, test.asked)
			}
			if test.envelope && result.Counts.Events != 1 || !test.envelope && result.Counts.Events != 0 {
				t.Fatalf("Counts.Events = %d with envelope present=%v", result.Counts.Events, test.envelope)
			}
		})
	}
}

// Another Level's state no longer holds the envelope, so the set is asked
// for a RECOVERY decided beside an unavailable Level too, and its verdict
// keeps the Level the record went beside: the set holds for no open alert,
// and the record still says what it was decided beside.
func TestOpenAlertSetIsAskedBesideAnUnavailableLevel(t *testing.T) {
	const source = int64(300)
	identity := &contract.MonitorOutputIdentity{DimensionFields: []string{"host"}}
	plan := nativePlanV2(t, []contract.LevelIRV2{levelV2(1, 1, 1, 1, 1, nil), levelV2(2, 2, 1, 1, 1, nil)}, identity)
	levels := plan.Levels().Copy()
	set := &openAlertSetFixture{}
	request := requestV2(t, plan, source,
		[]DetectionFact{unavailableFactV2(levels[0], contract.ReasonRequiredValueMissing), factV2(levels[1], DetectionNormal)},
		[]LevelHistory{
			{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{source - 60: true}}},
			{LevelID: 2, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
		}, activeFactsV2(t, plan, source))
	request.OpenAlerts = set
	result, err := EvaluateV2(request)
	if err != nil {
		t.Fatalf("EvaluateV2() error = %v", err)
	}
	want := RecoveryGateV2{Held: true, Cause: RecoveryHeldNoOpenAlert, Beside: RecoveryBesideLevelUnavailable, BesideLevelID: 1,
		OpenAlertGate: OpenAlertGateHeldNoOpenAlert}
	if result.RecoveryGate != want {
		t.Fatalf("gate = %+v, want %+v", result.RecoveryGate, want)
	}
	if len(set.asked) != 1 || result.TriggerEvent != nil {
		t.Fatalf("the set was asked %v, envelope %+v; want it asked once and no envelope for a series with no open alert", set.asked, result.TriggerEvent)
	}
}

// An ABNORMAL record is never gated by the set: an anomaly is pushed whether
// or not the consumer already holds an alert, and the set is not asked.
func TestOpenAlertSetDoesNotTouchAnAbnormalRecord(t *testing.T) {
	const source = int64(300)
	identity := &contract.MonitorOutputIdentity{DimensionFields: []string{"host"}}
	plan := nativePlanV2(t, []contract.LevelIRV2{levelV2(1, 1, 1, 1, 1, nil)}, identity)
	set := &openAlertSetFixture{}
	request := requestV2(t, plan, source, []DetectionFact{factV2(plan.Levels().At(0), DetectionAnomalous)},
		[]LevelHistory{{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{source: true}}}}, activeFactsV2(t, plan, source))
	request.OpenAlerts = set
	result, err := EvaluateV2(request)
	if err != nil {
		t.Fatalf("EvaluateV2() error = %v", err)
	}
	if result.RecordResult != contract.LevelResultAbnormal || result.TriggerEvent == nil || result.TriggerEvent.EventKind != contract.TriggerEventAbnormal {
		t.Fatalf("result = %q envelope = %+v, want an ABNORMAL envelope", result.RecordResult, result.TriggerEvent)
	}
	if result.RecoveryGate != (RecoveryGateV2{}) || len(set.asked) != 0 {
		t.Fatalf("gate = %+v asked = %v, want untouched", result.RecoveryGate, set.asked)
	}
}

// openLevelSetFixture is an open alert set that also says which Levels of
// the series' alert are open (contract.OpenLevelSet); known false is a set
// that cannot say, as after a restart.
type openLevelSetFixture struct {
	openAlertSetFixture
	open  []uint32
	known bool
}

func (set *openLevelSetFixture) OpenLevels(string, string, string) ([]uint32, bool) {
	return set.open, set.known
}

// The consumer keeps one alert per series, and Python recovers an alert
// only at its own Level, the most severe it has open: a RECOVERY of any
// other Level is an orphan at the consumer or a recovery Python never sends
// (a warning RECOVERY beside an open info alert, seen many times an
// hour). So a RECOVERY envelope carries only the current
// Level's recovery, and is held when the record has none; where the set
// cannot say which Levels are open, every Level goes as before.
func TestARecoveryCarriesOnlyTheLevelsTheAlertHasOpen(t *testing.T) {
	const source = int64(300)
	identity := &contract.MonitorOutputIdentity{DimensionFields: []string{"host"}}
	both := []contract.LevelIRV2{levelV2(1, 1, 1, 1, 1, nil), levelV2(2, 2, 1, 1, 1, nil)}
	member := func(t *testing.T) map[string]bool {
		t.Helper()
		md5, err := contract.MonitorDedupeMD5("1001", "2", map[string]json.RawMessage{"host": json.RawMessage(`"127.0.0.1"`)}, *identity)
		if err != nil {
			t.Fatal(err)
		}
		return map[string]bool{"default/1001/" + md5: true}
	}
	recoveredLevels := func(event *contract.TriggerEventV1) []uint32 {
		var ids []uint32
		for _, result := range event.LevelResults {
			if result.Result == contract.LevelResultRecovery {
				ids = append(ids, result.LevelID)
			}
		}
		return ids
	}
	for _, test := range []struct {
		name string
		// level1Unavailable recovers Level 2 alone: Level 1 is not decided.
		level1Unavailable bool
		// levels overrides the Plan's two Levels (their priorities).
		levels        []contract.LevelIRV2
		open          []uint32
		known         bool
		wantGate      string
		wantRecovered []uint32
	}{
		{name: "both Levels open, both recover: the current Level's goes", open: []uint32{1, 2}, known: true,
			wantGate: OpenAlertGatePassed, wantRecovered: []uint32{1}},
		{name: "both Levels open, the lower recovers alone: held while the current one is open", level1Unavailable: true,
			open: []uint32{1, 2}, known: true, wantGate: OpenAlertGateHeldLevelNotOpen},
		{name: "only Level 1 open, both recover: Level 1's goes, Level 2's is left out", open: []uint32{1}, known: true,
			wantGate: OpenAlertGatePassed, wantRecovered: []uint32{1}},
		{name: "Level 2 recovers alone beside open Level 1: held, an orphan at the consumer", level1Unavailable: true,
			open: []uint32{1}, known: true, wantGate: OpenAlertGateHeldLevelNotOpen},
		{name: "Level 2 recovers alone and is the open one: it goes", level1Unavailable: true,
			open: []uint32{2}, known: true, wantGate: OpenAlertGatePassed, wantRecovered: []uint32{2}},
		{name: "a detect priority inverted against the Level ids: the lowest id is current, as the link and Python rank, Level 1's goes",
			levels: []contract.LevelIRV2{levelV2(1, 2, 1, 1, 1, nil), levelV2(2, 1, 1, 1, 1, nil)}, open: []uint32{1, 2}, known: true,
			wantGate: OpenAlertGatePassed, wantRecovered: []uint32{1}},
		{name: "the open Levels are not known, as after a restart: every Level goes as before", open: nil, known: false,
			wantGate: OpenAlertGatePassed, wantRecovered: []uint32{1, 2}},
	} {
		t.Run(test.name, func(t *testing.T) {
			planLevels := both
			if test.levels != nil {
				planLevels = test.levels
			}
			plan := nativePlanV2(t, planLevels, identity)
			levels := plan.Levels().Copy()
			facts := []DetectionFact{factV2(levels[0], DetectionNormal), factV2(levels[1], DetectionNormal)}
			histories := []LevelHistory{
				{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
				{LevelID: 2, View: pointHistory{step: 60, points: map[int64]bool{source: false}}},
			}
			if test.level1Unavailable {
				facts[0] = unavailableFactV2(levels[0], contract.ReasonRequiredValueMissing)
				histories[0] = LevelHistory{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{source - 60: true}}}
			}
			request := requestV2(t, plan, source, facts, histories, activeFactsV2(t, plan, source))
			request.OpenAlerts = &openLevelSetFixture{openAlertSetFixture: openAlertSetFixture{members: member(t)}, open: test.open, known: test.known}
			result, err := EvaluateV2(request)
			if err != nil {
				t.Fatalf("EvaluateV2() error = %v", err)
			}
			if result.RecordResult != contract.LevelResultRecovery || result.RecoveryGate.OpenAlertGate != test.wantGate {
				t.Fatalf("record %q gate %+v, want RECOVERY gated %s", result.RecordResult, result.RecoveryGate, test.wantGate)
			}
			if test.wantGate == OpenAlertGateHeldLevelNotOpen {
				if result.TriggerEvent != nil || !result.RecoveryGate.Held || result.RecoveryGate.Cause != RecoveryHeldLevelNotOpen {
					t.Fatalf("held: envelope %+v gate %+v", result.TriggerEvent, result.RecoveryGate)
				}
				return
			}
			if result.TriggerEvent == nil || !slices.Equal(recoveredLevels(result.TriggerEvent), test.wantRecovered) {
				t.Fatalf("envelope %+v, want RECOVERY of Levels %v", result.TriggerEvent, test.wantRecovered)
			}
		})
	}
}

// After an edit that adds a Level, the carried history holds results for the
// Levels both versions have and none for the new one (worker carryHistory):
// the carried Level triggers on its carried window, and the new Level warms
// up on its own, as a new series does, and decides nothing.
func TestALevelAddedByAnEditWarmsUpOnItsOwnWindowBesideACarriedOne(t *testing.T) {
	const source = int64(300)
	identity := &contract.MonitorOutputIdentity{DimensionFields: []string{"host"}}
	plan := nativePlanV2(t, []contract.LevelIRV2{levelV2(1, 1, 3, 2, 1, nil), levelV2(2, 2, 3, 2, 1, nil)}, identity)
	levels := plan.Levels().Copy()
	facts := []DetectionFact{factV2(levels[0], DetectionAnomalous), factV2(levels[1], DetectionAnomalous)}
	histories := []LevelHistory{
		// Level 1 carried a whole window, one anomaly before this round's.
		{LevelID: 1, View: pointHistory{step: 60, points: map[int64]bool{source - 120: false, source - 60: true, source: true}}},
		// Level 2 is new: only this round's point.
		{LevelID: 2, View: pointHistory{step: 60, points: map[int64]bool{source: true}}},
	}
	request := requestV2(t, plan, source, facts, histories, activeFactsV2(t, plan, source))
	request.SeriesSeenBefore = true
	got, err := EvaluateV2(request)
	if err != nil {
		t.Fatal(err)
	}
	if got.LevelOutcomes[0].Result != contract.LevelResultAbnormal {
		t.Fatalf("the carried Level decided %+v, want ABNORMAL on its carried window (2 of 3)", got.LevelOutcomes[0])
	}
	if outcome := got.LevelOutcomes[1]; outcome.Result != "" || outcome.UnavailableReason != historyReasonV2(HistoryWarming) {
		t.Fatalf("the new Level decided %+v, want it warming on its own window", outcome)
	}
}
