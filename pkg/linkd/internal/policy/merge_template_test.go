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
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func templateFixture(t *testing.T, fields []TemplateField, edit func(map[string]any)) *Compiled {
	t.Helper()
	spec := policySpecMap(t, Merge)
	spec["aggregate_fields"] = []string{"model_id", "model_inst_id", "bk_biz_id"}
	spec["new_alarm_config"] = append([]TemplateField{{Key: "name", Value: "merged ${alarm_num}"}, {Key: "level", Value: "warning"}}, fields...)
	if edit != nil {
		edit(spec)
	}
	compiled, err := Compile(Merge, encodeSpec(t, spec))
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func TestMergeTemplateStableJSONAndLiteralMemberText(t *testing.T) {
	compiled := templateFixture(t, []TemplateField{
		{Key: "content", Value: "${cw_merged_content}|${cw_merged_meta_info}|${cw_merged_dimension_info}|${alarm_num}"},
		{Key: "bk_biz_id", Value: "${bk_biz_id}"},
		{Key: "model_id", Value: "${model_id}"},
		{Key: "model_inst_id", Value: "${model_inst_id}"},
	}, nil)
	members := []TemplateReader{
		Fields{Values: map[string]any{"content": "z ${alarm_num}", "meta_info": map[string]any{"z": true, "a": json.Number("9007199254740993")}, "dimension_info": []any{false, nil, "x"}, "bk_biz_id": json.Number("0"), "model_id": "cmdb.host", "model_inst_id": "0"}},
		Fields{Values: map[string]any{"content": "a", "meta_info": map[string]any{"a": json.Number("9007199254740993"), "z": true}, "dimension_info": []any{false, nil, "x"}, "bk_biz_id": json.Number("0.0"), "model_id": "cmdb.host", "model_inst_id": "0"}},
		Fields{Values: map[string]any{"content": "a", "meta_info": map[string]any{"z": true, "a": json.Number("9007199254740993")}, "dimension_info": []any{false, nil, "x"}, "bk_biz_id": 0, "model_id": "cmdb.host", "model_inst_id": "0"}},
	}
	got, err := compiled.Template.Render(t.Context(), members)
	if err != nil {
		t.Fatal(err)
	}
	want := `a###z ${alarm_num}|{"a":9007199254740993,"z":true}|[false,null,"x"]|3`
	if got["content"] != want || got["title"] != "merged 3" || got["extra_data"].(map[string]any)["bk_biz_id"] != json.Number("0") {
		t.Fatalf("unexpected render %#v", got)
	}
	// 改变遍历次序不影响合并字段去重/排序。普通聚合字段仍取已排序成员的首值表达。
	members[0], members[2] = members[2], members[0]
	second, err := compiled.Template.Render(t.Context(), members)
	if err != nil || !reflect.DeepEqual(got, second) {
		t.Fatal("unstable merged values", err)
	}
	again, err := Compile(Merge, compiled.Canonical)
	if err != nil || again.Summary.Digest != compiled.Summary.Digest || again.Merge.MaxMergeFieldLength != 500 {
		t.Fatal("template defaults not frozen", err)
	}
}

func TestMergeTemplateCharacterLimitAppliesOnlyToMergedVariable(t *testing.T) {
	c := templateFixture(t, []TemplateField{{Key: "content", Value: "prefix:${cw_merged_content}:suffix/${cw_merged_content}"}}, func(spec map[string]any) { spec["max_merge_field_length"] = 200 })
	long := strings.Repeat("中文🚨", 100)
	members := []TemplateReader{Fields{Values: map[string]any{"content": long}}, Fields{Values: map[string]any{"content": long}}}
	got, err := c.Template.Render(t.Context(), members)
	if err != nil {
		t.Fatal(err)
	}
	expected := string([]rune(long)[:200])
	if got["content"] != "prefix:"+expected+":suffix/"+expected || !utf8.ValidString(got["content"].(string)) {
		t.Fatal("truncated wrapper or Unicode incorrectly")
	}
}

func TestMergeTemplateMissingUnavailableAndGroupValues(t *testing.T) {
	c := templateFixture(t, []TemplateField{{Key: "content", Value: "${cw_merged_meta_info}"}}, nil)
	got, err := c.Template.Render(t.Context(), []TemplateReader{Fields{}, Fields{Values: map[string]any{"meta_info": nil}}})
	if err != nil || got["content"] != "--###None" {
		t.Fatal("missing/null distinction lost", got, err)
	}
	if _, err := c.Template.Render(t.Context(), []TemplateReader{Fields{}, Fields{Unavailable: map[string]bool{"meta_info": true}}}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("unavailable treated as missing", err)
	}
	grouped := templateFixture(t, []TemplateField{{Key: "content", Value: "${model_inst_id}"}}, nil)
	for _, bad := range []any{nil, " ", []any{}, map[string]any{}} {
		if _, err := grouped.Template.Render(t.Context(), []TemplateReader{Fields{Values: map[string]any{"model_inst_id": bad}}, Fields{Values: map[string]any{"model_inst_id": bad}}}); err == nil {
			t.Fatalf("invalid group accepted: %#v", bad)
		}
	}
	if _, err := grouped.Template.Render(t.Context(), []TemplateReader{Fields{Values: map[string]any{"model_inst_id": 0}}, Fields{Values: map[string]any{"model_inst_id": "0"}}}); err == nil {
		t.Fatal("distinct typed groups merged")
	}
	got, err = grouped.Template.Render(t.Context(), []TemplateReader{Fields{Values: map[string]any{"model_inst_id": false}}, Fields{Values: map[string]any{"model_inst_id": false}}})
	if err != nil || got["content"] != "False" {
		t.Fatal("false is valid group", err)
	}
}

func TestMergeTemplateTypedLiteralsAndMappedOutputs(t *testing.T) {
	c := templateFixture(t, []TemplateField{
		{Key: "bk_biz_id", Value: json.Number("9007199254740993")}, {Key: "meta_info", Value: map[string]any{"a": []any{1, false}}},
		{Key: "enabled", Value: false}, {Key: "details", Value: []any{"a", false}}, {Key: "at", Value: "2026-09-30T00:00:00Z"},
	}, func(s map[string]any) {
		s["field_mappings"] = map[string]FieldMapping{"enabled": {Path: "$.labels.enabled", Kind: FieldBoolean}, "details": {Path: "$.extra_data.details", Kind: FieldKeyword}, "at": {Path: "$.extra_data.at", Kind: FieldDate}}
	})
	got, err := c.Template.Render(t.Context(), []TemplateReader{Fields{}, Fields{}})
	if err != nil {
		t.Fatal(err)
	}
	extra := got["extra_data"].(map[string]any)
	if got["labels"].(map[string]any)["enabled"] != false || extra["bk_biz_id"] != json.Number("9007199254740993") {
		t.Fatalf("literal type/precision lost: %#v", got)
	}
	extra["meta_info"].(map[string]any)["a"] = "changed"
	next, err := c.Template.Render(t.Context(), []TemplateReader{Fields{}, Fields{}})
	if err != nil || reflect.DeepEqual(next, got) {
		t.Fatal("render shared mutable literals", err)
	}
}

func TestMergeTemplateRejectsInvalidPublishedConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"unknown variable", func(s map[string]any) {
			s["new_alarm_config"] = []TemplateField{{Key: "name", Value: "${content}"}, {Key: "level", Value: "warning"}}
		}},
		{"unknown merged", func(s map[string]any) {
			s["new_alarm_config"] = []TemplateField{{Key: "name", Value: "${cw_merged_unknown}"}, {Key: "level", Value: "warning"}}
		}},
		{"malformed", func(s map[string]any) {
			s["new_alarm_config"] = []TemplateField{{Key: "name", Value: "${alarm_num"}, {Key: "level", Value: "warning"}}
		}},
		{"missing severity", func(s map[string]any) { s["new_alarm_config"] = []TemplateField{{Key: "name", Value: "a"}} }},
		{"empty name", func(s map[string]any) {
			s["new_alarm_config"] = []TemplateField{{Key: "name", Value: " "}, {Key: "level", Value: "warning"}}
		}},
		{"numeric name", func(s map[string]any) {
			s["new_alarm_config"] = []TemplateField{{Key: "name", Value: 1}, {Key: "level", Value: "warning"}}
		}},
		{"oversized title", func(s map[string]any) {
			s["new_alarm_config"] = []TemplateField{{Key: "name", Value: strings.Repeat("名", 1<<20)}, {Key: "level", Value: "warning"}}
		}},
		{"system field", func(s map[string]any) {
			s["new_alarm_config"] = append(s["new_alarm_config"].([]TemplateField), TemplateField{Key: "bk_tenant_id", Value: "other"})
		}},
		{"alias system field", func(s map[string]any) {
			s["field_mappings"] = map[string]FieldMapping{"x": {Path: "$.labels.source_id", Kind: FieldKeyword}}
			s["new_alarm_config"] = append(s["new_alarm_config"].([]TemplateField), TemplateField{Key: "x", Value: "other"})
		}},
		{"overlap", func(s map[string]any) {
			s["field_mappings"] = map[string]FieldMapping{"a": {Path: "$.extra_data.x", Kind: FieldKeyword}, "b": {Path: "$.extra_data.x.b", Kind: FieldKeyword}}
			s["new_alarm_config"] = append(s["new_alarm_config"].([]TemplateField), TemplateField{Key: "a", Value: "a"}, TemplateField{Key: "b", Value: "b"})
		}},
		{"invalid date", func(s map[string]any) {
			s["field_mappings"] = map[string]FieldMapping{"at": {Path: "$.extra_data.at", Kind: FieldDate}}
			s["new_alarm_config"] = append(s["new_alarm_config"].([]TemplateField), TemplateField{Key: "at", Value: "tomorrow"})
		}},
		{"numeric label precision", func(s map[string]any) {
			s["field_mappings"] = map[string]FieldMapping{"x": {Path: "$.labels.x", Kind: FieldNumber}}
			s["new_alarm_config"] = append(s["new_alarm_config"].([]TemplateField), TemplateField{Key: "x", Value: json.Number("9007199254740993")})
		}},
		{"array target", func(s map[string]any) {
			s["field_mappings"] = map[string]FieldMapping{"x": {Path: "$.extra_data.x[0]", Kind: FieldKeyword}}
			s["new_alarm_config"] = append(s["new_alarm_config"].([]TemplateField), TemplateField{Key: "x", Value: "a"})
		}},
		{"null literal", func(s map[string]any) {
			s["new_alarm_config"] = append(s["new_alarm_config"].([]TemplateField), TemplateField{Key: "meta_info", Value: nil})
		}},
		{"number false", func(s map[string]any) {
			s["new_alarm_config"] = append(s["new_alarm_config"].([]TemplateField), TemplateField{Key: "bk_biz_id", Value: false})
		}},
		{"limit null", func(s map[string]any) { s["max_merge_field_length"] = nil }},
		{"limit zero", func(s map[string]any) { s["max_merge_field_length"] = 0 }},
		{"limit low", func(s map[string]any) { s["max_merge_field_length"] = 199 }},
		{"limit high", func(s map[string]any) { s["max_merge_field_length"] = 65537 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := policySpecMap(t, Merge)
			tc.edit(spec)
			if _, err := Compile(Merge, encodeSpec(t, spec)); err == nil {
				t.Fatal("invalid template accepted")
			}
		})
	}
}

