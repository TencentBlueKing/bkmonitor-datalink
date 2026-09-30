// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package evaluation

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Under the Python-compatible protocol a series keeps only the identity of a
// RECOVERY it decided: that event was held for the whole Slot and dropped at
// the sink. The standard protocol's RECOVERY, which the consumer receives, is
// kept whole - the native control - and so is every anomaly.
func TestASeriesKeepsOnlyTheIdentityOfAnEventTheSinkWouldDrop(t *testing.T) {
	legacy := &contract.LegacyEventContext{Configuration: &contract.FrozenLegacyOutput{}}
	record := contract.TriggerRecordRefV1{RecordID: "r-1", SourceTime: 1_700_000_000}

	pythonRecovery := &contract.TriggerEventV1{WireFormat: contract.WireFormatPythonCompatible,
		EventKind: contract.TriggerEventRecovery, LegacyOutput: legacy, RecordRef: record}
	events, dropped := keptEvents(pythonRecovery)
	if len(events) != 0 || len(dropped) != 1 {
		t.Fatalf("python RECOVERY kept %d events and %d identities, want its identity alone", len(events), len(dropped))
	}
	want := execution.EventWithoutMessage{Record: execution.RecordAnchor{RecordID: "r-1", SourceTime: 1_700_000_000},
		EventKind: contract.TriggerEventRecovery, Format: contract.WireFormatPythonCompatible}
	if dropped[0] != want {
		t.Fatalf("identity = %+v, want %+v", dropped[0], want)
	}

	for name, event := range map[string]*contract.TriggerEventV1{
		"native RECOVERY": {WireFormat: contract.WireFormatStandardRawEvent, EventKind: contract.TriggerEventRecovery, RecordRef: record},
		"python anomaly":  {WireFormat: contract.WireFormatPythonCompatible, EventKind: contract.TriggerEventAbnormal, LegacyOutput: legacy, RecordRef: record},
	} {
		events, dropped := keptEvents(event)
		if len(events) != 1 || len(dropped) != 0 || events[0].EventKind != event.EventKind {
			t.Errorf("%s kept %d events and %d identities, want the event whole", name, len(events), len(dropped))
		}
	}
	if events, dropped := keptEvents(nil); events != nil || dropped != nil {
		t.Errorf("no event kept %v and %v, want nothing", events, dropped)
	}
}

// A series of several records carries every record's kept outputs, events and
// identities alike: the second record's identity is as much the series' as the
// first record's event.
func TestASeriesOfSeveralRecordsCarriesEveryRecordsIdentities(t *testing.T) {
	first := &execution.StateEvaluation{Events: []contract.TriggerEventV1{{EventKind: contract.TriggerEventAbnormal}}}
	second := &execution.StateEvaluation{WithoutMessage: []execution.EventWithoutMessage{{EventKind: contract.TriggerEventRecovery,
		Format: contract.WireFormatPythonCompatible}}}
	events, dropped := appendKept(nil, nil, first)
	events, dropped = appendKept(events, dropped, second)
	if len(events) != 1 || len(dropped) != 1 || dropped[0].EventKind != contract.TriggerEventRecovery {
		t.Fatalf("series carries %d events and %d identities (%+v), want one of each", len(events), len(dropped), dropped)
	}
}
