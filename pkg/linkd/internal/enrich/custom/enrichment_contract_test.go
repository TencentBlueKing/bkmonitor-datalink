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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/onemodel"
)

func TestLiteralReplacementDoesNotReprocessGeneratedText(t *testing.T) {
	p := compileTest(t, "fields", `{"rules":[{"id":"r","operations":[{"id":"replace","type":"replace","target":"$.content","replacements":[{"from":"A","to":"B"},{"from":"B","to":"C"}]}]}]}`)
	input := map[string]any{"content": "A B"}
	result, err := p.Execute(t.Context(), input, input, "t", Sources{})
	if err != nil {
		t.Fatal(err)
	}
	effective, err := domain.ApplyEnrichPatches(input, result.Patches)
	if err != nil || effective["content"] != "B C" {
		t.Fatal(effective, err)
	}
}

func TestAllMatchesRenderMixedTemplatesWithoutChangingGroupMeaning(t *testing.T) {
	p := compileTest(t, "fields", `{"rules":[{"id":"r","operations":[{"id":"extract","type":"extract","pattern":"(?<=ID=)(?<id>[0-9]+)","source":{"jsonpath":"$.event.content"},"match_mode":"all","separator":";","assignments":[{"target":"$.title","value":{"template":"${prefix}-${capture}","variables":{"prefix":{"jsonpath":"$.event.title"},"capture":{"jsonpath":"$.extraction.matches[0].groups[0]"}}}}]}]}]}`)
	input := map[string]any{"content": "ID=12 ID=34", "title": "告警"}
	for range 2 {
		result, err := p.Execute(t.Context(), input, input, "t", Sources{})
		if err != nil {
			t.Fatal(err)
		}
		effective, err := domain.ApplyEnrichPatches(input, result.Patches)
		if err != nil || effective["title"] != "告警-12;告警-34" || input["title"] != "告警" {
			t.Fatal(effective, err)
		}
	}
}

func TestExtractSelectsMatchAndPreservesValueWhenMissing(t *testing.T) {
	p := compileTest(t, "fields", `{"rules":[{"id":"r","operations":[{"id":"extract","type":"extract","pattern":"[0-9]+","source":{"jsonpath":"$.event.content"},"match_mode":"first","match_index":1,"assignments":[{"target":"$.title","value":{"jsonpath":"$.extraction.matches[0].text"}}]}]}]}`)
	for _, tc := range []struct{ text, want string }{{"12 34", "34"}, {"12", "keep"}, {"none", "keep"}, {strings.Repeat("12 ", 2000), "12"}} {
		input := map[string]any{"content": tc.text, "title": "keep"}
		result, err := p.Execute(t.Context(), input, input, "t", Sources{})
		if err != nil {
			t.Fatal(err)
		}
		effective, err := domain.ApplyEnrichPatches(input, result.Patches)
		if err != nil || effective["title"] != tc.want {
			t.Fatal(effective, err)
		}
	}
}

type branchReader struct {
	identityReader
	called []string
}

func (r *branchReader) Related(_ context.Context, tenant string, roots []onemodel.Instance, relation, direction string, q onemodel.Query) ([]onemodel.Instance, error) {
	r.called = append(r.called, relation)
	if tenant != "t" || len(roots) != 1 || direction != "out" {
		return nil, errors.New("wrong relation scope")
	}
	if relation == "broken" {
		return nil, errors.New("relation unavailable")
	}
	return []onemodel.Instance{{TenantID: tenant, ModelCode: q.ModelID, InstanceID: "2", Attributes: map[string]any{"count": 0, "enabled": false}}}, nil
}

func TestIndependentCMDBBranchesAndFieldsKeepSuccessfulIdentity(t *testing.T) {
	p := compileTest(t, "cmdb", `{"rules":[{"id":"host","identity":{"bk_obj_id":"host","model_name":"主机"},"lookup":{"model_id":"cw-Host","expect":"first"},"relations":[{"id":"bad","relation":"broken","direction":"out","model_id":"app","expect":"first"},{"id":"dependent","from":"bad","relation":"must-not-query","direction":"out","model_id":"owner","expect":"first"},{"id":"good","relation":"room","direction":"out","model_id":"room","expect":"first"}],"assignments":[{"target":"$.extra_data.owner","value":{"jsonpath":"$.lookup.attributes.owner"}},{"target":"$.extra_data.missing","value":{"jsonpath":"$.lookup.relations.bad.attributes.name"}},{"target":"$.extra_data.count","value":{"jsonpath":"$.lookup.relations.good.attributes.count","transforms":[{"type":"display_text"}]}},{"target":"$.extra_data.enabled","value":{"jsonpath":"$.lookup.relations.good.attributes.enabled","transforms":[{"type":"display_text"}]}}]}]}`)
	input := map[string]any{"extra_data": map[string]any{"bk_obj_id": "host", "bk_inst_id": "101", "missing": "keep"}}
	reader := &branchReader{}
	result, err := p.Execute(t.Context(), input, input, "t", Sources{Instances: reader})
	if err != nil || result.Status != domain.EnrichStatusPartial {
		t.Fatal(result, err)
	}
	effective, err := domain.ApplyEnrichPatches(input, result.Patches)
	if err != nil {
		t.Fatal(err)
	}
	values := effective["extra_data"].(map[string]any)
	if values["owner"] != "alice" || values["missing"] != "keep" || values["count"] != "0" || values["enabled"] != "false" {
		t.Fatal(values)
	}
	if effective["labels"].(map[string]any)["model_inst_id"] != "101" || !reflect.DeepEqual(reader.called, []string{"broken", "room"}) {
		t.Fatal(effective, reader.called)
	}
}

