// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package assembly

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/view"
	"linkd/internal/onemodel"
)

type eventQueryReader struct{ calls atomic.Int32 }

func (r *eventQueryReader) Search(_ context.Context, tenant string, _ onemodel.Query) ([]onemodel.Instance, error) {
	r.calls.Add(1)
	return []onemodel.Instance{{TenantID: tenant, ModelCode: "host", InstanceID: "1", Attributes: map[string]any{"owner": "alice"}}}, nil
}

func (*eventQueryReader) Related(context.Context, string, []onemodel.Instance, string, string, onemodel.Query) ([]onemodel.Instance, error) {
	return nil, nil
}

func TestEventEvaluationsShareQueriesButKeepSeparateResults(t *testing.T) {
	var source config.EventSource
	err := json.Unmarshal([]byte(`{"event_source_id":"host","enrich":{"processors":[{"type":"cmdb","config":{"rules":[{"id":"host","lookup":{"model_id":"host","where":{"field":"model_inst_id","type":"keyword","operator":"eq","value":{"literal":"1"}}},"assignments":[{"target":"$.labels.owner","value":{"jsonpath":"$.lookup.attributes.owner"}}]}]}},{"type":"fields","config":{"rules":[{"id":"grade","operations":[{"id":"title","type":"assign","assignments":[{"target":"$.title","value":{"template":"${grade}:${owner}","variables":{"grade":{"jsonpath":"$.evaluation.severity"},"owner":{"jsonpath":"$.event.labels.owner"}}}}]}]}]}}]}}`), &source)
	if err != nil {
		t.Fatal(err)
	}
	reader := &eventQueryReader{}
	router, err := NewRouter([]config.EventSource{source}, enrich.Sources{CMDB: reader})
	if err != nil {
		t.Fatal(err)
	}
	event := baseCollectAlert("host")
	event.Evaluations = append(event.Evaluations, domain.EventEvaluation{Severity: "critical", Action: domain.EventActionResolved})
	for range 2 {
		result, err := router.Enrich(t.Context(), enrich.Input{Event: event})
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != domain.EnrichStatusSucceeded || len(result.Data.Evaluations) != 2 || result.ConfigDigest == "" {
			t.Fatalf("result: %+v", result)
		}
		enriched, err := event.WithEnrichment(domain.EventEnrichment{EnrichStatus: result.Status, Enrich: result.Data, EnrichedAt: &event.CreateAt, EnrichConfigDigest: result.ConfigDigest})
		if err != nil {
			t.Fatal(err)
		}
		for _, severity := range []string{"warning", "critical"} {
			effective, err := view.EnrichedEvent(enriched, severity)
			if err != nil {
				t.Fatal(err)
			}
			if effective.Title != severity+":alice" || event.Title != "CPU high" {
				t.Fatalf("grade %s title=%s", severity, effective.Title)
			}
		}
	}
	if reader.calls.Load() != 2 {
		t.Fatalf("must query once per Event, not once per grade or across Events: %d", reader.calls.Load())
	}
}
