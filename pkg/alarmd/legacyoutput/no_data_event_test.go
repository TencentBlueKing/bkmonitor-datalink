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
	"strconv"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

const noDataStrategyDocument = `{"id":1001,"bk_biz_id":2,"update_time":1725000000,"name":"cpu usage","scenario":"os",
	"items":[{"id":11,"name":"CPU使用率","query_configs":[{"data_source_label":"bk_monitor","data_type_label":"time_series","result_table_id":"system.cpu","metric_field":"usage"}]}]}`

// A synthetic no-data point converts, and it converts into the object the
// backend writes for the same silence.
//
// Every part of this failed before. The point carries no observed value --
// the backend's is None -- so reading one aborted the conversion and no
// no-data event was ever produced at all. The identity was hashed from the
// item's dimension fields without the tag, which is a different object from
// the one the backend hashes, so had the conversion succeeded the alert would
// have looked new on every round and never closed. And the alert said nothing
// about how long the silence had lasted.
func TestASyntheticNoDataPointConvertsIntoTheBackendsObject(t *testing.T) {
	const checkTime = int64(1725000000)
	dimensions := map[string]json.RawMessage{
		"bk_target_ip":              json.RawMessage(`"127.0.0.1"`),
		contract.NoDataDimensionTag: json.RawMessage("true"),
	}
	event := contract.TriggerEventV1{
		EventID: "event-1", TenantID: "tenant", BusinessID: "2",
		PlanRef:        contract.RuntimePlanRefV1{StrategyID: "1001"},
		EventKind:      contract.TriggerEventAbnormal,
		PrimaryLevelID: 2,
		RecordRef:      contract.TriggerRecordRefV1{SourceTime: checkTime, Dimensions: dimensions},
		Observed: contract.TriggerObservedV1{Values: map[string]json.RawMessage{
			"no_data":                      json.RawMessage("1"),
			contract.NoDataPeriodFactField: json.RawMessage("7"),
			// A value under the ordinary name, which today's producer does not
			// write. It is here because this converter is the last thing before
			// the wire and does not control what reaches it: a no-data alert
			// carrying a number would say there is no data and then show one.
			"value": json.RawMessage("42"),
		}},
		LegacyOutput: &contract.LegacyEventContext{
			Configuration: contract.FreezeLegacyOutput(&contract.LegacyOutputContext{
				Strategy:        json.RawMessage(noDataStrategyDocument),
				DimensionFields: []string{"bk_target_ip"},
				ItemID:          "11",
			}),
			AnomalyTimestamps: []int64{checkTime},
		},
	}

	converter := Converter{Store: &snapshotRecorder{}, Now: func() time.Time { return time.Unix(checkTime+30, 0) }}
	got, err := converter.ConvertBatch(context.Background(), []contract.TriggerEventV1{event})
	if err != nil {
		t.Fatalf("ConvertBatch() error = %v; a synthetic point that cannot be converted is a no-data "+
			"alert that is never sent", err)
	}
	if len(got) != 1 {
		t.Fatalf("events = %d, want one", len(got))
	}
	var payload map[string]any
	if err := json.Unmarshal(got[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	data := payload["extra_info"].(map[string]any)["origin_alarm"].(map[string]any)["data"].(map[string]any)

	// The identity the backend writes: count_md5 over the group's dimensions
	// with the tag, not over the item's identity fields.
	wantMD5, err := contract.PythonObjectMD5(dimensions)
	if err != nil {
		t.Fatal(err)
	}
	if got := data["record_id"]; got != wantMD5+"."+strconv.FormatInt(checkTime, 10) {
		t.Fatalf("record_id = %v, want the group-with-tag identity %s at the checked period. An identity "+
			"hashed without the tag matches nothing the backend ever wrote, so the alert looks new every "+
			"round and never closes", got, wantMD5)
	}
	anomaly := payload["extra_info"].(map[string]any)["origin_alarm"].(map[string]any)["anomaly"].(map[string]any)["2"].(map[string]any)
	wantAnomalyID := wantMD5 + "." + strconv.FormatInt(checkTime, 10) + ".1001.11.2"
	if anomaly["anomaly_id"] != wantAnomalyID {
		t.Fatalf("anomaly_id = %v, want %s", anomaly["anomaly_id"], wantAnomalyID)
	}

	// No value, and the backend's fixed observed shape in its place.
	if value, present := data["value"]; !present || value != nil {
		t.Fatalf("value = %#v (present=%t), want null: the point has no observed value and the backend "+
			"writes None", value, present)
	}
	wantValues := map[string]any{"timestamp": float64(checkTime), "loads": nil}
	if !reflect.DeepEqual(data["values"], wantValues) {
		t.Fatalf("values = %#v, want %#v", data["values"], wantValues)
	}

	// The tag is in the dimensions and in the field list, which is what the
	// alert side reads to know this is a no-data alert at all.
	fields, _ := data["dimension_fields"].([]any)
	if !reflect.DeepEqual(fields, []any{contract.NoDataDimensionTag, "bk_target_ip"}) {
		t.Fatalf("dimension_fields = %#v, want the group's fields with the tag, in name order", fields)
	}
	recordDimensions, _ := data["dimensions"].(map[string]any)
	if tag, ok := recordDimensions[contract.NoDataDimensionTag]; !ok || tag != true {
		t.Fatalf("dimensions = %#v, want the tag as a boolean true", recordDimensions)
	}

	// And the alert says how long the silence has lasted.
	const wantMessage = "当前指标(CPU使用率)已经有7个周期无数据上报"
	if anomaly["anomaly_message"] != wantMessage {
		t.Fatalf("anomaly_message = %v, want %q", anomaly["anomaly_message"], wantMessage)
	}
	if payload["alert_name"] != "[无数据] cpu usage" {
		t.Fatalf("alert_name = %v, want the no-data prefix", payload["alert_name"])
	}
}

// A point with no period count still says something, and says one period.
//
// The count travels with the point, and a point that somehow arrives without
// it is not a reason to abort the conversion: an alert that says "one period"
// is wrong by a number, an alert that is never sent is wrong by everything.
func TestANoDataPointWithoutItsCountStillConverts(t *testing.T) {
	const checkTime = int64(1725000000)
	dimensions := map[string]json.RawMessage{contract.NoDataDimensionTag: json.RawMessage("true")}
	event := contract.TriggerEventV1{
		EventID: "event-2", TenantID: "tenant", BusinessID: "2",
		PlanRef:        contract.RuntimePlanRefV1{StrategyID: "1001"},
		EventKind:      contract.TriggerEventAbnormal,
		PrimaryLevelID: 2,
		RecordRef:      contract.TriggerRecordRefV1{SourceTime: checkTime, Dimensions: dimensions},
		Observed:       contract.TriggerObservedV1{Values: map[string]json.RawMessage{"no_data": json.RawMessage("1")}},
		LegacyOutput: &contract.LegacyEventContext{
			Configuration: contract.FreezeLegacyOutput(&contract.LegacyOutputContext{
				Strategy: json.RawMessage(noDataStrategyDocument), DimensionFields: []string{}, ItemID: "11",
			}),
			AnomalyTimestamps: []int64{checkTime},
		},
	}

	converter := Converter{Store: &snapshotRecorder{}, Now: func() time.Time { return time.Unix(checkTime+30, 0) }}
	got, err := converter.ConvertBatch(context.Background(), []contract.TriggerEventV1{event})
	if err != nil || len(got) != 1 {
		t.Fatalf("ConvertBatch() = (%d events, %v)", len(got), err)
	}
	var payload map[string]any
	if err := json.Unmarshal(got[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	anomaly := payload["extra_info"].(map[string]any)["origin_alarm"].(map[string]any)["anomaly"].(map[string]any)["2"].(map[string]any)
	if anomaly["anomaly_message"] != "当前指标(CPU使用率)已经有1个周期无数据上报" {
		t.Fatalf("anomaly_message = %v", anomaly["anomaly_message"])
	}

	// The whole-item group's identity is the tag alone, which is the object
	// the backend writes when an item has no data at all.
	wantMD5, err := contract.PythonObjectMD5(dimensions)
	if err != nil {
		t.Fatal(err)
	}
	data := payload["extra_info"].(map[string]any)["origin_alarm"].(map[string]any)["data"].(map[string]any)
	if got := data["record_id"]; got != wantMD5+"."+strconv.FormatInt(checkTime, 10) {
		t.Fatalf("record_id = %v, want the whole-item identity %s", got, wantMD5)
	}
}
