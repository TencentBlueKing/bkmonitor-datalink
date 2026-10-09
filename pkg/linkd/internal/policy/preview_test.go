// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package policy

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store/storetest"
)

func TestDependencyReferencePreviewUsesMainEffectiveFieldsWithoutInstance(t *testing.T) {
	spec := policySpecMap(t, Shield)
	spec["shield_type"], spec["shield_mode"] = "rely_shield", "custom_shield"
	spec["rely_policy"] = map[string]any{"expression": "A", "A": map[string]any{"condition": "term", "target_key": "object", "target_value": "${object}", "is_alarm_field_referenced": true}}
	origin := storetest.Alert("tenant", "main", "opening", "main", "warning")
	origin.SubjectID = ""
	origin.SubjectType = ""
	origin.SubjectSystem = ""
	origin.Enrich = domain.JSONObject{"processors": json.RawMessage(`[{"fields":{"status":"succeeded","patches":[{"op":"set","path":"$.extra_data.object","value":"host-1"}]}}]`)}
	event := policyEvent(`[]`, domain.EnrichStatusSucceeded)
	event.ExtraData["object"] = json.RawMessage(`"host-1"`)
	facts := &previewFacts{alert: origin}
	p := NewPreviewer(nil, facts, policyTargets{}, nil, nil)
	at := event.OccurredAt
	r := PreviewRequest{Scope: Scope{TenantID: "tenant", Kind: Shield}, Spec: encodeSpec(t, spec), Event: &event, Rely: true, OriginAlertID: origin.AlertID, At: &at}
	result, err := p.Preview(t.Context(), r)
	if err != nil || !result.Evaluations[0].Matched {
		t.Fatal("custom field preview required canonical main instance", result, err)
	}
	r.OriginAlertID = ""
	result, err = p.Preview(t.Context(), r)
	if err != nil || result.Evaluations[0].Evaluated || result.Evaluations[0].Matched {
		t.Fatal("missing main became ordinary mismatch", result, err)
	}
	r.OriginAlertID = origin.AlertID
	facts.alert.Enrich = domain.JSONObject{"processors": json.RawMessage(`[{"fields":{"status":"failed","patches":[]}}]`)}
	result, err = p.Preview(t.Context(), r)
	if err != nil || result.Evaluations[0].Evaluated {
		t.Fatal("failed main enrichment became missing string", result, err)
	}
	facts.alert.BKTenantID = "other"
	if _, err := p.Preview(t.Context(), r); !errors.Is(err, ErrInvalid) {
		t.Fatal("cross tenant preview origin accepted", err)
	}
}

type previewFacts struct {
	event domain.Event
	alert domain.Alert
	reads int
}

func (f *previewFacts) Event(context.Context, string, string) (domain.Event, error) {
	f.reads++
	return f.event.Clone(), nil
}

func (f *previewFacts) Alert(context.Context, string, string) (domain.Alert, error) {
	f.reads++
	return f.alert.Clone(), nil
}

func TestPolicyPreviewUsesFrozenEventAndDoesNotPublish(t *testing.T) {
	docs := newTestDocuments()
	service := NewService(docs)
	event := policyEvent(`[]`, domain.EnrichStatusSucceeded)
	facts := &previewFacts{event: event}
	preview := NewPreviewer(service, facts, policyTargets{}, nil, nil)
	at := time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC)
	request := PreviewRequest{Scope: Scope{TenantID: "tenant", Kind: Suppression}, Spec: testSpec("preview"), EventID: event.EventID, At: &at}
	result, err := preview.Preview(t.Context(), request)
	if err != nil || len(result.Evaluations) != 1 || !result.Evaluations[0].Matched || result.Mode != "matching_only" {
		t.Fatalf("preview %+v %v", result, err)
	}
	if len(docs.rows) != 0 || facts.reads != 1 {
		t.Fatal("preview performed publication or extra reads")
	}
	event.Evaluations[0].Action = domain.EventActionResolved
	request.EventID = ""
	request.Event = &event
	result, err = preview.Preview(t.Context(), request)
	if err != nil || result.Evaluations[0].Reason != "terminal_bypass" {
		t.Fatalf("terminal preview %+v %v", result, err)
	}
}

func TestPolicyPreviewRejectsAmbiguityScopeAndMissingEnrich(t *testing.T) {
	service := NewService(newTestDocuments())
	event := policyEvent(`[]`, domain.EnrichStatusSucceeded)
	at := time.Now()
	base := PreviewRequest{Scope: Scope{TenantID: "tenant", Kind: Suppression}, Spec: testSpec("preview"), Event: &event, At: &at}
	preview := NewPreviewer(service, nil, policyTargets{}, nil, nil)
	for _, change := range []func(*PreviewRequest){func(r *PreviewRequest) { r.EventID = "extra" }, func(r *PreviewRequest) { r.TenantID = "other" }, func(r *PreviewRequest) { r.ID = "existing" }, func(r *PreviewRequest) { r.Severity = "missing" }, func(r *PreviewRequest) { r.Event = nil }} {
		request := base
		change(&request)
		if _, err := preview.Preview(t.Context(), request); !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected invalid: %v", err)
		}
	}
	pending := storetest.Event("tenant", "pending", "f", "warning")
	base.Event = &pending
	if _, err := preview.Preview(t.Context(), base); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("pending enrich silently used: %v", err)
	}
	for range cap(preview.slots) {
		preview.slots <- struct{}{}
	}
	if _, err := preview.Preview(t.Context(), base); !errors.Is(err, ErrPreviewCapacity) {
		t.Fatal("capacity ignored")
	}
}
