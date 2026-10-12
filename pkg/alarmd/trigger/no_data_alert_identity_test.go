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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// The alert fingerprints Python's alert builder gives strategy 1001 of
// business 2 for the series {host: 192.0.2.40}: once for its no-data group,
// whose event carries the tag, and once for the series' own threshold alert.
//
// Both were produced by running count_md5 from bkmonitor/utils/common_utils.py
// (the function itself, lifted out of that file unchanged) on the list
// Event.cal_dedupe_md5 hashes (alarm_backends/core/alert/event.py): the values
// of strategy_id, target_type, target and bk_biz_id - the default dedupe
// fields with alert_name dropped for a strategy event - and then each tag the
// adapter keyed the event by (MonitorEventAdapter.adapt, adapter.py). A
// dimension named host is no target, so the target type is "" and the target
// None. The no-data event's tags are the group's dimension and
// __NO_DATA_DIMENSION__: True; the threshold event's are the host alone.
const (
	pythonNoDataAlertFingerprint    = "abe1414899b831b0a9dbfac590f7a827"
	pythonThresholdAlertFingerprint = "678cfc69ada901086e4279923883415b"
)

// identityPlan is a Plan on the consumer's protocol for one tenant, detecting
// no-data on the host dimension, compiled by the production compiler.
func identityPlan(t *testing.T, tenant string) *strategy.CompiledPlan {
	t.Helper()
	return compilePlanV2WithOutput(t, []contract.LevelIRV2{levelV2(1, 1, 1, 1, 1, nil)}, func(p *contract.EvaluationPlanV2) {
		p.WireFormat = contract.WireFormatStandardRawEvent
		p.StrategyRef.TenantID, p.StrategyIR.StrategyRef.TenantID = tenant, tenant
		p.StrategyRef.SnapshotRevision, p.StrategyIR.StrategyRef.SnapshotRevision = 7, 7
		p.OutputIdentity = &contract.MonitorOutputIdentity{DimensionFields: []string{"host"}}
		p.NoData = &contract.NoDataConfigV1{Continuous: 1, Level: 1, AggDimension: []string{"host"}}
	})
}

// recoveryOn is a RECOVERY decision the plan's single level reaches for one
// record of the given dimensions, in the given tenant.
func recoveryOn(t *testing.T, plan *strategy.CompiledPlan, tenant string, dimensions map[string]json.RawMessage) EvaluationRequestV2 {
	t.Helper()
	const source = int64(300)
	level := plan.Levels().Copy()[0]
	request := requestV2(t, plan, source, []DetectionFact{factV2(level, DetectionNormal)},
		[]LevelHistory{{LevelID: level.Definition().LevelID, View: pointHistory{step: 60, points: map[int64]bool{source: false}}}},
		activeFactsV2(t, plan, source))
	request.TenantID = tenant
	request.RecordRef.Dimensions = dimensions
	return request
}

func evaluateToRecovery(t *testing.T, request EvaluationRequestV2) EvaluationResultV2 {
	t.Helper()
	result, err := EvaluateV2(request)
	if err != nil {
		t.Fatalf("EvaluateV2() error = %v", err)
	}
	if result.RecordResult != contract.LevelResultRecovery {
		t.Fatalf("record = %q, want RECOVERY: the fixture does not reach the open-alert gate", result.RecordResult)
	}
	return result
}