func TestMissingStepDoesNotPreventIndependentLaterStep(t *testing.T) {
	p := compileTest(t, "fields", `{"rules":[{"id":"r","operations":[{"id":"missing","type":"assign","assignments":[{"target":"$.content","value":{"jsonpath":"$.event.labels.absent"}}]},{"id":"later","type":"assign","assignments":[{"target":"$.title","value":{"literal":"updated"}}]}]}]}`)
	input := map[string]any{"title": "old", "content": "keep"}
	result, err := p.Execute(t.Context(), input, input, "t", Sources{})
	if err != nil || result.Status != domain.EnrichStatusPartial {
		t.Fatal(result, err)
	}
	effective, err := domain.ApplyEnrichPatches(input, result.Patches)
	if err != nil || effective["title"] != "updated" || effective["content"] != "keep" {
		t.Fatal(effective, err)
	}
}

func TestCMDBLiteralRegexIsValidatedAtPublication(t *testing.T) {
	var config map[string]any
	if err := json.Unmarshal([]byte(`{"rules":[{"id":"r","lookup":{"model_id":"host","where":{"field":"name","type":"keyword","operator":"not_regex","value":{"literal":"("}}}}]}`), &config); err != nil {
		t.Fatal(err)
	}
	_, err := Compile("cmdb", config)
	var invalid *ConfigurationError
	if !errors.As(err, &invalid) {
		t.Fatalf("invalid regex accepted: %v", err)
	}
}

func TestExistingIdentityCannotBypassRuleTenantScope(t *testing.T) {
	p := compileTest(t, "cmdb", `{"rules":[{"id":"host","bk_tenant_id":"owner","identity":{"bk_obj_id":"host","model_name":"主机"},"lookup":{"model_id":"cw-Host","expect":"first"},"assignments":[]}]}`)
	input := map[string]any{"labels": map[string]any{"model_id": "cw-Host", "model_inst_id": "101"}}
	result, err := p.Execute(t.Context(), input, input, "other", Sources{})
	if err != nil || result.Status != domain.EnrichStatusSkipped || len(result.Patches) != 0 {
		t.Fatalf("cross-tenant rule executed: %+v %v", result, err)
	}
}

func TestFallbackTopologyKeepsRuleTenantScope(t *testing.T) {
	for _, tenant := range []string{"owner", "t", ""} {
		p := compileTest(t, "cmdb", fmt.Sprintf(`{"rollback_unmatched":true,"rules":[{"id":"host","bk_tenant_id":%q,"identity":{"bk_obj_id":"host","model_name":"主机"},"lookup":{"model_id":"cw-Host","expect":"first"}}]}`, tenant))
		input := map[string]any{"extra_data": map[string]any{"bk_obj_id": "host", "bk_inst_id": "101"}}
		reader := &identityReader{}
		result, err := p.Execute(t.Context(), input, input, "t", Sources{Instances: reader})
		if err != nil {
			t.Fatal(err)
		}
		if tenant == "owner" {
			if len(result.Patches) != 0 || len(reader.queries) != 0 || result.Status != domain.EnrichStatusSkipped {
				t.Fatalf("foreign rule ran in fallback: %+v queries=%d", result, len(reader.queries))
			}
		} else if len(reader.queries) != 1 || len(result.Patches) == 0 {
			t.Fatalf("allowed identity was not bound: %+v", result)
		}
	}
}

func TestRegexConditionUsesTransformedPattern(t *testing.T) {
	for _, tc := range []struct {
		literal, pattern, operator string
		status                     domain.EnrichStatus
	}{
		{"^foo$", "^bar$", "regex", domain.EnrichStatusSucceeded},
		{"^foo$", "^bar$", "not_regex", domain.EnrichStatusSkipped},
		{"(", "^bar$", "regex", domain.EnrichStatusSucceeded},
		{"^foo$", "(", "not_regex", domain.EnrichStatusFailed},
	} {
		p := compileTest(t, "fields", fmt.Sprintf(`{"rules":[{"id":"r","when":{"left":{"literal":"bar"},"operator":%q,"right":{"literal":%q,"transforms":[{"type":"map","mapping":{%q:%q}}]}},"operations":[{"id":"assign","type":"assign","assignments":[{"target":"$.title","value":{"literal":"matched"}}]}]}]}`, tc.operator, tc.literal, tc.literal, tc.pattern))
		input := map[string]any{"title": "old"}
		for range 2 {
			result, err := p.Execute(t.Context(), input, input, "t", Sources{})
			if err != nil || result.Status != tc.status {
				t.Fatalf("%+v err=%v", result, err)
			}
		}
	}
}

