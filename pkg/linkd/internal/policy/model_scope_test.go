// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package policy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/onemodel"
	"linkd/internal/store/storetest"
)

func modelScopeSpec(t *testing.T, kind Kind) map[string]any {
	t.Helper()
	spec := policySpecMap(t, kind)
	spec["model_id"], spec["target_descriptor"] = "cw-Host", nil
	if kind == Merge {
		spec["aggregate_fields"] = []string{"model_id"}
	}
	return spec
}

func TestModelScopeCompileKACPublication(t *testing.T) {
	for _, kind := range []Kind{Suppression, Shield, Merge} {
		t.Run(string(kind), func(t *testing.T) {
			spec := modelScopeSpec(t, kind)
			first, err := Compile(kind, encodeSpec(t, spec))
			if err != nil {
				t.Fatal("KAC model/null publication rejected", err)
			}
			delete(spec, "target_descriptor")
			omitted, err := Compile(kind, encodeSpec(t, spec))
			if err != nil || first.Summary.Digest != omitted.Summary.Digest {
				t.Fatal("null and omitted target differ", err)
			}
			restored, err := Compile(kind, first.Canonical)
			if err != nil || restored.Common.ModelID != "cw-Host" || restored.Common.Targets != nil || restored.Summary.Digest != first.Summary.Digest {
				t.Fatal("release round-trip lost model scope", err)
			}
			spec["model_id"] = "cw-Service"
			other, err := Compile(kind, encodeSpec(t, spec))
			if err != nil || other.Summary.Digest == first.Summary.Digest {
				t.Fatal("model scope missing from digest", err)
			}
		})
	}
}

func TestModelScopeCompileKeepsStrictTargetsAndRuleGates(t *testing.T) {
	for _, kind := range []Kind{Suppression, Shield, Merge} {
		t.Run(string(kind), func(t *testing.T) {
			for _, model := range []any{" ", "cw|Host", "cw\nHost", strings.Repeat("x", 129), false, []string{"cw-Host"}} {
				spec := modelScopeSpec(t, kind)
				spec["model_id"] = model
				if _, err := Compile(kind, encodeSpec(t, spec)); err == nil {
					t.Fatalf("invalid model accepted: %q", model)
				}
			}
			for _, descriptor := range []any{map[string]any{}, false, []any{}, onemodel.TargetDescriptor{SchemaVersion: 1, ModelID: "cw-Host"}} {
				spec := modelScopeSpec(t, kind)
				spec["target_descriptor"] = descriptor
				if _, err := Compile(kind, encodeSpec(t, spec)); err == nil {
					t.Fatalf("invalid target became unrestricted: %+v", descriptor)
				}
			}
			spec := modelScopeSpec(t, kind)
			spec["policy"] = map[string]any{}
			if kind == Merge {
				spec["policy"] = []any{map[string]any{}}
			}
			_, err := Compile(kind, encodeSpec(t, spec))
			if (err == nil) != (kind != Merge) {
				t.Fatal("model-only rule gate changed", err)
			}
			delete(spec, "model_id")
			if _, err := Compile(kind, encodeSpec(t, spec)); err == nil {
				t.Fatal("no model, no members and no conditions accepted")
			}
		})
	}
	spec := modelScopeSpec(t, Shield)
	delete(spec, "model_id")
	if _, err := Compile(Shield, encodeSpec(t, spec)); err != nil {
		t.Fatal("valid advanced-filter-only time shield rejected", err)
	}
	for _, missing := range []string{"policy", "rely_policy"} {
		spec := modelScopeSpec(t, Shield)
		spec["shield_type"], spec["shield_mode"], spec["rely_policy"] = "rely_shield", "custom_shield", spec["policy"]
		spec[missing] = map[string]any{}
		if _, err := Compile(Shield, encodeSpec(t, spec)); err == nil {
			t.Fatal("model scope bypassed dependency condition requirement", missing)
		}
	}
}

