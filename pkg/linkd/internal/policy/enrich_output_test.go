// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package policy

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store/storetest"
)

func currentOutputViews(t *testing.T, event domain.Event, mappings map[string]FieldMapping) map[string]*FactView {
	t.Helper()
	alert := storetest.Alert(event.BKTenantID, "a1", event.EventID, event.Fingerprint, "warning")
	alert.Title, alert.Content, alert.SubjectID, alert.SubjectName = event.Title, event.Content, event.SubjectID, event.SubjectName
	alert.Labels, alert.ExtraData, alert.Dimensions = event.Labels, event.ExtraData, event.Dimensions
	alert.EnrichStatus, alert.Enrich = event.EnrichStatus, event.Enrich.Evaluations[0].Data
	view, err := AlertView(alert, mappings, nil, RelationContext{})
	if err != nil {
		t.Fatal(err)
	}
	return map[string]*FactView{"event": mustEventView(t, event, mappings), "alert": view}
}

func TestAllPoliciesMatchCurrentEnrichOutput(t *testing.T) {
	for _, status := range []domain.EnrichStatus{domain.EnrichStatusFailed, domain.EnrichStatusPartial} {
		for _, kind := range []Kind{Shield, Suppression, Merge} {
			t.Run(string(status)+"/"+string(kind), func(t *testing.T) {
				// 来源先成功，CMDB 后失败；业务只在 extra_data，标题保持原值。
				event := policyEvent(`[{"source":{"status":"succeeded","patches":[{"op":"set","path":"$.labels.source_name","value":"betty-推送-克隆"}]}},{"cmdb":{"status":"`+string(status)+`","patches":[],"diagnostics":[{"code":"missing_field"}]}}]`, status)
				if status == domain.EnrichStatusFailed {
					event.Labels["source_name"] = domain.NewStringScalar("betty-推送-克隆")
					event.Enrich.Evaluations[0].Data["processors"] = json.RawMessage(`[{"cmdb":{"status":"failed","patches":[],"diagnostics":[{"code":"missing_field"}]}}]`)
				}
				event.Title = "host-CPU使用率过高-时间屏蔽"
				event.ExtraData["bk_biz_id"] = json.RawMessage(`2`)
				event.Labels["model_id"] = domain.NewStringScalar("cw-Host")
				event.Labels["model_inst_id"] = domain.NewStringScalar("7")
				condition := json.RawMessage(`{"expression":"A and B","A":{"condition":"term","target_key":"source_name","target_value":"betty-推送-克隆"},"B":{"condition":"regexp","target_key":"name","target_value":"时间屏蔽"}}`)
				spec := policySpecMap(t, kind)
				delete(spec, "target_descriptor")
				spec["model_id"] = "cw-Host"
				spec["policy"] = condition
				if kind == Merge {
					spec["policy"] = []json.RawMessage{condition, condition}
				}
				compiled, err := Compile(kind, encodeSpec(t, spec))
				if err != nil {
					t.Fatal(err)
				}
				release := Release{Scope: Scope{TenantID: "tenant", Kind: kind}, ID: "current-output", Version: 1, Spec: compiled.Canonical, Compiled: compiled.Summary}
				preview := NewPreviewer(nil, nil, policyTargets{}, nil, nil)
				at := time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC)
				request := PreviewRequest{Scope: release.Scope, Spec: compiled.Canonical, Event: &event, At: &at}
				result, err := preview.Preview(t.Context(), request)
				if err != nil || len(result.Evaluations) != 1 || !result.Evaluations[0].Evaluated || !result.Evaluations[0].Matched {
					t.Fatalf("preview: %+v %v", result, err)
				}
				for name, view := range currentOutputViews(t, event, compiled.Common.FieldMappings) {
					verdict, err := Evaluate(t.Context(), release, compiled, view, policyTargets{}, time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC), false)
					if err != nil || !verdict.Evaluated || !verdict.Matched {
						t.Fatalf("%s: %+v %v", name, verdict, err)
					}
					if kind == Merge && verdict.GroupKey == "" {
						t.Fatal("merge grouping lost")
					}
				}
			})
		}
	}
}

func TestEnrichStatusPreservesPresenceAndReadOrder(t *testing.T) {
	mappings := map[string]FieldMapping{"owner": {Path: "$.extra_data.owner", ReadPath: `$["extra_data","labels"]["owner"]`, Kind: FieldText}}
	for _, status := range []domain.EnrichStatus{domain.EnrichStatusSucceeded, domain.EnrichStatusFailed, domain.EnrichStatusPartial} {
		for _, raw := range []string{`""`, `false`, `0`, `null`, `"value"`} {
			t.Run(string(status)+"/"+raw, func(t *testing.T) {
				event := policyEvent(`[{"cmdb":{"status":"`+string(status)+`","patches":[],"diagnostics":[{"code":"dependency_invalid"}]}}]`, status)
				event.ExtraData["owner"] = json.RawMessage(raw)
				event.ExtraData["source_name"] = json.RawMessage(`"fallback"`)
				event.Labels["owner"] = domain.NewStringScalar("label owner")
				event.Labels["source_name"] = domain.NewStringScalar("label source")
				var want any
				// JSON 数字在视图中保留原始精度。
				if raw == "0" {
					want = json.Number("0")
				} else if err := json.Unmarshal([]byte(raw), &want); err != nil {
					t.Fatal(err)
				}
				for name, view := range currentOutputViews(t, event, mappings) {
					got, err := view.Field(t.Context(), "owner")
					if err != nil || !got.Present || !reflect.DeepEqual(got.Data, want) {
						t.Fatalf("%s owner: %+v %v want=%#v", name, got, err, want)
					}
					got, err = view.Field(t.Context(), "source_name")
					if err != nil || got.Data != "label source" {
						t.Fatalf("%s labels priority: %+v %v", name, got, err)
					}
				}
				delete(event.ExtraData, "owner")
				delete(event.Labels, "source_name")
				for name, view := range currentOutputViews(t, event, mappings) {
					for field, want := range map[string]string{"owner": "label owner", "source_name": "fallback"} {
						got, err := view.Field(t.Context(), field)
						if err != nil || !got.Present || got.Data != want {
							t.Fatalf("%s fallback %s: %+v %v", name, field, got, err)
						}
					}
				}
			})
		}
	}
}
