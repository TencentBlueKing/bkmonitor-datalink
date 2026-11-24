// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package view

import (
	"encoding/json"
	"reflect"
	"testing"

	"linkd/internal/domain"
)

func TestEnrichedLabelMatchesFullView(t *testing.T) {
	for _, raw := range []string{
		`[]`,
		`[{"strategy":{"status":"succeeded","value":{"strategy_id":"00123","object":{"nested":true}}}}]`,
		`[{"strategy":{"status":"partial","value":{"strategy_id":null}}}]`,
		`[{"strategy":{"status":"succeeded","value":{"strategy_id":[1,2]}}}]`,
		`[{"fields":{"status":"succeeded","patches":[{"op":"set","path":"$.labels.strategy_id","value":123},{"op":"set","path":"$['labels']['strategy_id']","value":"last"}]}},{"ignored":{"status":"failed","patches":[{"op":"set","path":"$.labels.strategy_id","value":"ignored"}]}}]`,
		`[{"strategy":{"status":"succeeded","value":{"strategy_id":"legacy"}}},{"fields":{"status":"partial","patches":[{"op":"set","path":"$[\"labels\"].strategy_id","value":789}]}}]`,
		`[{"fields":{"status":"succeeded","patches":[{"op":"set","path":"$.labels.strategy_id","value":""}]}}]`,
	} {
		t.Run(raw, func(t *testing.T) {
			alert := domain.Alert{Labels: domain.DimensionMap{"strategy_id": domain.NewStringScalar("raw")}, Enrich: domain.JSONObject{"processors": json.RawMessage(raw)}}
			full, err := EnrichedAlert(alert)
			if err != nil {
				t.Fatal(err)
			}
			label, exists, err := EnrichedLabel(alert.Labels, alert.Enrich, "strategy_id")
			if err != nil || !exists || !reflect.DeepEqual(label, full.Labels["strategy_id"]) {
				t.Fatalf("label=%v full=%v error=%v", label, full.Labels, err)
			}
			if original, _ := alert.Labels["strategy_id"].StringValue(); original != "raw" {
				t.Fatal("label extraction changed stored label")
			}
		})
	}
}

func TestEnrichedLabelIgnoresUnrelatedValues(t *testing.T) {
	payload := domain.JSONObject{"processors": json.RawMessage(`[{"fields":{"status":"succeeded","patches":[{"op":"set","path":"$.content","value":123},{"op":"set","path":"$.extra_data.legacy","value":{"old":true}},{"op":"set","path":"$.labels.unrelated","value":{"old":true}},{"op":"set","path":"$.labels.strategy_id","value":"wanted"}]}}]`)}
	label, exists, err := EnrichedLabel(nil, payload, "strategy_id")
	text, _ := label.StringValue()
	if err != nil || !exists || text != "wanted" {
		t.Fatalf("unrelated historical field blocked index: %v", err)
	}
	if _, err := EffectiveEnrichPatches(payload); err == nil {
		t.Fatal("full patch application lost its validation")
	}
	if _, exists, err := EnrichedLabel(nil, nil, "strategy_id"); err != nil || exists {
		t.Fatal("missing label was fabricated")
	}
}

func TestEnrichedLabelRejectsAmbiguousOrInvalidTarget(t *testing.T) {
	for _, raw := range []string{
		`[{"fields":{"status":"succeeded","patches":[{"op":"set","path":"$.labels.strategy_id","value":{}}]}}]`,
		`[{"fields":{"status":"succeeded","patches":[{"op":"set","path":"$.labels.strategy_id","value":null}]}}]`,
		`[{"fields":{"status":"succeeded","patches":[{"op":"remove","path":"$.labels.strategy_id","value":"x"}]}}]`,
		`[{"fields":{"status":"succeeded","patches":[{"op":"set","path":"$.labels","value":{}}]}}]`,
		`[{"fields":{"status":"unknown","patches":[]}}]`,
		`[{"fields":{"status":"succeeded","patches":[]}},{"fields":{"status":"succeeded","patches":[]}}]`,
		`[`,
	} {
		t.Run(raw, func(t *testing.T) {
			if _, _, err := EnrichedLabel(nil, domain.JSONObject{"processors": json.RawMessage(raw)}, "strategy_id"); err == nil {
				t.Fatal("invalid index material was accepted")
			}
		})
	}
}