type modelScopeTargets struct {
	global                  bool
	business                int64
	err                     error
	scopeReads, memberReads int
}

func (r *modelScopeTargets) ResolvePolicyScope(_ context.Context, tenant, space string) (onemodel.TargetScope, error) {
	r.scopeReads++
	business := r.business
	if business == 0 {
		business = 2
	}
	return onemodel.TargetScope{TenantID: tenant, SpaceCode: space, BusinessIDs: []int64{business}, Global: r.global}, r.err
}

func (r *modelScopeTargets) Resolve(context.Context, string, string, onemodel.TargetDescriptor) (onemodel.TargetResult, error) {
	r.memberReads++
	return onemodel.TargetResult{}, errors.New("model scope must not enumerate instances")
}

func (r *modelScopeTargets) PrepareTargets(context.Context, string, string, *onemodel.TargetDescriptor) (TargetReader, error) {
	r.memberReads++
	return nil, errors.New("model scope must not prepare members")
}

func (r *modelScopeTargets) ContainsTarget(context.Context, string, string, *onemodel.TargetDescriptor, onemodel.InstanceRef) (bool, error) {
	r.memberReads++
	return false, errors.New("model scope must not query membership")
}

func TestModelScopeEvaluateKeepsModelTenantBusinessAndAvailability(t *testing.T) {
	for _, kind := range []Kind{Suppression, Shield, Merge} {
		for _, name := range []string{"same without instance", "invalid instance ignored", "other model", "missing model", "invalid model", "unavailable model", "other tenant", "other business", "global without business", "non BKCC", "scope unavailable", "scope denied", "terminal"} {
			t.Run(string(kind)+"/"+name, func(t *testing.T) {
				spec := modelScopeSpec(t, kind)
				event := policyEvent("[]", domain.EnrichStatusSucceeded)
				event.Labels["model_id"] = domain.NewStringScalar("cw-Host")
				reader := &modelScopeTargets{}
				matched, evaluated, wantErr := true, true, false
				switch name {
				case "invalid instance ignored":
					event.ExtraData["model_inst_id"] = json.RawMessage("[]")
				case "other model":
					event.Labels["model_id"] = domain.NewStringScalar("cw-Service")
					matched = false
				case "missing model":
					delete(event.Labels, "model_id")
					matched = false
				case "invalid model":
					delete(event.Labels, "model_id")
					event.ExtraData["model_id"] = json.RawMessage("42")
					matched, evaluated = false, false
				case "unavailable model":
					event.Enrich.Evaluations[0].Data["processors"] = json.RawMessage("[{\"resource\":{\"status\":\"failed\",\"patches\":[]}}]")
					matched, evaluated = false, false
				case "other tenant":
					event.BKTenantID = "other"
					matched, wantErr = false, true
				case "other business":
					event.Dimensions["bk_biz_id"] = domain.NewStringScalar("3")
					matched = false
				case "global without business":
					reader.global = true
					spec["space_code"] = "bkcc__99"
					delete(event.Dimensions, "bk_biz_id")
				case "non BKCC":
					spec["space_code"] = "default__custom"
					reader.business = -42
					event.Dimensions["bk_biz_id"] = domain.NewStringScalar("-42")
				case "scope unavailable":
					reader.err = ErrUnavailable
					matched, evaluated = false, false
				case "scope denied":
					reader.err = ErrAccess
					matched, wantErr = false, true
				case "terminal":
					event.Evaluations[0].Action = domain.EventActionResolved
					matched = false
				}
				c, err := Compile(kind, encodeSpec(t, spec))
				if err != nil {
					t.Fatal(err)
				}
				release := Release{Scope: Scope{TenantID: "tenant", Kind: kind}, ID: "model", Version: 1, Spec: c.Canonical, Compiled: c.Summary}
				verdict, err := Evaluate(t.Context(), release, c, mustEventView(t, event, nil), reader, time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC), false)
				if (err != nil) != wantErr || (!wantErr && (verdict.Matched != matched || verdict.Evaluated != evaluated)) {
					t.Fatalf("verdict=%+v err=%v", verdict, err)
				}
				if reader.memberReads != 0 {
					t.Fatal("model range read instance members")
				}
			})
		}
	}
}