func TestMergeTemplateBoundsCancellationAndConcurrentReuse(t *testing.T) {
	c := templateFixture(t, []TemplateField{{Key: "content", Value: "${cw_merged_content}"}}, nil)
	members := []TemplateReader{Fields{Values: map[string]any{"content": "b"}}, Fields{Values: map[string]any{"content": "a"}}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.Template.Render(ctx, members); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			got, err := c.Template.Render(t.Context(), members)
			if err != nil || got["content"] != "a###b" {
				t.Error("shared compiler state", err)
			}
		})
	}
	wg.Wait()
	for _, count := range []int{0, 1} {
		if _, err := c.Template.Render(t.Context(), make([]TemplateReader, count)); err == nil {
			t.Fatal("member limit ignored")
		}
	}
	large := strings.Repeat("x", 600000)
	many := make([]TemplateReader, 64)
	for i := range many {
		many[i] = Fields{Values: map[string]any{"content": large}}
	}
	if _, err := c.Template.Render(t.Context(), many); err == nil {
		t.Fatal("read budget ignored")
	}
	// 每个变量均合法，但重复展开后的完整内容越界，不能截掉外层模板使其假成功。
	c = templateFixture(t, []TemplateField{{Key: "content", Value: strings.Repeat("${cw_merged_content}", 20)}}, func(s map[string]any) { s["max_merge_field_length"] = 65536 })
	if _, err := c.Template.Render(t.Context(), many[:2]); err == nil {
		t.Fatal("output budget ignored")
	}
}