func TestOptionalCaptureDefaultIsIndependentOfPathNotation(t *testing.T) {
	for _, path := range []string{"$.extraction.matches[0].groups[0]", "$['extraction']['matches'][0]['groups'][0]"} {
		for _, tc := range []struct {
			pattern, text string
			want          any
		}{
			{`(a)?b`, "b", "fallback"}, {`(a*)b`, "b", ""}, {`(a)?b`, "ab", "a"},
		} {
			p := compileTest(t, "fields", fmt.Sprintf(`{"rules":[{"id":"r","operations":[{"id":"extract","type":"extract","pattern":%q,"source":{"literal":%q},"match_mode":"first","assignments":[{"target":"$.extra_data.capture","value":{"jsonpath":%q,"default":{"literal":"fallback"}}}]}]}]}`, tc.pattern, tc.text, path))
			input := map[string]any{"extra_data": map[string]any{}}
			result, err := p.Execute(t.Context(), input, input, "t", Sources{})
			if err != nil {
				t.Fatal(err)
			}
			effective, err := domain.ApplyEnrichPatches(input, result.Patches)
			if err != nil || effective["extra_data"].(map[string]any)["capture"] != tc.want {
				t.Fatalf("%s: %v err=%v", path, effective, err)
			}
		}
	}
}

func TestExtractAllTruncatesUnicodeAndKeepsAtomicFailure(t *testing.T) {
	for _, target := range []string{"$.title", "$.subject_name", "$.content"} {
		p := compileTest(t, "fields", fmt.Sprintf(`{"rules":[{"id":"r","operations":[{"id":"extract","type":"extract","pattern":"a","source":{"literal":"aa"},"match_mode":"all","separator":"🚨分隔","assignments":[{"target":%q,"value":{"template":"${body}","variables":{"body":{"jsonpath":"$.event.content"}}}}]}]}]}`, target))
		body := strings.Repeat("警", 1999)
		input := map[string]any{"title": "old", "subject_name": "old", "content": body}
		result, err := p.Execute(t.Context(), input, input, "t", Sources{})
		if err != nil || result.Status != domain.EnrichStatusSucceeded {
			t.Fatalf("%+v %v", result, err)
		}
		effective, err := domain.ApplyEnrichPatches(input, result.Patches)
		want := body + "🚨"
		if target == "$.content" {
			want = body + "🚨分隔" + body
		}
		if err != nil || effective[strings.TrimPrefix(target, "$.")] != want {
			t.Fatal("incorrect truncation", err)
		}
	}
	for _, tc := range []struct {
		name, pattern, source, target, template, group string
		status                                         domain.EnrichStatus
	}{
		{"over budget", "a", "aaaaa", "$.content", "${body}", "$.extraction.matches[0].text", domain.EnrichStatusFailed},
		{"missing after truncation", "(a)?b", "ab b", "$.title", "${body}${group}", "$.extraction.matches[0].groups[0]", domain.EnrichStatusSkipped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := compileTest(t, "fields", fmt.Sprintf(`{"rules":[{"id":"r","operations":[{"id":"extract","type":"extract","pattern":%q,"source":{"literal":%q},"match_mode":"all","assignments":[{"target":"$.extra_data.first","value":{"literal":"not committed"}},{"target":%q,"value":{"template":%q,"variables":{"body":{"jsonpath":"$.event.content"},"group":{"jsonpath":%q}}}}]}]}]}`, tc.pattern, tc.source, tc.target, tc.template, tc.group))
			input := map[string]any{"title": "old", "content": strings.Repeat("x", 256<<10)}
			result, err := p.Execute(t.Context(), input, input, "t", Sources{})
			if err != nil || result.Status != tc.status || len(result.Patches) != 0 {
				t.Fatalf("operation partially committed: %+v err=%v", result, err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := p.Execute(ctx, input, input, "t", Sources{}); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}

func BenchmarkExtractAllBoundedTitle(b *testing.B) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(`{"rules":[{"id":"r","operations":[{"id":"extract","type":"extract","pattern":"a","source":{"jsonpath":"$.event.title"},"match_mode":"all","assignments":[{"target":"$.title","value":{"template":"${content}","variables":{"content":{"jsonpath":"$.event.content"}}}}]}]}]}`), &raw); err != nil {
		b.Fatal(err)
	}
	p, err := Compile("fields", raw)
	if err != nil {
		b.Fatal(err)
	}
	input := map[string]any{"title": strings.Repeat("a", 64), "content": strings.Repeat("x", 256<<10)}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		result, err := p.Execute(b.Context(), input, input, "t", Sources{})
		if err != nil || result.Status != domain.EnrichStatusSucceeded {
			b.Fatalf("status=%s err=%v", result.Status, err)
		}
	}
}
