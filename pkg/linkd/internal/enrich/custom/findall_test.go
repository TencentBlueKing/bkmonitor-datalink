// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package custom

import (
	"fmt"
	"strings"
	"testing"

	"linkd/internal/domain"
)

func TestFindAllUsesMatchOrGroupNumbering(t *testing.T) {
	for _, tc := range []struct {
		name, pattern, text, path, want string
	}{
		{"no_group_second_match", `[0-9]+`, "x12 y34", "$.extraction.values[1]", "34"},
		{"single_group_second_match", `ID=([0-9]+)`, "ID=12 ID=34", "$.extraction.values[1]", "34"},
		{"named_group_second_match", `ID=(?<id>[0-9]+)`, "ID=12 ID=34", "$.extraction.values[1]", "34"},
		{"multiple_groups_first_match", `([a-z]+)=([0-9]+)`, "a=12 b=34", "$.extraction.values[1]", "12"},
		{"multiple_groups_only_scan_first", `([a-z]+)=([0-9]+)`, strings.Repeat("a=12 ", 1100), "$.extraction.values[1]", "12"},
		{"whole_match", `ID=([0-9]+)`, "ID=12 ID=34", "$.extraction.matches[0].text", "ID=12"},
		{"raw_groups_unchanged", `([a-z]+)=([0-9]+)`, "a=12 b=34", "$.extraction.matches[0].groups[1]", "12"},
		{"single_optional_group", `(a)?b`, "b", "$.extraction.values[0]", ""},
		{"multiple_optional_groups", `(a)?(b)`, "b", "$.extraction.values[0]", ""},
		{"raw_optional_group_stays_missing", `(a)?(b)`, "b", "$.extraction.matches[0].groups[0]", "keep"},
		{"empty_capture", `(a*)b`, "b", "$.extraction.values[0]", ""},
		{"missing_second_match", `([0-9]+)`, "12", "$.extraction.values[1]", "keep"},
		{"no_match", `([0-9]+)`, "none", "$.extraction.values[0]", "keep"},
		{"match_budget_boundary", `([0-9]+)`, strings.Repeat("12 ", 1024), "$.extraction.values[1023]", "12"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := compileTest(t, "fields", fmt.Sprintf(`{"rules":[{"id":"r","operations":[{"id":"extract","type":"extract","pattern":%q,"source":{"jsonpath":"$.event.content"},"match_mode":"findall","match_index":1,"separator":";","assignments":[{"target":"$.title","value":{"jsonpath":%q}}]}]}]}`, tc.pattern, tc.path))
			for _, tenant := range []string{"tenant-a", "tenant-b"} {
				input := map[string]any{"content": tc.text, "title": "keep"}
				result, err := p.Execute(t.Context(), input, input, tenant, Sources{})
				if err != nil {
					t.Fatal(err)
				}
				effective, err := domain.ApplyEnrichPatches(input, result.Patches)
				if err != nil || effective["title"] != tc.want || input["title"] != "keep" {
					t.Fatalf("effective=%v err=%v", effective, err)
				}
			}
		})
	}
}

func TestFindAllKeepsAtomicFailureAtExistingMatchBudget(t *testing.T) {
	p := compileTest(t, "fields", `{"rules":[{"id":"r","operations":[{"id":"extract","type":"extract","pattern":"([0-9]+)","source":{"jsonpath":"$.event.content"},"match_mode":"findall","match_index":1,"separator":";","assignments":[{"target":"$.title","value":{"jsonpath":"$.extraction.values[0]"}}]}]}]}`)
	input := map[string]any{"content": strings.Repeat("12 ", 1025), "title": "keep"}
	result, err := p.Execute(t.Context(), input, input, "tenant", Sources{})
	if err != nil || result.Status != domain.EnrichStatusFailed || len(result.Patches) != 0 {
		t.Fatalf("status=%s patches=%v err=%v", result.Status, result.Patches, err)
	}
}

func TestFindAllRendersMixedTemplateOnce(t *testing.T) {
	p := compileTest(t, "fields", `{"rules":[{"id":"r","operations":[{"id":"extract","type":"extract","pattern":"ID=([0-9]+)","source":{"jsonpath":"$.event.content"},"match_mode":"findall","assignments":[{"target":"$.title","value":{"template":"${prefix}-${whole}/${first}/${second}/${twelfth}","variables":{"prefix":{"jsonpath":"$.event.title"},"whole":{"jsonpath":"$.extraction.matches[0].text"},"first":{"jsonpath":"$.extraction.values[0]"},"second":{"jsonpath":"$.extraction.values[1]"},"twelfth":{"jsonpath":"$.extraction.values[11]"}}}}]}]}]}`)
	input := map[string]any{"content": "ID=1 ID=2 ID=3 ID=4 ID=5 ID=6 ID=7 ID=8 ID=9 ID=10 ID=11 ID=12", "title": "告警"}
	result, err := p.Execute(t.Context(), input, input, "tenant", Sources{})
	if err != nil {
		t.Fatal(err)
	}
	effective, err := domain.ApplyEnrichPatches(input, result.Patches)
	if err != nil || effective["title"] != "告警-ID=1/1/2/12" || len(result.Patches) != 1 {
		t.Fatalf("effective=%v patches=%v err=%v", effective, result.Patches, err)
	}
}
