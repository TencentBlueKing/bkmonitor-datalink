// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package trigger

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/activecopy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// ownGateSeries drives one series of a two-Level native Plan through the
// evaluator with the record of this process's own opens as its gate, the
// way a deployment without the link wires it: each envelope the evaluator
// emits is handed to the record as the broker's acknowledgement.
type ownGateSeries struct {
	t      *testing.T
	now    time.Time
	own    *activecopy.Own
	source int64
}

func newOwnGateSeries(t *testing.T) *ownGateSeries {
	series := &ownGateSeries{t: t, now: time.Unix(1_800_000_000, 0), source: 300}
	series.own = activecopy.NewOwn(func() time.Time { return series.now })
	return series
}

// round evaluates one round. Level 1 is the more severe (priority 1), Level
// 2 the less; each is anomalous, normal or unavailable.
// It returns the envelope's kind, or "" when the gate held it.
func (series *ownGateSeries) round(severe, mild string) string {
	t := series.t
	t.Helper()
	identity := &contract.MonitorOutputIdentity{DimensionFields: []string{"host"}}
	plan := nativePlanV2(t, []contract.LevelIRV2{levelV2(1, 1, 1, 1, 1, nil), levelV2(2, 2, 1, 1, 1, nil)}, identity)
	levels := plan.Levels().Copy()
	source := series.source
	var facts []DetectionFact
	var histories []LevelHistory
	for i, state := range []string{severe, mild} {
		switch state {
		case "unavailable":
			facts = append(facts, unavailableFactV2(levels[i], contract.ReasonRequiredValueMissing))
			histories = append(histories, LevelHistory{LevelID: uint32(i + 1), View: pointHistory{step: 60, points: map[int64]bool{source - 60: true}}})
		default:
			facts = append(facts, factV2(levels[i], state))
			histories = append(histories, LevelHistory{LevelID: uint32(i + 1), View: pointHistory{step: 60, points: map[int64]bool{source: state == DetectionAnomalous}}})
		}
	}
	request := requestV2(t, plan, source, facts, histories, activeFactsV2(t, plan, source))
	request.OpenAlerts = series.own
	result, err := EvaluateV2(request)
	if err != nil {
		t.Fatalf("EvaluateV2() error = %v", err)
	}
	series.source += 60
	series.now = series.now.Add(time.Minute)
	if result.TriggerEvent == nil {
		return ""
	}
	series.own.Acknowledged([]contract.TriggerEventV1{*result.TriggerEvent})
	return result.TriggerEvent.EventKind
}

const (
	anomalous   = DetectionAnomalous
	normal      = DetectionNormal
	unavailable = "unavailable"
)

// The consumer ends an alert only on a RECOVERY at the level it stands at,
// and every later RECOVERY of the series is an orphan there. So once the
// standing level's RECOVERY has gone, the series' later RECOVERYs are held,
// not passed for the rest of a grace.
func TestOwnGateHoldsRecoveriesAfterTheStandingLevelRecovered(t *testing.T) {
	series := newOwnGateSeries(t)
	if kind := series.round(normal, anomalous); kind != contract.TriggerEventAbnormal {
		t.Fatalf("opening round = %q, want ABNORMAL", kind)
	}
	if kind := series.round(normal, normal); kind != contract.TriggerEventRecovery {
		t.Fatalf("the standing level's first RECOVERY = %q, want it to pass", kind)
	}
	for i := 0; i < 3; i++ {
		if kind := series.round(normal, normal); kind != "" {
			t.Fatalf("RECOVERY %d after the standing level recovered = %q, want held", i+2, kind)
		}
	}
	if kind := series.round(normal, anomalous); kind != contract.TriggerEventAbnormal {
		t.Fatalf("a new anomaly = %q, want ABNORMAL", kind)
	}
	if kind := series.round(normal, normal); kind != contract.TriggerEventRecovery {
		t.Fatalf("the reopened alert's RECOVERY = %q, want it to pass", kind)
	}
}

