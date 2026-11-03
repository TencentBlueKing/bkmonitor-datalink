// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func emittedEvent(id, kind string, slot int64, noData bool) contract.TriggerEventV1 {
	dimensions := map[string]json.RawMessage{"bk_target_ip": json.RawMessage(`"192.0.2.` + id + `"`)}
	if noData {
		dimensions[contract.NoDataDimensionTag] = json.RawMessage("true")
	}
	return contract.TriggerEventV1{EventID: "event-" + id, EventKind: kind, EvaluationTime: slot, DedupeMD5: "dedupe-" + id,
		RecordRef: contract.TriggerRecordRefV1{Dimensions: dimensions}}
}

// A write's no-data events are read off the write's own answer: only tagged
// events count; an acknowledged write counts by kind and keeps the latest of
// each with the key the alert store files it under; on a partial write only
// the events it names are known unwritten; a whole batch with its
// acknowledgement unknown is counted apart, not as lost; any other failed
// batch is known unwritten; and a write with no no-data event says nothing.
func TestAWritesNoDataEventsAreCountedByWhatTheWriteAnswered(t *testing.T) {
	// The later ABNORMAL first: the latest is the one its Slot decided last,
	// not the last one in the batch.
	batch := []contract.TriggerEventV1{
		emittedEvent("13", contract.TriggerEventAbnormal, 660, true),
		emittedEvent("11", contract.TriggerEventAbnormal, 600, false),
		emittedEvent("12", contract.TriggerEventRecovery, 600, true),
		emittedEvent("10", contract.TriggerEventAbnormal, 600, true),
	}
	facts := noDataEmissionOf(batch, nil)
	if facts == nil || facts.AbnormalSent != 2 || facts.RecoverySent != 1 || facts.AckUnknown != 0 || facts.NotWritten != 0 {
		t.Fatalf("acknowledged write = %+v, want two no-data ABNORMAL and one RECOVERY, the threshold event left out", facts)
	}
	if facts.LastAbnormal == nil || facts.LastAbnormal.EvaluationTime != 660 || facts.LastAbnormal.AlertKey != "dedupe-13" ||
		facts.LastAbnormal.Group["bk_target_ip"] != "192.0.2.13" || facts.LastRecovery == nil || facts.LastRecovery.AlertKey != "dedupe-12" {
		t.Fatalf("latest = %+v / %+v, want the later ABNORMAL and the RECOVERY with their keys and groups", facts.LastAbnormal, facts.LastRecovery)
	}
	partial := noDataEmissionOf(batch, &partialEventError{notWritten: []string{"event-13"}})
	if partial == nil || partial.AbnormalSent != 1 || partial.RecoverySent != 1 || partial.NotWritten != 1 || partial.AckUnknown != 0 ||
		partial.LastAbnormal == nil || partial.LastAbnormal.AlertKey != "dedupe-10" {
		t.Fatalf("partial write = %+v, want the named event known unwritten and the rest acknowledged", partial)
	}
	unknown := noDataEmissionOf(batch, retryableOutputError{})
	if unknown == nil || unknown.AckUnknown != 3 || unknown.NotWritten != 0 || unknown.AbnormalSent != 0 || unknown.LastAbnormal != nil {
		t.Fatalf("acknowledgement unknown = %+v, want all three counted apart and none as sent or lost", unknown)
	}
	refused := noDataEmissionOf(batch, errors.New("refused"))
	if refused == nil || refused.NotWritten != 3 || refused.AckUnknown != 0 || refused.AbnormalSent != 0 {
		t.Fatalf("refused batch = %+v, want all three known unwritten", refused)
	}
	if none := noDataEmissionOf(batch[1:2], nil); none != nil {
		t.Fatalf("a write without no-data events = %+v, want nothing", none)
	}
}

// eventSink takes every batch.
type eventSink struct{ execution.EventSink }

func (eventSink) WriteBatch(context.Context, []contract.TriggerEventV1) error { return nil }

// The write's own observation carries what it did with its no-data events.
func TestTheWriteCarriesItsNoDataEventsOnItsObservation(t *testing.T) {
	var acked *observability.Observation
	coordinator := &SlotExecutionCoordinator{ports: Ports{Events: eventSink{},
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			if observation.Stage == observability.StageEventACKed {
				acked = &observation
			}
		})}}
	plan := execution.PlanIdentity{TenantID: "system", BusinessID: "2", StrategyID: "4101"}
	if err := coordinator.writeEvents(context.Background(), execution.OperationNormal, plan,
		[]contract.TriggerEventV1{emittedEvent("10", contract.TriggerEventAbnormal, 600, true)}, nil); err != nil {
		t.Fatal(err)
	}
	if acked == nil || acked.NoDataEmission == nil || acked.NoDataEmission.AbnormalSent != 1 {
		t.Fatalf("event_acked = %+v, want it to carry the no-data ABNORMAL it wrote", acked)
	}
}

// Under the compatibility protocol the key a reader finds the alert by is
// the one the message is written under: the dedupe md5 of the group's
// dimensions and the tag, which Python's alert builder files the alert
// under. It is not the event's own dedupe_md5, which is empty for a Plan
// with no frozen snapshot revision and, when it is set, hashes the Plan's
// identity fields rather than the group's. Expected keys are Python's
// (event.py cal_dedupe_md5 over adapter.py extract_target, bk-monitor
// c0e828fa8c), for strategy 41 in business 2.
func TestACompatibleNoDataEventIsKeyedByWhatItsMessageCarries(t *testing.T) {
	for _, tc := range []struct {
		name       string
		dimensions map[string]json.RawMessage
		dedupe     string
		want       string
	}{
		{"no_snapshot_revision", map[string]json.RawMessage{"path": json.RawMessage(`"/data"`)}, "", "6be6ecb2f642dcd62da2dfc6fdd1e918"},
		{"identity_md5_set", map[string]json.RawMessage{"bk_target_ip": json.RawMessage(`"127.0.0.1"`), "bk_target_cloud_id": json.RawMessage(`"0"`)},
			"0123456789abcdef0123456789abcdef", "2bfe71eec3bb4c3bd4331aeeb2bee3f3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.dimensions[contract.NoDataDimensionTag] = json.RawMessage("true")
			event := contract.TriggerEventV1{EventID: "event-1", EventKind: contract.TriggerEventAbnormal, EvaluationTime: 600,
				WireFormat: contract.WireFormatPythonCompatible, BusinessID: "2", PlanRef: contract.RuntimePlanRefV1{StrategyID: "41"},
				DedupeMD5: tc.dedupe, RecordRef: contract.TriggerRecordRefV1{Dimensions: tc.dimensions}}
			facts := noDataEmissionOf([]contract.TriggerEventV1{event}, nil)
			if facts == nil || facts.LastAbnormal == nil {
				t.Fatalf("facts = %+v, want the ABNORMAL kept as the latest", facts)
			}
			if got := facts.LastAbnormal.AlertKey; got != tc.want {
				t.Fatalf("alert key = %q, want %s", got, tc.want)
			}
		})
	}
}
