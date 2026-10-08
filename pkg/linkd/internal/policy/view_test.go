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
	"linkd/internal/onemodel"
	"linkd/internal/store/storetest"
)

func policyEvent(raw string, status domain.EnrichStatus) domain.Event {
	e := storetest.Event("tenant", "e1", "f1", "warning")
	e.SubjectID = "raw-subject"
	e.Title = "original"
	e.Dimensions["bk_biz_id"] = domain.NewStringScalar("2")
	e.Labels["source_id"] = domain.NewStringScalar("source")
	at := e.CreateAt
	e.EventEnrichment = domain.EventEnrichment{EnrichStatus: status, EnrichedAt: &at, EnrichConfigDigest: "digest", Enrich: domain.EventEnrichData{Evaluations: []domain.EvaluationEnrich{{Severity: "warning", Status: status, Data: domain.JSONObject{"processors": json.RawMessage(raw)}}}}}
	return e
}

func mustEventView(t *testing.T, e domain.Event, mappings map[string]FieldMapping) *FactView {
	t.Helper()
	v, err := EventView(e, "warning", mappings, nil, RelationContext{})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestPolicyEffectiveViewUsesFrozenEnrichAndExplicitFields(t *testing.T) {
	e := policyEvent(`[{"source":{"status":"succeeded","patches":[{"op":"set","path":"$.labels.source_id","value":"kac-source"},{"op":"set","path":"$.labels.source_name","value":"KAC source"}]}},{"display":{"status":"succeeded","patches":[{"op":"set","path":"$.title","value":"effective"}]}}]`, domain.EnrichStatusSucceeded)
	e.ExtraData["zone"] = json.RawMessage(`{"active":false}`)
	e.SourceRawData["secret"] = json.RawMessage(`"private"`)
	v := mustEventView(t, e, map[string]FieldMapping{"zone_active": {Path: "$.extra_data.zone.active", Kind: FieldBoolean}})
	for name, want := range map[string]any{"name": "effective", "source_id": "kac-source", "source_name": "KAC source", "bk_biz_id": "2", "level": "warning", "object": "raw-subject", "zone_active": false, "alarm_time": "2026-09-01 08:00:00"} {
		got, err := v.Field(t.Context(), name)
		if err != nil || !got.Present || got.Data != want {
			t.Fatalf("%s %+v %v want=%v", name, got, err, want)
		}
	}
	if e.Title != "original" {
		t.Fatal("view changed source facts")
	}
	if _, err := v.Field(t.Context(), "secret"); err == nil {
		t.Fatal("raw field readable")
	}
	v.document["title"] = "changed"
	again := mustEventView(t, e, nil)
	title, _ := again.Field(t.Context(), "name")
	if title.Data != "effective" {
		t.Fatal("shared view mutation")
	}
}

func TestPolicyMissingAndFailedEnrichDifferForNegativeConditions(t *testing.T) {
	condition := json.RawMessage(`{"expression":"A","A":{"condition":"must_not_term","target_key":"model_id","target_value":"cw-Host"}}`)
	expr, err := CompileExpression(condition, KACFields())
	if err != nil {
		t.Fatal(err)
	}
	empty := mustEventView(t, policyEvent(`[]`, domain.EnrichStatusSucceeded), nil)
	result, err := expr.Match(t.Context(), empty)
	if err != nil || !result.Matched {
		t.Fatalf("genuine missing should negate %+v %v", result, err)
	}
	failed := mustEventView(t, policyEvent(`[{"resource":{"status":"failed","patches":[]}}]`, domain.EnrichStatusFailed), nil)
	result, err = expr.Match(t.Context(), failed)
	if !errors.Is(err, ErrUnavailable) || result.Evaluated || result.Matched {
		t.Fatalf("failure became negative match %+v %v", result, err)
	}
	title, err := failed.Field(t.Context(), "name")
	if err != nil || title.Data != "original" {
		t.Fatal("unrelated source field affected")
	}
	resolved := mustEventView(t, policyEvent(`[{"resource":{"status":"failed","patches":[]}},{"cmdb":{"status":"succeeded","patches":[{"op":"set","path":"$.labels.model_id","value":"cw-Host"}]}}]`, domain.EnrichStatusPartial), nil)
	value, err := resolved.Field(t.Context(), "model_id")
	if err != nil || value.Data != "cw-Host" {
		t.Fatalf("later known write did not clear uncertainty %+v %v", value, err)
	}
	if _, err := resolved.Field(t.Context(), "model_inst_id"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("unwritten field uncertainty cleared")
	}
}

func TestPolicyViewKeepsAlertOpeningResultAndCurrentLevel(t *testing.T) {
	a := storetest.Alert("tenant", "a1", "opening", "f", "critical")
	a.Enrich = domain.JSONObject{"processors": json.RawMessage(`[{"display":{"status":"succeeded","patches":[{"op":"set","path":"$.title","value":"opening title"}]}}]`)}
	a.LatestEventID = "later"
	view, err := AlertView(a, nil, nil, RelationContext{})
	if err != nil {
		t.Fatal(err)
	}
	name, _ := view.Field(t.Context(), "name")
	level, _ := view.Field(t.Context(), "level")
	if name.Data != "opening title" || level.Data != "fatal" {
		t.Fatalf("wrong alert snapshot name=%+v level=%+v", name, level)
	}
}

type policyTargets struct {
	err    error
	tenant string
	refs   []onemodel.InstanceRef
}

func (r policyTargets) ResolveScope(_ context.Context, tenant, space string) (onemodel.TargetScope, error) {
	if r.tenant != "" {
		tenant = r.tenant
	}
	return onemodel.TargetScope{TenantID: tenant, SpaceCode: space, BusinessIDs: []int64{2}}, r.err
}

func (r policyTargets) Resolve(ctx context.Context, tenant, space string, _ onemodel.TargetDescriptor) (onemodel.TargetResult, error) {
	scope, err := r.ResolveScope(ctx, tenant, space)
	return onemodel.TargetResult{Scope: scope, Instances: r.refs}, err
}

func TestPolicyEvaluationGatesAndGroupValues(t *testing.T) {
	s := NewService(newTestDocuments())
	request := testRequest()
	release := mustApply(t, s, request)
	compiled, err := Compile(release.Kind, release.Spec)
	if err != nil {
		t.Fatal(err)
	}
	v := mustEventView(t, policyEvent(`[]`, domain.EnrichStatusSucceeded), nil)
	now := time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC)
	result, err := Evaluate(t.Context(), release, compiled, v, policyTargets{}, now, false)
	if err != nil || !result.Evaluated || !result.Matched {
		t.Fatalf("match %+v %v", result, err)
	}
	result, err = Evaluate(t.Context(), release, compiled, v, policyTargets{err: errors.New("down")}, now, false)
	if err != nil || result.Evaluated || result.Reason != "business_scope_unavailable" {
		t.Fatalf("dependency failure %+v %v", result, err)
	}
	if _, err := Evaluate(t.Context(), release, compiled, v, policyTargets{tenant: "other"}, now, false); err == nil {
		t.Fatal("tenant mismatch swallowed")
	}
	deleted := release
	deleted.Deleted = true
	result, err = Evaluate(t.Context(), deleted, compiled, v, nil, now, false)
	if err != nil || !result.Evaluated || result.Matched || result.Reason != "inactive" {
		t.Fatalf("deleted queried resources %+v %v", result, err)
	}
	spec := policySpecMap(t, Merge)
	spec["aggregate_fields"] = []string{"flag"}
	spec["field_mappings"] = map[string]FieldMapping{"flag": {Path: "$.labels.flag", Kind: FieldBoolean}}
	c, err := Compile(Merge, encodeSpec(t, spec))
	if err != nil {
		t.Fatal(err)
	}
	rel := Release{Scope: Scope{TenantID: "tenant", Kind: Merge}, ID: "merge", Version: 1, Spec: c.Canonical, Compiled: c.Summary}
	event := policyEvent(`[]`, domain.EnrichStatusSucceeded)
	event.Labels["flag"] = domain.NewBoolScalar(false)
	view := mustEventView(t, event, c.Common.FieldMappings)
	result, err = Evaluate(t.Context(), rel, c, view, policyTargets{}, now, false)
	if err != nil || !result.Matched || result.GroupKey == "" {
		t.Fatalf("false group invalid %+v %v", result, err)
	}
	delete(event.Labels, "flag")
	result, err = Evaluate(t.Context(), rel, c, mustEventView(t, event, c.Common.FieldMappings), policyTargets{}, now, false)
	if err != nil || result.Evaluated || result.Reason != "invalid_group_value" {
		t.Fatalf("missing grouping accepted %+v %v", result, err)
	}
}