// A RECOVERY without the standing level ends nothing at the consumer: the
// alert stays in the record and its RECOVERYs pass through the grace, so
// the standing level's own RECOVERY can still reach it; past the grace the
// record lets it go.
func TestOwnGateKeepsTheGraceForARecoveryWithoutTheStandingLevel(t *testing.T) {
	series := newOwnGateSeries(t)
	series.round(anomalous, anomalous)
	if kind := series.round(unavailable, normal); kind != contract.TriggerEventRecovery {
		t.Fatalf("a RECOVERY beside the unavailable standing level = %q, want it to pass", kind)
	}
	if kind := series.round(unavailable, normal); kind != contract.TriggerEventRecovery {
		t.Fatalf("within the grace = %q, want it to pass: the alert is still open at the consumer", kind)
	}
	series.now = series.now.Add(activecopy.RecoveredGrace)
	if kind := series.round(unavailable, normal); kind != "" {
		t.Fatalf("past the grace = %q, want held", kind)
	}
}

// An ABNORMAL at a more severe level raises the level the alert stands at,
// as the consumer upgrades it. A RECOVERY of the less severe level after it
// ends nothing, so the record keeps the alert and the more severe level's
// RECOVERY still passes, after which the series is held.
func TestOwnGateFollowsAnUpgradeToTheMoreSevereLevel(t *testing.T) {
	series := newOwnGateSeries(t)
	series.round(normal, anomalous)
	if kind := series.round(anomalous, anomalous); kind != contract.TriggerEventAbnormal {
		t.Fatalf("the upgrade round = %q, want ABNORMAL", kind)
	}
	if kind := series.round(unavailable, normal); kind != contract.TriggerEventRecovery {
		t.Fatalf("the less severe level's RECOVERY = %q, want it to pass within the grace", kind)
	}
	if kind := series.round(normal, normal); kind != contract.TriggerEventRecovery {
		t.Fatalf("the standing level's RECOVERY after the upgrade = %q, want it to pass", kind)
	}
	if kind := series.round(normal, normal); kind != "" {
		t.Fatalf("after the standing level recovered = %q, want held", kind)
	}
}

// An ABNORMAL that triggers a less severe level and says RECOVERY at the
// standing one rotates the alert at the consumer: the old alert ends and a
// new one opens at the less severe level. Its RECOVERY then ends the record,
// and a RECOVERY of the old level alone must not have ended it before.
func TestOwnGateFollowsARotationToTheLessSevereLevel(t *testing.T) {
	series := newOwnGateSeries(t)
	series.round(anomalous, anomalous)
	if kind := series.round(normal, anomalous); kind != contract.TriggerEventAbnormal {
		t.Fatalf("the rotating round = %q, want ABNORMAL", kind)
	}
	if kind := series.round(normal, unavailable); kind != contract.TriggerEventRecovery {
		t.Fatalf("a RECOVERY of the old level beside the rotated one = %q, want it to pass within the grace", kind)
	}
	if kind := series.round(normal, normal); kind != contract.TriggerEventRecovery {
		t.Fatalf("the rotated level's RECOVERY = %q, want it to pass: the rotated alert is still open", kind)
	}
	if kind := series.round(normal, normal); kind != "" {
		t.Fatalf("after the rotated level recovered = %q, want held", kind)
	}
}

// An ABNORMAL after a RECOVERY without the standing level renews the alert
// and stops the grace that RECOVERY started: the standing level's RECOVERY
// still passes past the first grace's end.
func TestOwnGateRenewsTheAlertOnAnAbnormalAfterAGraceStarted(t *testing.T) {
	series := newOwnGateSeries(t)
	series.round(anomalous, normal)
	if kind := series.round(unavailable, normal); kind != contract.TriggerEventRecovery {
		t.Fatalf("a RECOVERY beside the unavailable standing level = %q, want it to pass", kind)
	}
	series.now = series.now.Add(10 * time.Minute)
	if kind := series.round(anomalous, normal); kind != contract.TriggerEventAbnormal {
		t.Fatalf("the renewing round = %q, want ABNORMAL", kind)
	}
	series.now = series.now.Add(activecopy.RecoveredGrace - 10*time.Minute)
	if kind := series.round(normal, normal); kind != contract.TriggerEventRecovery {
		t.Fatalf("the standing level's RECOVERY past the first grace = %q, want it to pass: the ABNORMAL renewed the alert", kind)
	}
}
