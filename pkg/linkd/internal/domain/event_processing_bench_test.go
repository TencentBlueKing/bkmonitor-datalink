// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package domain_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"linkd/internal/domain"
)

// BenchmarkEventUpdates 只测一次丰富提交与一次关联提交，不包含外部 I/O。
func BenchmarkEventUpdates(b *testing.B) {
	event := validEvent()
	event.Evaluations = []domain.EventEvaluation{{Severity: "warning", Action: domain.EventActionTriggered}}
	result := completedEventEnrichment()
	patches := make([]domain.EnrichPatch, 64)
	for i := range patches {
		patches[i] = domain.EnrichPatch{Op: "set", Path: fmt.Sprintf("$.extra_data.field_%d", i), Value: json.RawMessage(`{"name":"sample","values":[1,2,3],"nested":{"enabled":true}}`)}
	}
	raw, err := json.Marshal([]map[string]any{{"resource": map[string]any{"status": "succeeded", "patches": patches}}})
	if err != nil {
		b.Fatal(err)
	}
	result.Enrich.Evaluations[0].Data["processors"] = raw
	event.SourceRawData = domain.JSONObject{"payload": raw}
	b.ReportAllocs()
	for b.Loop() {
		enriched, err := event.WithEnrichment(result)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := enriched.WithRelatedAlertIDs([]string{"alert-1"}); err != nil {
			b.Fatal(err)
		}
	}
}
