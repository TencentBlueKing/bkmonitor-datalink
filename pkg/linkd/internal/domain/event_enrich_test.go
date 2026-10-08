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
	"reflect"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
)

func completedEventEnrichment() domain.EventEnrichment {
	at := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	return domain.EventEnrichment{
		EnrichStatus: domain.EnrichStatusSucceeded, EnrichedAt: &at, EnrichConfigDigest: "digest",
		Enrich: domain.EventEnrichData{Evaluations: []domain.EvaluationEnrich{{Severity: "warning", Status: domain.EnrichStatusSucceeded, Data: domain.JSONObject{"processors": json.RawMessage(`[]`)}}}},
	}
}

func TestEventEnrichmentValidation(t *testing.T) {
	event := validEvent()
	event.Evaluations = []domain.EventEvaluation{{Severity: "warning", Action: domain.EventActionTriggered}}
	for _, tc := range []struct {
		name   string
		change func(*domain.EventEnrichment)
	}{
		{"missing evaluation", func(e *domain.EventEnrichment) { e.Enrich.Evaluations = nil }},
		{"unexpected severity", func(e *domain.EventEnrichment) { e.Enrich.Evaluations[0].Severity = "critical" }},
		{"duplicate severity", func(e *domain.EventEnrichment) {
			e.Enrich.Evaluations = append(e.Enrich.Evaluations, e.Enrich.Evaluations[0])
		}},
		{"pending evaluation", func(e *domain.EventEnrichment) { e.Enrich.Evaluations[0].Status = domain.EnrichStatusPending }},
		{"aggregate mismatch", func(e *domain.EventEnrichment) { e.EnrichStatus = domain.EnrichStatusFailed }},
		{"processor mismatch", func(e *domain.EventEnrichment) { e.Enrich.Evaluations[0].Status = domain.EnrichStatusFailed }},
		{"invalid JSON", func(e *domain.EventEnrichment) { e.Enrich.Evaluations[0].Data["processors"] = json.RawMessage(`!`) }},
		{"missing time", func(e *domain.EventEnrichment) { e.EnrichedAt = nil }},
		{"zero time", func(e *domain.EventEnrichment) { e.EnrichedAt = &time.Time{} }},
		{"missing digest", func(e *domain.EventEnrichment) { e.EnrichConfigDigest = "" }},
		{"oversized digest", func(e *domain.EventEnrichment) { e.EnrichConfigDigest = strings.Repeat("x", 129) }},
		{"unknown status", func(e *domain.EventEnrichment) { e.EnrichStatus = "unknown" }},
		{"pending with data", func(e *domain.EventEnrichment) { e.EnrichStatus = domain.EnrichStatusPending }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := completedEventEnrichment()
			tc.change(&candidate)
			if _, err := event.WithEnrichment(candidate); err == nil {
				t.Fatal("invalid result accepted")
			}
		})
	}
	for _, status := range []domain.EnrichStatus{domain.EnrichStatusSucceeded, domain.EnrichStatusPartial, domain.EnrichStatusFailed, domain.EnrichStatusSkipped} {
		t.Run(string(status), func(t *testing.T) {
			result := completedEventEnrichment()
			result.EnrichStatus = status
			result.Enrich.Evaluations[0].Status = status
			if status != domain.EnrichStatusSucceeded {
				raw, _ := json.Marshal([]map[string]any{{"fields": map[string]any{"status": status, "patches": []domain.EnrichPatch{}}}})
				result.Enrich.Evaluations[0].Data["processors"] = raw
			}
			enriched, err := event.WithEnrichment(result)
			if err != nil {
				t.Fatal(err)
			}
			if err := domain.ValidateNewEvent(enriched); err == nil {
				t.Fatal("source can forge enrichment")
			}
			if _, err := enriched.WithEnrichment(result); err != nil {
				t.Fatal(err)
			}
			changed := result.Clone()
			changed.EnrichConfigDigest = "changed"
			if _, err := enriched.WithEnrichment(changed); err == nil {
				t.Fatal("frozen enrich overwritten")
			}
			if err := domain.ValidateEventReplacement(event, enriched); err == nil {
				t.Fatal("association CAS can change enrichment")
			}
		})
	}
}

func TestEventEnrichmentCloneAndCanonicalOrder(t *testing.T) {
	event := validEvent()
	event.Evaluations = []domain.EventEvaluation{{Severity: "warning", Action: domain.EventActionTriggered}, {Severity: "critical", Action: domain.EventActionResolved}}
	result := completedEventEnrichment()
	other := result.Enrich.Evaluations[0]
	other.Severity = "critical"
	result.Enrich.Evaluations = append(result.Enrich.Evaluations, other)
	result.EnrichedAt = new(result.EnrichedAt.In(time.FixedZone("CST", 8*3600)))
	enriched, err := event.WithEnrichment(result)
	if err != nil {
		t.Fatal(err)
	}
	if enriched.Enrich.Evaluations[0].Severity != "critical" || enriched.EnrichedAt.Location() != time.UTC {
		t.Fatal("not canonical")
	}
	before := enriched.Clone()
	selected, ok := enriched.ForSeverity("critical")
	if !ok {
		t.Fatal("result missing")
	}
	selected.Data["processors"][0] = '!'
	clone := enriched.Clone()
	clone.Enrich.Evaluations[0].Data["processors"][0] = '!'
	*clone.EnrichedAt = time.Time{}
	if !reflect.DeepEqual(before, enriched) {
		t.Fatal("result clone shares memory")
	}
	if _, ok := enriched.ForSeverity("missing"); ok {
		t.Fatal("unknown severity fell back")
	}
}

func TestEventEnrichmentAggregateBudget(t *testing.T) {
	event := validEvent()
	event.Evaluations = nil
	result := completedEventEnrichment()
	result.Enrich.Evaluations = nil
	// 单个合法的 content 补丁组合后仍受 Event 总预算约束，不能按等级放大 32 倍。
	patch, err := domain.NewEnrichPatch("$.content", strings.Repeat("x", 600000))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal([]map[string]any{{"fields": map[string]any{"status": "succeeded", "patches": []domain.EnrichPatch{patch}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, severity := range []string{"critical", "warning"} {
		event.Evaluations = append(event.Evaluations, domain.EventEvaluation{Severity: severity, Action: domain.EventActionTriggered})
		result.Enrich.Evaluations = append(result.Enrich.Evaluations, domain.EvaluationEnrich{Severity: severity, Status: domain.EnrichStatusSucceeded, Data: domain.JSONObject{"processors": raw}})
	}
	if err := result.Validate(event.Evaluations); err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("aggregate budget: %v", err)
	}
}