// The fingerprint a no-data recovery is released by is its tenant's own, and
// it is the fingerprint Python files the no-data alert under (no-data tracking
// retention proposal, section 9, "identity": tenants are isolated, and the
// no-data and metric fingerprints of one group differ; decision-007 sections 4
// and 5: the alert_id is DedupeMD5 and the open-alert set is kept per tenant
// and strategy).
//
// The fingerprint itself has no tenant in it - Python's dedupe values do not
// name one - so two tenants running strategy 1001 over a host of the same
// address produce the same alert_id. What keeps them apart is the tenant the
// message carries and the tenant whose set the gate reads. The case therefore
// decides the same group's recovery in two tenants against sets that hold the
// fingerprint for the other tenant only, and both are held; then against
// their own, and both go, each carrying its own tenant and Python's
// fingerprint. The fingerprint comes out of the trigger, derived from the
// record by the no-data view of a compiled Plan, not written into the event
// by the case.
func TestANoDataRecoveryIsReleasedOnlyByItsOwnTenantsOpenAlert(t *testing.T) {
	group := map[string]json.RawMessage{
		"host": json.RawMessage(`"192.0.2.40"`), contract.NoDataDimensionTag: json.RawMessage(`true`),
	}
	tenants := []string{"tenant-a", "tenant-b"}
	views := map[string]*strategy.CompiledPlan{}
	for _, tenant := range tenants {
		views[tenant] = identityPlan(t, tenant).NoDataView()
		if views[tenant] == nil {
			t.Fatalf("the %s Plan has no no-data view", tenant)
		}
	}
	other := map[string]string{"tenant-a": "tenant-b", "tenant-b": "tenant-a"}

	for _, tenant := range tenants {
		// The other tenant's set holds the fingerprint; this tenant's does not.
		foreign := &openAlertSetFixture{members: map[string]bool{
			other[tenant] + "/1001/" + pythonNoDataAlertFingerprint: true,
		}}
		request := recoveryOn(t, views[tenant], tenant, group)
		request.OpenAlerts = foreign
		held := evaluateToRecovery(t, request)
		if !held.RecoveryGate.Held || held.RecoveryGate.Cause != RecoveryHeldNoOpenAlert || held.TriggerEvent != nil {
			t.Fatalf("%s: gate = %+v envelope = %v, want the recovery held: only another tenant holds the alert",
				tenant, held.RecoveryGate, held.TriggerEvent != nil)
		}
		if len(foreign.asked) != 1 || foreign.asked[0] != tenant+"/1001/"+pythonNoDataAlertFingerprint {
			t.Fatalf("%s: the gate asked %v, want this tenant's set for Python's no-data fingerprint", tenant, foreign.asked)
		}

		own := &openAlertSetFixture{members: map[string]bool{tenant + "/1001/" + pythonNoDataAlertFingerprint: true}}
		request = recoveryOn(t, views[tenant], tenant, group)
		request.OpenAlerts = own
		released := evaluateToRecovery(t, request)
		if released.RecoveryGate.OpenAlertGate != OpenAlertGatePassed || released.TriggerEvent == nil {
			t.Fatalf("%s: gate = %+v, want the recovery released by this tenant's own open alert", tenant, released.RecoveryGate)
		}
		event := released.TriggerEvent
		if event.TenantID != tenant || event.DedupeMD5 != pythonNoDataAlertFingerprint {
			t.Fatalf("%s: the recovery is tenant %q fingerprint %q, want %q and Python's %q",
				tenant, event.TenantID, event.DedupeMD5, tenant, pythonNoDataAlertFingerprint)
		}
		converter, err := linkdoutput.NewConverter(nil)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := converter.Convert(event)
		if err != nil {
			t.Fatal(err)
		}
		var message struct {
			TenantID string `json:"bk_tenant_id"`
			AlertID  string `json:"alert_id"`
		}
		if err := json.Unmarshal(wire.Payload, &message); err != nil {
			t.Fatal(err)
		}
		if message.TenantID != tenant || message.AlertID != pythonNoDataAlertFingerprint {
			t.Fatalf("%s: the message says tenant %q alert %q, want %q and %q",
				tenant, message.TenantID, message.AlertID, tenant, pythonNoDataAlertFingerprint)
		}
	}
}

// One series' no-data alert and threshold alert are two alerts, and each is
// the one Python files: the tag is what moves the fingerprint (nodata-capability-
// decomposition, section 5.5 R9; decision-007 section 4).
//
// Both fingerprints come out of the trigger from the same compiled Plan: the
// threshold decision through the Plan, the no-data decision through its no-data
// view, over the same host. Neither is written into the event by the case.
func TestTheNoDataAndThresholdAlertsOfOneSeriesCarryPythonsTwoFingerprints(t *testing.T) {
	plan := identityPlan(t, "tenant-a")
	for _, test := range []struct {
		name       string
		plan       *strategy.CompiledPlan
		dimensions map[string]json.RawMessage
		want       string
	}{
		{"the threshold alert", plan, map[string]json.RawMessage{"host": json.RawMessage(`"192.0.2.40"`)},
			pythonThresholdAlertFingerprint},
		{"the no-data alert", plan.NoDataView(), map[string]json.RawMessage{
			"host": json.RawMessage(`"192.0.2.40"`), contract.NoDataDimensionTag: json.RawMessage(`true`)},
			pythonNoDataAlertFingerprint},
	} {
		request := recoveryOn(t, test.plan, "tenant-a", test.dimensions)
		request.OpenAlerts = &openAlertSetFixture{members: map[string]bool{"tenant-a/1001/" + test.want: true}}
		result := evaluateToRecovery(t, request)
		if result.TriggerEvent == nil || result.TriggerEvent.DedupeMD5 != test.want {
			t.Fatalf("%s: gate %+v event %+v, want it released under Python's fingerprint %s",
				test.name, result.RecoveryGate, result.TriggerEvent, test.want)
		}
	}
	if pythonNoDataAlertFingerprint == pythonThresholdAlertFingerprint {
		t.Fatal("the two Python fingerprints are equal, so this case could not tell the alerts apart")
	}
}
