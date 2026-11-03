// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package legacyoutput

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

const namedSnapshot = `{"version":1,"ref_values":{"A":{"value":1,"state":"SUCCESS"},"B":{"state":"MISSING"}},` +
	`"levels":[{"level":1,"algorithms":[{"type":"Threshold","facts":{"value":3.000000}}],"window":{"required":1,"observed":1,"size":1}}]}`

// An anomaly of a strategy with named outputs carries its outputs where the
// platform's record carries them, origin_alarm.data.ref_values, and its whole
// detection snapshot beside the origin alarm; the rest of the payload is the
// one the same anomaly writes without them, which is the one it wrote before.
func TestAnAnomalyWithNamedOutputsCarriesThemWhereThePlatformDoes(t *testing.T) {
	events, now := legacyFixtureAnomalies(t)
	named := events[0]
	named.DetectionSnapshot = json.RawMessage(namedSnapshot)
	converter := Converter{Store: &snapshotRecorder{}, Now: func() time.Time { return now }}
	plain, err := converter.ConvertBatch(context.Background(), events[:1])
	if err != nil {
		t.Fatal(err)
	}
	written, err := converter.ConvertBatch(context.Background(), []contract.TriggerEventV1{named})
	if err != nil {
		t.Fatal(err)
	}
	var withSnapshot, without map[string]any
	if err := json.Unmarshal(written[0].Payload, &withSnapshot); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(plain[0].Payload, &without); err != nil {
		t.Fatal(err)
	}
	origin := withSnapshot["extra_info"].(map[string]any)["origin_alarm"].(map[string]any)
	data := origin["data"].(map[string]any)
	refValues, _ := json.Marshal(data["ref_values"])
	snapshot, _ := json.Marshal(origin["detection_snapshot"])
	if string(refValues) != `{"A":{"state":"SUCCESS","value":1},"B":{"state":"MISSING"}}` {
		t.Fatalf("data.ref_values = %s", refValues)
	}
	var wantSnapshot any
	_ = json.Unmarshal([]byte(namedSnapshot), &wantSnapshot)
	if !reflect.DeepEqual(origin["detection_snapshot"], wantSnapshot) {
		t.Fatalf("origin_alarm.detection_snapshot = %s", snapshot)
	}
	delete(data, "ref_values")
	delete(origin, "detection_snapshot")
	if !reflect.DeepEqual(withSnapshot, without) {
		t.Fatalf("the snapshot changed the rest of the payload:\n%s\n%s", written[0].Payload, plain[0].Payload)
	}
}
