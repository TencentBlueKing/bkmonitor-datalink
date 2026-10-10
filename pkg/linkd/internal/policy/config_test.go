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
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

func policySpecMap(t *testing.T, kind Kind) map[string]any {
	t.Helper()
	var spec map[string]any
	if err := json.Unmarshal(testSpec("test"), &spec); err != nil {
		t.Fatal(err)
	}
	if kind == Shield {
		delete(spec, "scheme")
		spec["shield_type"] = "time_shield"
		spec["model_id"] = "cmdb.host"
		spec["target_descriptor"] = map[string]any{"schema_version": 1, "model_id": "cmdb.host", "selectors": []any{map[string]any{"type": "instances", "instances": []any{map[string]any{"model_id": "cmdb.host", "model_inst_id": "123", "entity_uid": "cmdb.host|123"}}}}}
	}
	if kind == Merge {
		delete(spec, "scheme")
		spec["policy"] = []any{spec["policy"], spec["policy"]}
		spec["merge_cycle"] = 60
		spec["aggregate_fields"] = []string{"model_id", "model_inst_id"}
		spec["new_alarm_config"] = []TemplateField{{Key: "name", Value: "merged ${alarm_num}"}, {Key: "level", Value: "warning"}}
	}
	return spec
}

func encodeSpec(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestTimeShieldAcceptsEmptyKACDependencyCondition(t *testing.T) {
	t.Parallel()
	spec := policySpecMap(t, Shield)
	without, err := Compile(Shield, encodeSpec(t, spec))
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []json.RawMessage{json.RawMessage(`{}`), json.RawMessage(` { } `)} {
		spec["rely_policy"] = raw
		compiled, err := Compile(Shield, encodeSpec(t, spec))
		if err != nil {
			t.Fatalf("KAC empty dependency condition rejected: %v", err)
		}
		if compiled.Rely != nil || compiled.Summary.Digest != without.Summary.Digest {
			t.Fatalf("empty condition changes time shield semantics: %+v", compiled.Summary)
		}
	}
	spec["rely_policy"] = spec["policy"]
	if _, err := Compile(Shield, encodeSpec(t, spec)); err == nil {
		t.Fatal("nonempty dependency condition accepted for time shield")
	}
}

func TestPolicyConfigurationCompilation(t *testing.T) {
	at := time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC)
	for _, kind := range []Kind{Suppression, Shield, Merge} {
		t.Run(string(kind), func(t *testing.T) {
			spec := policySpecMap(t, kind)
			compiled, err := Compile(kind, encodeSpec(t, spec))
			if err != nil {
				t.Fatal(err)
			}
			if compiled.Summary.CompilerVersion != 1 || compiled.Summary.Digest == "" || compiled.Summary.Timezone != "Asia/Shanghai" || !compiled.Active(at) {
				t.Fatalf("incomplete compile %+v", compiled.Summary)
			}
			second, err := Compile(kind, compiled.Canonical)
			if err != nil || second.Summary.Digest != compiled.Summary.Digest {
				t.Fatalf("compile not stable: %v", err)
			}
			spec["is_enable"] = false
			disabled, err := Compile(kind, encodeSpec(t, spec))
			if err != nil || disabled.Active(at) {
				t.Fatalf("disabled policy active: %v", err)
			}
		})
	}
	spec := policySpecMap(t, Shield)
	spec["activate_times"] = []any{}
	spec["shield_type"] = "rely_shield"
	spec["shield_mode"] = "custom_shield"
	spec["rely_policy"] = spec["policy"]
	compiled, err := Compile(Shield, encodeSpec(t, spec))
	if err != nil || !compiled.Active(at) {
		t.Fatalf("dependency should ignore schedule: %v", err)
	}
	spec = policySpecMap(t, Suppression)
	schemes := []Scheme{{Type: "aggregation", Duration: 1, DurationType: "hour", Fields: []string{"source_id"}}, {Type: "clip", Duration: 2, DurationType: "minute", Count: 3}}
	spec["scheme"] = schemes
	compiled, err = Compile(Suppression, encodeSpec(t, spec))
	if err != nil || compiled.Summary.Schemes[0].Type != "clip" || compiled.Summary.Schemes[0].Seconds != 120 || compiled.Summary.Schemes[1].Seconds != 3600 {
		t.Fatalf("scheme normalization: %+v %v", compiled, err)
	}
}

