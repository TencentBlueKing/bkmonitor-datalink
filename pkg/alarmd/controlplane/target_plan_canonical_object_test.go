// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cmdbcache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// canonicalObjectCatalog builds a catalog from the second fixture strategy
// with the given query target_identity (nil for none) and target_plan, and
// returns it with the strategy's disposition.
func canonicalObjectCatalog(t *testing.T, targetIdentity, targetPlan string) (controlplane.Catalog, controlplane.ObjectDisposition) {
	t.Helper()
	payload, err := os.ReadFile("testdata/two_threshold_strategies.json")
	if err != nil {
		t.Fatal(err)
	}
	var documents []json.RawMessage
	if err := json.Unmarshal(payload, &documents); err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(documents[1], &document); err != nil {
		t.Fatal(err)
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(document["items"], &items); err != nil {
		t.Fatal(err)
	}
	var queries []map[string]json.RawMessage
	if err := json.Unmarshal(items[0]["query_configs"], &queries); err != nil {
		t.Fatal(err)
	}
	queries[0]["agg_dimension"] = json.RawMessage(`["model_id","model_inst_id"]`)
	if targetIdentity != "" {
		queries[0]["target_identity"] = json.RawMessage(targetIdentity)
	}
	if items[0]["query_configs"], err = json.Marshal(queries); err != nil {
		t.Fatal(err)
	}
	items[0]["target_plan"] = json.RawMessage(targetPlan)
	if document["items"], err = json.Marshal(items); err != nil {
		t.Fatal(err)
	}
	identity := controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}
	changed := controlplane.SourceStrategy{SourceID: "1002", Identity: identity}
	if changed.Document, err = json.Marshal(document); err != nil {
		t.Fatal(err)
	}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: []controlplane.SourceStrategy{{SourceID: "1001", Document: documents[0], Identity: identity}, changed},
		Planner:    &recordingPlanner{facts: queryFacts(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, disposition := range catalog.Dispositions {
		if disposition.SourceID == "1002" {
			return catalog, disposition
		}
	}
	t.Fatalf("no disposition for strategy 1002 in %+v", catalog.Dispositions)
	return catalog, controlplane.ObjectDisposition{}
}

const canonicalObjectTargetIdentity = `{"type":"object_model_inst","object_model_field":"model_id","object_model_inst_field":"model_inst_id"}`

// The model is named by its code, as the data carries it, and the instance
// by the opaque string OneModel gives it.
const canonicalObjectTargetPlan = `{"schema_version":1,"model_id":"cw-MySQL","target_rule":"model_inst_id","failure_policy":"no_match",
	"model_match":{"model_id":"cw-MySQL"},
	"static_targets":[{"model_id":"cw-MySQL","model_inst_id":"22"}],"dynamic_groups":[],"dynamic_topologies":[]}`

// An ordinary object's plan in the canonical OneModel identity -- the query
// naming model_id/model_inst_id as its target identity, the plan gating on
// model_id = "cw-MySQL" (the model code, a string) and listing instance
// "22" -- is accepted and frozen as that gate and that key, and its series
// are placed by the canonical labels alone: the instance in the target, an
// instance outside it, a series without the instance label, a series of
// another model, a series carrying only the legacy cw_* pair with real
// values, and a series carrying the canonical pair beside the placeholder
// cw_* labels old plugin versions still emit (a placeholder whose value is
// its own key). The platform's default cw_* pair stays a candidate behind
// the named one and is never what this plan reads.
func TestACanonicalOrdinaryObjectPlanIsAdmittedOnTheCanonicalLabels(t *testing.T) {
	catalog, disposition := canonicalObjectCatalog(t, canonicalObjectTargetIdentity, canonicalObjectTargetPlan)
	if disposition.Disposition != controlplane.DispositionAccepted {
		t.Fatalf("disposition %+v, want the canonical plan accepted", disposition)
	}
	var plan *contract.TargetPlanV1
	for _, group := range catalog.QueryGroups {
		for _, frozen := range group.Plans {
			if frozen.Identity.StrategyID == "1002" {
				plan = frozen.Plan.TargetPlan
			}
		}
	}
	wantIdentity := contract.TargetPlanIdentityV1{Dimensions: []string{"model_inst_id"}, ModelDimension: "model_id", ModelValue: "cw-MySQL"}
	if plan == nil || plan.ModelID != "cw-MySQL" || plan.Rule != contract.TargetPlanRuleModelInstID ||
		!reflect.DeepEqual(plan.Identity, wantIdentity) || !reflect.DeepEqual(plan.StaticKeys, []string{"22"}) ||
		len(plan.StaticMembers) != 0 || len(plan.DynamicGroups) != 0 || len(plan.DynamicTopologies) != 0 {
		t.Fatalf("frozen target plan = %+v, want model cw-MySQL, gate model_id = cw-MySQL, key model_inst_id, static key 22", plan)
	}

	// The worker's resolution of the frozen plan for one Slot, and the
	// admission chain's identity fuller and target plan filter on records.
	resolution := cmdbcache.NewTargetResolver(nil, nil, time.Now).Resolve(context.Background(), plan, time.Minute)
	resolution.Compose()
	context := admission.PlanContext{TargetPlan: &admission.TargetPlanContext{Identity: plan.Identity, Members: resolution}}
	placeholders := map[string]string{"cw_object_model_id": "cw_object_model_id", "cw_object_model_inst_id": "cw_object_model_inst_id"}
	for name, test := range map[string]struct {
		labels map[string]string
		admit  bool
		reason string
	}{
		"in target":                          {labels: map[string]string{"model_id": "cw-MySQL", "model_inst_id": "22"}, admit: true, reason: "in_target"},
		"in target beside cw_* placeholders": {labels: with(placeholders, map[string]string{"model_id": "cw-MySQL", "model_inst_id": "22"}), admit: true, reason: "in_target"},
		"another instance":                   {labels: map[string]string{"model_id": "cw-MySQL", "model_inst_id": "23"}, reason: admission.TargetPlanReasonOutOfTarget},
		"another instance beside cw_*":       {labels: with(placeholders, map[string]string{"model_id": "cw-MySQL", "model_inst_id": "23"}), reason: admission.TargetPlanReasonOutOfTarget},
		"no instance label":                  {labels: map[string]string{"model_id": "cw-MySQL"}, reason: admission.TargetPlanReasonKeyMissing},
		"another model":                      {labels: map[string]string{"model_id": "cw-Redis", "model_inst_id": "22"}, reason: admission.TargetPlanReasonKeyMissing},
		"legacy cw_* pair only":              {labels: map[string]string{"cw_object_model_id": "8", "cw_object_model_inst_id": "22"}, reason: admission.TargetPlanReasonKeyMissing},
		"cw_* placeholders only":             {labels: placeholders, reason: admission.TargetPlanReasonKeyMissing},
	} {
		t.Run(name, func(t *testing.T) {
			raw := make(map[string]json.RawMessage, len(test.labels))
			for label, value := range test.labels {
				raw[label], _ = json.Marshal(value)
			}
			facts := &admission.Facts{Dimensions: raw}
			admission.IdentityFuller{}.Fill(raw, facts)
			decision := (admission.TargetPlanFilter{}).Admit(context, facts)
			reason := decision.Reason
			if decision.Admit && reason == "" {
				reason = "in_target"
			}
			if decision.Admit != test.admit || reason != test.reason {
				t.Fatalf("Admit(%v) = %+v, want admit=%v reason=%q", test.labels, decision, test.admit, test.reason)
			}
		})
	}
}

// Without the query naming the canonical pair, a gate on model_id has no
// identity pair to read the instance from -- the platform's default pair
// reads cw_object_model_id as the model -- and the plan is refused by name,
// not frozen as a gate on one dimension and a key on an unrelated one.
func TestACanonicalGateWithoutTheQuerysTargetIdentityIsRefusedByName(t *testing.T) {
	_, disposition := canonicalObjectCatalog(t, "", canonicalObjectTargetPlan)
	if disposition.Disposition == controlplane.DispositionAccepted ||
		!strings.Contains(disposition.FieldPath+" "+disposition.Detail, "model_match.model_id") {
		t.Fatalf("disposition %+v, want the plan refused naming model_match.model_id", disposition)
	}
}

func with(base, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range extra {
		out[key] = value
	}
	return out
}
