// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// openAlertGatesFor is what the open-alert gate answered for one series'
// records in the observations after a mark.
func (fixture *roundsFixture) openAlertGatesFor(mark int, series string) []observability.OpenAlertGateFact {
	var facts []observability.OpenAlertGateFact
	for _, observation := range fixture.since(mark) {
		if observation.Stage == observability.StageEvaluationCompleted && observation.Trace.DimensionIdentityDigest == series {
			facts = append(facts, observation.OpenAlertGates...)
		}
	}
	return facts
}

// A no-data RECOVERY reaches the alert consumer only for an alert it holds
// open, asked by the no-data alert's own identity.
//
// The gate is the recovery gate's second step (trigger openAlertGateV2): on
// the consumer's protocol a RECOVERY envelope goes when the consumer's open
// alert set holds the series' alert, and is held, by the name
// held_no_open_alert, when it does not. The set is keyed by the fingerprint
// the alert was opened under, and a no-data alert's identity carries the
// no-data tag, so it is a different alert from the threshold one on the same
// host (nodata-progress section 2, P10; decision-007). A no-data recovery
// asked under any other identity than the one its anomaly went out with would
// either be held for ever, leaving the alert open, or close another alert.
//
// Host B is absent in round 1 and its no-data alert goes out; in round 2 its
// data is back and the round decides the closing RECOVERY. With the alert
// open in the copy this process keeps of the consumer's set - it took the
// anomaly's acknowledgement - the envelope passes and is written. A process
// that did not send the anomaly holds no such alert in its copy, and the
// consumer's set it reads holds none either: the same decision is held and
// nothing is written. The third shape the row names, a worker with no set at
// all, is refused when the coordinator is built
// (TestCoordinatorRequiresTheOpenAlertCopy); the production wiring always
// passes one, which is why neither case here may see not_configured.
func TestANoDataRecoveryGoesOnlyToAnOpenAlert(t *testing.T) {
	open := func(t *testing.T, fixture *roundsFixture) string {
		t.Helper()
		fixture.run(1)
		raised, _ := eventsAbout(fixture.written(), "bk_target_ip", followsHostB)
		if len(raised) != 1 || raised[0].EventKind != contract.TriggerEventAbnormal {
			t.Fatalf("fixture: round 1 sent host B's no-data events %+v, want the anomaly that opens its alert", raised)
		}
		return raised[0].RecordRef.DimensionIdentityDigest
	}
	t.Run("the alert is open", func(t *testing.T) {
		fixture := startRoundsFixture(t, roundsOptions{hosts: true})
		series := open(t, fixture)
		fixture.report(ipSeries(followsHostA), ipSeries(followsHostB))
		mark, sent := fixture.mark(), len(fixture.written())
		fixture.run(2)
		closed, _ := eventsAbout(fixture.written()[sent:], "bk_target_ip", followsHostB)
		if len(closed) != 1 || closed[0].EventKind != contract.TriggerEventRecovery {
			t.Fatalf("round 2 wrote host B's no-data events %+v, want the one RECOVERY that closes its open alert", closed)
		}
		if closed[0].RecordRef.DimensionIdentityDigest != series {
			t.Fatalf("the RECOVERY is about series %s, the anomaly was about %s", closed[0].RecordRef.DimensionIdentityDigest, series)
		}
		want := []observability.OpenAlertGateFact{{Outcome: "passed", Records: 1}}
		if got := fixture.openAlertGatesFor(mark, series); !reflect.DeepEqual(got, want) {
			t.Fatalf("the gate answered %+v for host B's no-data series, want %+v", got, want)
		}
	})
	t.Run("no open alert", func(t *testing.T) {
		first := startRoundsFixture(t, roundsOptions{hosts: true})
		series := open(t, first)
		// Another process takes the Plan over from the same store: the State
		// that says the alert is open carries over, the copy of the
		// consumer's set does not, and the consumer's set holds nothing.
		first.stopBundle()
		fixture := startRoundsFixture(t, roundsOptions{address: first.address, client: first.redis, base: first.base})
		fixture.report(ipSeries(followsHostA), ipSeries(followsHostB))
		mark := fixture.mark()
		fixture.run(2)
		if held, _ := eventsAbout(fixture.written(), "bk_target_ip", followsHostB); len(held) != 0 {
			t.Fatalf("round 2 wrote host B's no-data events %+v; the consumer holds no alert for it to close", held)
		}
		want := []observability.OpenAlertGateFact{{Outcome: "held_no_open_alert", Records: 1}}
		if got := fixture.openAlertGatesFor(mark, series); !reflect.DeepEqual(got, want) {
			t.Fatalf("the gate answered %+v for host B's no-data series, want %+v: a RECOVERY was decided and "+
				"held for want of an open alert", got, want)
		}
	})
}