func TestPolicyRejectsInvalidConfigurations(t *testing.T) {
	tests := []struct {
		name   string
		kind   Kind
		change func(map[string]any)
	}{
		{"unknown", Suppression, func(s map[string]any) { s["unsupported"] = true }},
		{"invalid business code", Suppression, func(s map[string]any) { s["space_code"] = "bkcc__0" }},
		{"missing updated time", Suppression, func(s map[string]any) { delete(s, "updated_at") }},
		{"bad timezone", Suppression, func(s map[string]any) { s["timezone"] = "Somewhere/Else" }},
		{"model without target", Suppression, func(s map[string]any) { s["model_id"] = "cmdb.host" }},
		{"empty expression", Suppression, func(s map[string]any) { s["policy"] = map[string]any{} }},
		{"zero duration", Suppression, func(s map[string]any) {
			s["scheme"] = []Scheme{{Type: "clip", Duration: 0, DurationType: "second", Count: 1}}
		}},
		{"negative duration", Suppression, func(s map[string]any) {
			s["scheme"] = []Scheme{{Type: "clip", Duration: -1, DurationType: "second", Count: 1}}
		}},
		{"duration overflow", Suppression, func(s map[string]any) {
			s["scheme"] = []Scheme{{Type: "clip", Duration: 1 << 62, DurationType: "hour", Count: 1}}
		}},
		{"unknown unit", Suppression, func(s map[string]any) {
			s["scheme"] = []Scheme{{Type: "clip", Duration: 1, DurationType: "day", Count: 1}}
		}},
		{"zero count", Suppression, func(s map[string]any) { s["scheme"] = []Scheme{{Type: "clip", Duration: 1, DurationType: "second"}} }},
		{"duplicate scheme", Suppression, func(s map[string]any) {
			s["scheme"] = []Scheme{{Type: "clip", Duration: 1, DurationType: "second", Count: 1}, {Type: "clip", Duration: 1, DurationType: "second", Count: 2}}
		}},
		{"empty aggregation", Suppression, func(s map[string]any) {
			s["scheme"] = []Scheme{{Type: "aggregation", Duration: 1, DurationType: "second"}}
		}},
		{"duplicate grouping", Merge, func(s map[string]any) { s["aggregate_fields"] = []string{"name", "name"} }},
		{"unknown grouping", Merge, func(s map[string]any) { s["aggregate_fields"] = []string{"raw.secret"} }},
		{"merge window overflow", Merge, func(s map[string]any) { s["merge_cycle"] = 86401 }},
		{"duplicate template", Merge, func(s map[string]any) {
			s["new_alarm_config"] = []TemplateField{{Key: "name", Value: "a"}, {Key: "name", Value: "b"}}
		}},
		{"negative tags", Shield, func(s map[string]any) { s["alarm_tags"] = []int{0} }},
		{"duplicate tags", Shield, func(s map[string]any) { s["alarm_tags"] = []int{1, 1} }},
		{"time with dependency", Shield, func(s map[string]any) { s["time_range_before"] = 1 }},
		{"missing dependency condition", Shield, func(s map[string]any) { s["shield_type"] = "rely_shield"; s["shield_mode"] = "cmdb_shield" }},
		{"unknown target field", Shield, func(s map[string]any) { s["target_descriptor"].(map[string]any)["unknown"] = true }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := policySpecMap(t, tc.kind)
			tc.change(spec)
			if _, err := Compile(tc.kind, encodeSpec(t, spec)); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	raw := strings.Replace(string(testSpec("test")), `"name":"test"`, `"name":"test","name":"again"`, 1)
	if _, err := Compile(Suppression, []byte(raw)); err == nil {
		t.Fatal("duplicate key accepted")
	}
}

func TestCustomPolicyFieldsRequireExplicitSafeMapping(t *testing.T) {
	for _, path := range []string{"$.extra_data.region", "$.labels.region"} {
		spec := policySpecMap(t, Suppression)
		spec["policy"] = map[string]any{"expression": "A", "A": map[string]any{"condition": "term", "target_key": "region", "target_value": "east"}}
		spec["field_mappings"] = map[string]FieldMapping{"region": {Path: path, Kind: FieldKeyword}}
		if _, err := Compile(Suppression, encodeSpec(t, spec)); err != nil {
			t.Fatal(err)
		}
	}
	for _, mapping := range []map[string]FieldMapping{{"name": {Path: "$.labels.name", Kind: FieldText}}, {"region": {Path: "$.source_raw_data.region", Kind: FieldKeyword}}, {"region": {Path: "$.extra_data.*", Kind: FieldKeyword}}, {"region": {Path: "$.extra_data.region", Kind: "unknown"}}} {
		spec := policySpecMap(t, Suppression)
		spec["field_mappings"] = mapping
		if _, err := Compile(Suppression, encodeSpec(t, spec)); err == nil {
			t.Fatalf("invalid field mapping accepted %+v", mapping)
		}
	}
	fields := KACFields()
	fields["name"] = FieldBoolean
	if KACFields()["name"] != FieldText || KACFields()["bk_service_id"] != FieldNumber {
		t.Fatal("shared/incorrect field catalog")
	}
}

func TestJSONFormattingDoesNotChangeOperationIdentity(t *testing.T) {
	d := newTestDocuments()
	service := NewService(d)
	r := testRequest()
	first := mustApply(t, service, r)
	var spec map[string]json.RawMessage
	if err := json.Unmarshal(r.Spec, &spec); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(spec))
	for key := range spec {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	slices.Reverse(keys)
	parts := []string{}
	for _, key := range keys {
		parts = append(parts, `"`+key+`": `+string(spec[key]))
	}
	r.Spec = json.RawMessage("{\n" + strings.Join(parts, ",\n") + "\n}")
	retry := mustApply(t, service, r)
	if retry.RequestDigest != first.RequestDigest {
		t.Fatal("JSON formatting changed operation identity")
	}
}
