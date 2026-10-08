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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type kacRuleCase struct {
	Input      map[string]any
	KAC, Linkd any
	Difference string
}

type kacRuleResult struct {
	ID     string         `json:"id"`
	Kind   string         `json:"kind"`
	Result map[string]any `json:"result"`
}

type kacRuleResponse struct {
	Sources map[string]string `json:"source_sha256"`
	Runtime map[string]string `json:"runtime"`
	Results []kacRuleResult   `json:"results"`
}

type comparisonOutput struct {
	bytes.Buffer
	limit int
}

func (b *comparisonOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errors.New("comparison output exceeds budget")
	}
	return b.Buffer.Write(p)
}

func runKACRules(ctx context.Context, python, root string, inputs []map[string]any) (kacRuleResponse, string, error) {
	raw, err := json.Marshal(inputs)
	if err != nil {
		return kacRuleResponse{}, "", err
	}
	if len(raw) > 1<<20 {
		return kacRuleResponse{}, "", errors.New("comparison input exceeds 1 MiB")
	}
	call, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// 外部源码仅由显式启用的对照测试读取；Python 不导入 Django，不访问数据库或 Redis。
	//nolint:gosec // G204: Python 与源码目录来自本地显式测试配置，参数独立传递，不经过 shell。
	cmd := exec.CommandContext(call, python, "-B", filepath.Join("..", "..", "tests", "kac_behavior_comparison", "kac_policy_behavior_comparison.py"), root)
	cmd.Env = append(os.Environ(), "PYTHONHASHSEED=0", "PYTHONDONTWRITEBYTECODE=1")
	cmd.Stdin = bytes.NewReader(raw)
	out, stderr := &comparisonOutput{limit: 1 << 20}, &comparisonOutput{limit: 8192}
	cmd.Stdout, cmd.Stderr = out, stderr
	if err := cmd.Run(); err != nil {
		return kacRuleResponse{}, stderr.String(), err
	}
	var response kacRuleResponse
	decoder := json.NewDecoder(bytes.NewReader(out.Bytes()))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return response, stderr.String(), err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return response, stderr.String(), errors.New("trailing comparison response")
	}
	return response, stderr.String(), nil
}