type mergeTemplateReaderFunc func(context.Context, string) (Value, error)

func (f mergeTemplateReaderFunc) Field(ctx context.Context, name string) (Value, error) {
	return f(ctx, name)
}

func TestMergeTemplateLargeGroups(t *testing.T) {
	for _, count := range []int{5000, 10000} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			compiled := templateFixture(t, []TemplateField{
				{Key: "content", Value: "${cw_merged_content}|${cw_merged_content}"},
				{Key: "model_id", Value: "${model_id}"},
				{Key: "model_inst_id", Value: "${model_inst_id}"},
			}, func(spec map[string]any) { spec["max_merge_field_length"] = 65536 })
			members := make([]TemplateReader, count)
			reads := map[string]int{}
			for i := range members {
				fields := Fields{Values: map[string]any{
					"content":  fmt.Sprintf("member-%03d", (count-i)%100),
					"model_id": "cmdb.host", "model_inst_id": "host-1",
				}}
				members[i] = mergeTemplateReaderFunc(func(ctx context.Context, name string) (Value, error) {
					reads[name]++
					return fields.Field(ctx, name)
				})
			}
			got, err := compiled.Template.Render(t.Context(), members)
			if err != nil {
				t.Fatal(err)
			}
			unique := make([]string, 100)
			for i := range unique {
				unique[i] = fmt.Sprintf("member-%03d", i)
			}
			merged := strings.Join(unique, "###")
			if got["title"] != "merged "+strconv.Itoa(count) || got["content"] != merged+"|"+merged {
				t.Fatalf("member count or sorted/deduplicated merged field lost: %#v", got)
			}
			extra := got["extra_data"].(map[string]any)
			if extra["model_id"] != "cmdb.host" || extra["model_inst_id"] != "host-1" {
				t.Fatalf("aggregate fields lost: %#v", extra)
			}
			for _, name := range []string{"content", "model_id", "model_inst_id"} {
				if reads[name] != count {
					t.Fatalf("%s read %d times, want exactly %d (including repeated variable cache)", name, reads[name], count)
				}
			}
			// 末尾成员同样参与聚合字段一致语义，不能只渲染旧上限内的前缀。
			members[count-1] = Fields{Values: map[string]any{"content": "last", "model_id": "cmdb.host", "model_inst_id": "different"}}
			if _, err := compiled.Template.Render(t.Context(), members); err == nil {
				t.Fatal("last member's different aggregate value ignored")
			}
		})
	}
}

