// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import "testing"

// An event is dropped at the sink only when its resolved protocol has no
// message for its kind and nothing about it would make the sink refuse it
// first. Every format the sink resolves to the standard protocol - named, the
// older name for it, or empty on a revisioned strategy - carries every kind.
func TestOnlyAnEventTheSinkWouldSilentlyDropIsReportedDropped(t *testing.T) {
	legacy := &LegacyEventContext{Configuration: &FrozenLegacyOutput{}}
	revisioned := &StrategySnapshotRef{Revision: 7}
	for _, testCase := range []struct {
		name    string
		event   TriggerEventV1
		dropped bool
	}{
		{"python recovery", TriggerEventV1{WireFormat: WireFormatPythonCompatible, EventKind: TriggerEventRecovery, LegacyOutput: legacy}, true},
		{"python anomaly", TriggerEventV1{WireFormat: WireFormatPythonCompatible, EventKind: TriggerEventAbnormal, LegacyOutput: legacy}, false},
		{"python recovery the sink would refuse", TriggerEventV1{WireFormat: WireFormatPythonCompatible, EventKind: TriggerEventRecovery}, false},
		{"standard recovery", TriggerEventV1{WireFormat: WireFormatStandardRawEvent, EventKind: TriggerEventRecovery}, false},
		{"older name for standard", TriggerEventV1{WireFormat: WireFormatTriggerEvent, EventKind: TriggerEventRecovery, LegacyOutput: legacy}, false},
		{"empty on a revisioned strategy", TriggerEventV1{EventKind: TriggerEventRecovery, StrategyRef: revisioned, LegacyOutput: legacy}, false},
		{"empty on an unrevisioned strategy", TriggerEventV1{EventKind: TriggerEventRecovery, LegacyOutput: legacy}, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			event := testCase.event
			if got := DroppedAtSink(&event); got != testCase.dropped {
				t.Fatalf("DroppedAtSink = %v, want %v (resolved format %q)", got, testCase.dropped, OutputWireFormatOf(&event))
			}
		})
	}
	if DroppedAtSink(nil) {
		t.Fatal("no event reported dropped")
	}
}
