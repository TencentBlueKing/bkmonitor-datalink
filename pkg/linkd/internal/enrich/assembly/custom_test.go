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
	"fmt"
	"testing"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/view"
	"linkd/internal/onemodel"
	"linkd/internal/policy"
)

type cmdbReader struct{}

func (cmdbReader) Search(context.Context, string, onemodel.Query) ([]onemodel.Instance, error) {
	return []onemodel.Instance{{TenantID: "t", ModelCode: "cw-Host", InstanceID: "1", Attributes: map[string]any{"operator": "alice"}}}, nil
}

func (cmdbReader) Related(context.Context, string, []onemodel.Instance, string, string, onemodel.Query) ([]onemodel.Instance, error) {
	return nil, nil
}

func TestCMDBThenFieldsShareEffectiveView(t *testing.T) {
	var source config.EventSource
	raw := `{"event_source_id":"host","enrich":{"processors":[{"type":"cmdb","config":{"rules":[{"id":"host","lookup":{"model_id":"cw-Host","where":{"field":"attributes.ip","type":"keyword","operator":"eq","value":{"literal":"10.0.0.1"}}},"assignments":[{"target":"$.labels.owner","value":{"jsonpath":"$.lookup.attributes.operator"}}]}]}},{"type":"fields","config":{"rules":[{"id":"title","operations":[{"id":"set","type":"assign","assignments":[{"target":"$.title","value":{"template":"${title}: ${owner}","variables":{"title":{"jsonpath":"$.original.title"},"owner":{"jsonpath":"$.event.labels.owner"}}}}]}]}]}}]}}`
	if err := json.Unmarshal([]byte(raw), &source); err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter([]config.EventSource{source}, enrich.Sources{CMDB: cmdbReader{}})
	if err != nil {
		t.Fatal(err)
	}
	alert := domain.Event{Evaluations: []domain.EventEvaluation{{Severity: "warning", Action: domain.EventActionTriggered}}, BKTenantID: "t", EventSourceID: "host", Title: "CPU"}
	result, err := router.Enrich(t.Context(), enrich.Input{Event: alert, Preview: true})
	if err != nil {
		t.Fatal(err)
	}
	alert.Enrich = result.Data
	view, err := view.EnrichedEvent(alert, "warning")
	if err != nil {
		t.Fatal(err)
	}
	if view.Title != "CPU: alice" || alert.Title != "CPU" || result.Status != domain.EnrichStatusSucceeded {
		t.Fatalf("title=%s original=%s status=%s", view.Title, alert.Title, result.Status)
	}
	var payload struct {
		Processors []map[string]map[string]json.RawMessage `json:"processors"`
	}
	if err := json.Unmarshal(result.Data.Evaluations[0].Data["processors"], &payload.Processors); err != nil {
		t.Fatal(err)
	}
	for _, entry := range payload.Processors {
		for _, envelope := range entry {
			if _, ok := envelope["value"]; ok {
				t.Fatal("new write persisted value")
			}
			if _, ok := envelope["patches"]; !ok {
				t.Fatal("missing patches")
			}
		}
	}
}

type cmdbGroups struct{ calls int }

func (g *cmdbGroups) GetDynamicGroupIDs(_ context.Context, tenant, model, inst string) ([]string, error) {
	g.calls++
	if tenant != "t" || model != "cw-Host" || inst != "202" {
		return nil, fmt.Errorf("wrong effective identity")
	}
	return []string{"g1"}, nil
}

func TestCMDBBindingFieldsAccessAndPolicyShareEffectiveIdentity(t *testing.T) {
	var source config.EventSource
	raw := `{"event_source_id":"host","enrich":{"processors":[{"type":"cmdb","config":{"rules":[{"id":"host","identity":{"bk_obj_id":"host","model_name":"主机"},"lookup":{"model_id":"cw-Host","expect":"first"}}]}},{"type":"fields","config":{"rules":[{"id":"change","operations":[{"id":"instance","type":"assign","assignments":[{"target":"$.labels.model_inst_id","value":{"literal":"202"}}]}]}]}},{"type":"cmdb-access"}]}}`
	if err := json.Unmarshal([]byte(raw), &source); err != nil {
		t.Fatal(err)
	}
	for _, preview := range []bool{false, true} {
		groups := &cmdbGroups{}
		router, err := NewRouter([]config.EventSource{source}, enrich.Sources{CMDB: cmdbReader{}, DynamicGroup: groups})
		if err != nil {
			t.Fatal(err)
		}
		event := baseCollectAlert("host")
		event.BKTenantID = "t"
		event.ExtraData = domain.JSONObject{"bk_obj_id": json.RawMessage(`"host"`), "bk_inst_id": json.RawMessage(`"101"`)}
		event.Labels = domain.DimensionMap{"model_id": domain.NewStringScalar("cw-Host")}
		result, err := router.Enrich(t.Context(), enrich.Input{Event: event, Preview: preview})
		if err != nil {
			t.Fatal(err)
		}
		event.Enrich = result.Data
		effective, err := view.EnrichedEvent(event, "warning")
		if err != nil {
			t.Fatal(err)
		}
		if string(effective.ExtraData["dynamic_group_id"]) != `["g1"]` || groups.calls != 1 {
			t.Fatal(effective, groups)
		}
		facts, err := policy.EventView(event, "warning", nil, func(v string) (string, error) { return v, nil }, policy.RelationContext{})
		if err != nil {
			t.Fatal(err)
		}
		ref, found, err := facts.CanonicalInstance(t.Context())
		if err != nil || !found || ref.InstanceID != "202" {
			t.Fatal(ref, found, err)
		}
	}
}