func TestModelScopeDependencyChildDoesNotInheritMainModel(t *testing.T) {
	spec := modelScopeSpec(t, Shield)
	spec["shield_type"], spec["shield_mode"], spec["rely_policy"] = "rely_shield", "custom_shield", spec["policy"]
	c, err := Compile(Shield, encodeSpec(t, spec))
	if err != nil {
		t.Fatal(err)
	}
	release := Release{Scope: Scope{TenantID: "tenant", Kind: Shield}, ID: "model", Version: 1, Spec: c.Canonical, Compiled: c.Summary}
	event := policyEvent("[]", domain.EnrichStatusSucceeded)
	event.Labels["model_id"] = domain.NewStringScalar("cw-Service")
	view := mustEventView(t, event, nil)
	reader := &modelScopeTargets{}
	at := time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC)
	main, err := Evaluate(t.Context(), release, c, view, reader, at, false)
	if err != nil || main.Matched {
		t.Fatal("wrong main model accepted", main, err)
	}
	child, err := Evaluate(t.Context(), release, c, view, reader, at, true)
	if err != nil || !child.Matched || reader.memberReads != 0 {
		t.Fatal("main model leaked into child", child, err)
	}
}

func TestModelScopePreviewAndCandidateIndex(t *testing.T) {
	event := policyEvent("[]", domain.EnrichStatusSucceeded)
	event.Labels["model_id"] = domain.NewStringScalar("cw-Host")
	at := time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC)
	for _, kind := range []Kind{Suppression, Shield, Merge} {
		t.Run(string(kind), func(t *testing.T) {
			spec := modelScopeSpec(t, kind)
			if kind != Merge {
				spec["policy"] = map[string]any{}
			}
			raw := encodeSpec(t, spec)
			reader := &modelScopeTargets{}
			result, err := NewPreviewer(nil, nil, reader, nil, nil).Preview(t.Context(), PreviewRequest{Scope: Scope{TenantID: "tenant", Kind: kind}, Spec: raw, Event: &event, At: &at})
			if err != nil || len(result.Evaluations) != 1 || !result.Evaluations[0].Matched || reader.memberReads != 0 {
				t.Fatal(result, err)
			}
			c, err := Compile(kind, raw)
			if err != nil {
				t.Fatal(err)
			}
			index := newCandidateIndex([]FrozenPolicy{{Release: Release{ID: "model"}, Compiled: c}})
			items, err := index.selectPolicies(t.Context(), func(ctx context.Context, field string, _ FieldMapping) (Value, error) {
				return mustEventView(t, event, nil).Field(ctx, field)
			})
			if err != nil || len(items) != 1 {
				t.Fatal("candidate index lost model scope", items, err)
			}
		})
	}
	spec := modelScopeSpec(t, Shield)
	spec["policy"] = map[string]any{}
	alert := storetest.Alert("tenant", "same", "opening", "fingerprint", "warning")
	alert.Labels["model_id"], alert.Labels["bk_biz_id"] = domain.NewStringScalar("cw-Host"), domain.NewStringScalar("2")
	other := alert.Clone()
	other.AlertID = "other"
	other.Labels["model_id"] = domain.NewStringScalar("cw-Service")
	facts := batchShieldFacts{alerts: map[string]domain.Alert{"same": alert, "other": other}}
	result, err := NewPreviewer(nil, facts, &modelScopeTargets{}, nil, nil).PreviewBatch(t.Context(), BatchPreviewRequest{Scope: Scope{TenantID: "tenant", Kind: Shield}, Spec: encodeSpec(t, spec), At: &at, AlertIDs: []string{"same", "other"}})
	if err != nil || len(result.Items) != 2 || !result.Items[0].Matched || result.Items[1].Matched {
		t.Fatal(result, err)
	}
}