func TestMergeTemplateLargeGroupsKeepByteAndCancellationBudgets(t *testing.T) {
	compiled := templateFixture(t, []TemplateField{{Key: "content", Value: "${cw_merged_content}"}}, nil)
	for _, count := range []int{5000, 10000} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			// 即使合并去重后的输出很短，仍按读取的实际成员输入累计 32 MiB。
			value := strings.Repeat("x", mergeTemplateReadBytes/count+1)
			members := make([]TemplateReader, count)
			for i := range members {
				members[i] = Fields{Values: map[string]any{"content": value}}
			}
			if _, err := compiled.Template.Render(t.Context(), members); err == nil || !strings.Contains(err.Error(), "input exceeds 32 MiB") {
				t.Fatalf("large group bypassed input byte budget: %v", err)
			}
			outputLimited := templateFixture(t, []TemplateField{{Key: "content", Value: strings.Repeat("${cw_merged_content}", 20)}}, func(spec map[string]any) {
				spec["max_merge_field_length"] = 65536
			})
			for i := range members {
				members[i] = Fields{Values: map[string]any{"content": "x"}}
			}
			members[count-1] = Fields{Values: map[string]any{"content": strings.Repeat("z", 60000)}}
			if _, err := outputLimited.Template.Render(t.Context(), members); err == nil || !strings.Contains(err.Error(), "output field exceeds 1 MiB") {
				t.Fatalf("large group bypassed output byte budget: %v", err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			reads := 0
			for i := range members {
				members[i] = mergeTemplateReaderFunc(func(ctx context.Context, name string) (Value, error) {
					reads++
					if reads == 129 {
						cancel()
					}
					return Fields{Values: map[string]any{"content": "x"}}.Field(ctx, name)
				})
			}
			if _, err := compiled.Template.Render(ctx, members); !errors.Is(err, context.Canceled) || reads != 129 {
				t.Fatalf("large group cancellation not respected: reads=%d err=%v", reads, err)
			}
		})
	}
}