func kacRuleCases() []kacRuleCase {
	cases := []kacRuleCase{}
	schedule := func(id, at string, rules []ActiveTime, want bool) {
		cases = append(cases, kacRuleCase{Input: map[string]any{"id": id, "kind": "schedule", "at": at, "rules": append([]ActiveTime{}, rules...)}, KAC: want, Linkd: want})
	}
	daily := []ActiveTime{{Period: "everyday", OpenClock: "10:00:00", CloseClock: "11:00:00"}}
	for _, c := range []struct {
		id, at string
		want   bool
	}{{"daily-before", "2026-09-30T09:59:59.999+08:00", false}, {"daily-start", "2026-09-30T10:00:00+08:00", true}, {"daily-last-ms", "2026-09-30T11:00:00.999+08:00", true}, {"daily-after", "2026-09-30T11:00:01+08:00", false}} {
		schedule(c.id, c.at, daily, c.want)
	}
	once := []ActiveTime{{Period: "once", OpenOnce: "2026-09-30 10:00:00", CloseOnce: "2026-09-30 11:00:00"}}
	schedule("once-last-ms", "2026-09-30T11:00:00.999+08:00", once, true)
	schedule("once-after", "2026-09-30T11:00:01+08:00", once, false)
	weekly := []ActiveTime{{Period: "every_week", OpenClock: "10:00:00", CloseClock: "11:00:00", DaysOfWeek: "1,3,7"}}
	schedule("weekly-wednesday", "2026-09-30T10:00:00+08:00", weekly, true)
	schedule("weekly-thursday", "2026-10-01T10:00:00+08:00", weekly, false)
	schedule("weekly-sunday", "2026-10-04T10:00:00+08:00", weekly, true)
	weekly[0].DaysOfWeek = "*"
	schedule("weekly-any", "2026-10-01T10:00:00+08:00", weekly, true)
	monthly := []ActiveTime{{Period: "every_month", OpenClock: "12:00:00", CloseClock: "12:00:00", DaysOfMonth: "29,31"}}
	schedule("monthly-leap", "2028-02-29T12:00:00.999+08:00", monthly, true)
	schedule("monthly-other", "2028-02-28T12:00:00+08:00", monthly, false)
	schedule("monthly-31", "2026-10-31T12:00:00+08:00", monthly, true)
	schedule("empty-schedule", "2026-09-30T10:00:00+08:00", []ActiveTime{}, false)
	schedule("interval-union", "2026-09-30T12:00:00+08:00", append(append([]ActiveTime{}, daily...), ActiveTime{Period: "everyday", OpenClock: "12:00:00", CloseClock: "13:00:00"}), true)
	group := func(id string, values []any, kac, linkd bool, diff string) {
		cases = append(cases, kacRuleCase{Input: map[string]any{"id": id, "kind": "group", "values": values}, KAC: kac, Linkd: linkd, Difference: diff})
	}
	group("no-group", []any{}, true, true, "")
	group("missing-group", []any{nil}, false, false, "")
	group("empty-string", []any{""}, false, false, "")
	group("empty-array", []any{[]any{}}, false, false, "")
	group("empty-object", []any{map[string]any{}}, false, false, "")
	group("zero", []any{0}, false, true, "zero is explicitly valid")
	group("false", []any{false}, false, true, "false is explicitly valid")
	group("blank", []any{" \t"}, true, false, "whitespace is explicitly invalid")
	group("unicode-blank", []any{"\u00a0"}, true, false, "Unicode whitespace is explicitly invalid")
	group("nested-null", []any{[]any{nil}}, true, false, "nested values must be valid")
	group("nested-empty", []any{map[string]any{"x": ""}}, true, false, "nested values must be valid")
	group("numeric-one", []any{1}, true, true, "")
	group("decimal-one", []any{json.Number("1.0")}, true, true, "")
	group("string-one", []any{"1"}, true, true, "")
	group("bool-true", []any{true}, true, true, "")
	group("string-true", []any{"True"}, true, true, "")
	group("separator-left", []any{"a|b", "c"}, true, true, "")
	group("separator-right", []any{"a", "b|c"}, true, true, "")
	template := func(id, pattern string, fields []string, members []map[string]any, limit int, kac, linkd, diff string) {
		cases = append(cases, kacRuleCase{Input: map[string]any{"id": id, "kind": "template", "template": pattern, "fields": fields, "members": members, "limit": limit}, KAC: kac, Linkd: linkd, Difference: diff})
	}
	repeat := func(field string, value any) []map[string]any {
		return []map[string]any{{field: value}, {field: value}}
	}
	template("member-count", "${alarm_num}", []string{}, repeat("content", "same"), 500, "2", "2", "")
	template("stable-merged-order", "${cw_merged_content}", []string{}, []map[string]any{{"content": "beta"}, {"content": "alpha"}, {"content": "gamma"}, {"content": "delta"}}, 500, "alpha###delta###beta###gamma", "alpha###beta###delta###gamma", "merged values use stable sorted order")
	template("missing-template-value", "${cw_merged_content}", []string{}, []map[string]any{{}, {}}, 500, "--", "--", "")
	template("null-template-value", "${cw_merged_content}", []string{}, repeat("content", nil), 500, "None", "None", "")
	template("false-template-value", "${cw_merged_content}", []string{}, repeat("content", false), 500, "False", "False", "")
	template("aggregate-value", "${object}", []string{"object"}, repeat("object", "host-1"), 500, "host-1", "host-1", "")
	long := strings.Repeat("中文🚨", 100)
	short := string([]rune(long)[:200])
	template("unicode-per-variable", "prefix:${cw_merged_content}:suffix/${cw_merged_content}", []string{}, repeat("content", long), 200, "prefix:"+short+":suffix/"+short, "prefix:"+short+":suffix/"+short, "")
	template("complex-array", "${cw_merged_dimension_info}", []string{}, repeat("dimension_info", []any{false, nil, "x"}), 500, "[False, None, 'x']", `[false,null,"x"]`, "complex values use stable JSON")
	template("complex-object", "${cw_merged_meta_info}", []string{}, repeat("meta_info", map[string]any{"a": 1, "b": true}), 500, "{'a': 1, 'b': True}", `{"a":1,"b":true}`, "complex values use stable JSON")
	template("no-recursive-member-text", "${cw_merged_content}|${alarm_num}", []string{}, repeat("content", "value-${alarm_num}"), 500, "value-2|2", "value-${alarm_num}|2", "member values are not templates")
	template("unknown-variable", "${unknown}", []string{}, repeat("content", "x"), 500, "${unknown}", "configuration_rejected", "unknown variables rejected at publication")
	template("nonaggregate-variable", "${content}", []string{}, repeat("content", "x"), 500, "${content}", "configuration_rejected", "ordinary variables require declared grouping")
	template("invalid-length-limit", "${cw_merged_content}", []string{}, repeat("content", long), 199, short, "configuration_rejected", "limit must be 200..65536")
	return cases
}

