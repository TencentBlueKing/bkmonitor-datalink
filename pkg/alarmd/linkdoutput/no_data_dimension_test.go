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

// The no-data tag reaches the native wire.
//
// It reaches it through a projection that drops most dimensions: the target
// fields are consumed into the subject, and anything the item does not
// aggregate on is left behind. The tag survives by an explicit exception, and
// an exception is exactly the kind of thing a later tidy-up removes -- the
// condition reads like a special case for a string, and the reason it is there
// is that a no-data alert with no tag is indistinguishable on the wire from a
// threshold alert on the same series.
//
// The subject here is not written by hand. It comes from the same projection
// the evaluator calls, so this asserts the path rather than a fixture that was
// built to agree with it.
func TestTheNoDataTagReachesTheNativeWire(t *testing.T) {
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
	// And the projection did what it does to everything else, so this is the
	// real shape and not one where nothing was dropped.
	if _, kept := dimensions["bk_target_ip"]; kept {
		t.Fatalf("dimensions = %v: the target field was not consumed into the subject, so this fixture "+
			"is not exercising the projection that drops things", dimensions)
	}
}
