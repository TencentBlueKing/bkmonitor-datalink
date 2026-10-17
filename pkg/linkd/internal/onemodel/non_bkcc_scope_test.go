// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package onemodel

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestNonBKCCSpaceUsesTenantDirectoryIdentity(t *testing.T) {
	d, p, descriptor := targetFixtures()
	d.otherSpaces = map[string]BusinessSpace{"default__custom": {TenantID: "t", BusinessID: -42}}
	r := NewTargetResolver(d, p, nil)
	scope, err := r.ResolveScope(t.Context(), "t", "default__custom")
	if err != nil || !reflect.DeepEqual(scope.BusinessIDs, []int64{-42}) {
		t.Fatalf("scope %+v %v", scope, err)
	}
	got, err := r.ResolvePolicyScope(t.Context(), "t", "default__custom")
	if err != nil || got.Global || !reflect.DeepEqual(got.BusinessIDs, []int64{-42}) {
		t.Fatalf("scope %+v %v", got, err)
	}

	p.pages = []Page{{Instances: []Instance{targetInstance("cw-Host", "a", -42)}}}
	result, err := r.Resolve(t.Context(), "t", "default__custom", descriptor)
	if err != nil || len(result.Instances) != 1 {
		t.Fatalf("target %+v %v", result, err)
	}
	p.pages = []Page{{Instances: []Instance{targetInstance("cw-Host", "a", -43)}}}
	p.queries = nil
	if _, err := r.Resolve(t.Context(), "t", "default__custom", descriptor); err == nil {
		t.Fatal("foreign space instance accepted")
	}
	if _, err := r.ResolveScope(t.Context(), "other", "default__custom"); err == nil {
		t.Fatal("foreign tenant space accepted")
	}
}

func TestNonBKCCDynamicGroupKeepsSpaceBoundary(t *testing.T) {
	d, p, descriptor := targetFixtures()
	d.otherSpaces = map[string]BusinessSpace{
		"default__custom": {TenantID: "t", BusinessID: -42},
		"default__other":  {TenantID: "t", BusinessID: -43},
	}
	d.group = DynamicGroupDefinition{TenantID: "t", ID: "23", ModelID: "cw-Host", SpaceCode: "default__custom", Conditions: json.RawMessage(`[{"field":"cpu","operator":"greater","value":"2"}]`)}
	descriptor.Selectors = []TargetSelector{{Type: "dynamic_group", Provider: "kingeye", DynamicGroupID: "23"}}
	p.pages = []Page{{Instances: []Instance{targetInstance("cw-Host", "a", -42)}}}
	resolver := NewTargetResolver(d, p, nil)
	result, err := resolver.Resolve(t.Context(), "t", "default__custom", descriptor)
	if err != nil || len(result.Instances) != 1 {
		t.Fatalf("result %+v %v", result, err)
	}
	d.group.SpaceCode = "default__other"
	if _, err := resolver.Resolve(t.Context(), "t", "default__custom", descriptor); err == nil {
		t.Fatal("foreign dynamic group accepted")
	}
}
