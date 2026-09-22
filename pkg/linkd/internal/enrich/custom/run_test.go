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
	"sync"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/onemodel"
)

func compileTest(t *testing.T, kind, config string) *Program {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal([]byte(config), &raw); err != nil {
		t.Fatal(err)
	}
	p, err := Compile(kind, raw)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRuleMatchesOnceAndOperationsSeeEarlierWrites(t *testing.T) {
	p := compileTest(t, "fields", `{"rules":[{"id":"normalize","when":{"left":{"jsonpath":"$.alert.content"},"operator":"contains","right":{"literal":"alarm"}},"operations":[{"id":"replace1","type":"replace","target":"$.content","replacements":[{"from":"alarm1","to":"alarm22"}]},{"id":"replace2","type":"replace","target":"$.content","replacements":[{"from":"alarm221","to":"success"}]}]},{"id":"title","when":{"left":{"jsonpath":"$.alert.content"},"operator":"eq","right":{"literal":"success"}},"operations":[{"id":"assign","type":"assign","assignments":[{"target":"$.title","value":{"template":"${content}-1","variables":{"content":{"jsonpath":"$.alert.content"}}}}]}]}]}`)
	input := map[string]any{"content": "alarm11", "title": "original"}
	run, err := p.Execute(t.Context(), input, input, "tenant-a", Sources{})
	if err != nil {
		t.Fatal(err)
	}
	effective, err := domain.ApplyEnrichPatches(input, run.Patches)
	if err != nil {
		t.Fatal(err)
	}
	if effective["content"] != "success" || effective["title"] != "success-1" || input["content"] != "alarm11" || run.Status != domain.EnrichStatusSucceeded {
		t.Fatalf("result=%#v input=%#v status=%s", effective, input, run.Status)
	}
}

func TestExtractGroupsMultipleTargetsAndAtomicFailure(t *testing.T) {
	p := compileTest(t, "fields", `{"rules":[{"id":"extract","operations":[{"id":"parts","type":"extract","source":{"jsonpath":"$.alert.content"},"pattern":"host=([^,]+),zone=([0-9]+)","assignments":[{"target":"$.labels.host","value":{"jsonpath":"$.extraction.matches[0].groups[0]"}},{"target":"$.labels.zone","value":{"jsonpath":"$.extraction.matches[0].groups[1]","transforms":[{"type":"number"}]}}]},{"id":"atomic","type":"assign","assignments":[{"target":"$.labels.should_not_exist","value":{"literal":"no"}},{"target":"$.labels.bad","value":{"literal":{}}}]}]},{"id":"next","operations":[{"id":"bool","type":"assign","assignments":[{"target":"$.labels.enabled","value":{"literal":false}}]}]}]}`)
	input := map[string]any{"content": "host=node,zone=0", "labels": map[string]any{}}
	result, err := p.Execute(t.Context(), input, input, "tenant-a", Sources{})
	if err != nil {
		t.Fatal(err)
	}
	effective, err := domain.ApplyEnrichPatches(input, result.Patches)
	if err != nil {
		t.Fatal(err)
	}
	labels := effective["labels"].(map[string]any)
	if labels["host"] != "node" || labels["zone"] != json.Number("0") || labels["enabled"] != false || labels["should_not_exist"] != nil || result.Status != domain.EnrichStatusPartial {
		t.Fatalf("%#v status=%s", labels, result.Status)
	}
}

func TestJSONPathFiltersMissingNullAndConcurrency(t *testing.T) {
	p := compileTest(t, "fields", `{"rules":[{"id":"read","operations":[{"id":"select","type":"assign","assignments":[{"target":"$.extra_data.selected","value":{"jsonpath":"$.alert.extra_data.items[?@.active == true].name","select":"all"}},{"target":"$.extra_data.null","value":{"literal":null}},{"target":"$.labels.zero","value":{"jsonpath":"$.alert.labels.absent","default":{"literal":0}}}]}]}]}`)
	input := map[string]any{"labels": map[string]any{}, "extra_data": map[string]any{"items": []any{map[string]any{"name": "a", "active": true}, map[string]any{"name": "b", "active": false}}}}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			result, err := p.Execute(t.Context(), input, input, "tenant-a", Sources{})
			if err != nil || result.Status != domain.EnrichStatusSucceeded || len(result.Patches) != 3 {
				t.Errorf("result=%#v err=%v", result, err)
			}
		})
	}
	wg.Wait()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.Execute(ctx, input, input, "tenant-a", Sources{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
}

func TestRejectInvalidConfiguration(t *testing.T) {
	for _, config := range []string{
		`{"rules":[{"id":"x","operations":[{"id":"op","type":"assign","assignments":[{"target":"$.severity","value":{"literal":"fatal"}}]}]}]}`,
		`{"rules":[{"id":"x","operations":[{"id":"op","type":"extract","pattern":"(?<=x)y","source":{"literal":"xy"},"assignments":[{"target":"$.title","value":{"literal":"x"}}]}]}]}`,
		`{"rules":[{"id":"x","operations":[{"id":"op","type":"assign","assignments":[{"target":"$.extra_data.x","value":{"literal":{}}},{"target":"$.extra_data.x.y","value":{"literal":1}}]}]}]}`,
		`{"rules":[],"typo":true}`,
	} {
		var raw map[string]any
		if err := json.Unmarshal([]byte(config), &raw); err != nil {
			t.Fatal(err)
		}
		if _, err := Compile("fields", raw); err == nil {
			t.Fatalf("accepted %s", config)
		}
	}
}

type fakeReader struct {
	items  []onemodel.Instance
	calls  int
	tenant string
	filter onemodel.Filter
}

func (f *fakeReader) Search(_ context.Context, tenant string, q onemodel.Query) ([]onemodel.Instance, error) {
	f.calls++
	f.tenant = tenant
	f.filter = q.Where
	return f.items, nil
}

func (f *fakeReader) Related(context.Context, string, []onemodel.Instance, string, string, onemodel.Query) ([]onemodel.Instance, error) {
	return nil, errors.New("unexpected related query")
}

func TestCMDBTypedInputAndMultipleMatches(t *testing.T) {
	p := compileTest(t, "cmdb", `{"rules":[{"id":"host","lookup":{"model_id":"cw-Host","expect":"one","where":{"field":"attributes.bk_cloud_id","type":"long","operator":"eq","value":{"jsonpath":"$.alert.labels.cloud"}}},"assignments":[{"target":"$.labels.owner","value":{"jsonpath":"$.lookup.attributes.operator"}}]}]}`)
	input := map[string]any{"labels": map[string]any{"cloud": 0}}
	reader := &fakeReader{items: []onemodel.Instance{{TenantID: "tenant-a", ModelCode: "cw-Host", InstanceID: "1", Attributes: map[string]any{"operator": "alice"}}}}
	result, err := p.Execute(t.Context(), input, input, "tenant-a", Sources{Instances: reader})
	if err != nil || result.Status != domain.EnrichStatusSucceeded || reader.tenant != "tenant-a" || reader.filter.Value != 0 {
		t.Fatalf("%#v err=%v reader=%#v", result, err, reader)
	}
	reader.items = append(reader.items, reader.items[0])
	result, err = p.Execute(t.Context(), input, input, "tenant-a", Sources{Instances: reader})
	if err != nil || result.Status != domain.EnrichStatusFailed || len(result.Patches) != 0 {
		t.Fatalf("ambiguity=%#v err=%v", result, err)
	}
}