func TestKACSourcePolicyRulesBehaviorComparison(t *testing.T) {
	root := os.Getenv("LINKD_TEST_KAC_SOURCE_ROOT")
	if root == "" {
		t.Skip("set LINKD_TEST_KAC_SOURCE_ROOT for pinned KAC source comparison")
	}
	python := os.Getenv("LINKD_TEST_KAC_PYTHON")
	if python == "" {
		python = "python3"
	}
	cases := kacRuleCases()
	inputs := make([]map[string]any, 0, len(cases))
	for _, tc := range cases {
		inputs = append(inputs, tc.Input)
	}
	response, stderr, err := runKACRules(t.Context(), python, root, inputs)
	if err != nil {
		t.Fatalf("KAC source comparison: %v %s", err, stderr)
	}
	if len(response.Results) != len(cases) || len(response.Sources) != 6 || response.Runtime["hash_seed"] != "0" || response.Runtime["implementation"] != "cpython" {
		t.Fatalf("incomplete comparison response: results=%d sources=%d", len(response.Results), len(response.Sources))
	}
	t.Logf("KAC comparison runtime: %s", response.Runtime["version"])
	reports := []map[string]any{}
	kacKeys, linkdKeys := map[string]string{}, map[string]string{}
	differences := 0
	for i, tc := range cases {
		row := response.Results[i]
		t.Run(tc.Input["id"].(string), func(t *testing.T) {
			if row.ID != tc.Input["id"] || row.Kind != tc.Input["kind"] {
				t.Fatal("comparison identity/order mismatch")
			}
			var kac, linkd any
			switch row.Kind {
			case "schedule":
				kac = row.Result["active"]
				at, err := time.Parse(time.RFC3339Nano, tc.Input["at"].(string))
				if err != nil {
					t.Fatal(err)
				}
				schedule, err := CompileSchedule("Asia/Shanghai", tc.Input["rules"].([]ActiveTime))
				if err != nil {
					t.Fatal(err)
				}
				linkd = schedule.Active(at)
			case "group":
				kac = row.Result["valid"]
				key, err := GroupKey(tc.Input["values"].([]any))
				linkd = err == nil
				if valid, ok := kac.(bool); !ok {
					t.Fatal("comparison group validity missing")
				} else if valid {
					kacKeys[row.ID], ok = row.Result["cache_key"].(string)
					if !ok {
						t.Fatal("comparison group key missing")
					}
				}
				if err == nil {
					linkdKeys[row.ID] = key
				}
			case "template":
				kac = row.Result["content"]
				spec := policySpecMap(t, Merge)
				spec["aggregate_fields"], spec["max_merge_field_length"] = tc.Input["fields"], tc.Input["limit"]
				spec["new_alarm_config"] = []TemplateField{{Key: "name", Value: "merged ${alarm_num}"}, {Key: "level", Value: "warning"}, {Key: "content", Value: tc.Input["template"]}}
				compiled, err := Compile(Merge, encodeSpec(t, spec))
				if err != nil {
					if tc.Linkd != "configuration_rejected" {
						t.Fatal(err)
					}
					linkd = "configuration_rejected"
					break
				}
				if tc.Linkd == "configuration_rejected" {
					t.Fatal("invalid configuration accepted")
				}
				members := []TemplateReader{}
				for _, values := range tc.Input["members"].([]map[string]any) {
					members = append(members, Fields{Values: values})
				}
				result, err := compiled.Template.Render(t.Context(), members)
				if err != nil {
					t.Fatal(err)
				}
				if result["title"] != row.Result["title"] {
					t.Fatal("member count title differs")
				}
				linkd = result["content"]
			default:
				t.Fatal("unknown comparison result")
			}
			if !reflect.DeepEqual(kac, tc.KAC) || !reflect.DeepEqual(linkd, tc.Linkd) {
				t.Fatalf("KAC=%#v expected=%#v; Linkd=%#v expected=%#v", kac, tc.KAC, linkd, tc.Linkd)
			}
			if tc.Difference == "" {
				if !reflect.DeepEqual(kac, linkd) {
					t.Fatal("unexplained difference")
				}
			} else {
				if reflect.DeepEqual(kac, linkd) {
					t.Fatal("expected difference disappeared")
				}
				differences++
			}
			reports = append(reports, map[string]any{"input": tc.Input, "kac": kac, "linkd": linkd, "difference": tc.Difference})
		})
	}
	for _, pair := range []struct {
		a, b               string
		kacSame, linkdSame bool
	}{{"numeric-one", "string-one", true, false}, {"bool-true", "string-true", true, false}, {"numeric-one", "decimal-one", false, true}, {"separator-left", "separator-right", true, false}} {
		if kacKeys[pair.a] == "" || kacKeys[pair.b] == "" || linkdKeys[pair.a] == "" || linkdKeys[pair.b] == "" {
			t.Fatal("comparison pair result missing")
		}
		if (kacKeys[pair.a] == kacKeys[pair.b]) != pair.kacSame || (linkdKeys[pair.a] == linkdKeys[pair.b]) != pair.linkdSame {
			t.Fatalf("unexpected grouping relationship: %+v", pair)
		}
		reports = append(reports, map[string]any{"group_pair": []string{pair.a, pair.b}, "kac_same": pair.kacSame, "linkd_same": pair.linkdSame, "difference": "typed tuple encoding and numeric normalization"})
	}
	if path := os.Getenv("LINKD_TEST_KAC_BEHAVIOR_COMPARISON_REPORT"); path != "" && !t.Failed() {
		raw, err := json.MarshalIndent(map[string]any{"source_sha256": response.Sources, "runtime": response.Runtime, "cases": reports}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		//nolint:gosec // G703: 报告路径由本机测试执行者显式指定，不接受 KAC 或业务载荷中的路径。
		if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// 源码漂移不得静默更新真值，也不能继续执行后面的 AST。
	changed := t.TempDir()
	file := filepath.Join(changed, "src", "kingeye", "kac", "alarm_merge", "utils.py")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("raise RuntimeError('must not execute')"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := runKACRules(t.Context(), python, changed, inputs[:1]); err == nil || !strings.Contains(stderr, "source snapshot mismatch") {
		t.Fatalf("changed source accepted: %v %s", err, stderr)
	}
	if !t.Failed() {
		t.Logf("verified %d source comparisons (%d declared behavior differences) and 4 grouping identity differences", len(cases), differences)
	}
}
