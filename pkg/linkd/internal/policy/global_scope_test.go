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
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/onemodel"
)

type globalPolicyDirectory struct{}

func (globalPolicyDirectory) Model(context.Context, string, string) (onemodel.ModelDefinition, bool, error) {
	panic("no target model")
}

func (globalPolicyDirectory) DynamicGroup(context.Context, string, string) (onemodel.DynamicGroupDefinition, bool, error) {
	panic("no dynamic group")
}

func (globalPolicyDirectory) Space(_ context.Context, tenant, space string) (onemodel.BusinessSpace, bool, error) {
	id := int64(2)
	if space == "bkcc__99" {
		id = 99
	}
	return onemodel.BusinessSpace{TenantID: tenant, BusinessID: id, Global: id == 99}, true, nil
}

func TestGlobalPolicyAcceptsEventsWithoutBusiness(t *testing.T) {
	for _, kind := range []Kind{Suppression, Shield, Merge} {
		for _, raw := range []string{"", "null", `""`, "0", "2", "123", `"invalid"`} {
			t.Run(string(kind)+"/"+raw, func(t *testing.T) {
				spec := policySpecMap(t, kind)
				spec["space_code"] = "bkcc__99"
				if kind != Shield {
					delete(spec, "model_id")
					delete(spec, "target_descriptor")
				}
				if kind == Merge {
					spec["aggregate_fields"] = []string{"source_id"}
				}
				compiled, err := Compile(kind, encodeSpec(t, spec))
				if err != nil {
					t.Fatal(err)
				}
				release := Release{Scope: Scope{TenantID: "tenant", Kind: kind}, ID: "global", Version: 1, Spec: compiled.Canonical, Compiled: compiled.Summary}
				event := policyEvent(`[]`, domain.EnrichStatusSucceeded)
				delete(event.Dimensions, "bk_biz_id")
				if raw != "" {
					event.ExtraData["bk_biz_id"] = json.RawMessage(raw)
				}
				// 全局且无目标时只读取空间定义，不配置 CMDB 分页/拓扑读取器。
				targets := globalPolicyTargets{TargetResolver: onemodel.NewTargetResolver(globalPolicyDirectory{}, nil, nil)}
				event.Labels["model_id"] = domain.NewStringScalar("cmdb.host")
				event.Labels["model_inst_id"] = domain.NewStringScalar("123")
				verdict, err := Evaluate(t.Context(), release, compiled, mustEventView(t, event, nil), targets, time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC), false)
				if err != nil || !verdict.Evaluated || !verdict.Matched {
					t.Fatalf("verdict=%+v err=%v", verdict, err)
				}
			})
		}
	}
}

// Scope 用真实目录解析；显式目标仅提供固定测试实例，不连接 CMDB。
type globalPolicyTargets struct{ *onemodel.TargetResolver }

func (r globalPolicyTargets) Resolve(ctx context.Context, tenant, space string, _ onemodel.TargetDescriptor) (onemodel.TargetResult, error) {
	scope, err := r.ResolvePolicyScope(ctx, tenant, space)
	return onemodel.TargetResult{Scope: scope, Instances: []onemodel.InstanceRef{{ModelID: "cmdb.host", InstanceID: "123", EntityUID: "cmdb.host|123"}}}, err
}

func TestSpecificPolicyKeepsBusinessConstraint(t *testing.T) {
	compiled, err := Compile(Suppression, testSpec("specific"))
	if err != nil {
		t.Fatal(err)
	}
	release := Release{Scope: Scope{TenantID: "tenant", Kind: Suppression}, ID: "specific", Version: 1, Spec: compiled.Canonical, Compiled: compiled.Summary}
	for _, raw := range []string{"", "null", `""`, "0", "2", "3", "-42"} {
		t.Run(raw, func(t *testing.T) {
			event := policyEvent(`[]`, domain.EnrichStatusSucceeded)
			delete(event.Dimensions, "bk_biz_id")
			if raw != "" {
				event.ExtraData["bk_biz_id"] = json.RawMessage(raw)
			}
			targets := onemodel.NewTargetResolver(globalPolicyDirectory{}, nil, nil)
			verdict, err := Evaluate(t.Context(), release, compiled, mustEventView(t, event, nil), targets, time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC), false)
			if err != nil || verdict.Matched != (raw == "2") {
				t.Fatal(verdict, err)
			}
		})
	}
}

func TestGlobalShieldKeepsTargetsConditionsAndTenant(t *testing.T) {
	for _, name := range []string{"matched", "outside target", "missing target", "wrong model", "wrong source", "explicit business", "other tenant", "dependency child"} {
		t.Run(name, func(t *testing.T) {
			spec := policySpecMap(t, Shield)
			spec["space_code"] = "bkcc__99"
			if name == "explicit business" {
				spec["policy"] = map[string]any{"expression": "A", "A": map[string]any{"target_key": "bk_biz_id", "condition": "term", "target_value": "2"}}
			}
			if name == "dependency child" {
				spec["shield_type"] = "rely_shield"
				spec["shield_mode"] = "custom_shield"
				spec["rely_policy"] = spec["policy"]
			}
			compiled, err := Compile(Shield, encodeSpec(t, spec))
			if err != nil {
				t.Fatal(err)
			}
			release := Release{Scope: Scope{TenantID: "tenant", Kind: Shield}, ID: "shield", Version: 1, Spec: compiled.Canonical, Compiled: compiled.Summary}
			event := policyEvent(`[]`, domain.EnrichStatusSucceeded)
			delete(event.Dimensions, "bk_biz_id")
			event.Labels["model_id"] = domain.NewStringScalar("cmdb.host")
			event.Labels["model_inst_id"] = domain.NewStringScalar("123")
			switch name {
			case "outside target":
				event.Labels["model_inst_id"] = domain.NewStringScalar("456")
			case "missing target", "dependency child":
				delete(event.Labels, "model_inst_id")
			case "wrong model":
				event.Labels["model_id"] = domain.NewStringScalar("cmdb.service")
			case "wrong source":
				event.Labels["source_id"] = domain.NewStringScalar("other-source")
			case "other tenant":
				release.TenantID = "other"
			}
			targets := globalPolicyTargets{TargetResolver: onemodel.NewTargetResolver(globalPolicyDirectory{}, nil, nil)}
			verdict, err := EvaluateConditions(t.Context(), release, compiled, mustEventView(t, event, nil), targets, time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC), name == "dependency child")
			if name == "other tenant" {
				if err == nil {
					t.Fatal("cross tenant accepted")
				}
				return
			}
			if err != nil || verdict.Matched != (name == "matched" || name == "dependency child") {
				t.Fatal(verdict, err)
			}
		})
	}
}
