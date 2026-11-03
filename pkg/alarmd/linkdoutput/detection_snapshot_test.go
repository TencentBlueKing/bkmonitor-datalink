// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package linkdoutput

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

const namedSnapshot = `{"version":1,"ref_values":{"A":{"value":1,"state":"SUCCESS"},"C":{"value":92.5,"state":"SUCCESS"}},` +
	`"levels":[{"level":2,"algorithms":[{"type":"Threshold","facts":{"value":92.500000,"group":0,"method":"gte","threshold":90.000000}}],` +
	`"window":{"required":2,"observed":3,"size":5},"anomaly_begin_time":1756684680,"unit":"%"}]}`

// A decision of a strategy with named outputs carries its detection
// snapshot in extra_data as the event carries it, byte for byte, beside
// everything extra_data already says; the same decision without one writes
// the message it wrote before, with no snapshot key at all.
func TestTheDetectionSnapshotRidesInExtraDataUnchanged(t *testing.T) {
	plain := convertRaw(t, decision(nil))
	named := convertRaw(t, decision(func(event *contract.TriggerEventV1) {
		event.DetectionSnapshot = json.RawMessage(namedSnapshot)
	}))
	if err := checkStandardPayload(named.Payload); err != nil {
		t.Fatalf("the consumer's cleaner would refuse the message with its snapshot: %v", err)
	}
	var message struct {
		ExtraData map[string]json.RawMessage `json:"extra_data"`
	}
	if err := json.Unmarshal(named.Payload, &message); err != nil {
		t.Fatal(err)
	}
	if got := string(message.ExtraData["detection_snapshot"]); got != namedSnapshot {
		t.Fatalf("extra_data.detection_snapshot = %s, want the event's", got)
	}
	if strings.Contains(string(plain.Payload), "detection_snapshot") {
		t.Fatalf("a message without a snapshot names one: %s", plain.Payload)
	}
	without := bytes.Replace(named.Payload, []byte(`,"detection_snapshot":`+namedSnapshot), nil, 1)
	if !bytes.Equal(without, plain.Payload) {
		t.Fatalf("the snapshot changed the rest of the message:\n%s\n%s", without, plain.Payload)
	}
}
