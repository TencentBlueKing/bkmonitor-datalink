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
	"testing"

	"linkd/internal/domain"
)

func TestEffectiveAlertReplaysLegacyAndPatchesInOrder(t *testing.T) {
	alert := domain.Alert{Title: "original", Labels: domain.DimensionMap{}, ExtraData: domain.JSONObject{}, Enrich: domain.JSONObject{"processors": json.RawMessage(`[{"display":{"status":"succeeded","value":{"title":"legacy"}}},{"fields":{"status":"partial","patches":[{"op":"set","path":"$.title","value":"custom"},{"op":"set","path":"$.labels.strategy_id","value":9001}]}},{"ignored":{"status":"failed","patches":[]}}]`)}}
	got, err := EnrichedAlert(alert)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "custom" || alert.Title != "original" {
		t.Fatalf("got=%s source=%s", got.Title, alert.Title)
	}
	id, _ := got.Labels["strategy_id"].NumberValue()
	if id != 9001 {
		t.Fatalf("id=%v", id)
	}
}
