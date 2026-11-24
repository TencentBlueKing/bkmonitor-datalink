// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodata_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
)

// The contract alarmd's closes select no-data alerts by (filterclose): a
// no-data event, from the dimensions the no-data series itself produces,
// reaches the link through the link converter with the tag as the JSON
// boolean true (the link keeps it; exists, term "true" and term true match
// it, "1" and 1 do not) and evaluation_family no_data; a threshold event
// carries no tag and evaluation_family metric_algorithm. Both come from
// the one test of the tag, so on alarmd's own source the tag's presence
// and the family never disagree - which is what lets a bucket filter tell
// the family by exists on the tag (extra_data is not indexed at the link).
func TestTheNoDataTagReachesTheLinkAsTrueAndAgreesWithTheFamily(t *testing.T) {
	group, ok := nodata.Project(map[string]string{"bk_target_ip": "192.0.2.1", "bk_target_cloud_id": "0"},
		[]string{"bk_target_ip", "bk_target_cloud_id"})
	if !ok {
		t.Fatal("no group")
	}
	series := nodata.SyntheticSeries{Group: group, Value: nodata.AbsentValue, Periods: 3}
	threshold := map[string]json.RawMessage{"bk_target_ip": json.RawMessage(`"192.0.2.1"`), "bk_target_cloud_id": json.RawMessage(`"0"`)}
	converter, err := linkdoutput.NewConverter(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		dimensions map[string]json.RawMessage
		observed   map[string]json.RawMessage
		tag        string
		family     string
	}{
		{"no-data", series.IdentityFields(), map[string]json.RawMessage{contract.NoDataPeriodFactField: json.RawMessage("3")}, "true", "no_data"},
		{"threshold", threshold, map[string]json.RawMessage{"value": json.RawMessage("92.5")}, "", "metric_algorithm"},
	} {
		event := &contract.TriggerEventV1{
			EventID: strings.Repeat("a", 64), EventKind: contract.TriggerEventAbnormal, EventSemanticDigest: strings.Repeat("c", 64),
			PrimaryLevelID: 2, TenantID: "tenant-a", BusinessID: "2", SignalType: contract.SignalTypeMetric,
			StrategyRef: &contract.StrategySnapshotRef{TenantID: "tenant-a", BusinessID: 2, StrategyID: 123, Revision: 7},
			DedupeMD5:   strings.Repeat("b", 32), EvaluationTime: 1756684860,
			RecordRef: contract.TriggerRecordRefV1{SourceTime: 1756684800, Dimensions: tc.dimensions},
			Observed:  contract.TriggerObservedV1{Values: tc.observed},
			LevelResults: []contract.LevelResultV1{{LevelID: 2, Priority: 1, Result: contract.LevelResultAbnormal,
				DecisionWindow: contract.DecisionWindowV1{Trigger: contract.TriggerWindowEvidenceV1{WindowSize: 5, RequiredAnomalies: 2,
					ObservedAnomalies: 3}}}},
		}
		written, err := converter.Convert(event)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		var message struct {
			Dimensions map[string]json.RawMessage `json:"dimensions"`
			ExtraData  struct {
				EvaluationFamily string `json:"evaluation_family"`
			} `json:"extra_data"`
		}
		if err := json.Unmarshal(written.Payload, &message); err != nil {
			t.Fatal(err)
		}
		if tag := string(message.Dimensions[contract.NoDataDimensionTag]); tag != tc.tag || message.ExtraData.EvaluationFamily != tc.family {
			t.Fatalf("%s: tag %q family %q, want %q and %q", tc.name, tag, message.ExtraData.EvaluationFamily, tc.tag, tc.family)
		}
	}
}
