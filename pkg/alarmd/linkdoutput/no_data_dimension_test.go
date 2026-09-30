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
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The no-data tag reaches the wire, and so does everything beside it.
//
// The dimensions written are the record's own, so nothing has to survive a
// projection to get here -- which is the change this protocol made, and the
// reason this test now checks the opposite of what it used to: the target
// fields are expected to be present rather than consumed.
//
// It still runs the real projection, and this fixture is the case that shows
// why the change was needed: an item aggregating on device alone consumes the
// target fields out of the dimensions and produces no subject, because the
// host pair is not part of its identity. Under the previous protocol the
// message then named no object anywhere -- not in the dimensions, which had
// lost them, and not in the subject, which was empty.
func TestTheNoDataTagAndTheTargetIdentityBothReachTheWire(t *testing.T) {
	recordDimensions := map[string]json.RawMessage{
		"bk_target_ip":              json.RawMessage(`"127.0.0.1"`),
		"bk_target_cloud_id":        json.RawMessage(`"0"`),
		"device":                    json.RawMessage(`"sda"`),
		contract.NoDataDimensionTag: json.RawMessage("true"),
	}
	subject, remaining, err := contract.ProjectMonitorSubject(recordDimensions,
		contract.MonitorOutputIdentity{DimensionFields: []string{"device"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The projection consumes the target fields, which is what it is for. That
	// is exactly why the wire takes the record's dimensions instead: what the
	// consumer fingerprints must not be the leftovers.
	if _, kept := remaining["bk_target_ip"]; kept {
		t.Fatalf("remaining = %v: this fixture is not exercising a projection that consumes the target", remaining)
	}

	message := convert(t, decision(func(event *contract.TriggerEventV1) {
		event.Subject = &contract.MonitorSubjectContext{Subject: subject, Dimensions: remaining}
		event.RecordRef.Dimensions = recordDimensions
	}))

	var dimensions map[string]json.RawMessage
	if err := json.Unmarshal(message["dimensions"], &dimensions); err != nil {
		t.Fatal(err)
	}
	tag, present := dimensions[contract.NoDataDimensionTag]
	if !present {
		t.Fatalf("dimensions = %v, want the no-data tag. Without it a no-data alert and a threshold "+
			"alert on the same series are the same object on this protocol", dimensions)
	}
	if string(tag) != "true" {
		t.Fatalf("tag = %s, want the boolean true", tag)
	}
	for _, field := range []string{"bk_target_ip", "bk_target_cloud_id", "device"} {
		if _, kept := dimensions[field]; !kept {
			t.Fatalf("dimensions = %v, want %s: the consumer fingerprints on these", dimensions, field)
		}
	}
	// No subject here, and that is the point: the projection had no object to
	// name. The dimensions are the only place this event says which host it is
	// about, which is why they are written whole.
	if _, written := message["subject"]; written {
		t.Fatalf("subject = %s: this fixture's projection produced none", message["subject"])
	}
}
