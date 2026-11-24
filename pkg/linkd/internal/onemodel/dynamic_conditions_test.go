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
	"strings"
	"testing"
)

func TestDynamicConditionTypesAndGroups(t *testing.T) {
	model := ModelDefinition{AttributeTypes: map[string]InstanceAttributeType{"cpu": InstanceAttributeLong, "enabled": InstanceAttributeBoolean, "name": InstanceAttributeKeyword, "at": InstanceAttributeDatetime}}
	raw := json.RawMessage(`{"condition":"OR","rules":[{"field":"cpu","operator":"greater","value":"2"},{"condition":"OR","rules":[{"field":"enabled","operator":"equal","value":"true"},{"field":"name","operator":"contains","value":".*a*b.*"}]},{"field":"at","value":"2026-09-30 01:00:00"}]}`)
	filter, err := CompileDynamicConditions(raw, model)
	if err != nil {
		t.Fatal(err)
	}
	if len(filter.All) != 3 || len(filter.Any) != 0 || len(filter.All[1].Any) != 2 {
		t.Fatalf("KAC root AND/nested OR differ %+v", filter)
	}
	clause, err := filter.Compile()
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(clause)
	for _, part := range []string{`"gt":2`, `"attribute_values.boolean_values":true`, `2026-09-30T01:00:00Z`, `*a\\*b*`} {
		if !strings.Contains(string(body), part) {
			t.Fatalf("missing %s in %s", part, body)
		}
	}
	empty, err := CompileDynamicConditions(json.RawMessage(`[]`), model)
	if err != nil || !empty.Empty() {
		t.Fatal("empty group should only apply scope")
	}
}

func TestDynamicConditionFailures(t *testing.T) {
	model := ModelDefinition{AttributeTypes: map[string]InstanceAttributeType{"cpu": InstanceAttributeLong, "flag": InstanceAttributeBoolean}}
	for _, raw := range []string{`[{"field":"unknown","value":1}]`, `[{"field":"cpu","operator":"strange","value":1}]`, `[{"field":"cpu","operator":"in","value":[]}]`, `[{"field":"cpu","operator":"equal","value":"no"}]`, `[{"field":"flag","value":"unknown"}]`, `[{"field":"cpu","value":1,"value":2}]`, `[{"field":"cpu","unexpected":1}]`, `[{"field":"cpu","operator":"exists","value":"yes"}]`} {
		if _, err := CompileDynamicConditions([]byte(raw), model); err == nil {
			t.Fatalf("invalid conditions accepted %s", raw)
		}
	}
	for _, name := range []string{"int", "integer", "long", "foreignkey"} {
		if NormalizeAttributeType(name) != InstanceAttributeLong {
			t.Fatal(name)
		}
	}
	if NormalizeAttributeType("unknown") != "" {
		t.Fatal("type inferred from unknown name")
	}
}

func TestDynamicNotExistsNegatesLeafOutsideNested(t *testing.T) {
	model := ModelDefinition{AttributeTypes: map[string]InstanceAttributeType{"cpu": InstanceAttributeLong}}
	filter, err := CompileDynamicConditions([]byte(`[{"field":"cpu","operator":"not exists"}]`), model)
	if err != nil {
		t.Fatal(err)
	}
	clause, err := filter.Compile()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(clause)
	if !strings.Contains(string(raw), `"must_not":[{"nested"`) {
		t.Fatalf("negation must apply outside nested: %s", raw)
	}
}
