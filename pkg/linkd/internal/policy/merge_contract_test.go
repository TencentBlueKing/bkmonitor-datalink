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
	"strings"
	"testing"
)

func TestMergeUsesLatestRegexWithoutAnEngineOption(t *testing.T) {
	spec := policySpecMap(t, Merge)
	spec["policy"] = []any{map[string]any{"expression": "A", "A": map[string]any{
		"condition": "regexp", "target_key": "name", "target_value": `(?<=GUID\()(?<name>主机)-\k<name>(?=\))`,
	}}}
	c, err := Compile(Merge, encodeSpec(t, spec))
	if err != nil {
		t.Fatal(err)
	}
	result, err := c.Conditions[0].Match(t.Context(), Fields{Values: map[string]any{"name": "前缀 GUID(主机-主机) 后缀"}})
	if err != nil || !result.Matched {
		t.Fatal(result, err)
	}
	spec["regex_engine"] = "regexp2"
	if _, err := Compile(Merge, encodeSpec(t, spec)); err == nil {
		t.Fatal("engine selection must not be part of the policy contract")
	}
}

func TestMergeLongTitleAndObjectRenderWithoutTruncation(t *testing.T) {
	text := strings.Repeat("告警🚨", 100)
	c := templateFixture(t, nil, func(spec map[string]any) {
		spec["aggregate_fields"] = []string{}
		spec["max_merge_field_length"] = 1000
		spec["new_alarm_config"] = []TemplateField{{Key: "name", Value: "${cw_merged_name}"}, {Key: "object", Value: text}, {Key: "level", Value: "warning"}}
	})
	result, err := c.Template.Render(t.Context(), []TemplateReader{Fields{Values: map[string]any{"name": text}}, Fields{Values: map[string]any{"name": text}}})
	if err != nil || result["title"] != text || result["subject_name"] != text {
		t.Fatal("display text was rejected or truncated", err)
	}
	field := mergeTemplateField{key: "name", kind: FieldText, target: pathFor("title")}
	for _, input := range []string{strings.Repeat("x", 2001), strings.Repeat("警", 1<<20), strings.Repeat("🚨", 2001)} {
		value, err := field.outputValue(input)
		if err != nil || len([]rune(value.(string))) != 2000 {
			t.Fatal("display text must be truncated to 2000 characters", err)
		}
	}
}