type cancelReader struct{ cancel context.CancelFunc }

func (r cancelReader) Field(context.Context, string) (Value, error) {
	r.cancel()
	return Value{}, context.Canceled
}

func (cancelReader) Related(context.Context, Condition) (bool, error) { return false, nil }

func TestExpressionPropagatesLastConditionCancellation(t *testing.T) {
	expr, err := CompileExpression(json.RawMessage(`{"expression":"A","A":{"condition":"term","target_key":"name","target_value":"x"}}`), KACFields())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if _, err := expr.Match(ctx, cancelReader{cancel}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation treated as skip: %v", err)
	}
}

type viewRelationRows []onemodel.InstanceRef

func (r viewRelationRows) Lookup(context.Context, string, onemodel.InstanceRef, string, string) ([]onemodel.InstanceRef, error) {
	return r, nil
}

func TestRelatedValidatesEntireSetBeforeMatching(t *testing.T) {
	match := onemodel.InstanceRef{ModelID: "cw-Host", InstanceID: "1", EntityUID: "cw-Host|1"}
	for _, tc := range []struct {
		name    string
		rows    viewRelationRows
		wantErr bool
	}{
		{"empty", nil, false},
		{"match", viewRelationRows{match}, false},
		{"wrong model after match", viewRelationRows{match, {ModelID: "other", InstanceID: "2", EntityUID: "other|2"}}, true},
		{"empty identity after match", viewRelationRows{match, {ModelID: "cw-Host", EntityUID: "cw-Host|"}}, true},
		{"duplicate identity after match", viewRelationRows{match, match}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := policyEvent(`[]`, domain.EnrichStatusSucceeded)
			e.Labels["model_id"] = domain.NewStringScalar("cw-Host")
			e.Labels["model_inst_id"] = domain.NewStringScalar("1")
			v := mustEventView(t, e, nil)
			v.relation = RelationContext{Origin: onemodel.InstanceRef{ModelID: "cw-Switch", InstanceID: "s1", EntityUID: "cw-Switch|s1"}, Lookup: tc.rows}
			matched, err := v.Related(t.Context(), Condition{Relation: "belongs", Value: json.RawMessage(`"cw-Host"`)})
			if tc.wantErr {
				if !errors.Is(err, ErrUnavailable) || matched {
					t.Fatalf("partial set used for match: matched=%t err=%v", matched, err)
				}
			} else if err != nil || matched != (len(tc.rows) > 0) {
				t.Fatalf("matched=%t err=%v", matched, err)
			}
		})
	}
}
