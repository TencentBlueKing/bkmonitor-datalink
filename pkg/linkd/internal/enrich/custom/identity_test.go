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
	"sync"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/onemodel"
)

type identityReader struct {
	queries     []onemodel.Query
	fail        bool
	empty       bool
	topologyErr bool
}

func (r *identityReader) Search(ctx context.Context, tenant string, q onemodel.Query) ([]onemodel.Instance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if tenant != "t" {
		return nil, fmt.Errorf("wrong tenant")
	}
	r.queries = append(r.queries, q)
	if r.fail {
		return nil, errors.New("query failure")
	}
	if r.empty {
		return nil, nil
	}
	id := "101"
	if q.Where.Field == "model_inst_id" {
		id = fmt.Sprint(q.Where.Value)
	}
	return []onemodel.Instance{{TenantID: tenant, ModelCode: q.ModelID, InstanceID: id, Attributes: map[string]any{"bk_host_id": json.Number(id), "owner": "alice"}}}, nil
}

func (*identityReader) Related(context.Context, string, []onemodel.Instance, string, string, onemodel.Query) ([]onemodel.Instance, error) {
	return nil, errors.New("unexpected related")
}

func (r *identityReader) FindCMDBTopology(context.Context, string, string) (onemodel.ResourceTopology, bool, error) {
	if r.topologyErr {
		return onemodel.ResourceTopology{}, false, errors.New("topology failure")
	}
	return onemodel.ResourceTopology{BKBizID: 2, BKBizName: "业务", BKSetIDs: []int64{3, 4}, BKSetNames: []string{"集群A", "集群B"}, BKModuleIDs: []int64{7, 8}, BKModuleNames: []string{"模块A", "模块B"}}, true, nil
}

const identityRule = `{"id":"host","identity":{"bk_obj_id":"host","model_name":"主机"},"when":{"left":{"literal":"yes"},"operator":"eq","right":{"jsonpath":"$.event.extra_data.match","default":{"literal":"no"}}},"lookup":{"model_id":"cw-Host","expect":"first","where":{"field":"attributes.ip","type":"keyword","operator":"eq","value":{"jsonpath":"$.event.extra_data.ip"}}}}`

func TestCMDBIdentityBranches(t *testing.T) {
	for _, tc := range []struct {
		name, input     string
		calls           int
		status          domain.EnrichStatus
		model, instance string
		failed, empty   bool
	}{
		{name: "无身份匹配后补模型实例", input: `{"match":"yes","ip":"10.0.0.1"}`, calls: 1, status: domain.EnrichStatusSucceeded, model: "cw-Host", instance: "101"},
		{name: "对象身份已有时跳过对象条件", input: `{"bk_obj_id":"host","ip":"10.0.0.1"}`, calls: 1, status: domain.EnrichStatusSucceeded, instance: "101"},
		{name: "已有实例忽略缺失查询字段", input: `{"bk_obj_id":"host","bk_inst_id":202}`, calls: 1, status: domain.EnrichStatusSucceeded, instance: "202"},
		{name: "已有其他对象不重绑", input: `{"bk_obj_id":"switch","match":"yes","ip":"10.0.0.1"}`, status: domain.EnrichStatusSkipped},
		{name: "对象未匹配不查询", input: `{"ip":"10.0.0.1"}`, status: domain.EnrichStatusSkipped},
		{name: "查空保留模型阶段", input: `{"match":"yes","ip":"10.0.0.1"}`, calls: 1, status: domain.EnrichStatusPartial, model: "cw-Host", empty: true},
		{name: "查询失败保留模型阶段", input: `{"match":"yes","ip":"10.0.0.1"}`, calls: 1, status: domain.EnrichStatusPartial, model: "cw-Host", failed: true},
		{name: "旧canonical身份归一化", input: `{"cw_object_model_code":"cw-Host","cw_object_model_inst_id":"303"}`, calls: 1, status: domain.EnrichStatusSucceeded, instance: "303"},
		{name: "其他canonical模型不被重绑", input: `{"cw_object_model_code":"cw-Service","cw_object_model_inst_id":"303","match":"yes"}`, status: domain.EnrichStatusSkipped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := compileTest(t, "cmdb", `{"rules":[`+identityRule+`]}`)
			input := map[string]any{}
			if err := json.Unmarshal([]byte(`{"extra_data":`+tc.input+`}`), &input); err != nil {
				t.Fatal(err)
			}
			reader := &identityReader{fail: tc.failed, empty: tc.empty}
			result, err := p.Execute(t.Context(), input, input, "t", Sources{Instances: reader})
			if err != nil || result.Status != tc.status || len(reader.queries) != tc.calls {
				t.Fatalf("result=%+v err=%v queries=%+v", result, err, reader.queries)
			}
			effective, err := domain.ApplyEnrichPatches(input, result.Patches)
			if err != nil {
				t.Fatal(err)
			}
			values, _ := effective["labels"].(map[string]any)
			if tc.model != "" && values["model_id"] != tc.model {
				t.Fatal(values)
			}
			if tc.instance != "" && values["model_inst_id"] != tc.instance {
				t.Fatal(values)
			}
			if len(reader.queries) > 0 {
				q := reader.queries[0]
				if !q.First || q.Limit != 1 {
					t.Fatal(q)
				}
				if tc.instance == "202" && q.Where.Field != "model_inst_id" {
					t.Fatal(q)
				}
			}
		})
	}
}

func TestCMDBModelOnlyAndRuleOrder(t *testing.T) {
	p := compileTest(t, "cmdb", `{"rules":[{"id":"first","identity":{"bk_obj_id":"host","model_name":"主机"},"when":{"left":{"literal":1},"operator":"eq","right":{"literal":1}},"lookup":{"model_id":"cw-Host","expect":"first"}}, {"id":"second","identity":{"bk_obj_id":"service","model_name":"服务"},"when":{"left":{"literal":1},"operator":"eq","right":{"literal":1}},"lookup":{"model_id":"cw-Service","expect":"first"}}]}`)
	input := map[string]any{"extra_data": map[string]any{}}
	result, err := p.Execute(t.Context(), input, input, "t", Sources{})
	if err != nil || result.Status != domain.EnrichStatusPartial {
		t.Fatal(result, err)
	}
	effective, _ := domain.ApplyEnrichPatches(input, result.Patches)
	if effective["labels"].(map[string]any)["model_id"] != "cw-Service" {
		t.Fatal(effective)
	}
	noWhen := compileTest(t, "cmdb", `{"rules":[{"id":"host","identity":{"bk_obj_id":"host","model_name":"主机"},"lookup":{"model_id":"cw-Host","expect":"first"}}]}`)
	result, err = noWhen.Execute(t.Context(), input, input, "t", Sources{})
	if err != nil || result.Status != domain.EnrichStatusSkipped {
		t.Fatal(result, err)
	}
}

func TestCMDBTopologyFailureStillAssignsAndPreservesIdentity(t *testing.T) {
	var config map[string]any
	_ = json.Unmarshal([]byte(`{"rules":[`+identityRule+`]}`), &config)
	config["rules"].([]any)[0].(map[string]any)["assignments"] = []any{map[string]any{"target": "$.extra_data.owner", "value": map[string]any{"jsonpath": "$.lookup.attributes.owner"}}}
	p, err := Compile("cmdb", config)
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"extra_data": map[string]any{"match": "yes", "ip": "ip", "bk_set_id": "existing"}}
	for _, fail := range []bool{false, true} {
		result, err := p.Execute(t.Context(), input, input, "t", Sources{Instances: &identityReader{topologyErr: fail}})
		if err != nil {
			t.Fatal(err)
		}
		effective, _ := domain.ApplyEnrichPatches(input, result.Patches)
		values := effective["extra_data"].(map[string]any)
		if values["owner"] != "alice" || effective["labels"].(map[string]any)["model_inst_id"] != "101" || values["bk_set_id"] != "existing" {
			t.Fatal(values)
		}
		if fail && result.Status != domain.EnrichStatusPartial {
			t.Fatal(result)
		}
		if !fail {
			if len(values["bk_module_id"].([]any)) != 2 {
				t.Fatal(values)
			}
		}
	}
}

func TestCMDBIdentityConcurrentAndCancellation(t *testing.T) {
	p := compileTest(t, "cmdb", `{"rules":[`+identityRule+`]}`)
	input := map[string]any{"extra_data": map[string]any{"bk_obj_id": "host", "bk_inst_id": "101"}}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			result, err := p.Execute(t.Context(), input, input, "t", Sources{Instances: &identityReader{}})
			if err != nil || result.Status != domain.EnrichStatusSucceeded {
				t.Error(result, err)
			}
		})
	}
	wg.Wait()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.Execute(ctx, input, input, "t", Sources{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestCMDBIdentityRejectsUnsafeConfig(t *testing.T) {
	for _, rule := range []string{
		`{"id":"x","identity":{"bk_obj_id":"host"},"lookup":{"model_id":"cw-Host","expect":"one"}}`,
		`{"id":"x","identity":{"bk_obj_id":"host","fields":{"model_id":"$.subject.id"}},"lookup":{"model_id":"cw-Host","expect":"first"}}`,
		`{"id":"x","identity":{"bk_obj_id":"host","fields":{"model_id":"$.extra_data.x","model_name":"$.extra_data.x"}},"lookup":{"model_id":"cw-Host","expect":"first"}}`,
	} {
		var config map[string]any
		_ = json.Unmarshal([]byte(`{"rules":[`+rule+`]}`), &config)
		if _, err := Compile("cmdb", config); err == nil {
			t.Fatal(rule)
		}
	}
}

func TestConvertedCMDBRollsBackOnlyWhenNoFieldSucceeded(t *testing.T) {
	for _, success := range []bool{false, true} {
		var raw map[string]any
		_ = json.Unmarshal([]byte(`{"rollback_unmatched":true,"rules":[`+identityRule+`]}`), &raw)
		if success {
			raw["rules"].([]any)[0].(map[string]any)["assignments"] = []any{map[string]any{"target": "$.extra_data.owner", "value": map[string]any{"literal": "alice"}}}
		}
		p, err := Compile("cmdb", raw)
		if err != nil {
			t.Fatal(err)
		}
		input := map[string]any{"extra_data": map[string]any{"match": "yes", "ip": "ip"}}
		result, err := p.Execute(t.Context(), input, input, "t", Sources{Instances: &identityReader{}})
		if err != nil {
			t.Fatal(err)
		}
		effective, _ := domain.ApplyEnrichPatches(input, result.Patches)
		labels, _ := effective["labels"].(map[string]any)
		if success && labels["model_id"] != "cw-Host" {
			t.Fatal(effective)
		}
		if !success && len(result.Patches) != 0 {
			t.Fatal(result)
		}
	}
}

func TestUnmatchedCMDBFallbackUsesOnlyInitialIdentity(t *testing.T) {
	p := compileTest(t, "cmdb", `{"rollback_unmatched":true,"rules":[{"id":"host","identity":{"bk_obj_id":"host","model_name":"主机"},"lookup":{"model_id":"cw-Host","expect":"first"}}]}`)
	input := map[string]any{"extra_data": map[string]any{"bk_obj_id": "host", "bk_inst_id": "101"}}
	result, err := p.Execute(t.Context(), input, input, "t", Sources{Instances: &identityReader{}})
	if err != nil {
		t.Fatal(err)
	}
	effective, err := domain.ApplyEnrichPatches(input, result.Patches)
	if err != nil {
		t.Fatal(err)
	}
	labels, _ := effective["labels"].(map[string]any)
	if labels["model_id"] != nil || labels["model_inst_id"] != nil || len(effective["extra_data"].(map[string]any)["bk_module_id"].([]any)) != 2 {
		t.Fatal(effective)
	}
}

func TestKingeyeAttributeTextKeepsZeroFalseNullAndLists(t *testing.T) {
	for _, tc := range []struct {
		input any
		want  string
	}{
		{json.Number("0"), "0"}, {false, "False"}, {true, "True"}, {nil, "None"}, {[]any{"alice", "bob"}, "['alice', 'bob']"},
	} {
		if got := kingeyeText(tc.input); got != tc.want {
			t.Errorf("%v=%q want=%q", tc.input, got, tc.want)
		}
	}
	for _, v := range []any{json.Number("0"), json.Number("0.0"), json.Number("-0"), false, "", nil} {
		if identityProvided(v) {
			t.Errorf("zero identity accepted: %v", v)
		}
	}
}

func TestLegacyCMDBAssignmentsKeepEarlierFieldsOnFailure(t *testing.T) {
	var raw map[string]any
	_ = json.Unmarshal([]byte(`{"rollback_unmatched":true,"rules":[`+identityRule+`]}`), &raw)
	rule := raw["rules"].([]any)[0].(map[string]any)
	rule["assignments"] = []any{
		map[string]any{"target": "$.extra_data.owner", "value": map[string]any{"literal": "first"}},
		map[string]any{"target": "$.extra_data.owner", "value": map[string]any{"literal": "second"}},
		map[string]any{"target": "$.extra_data.unavailable", "value": map[string]any{"jsonpath": "$.lookup.attributes.absent"}},
	}
	p, err := Compile("cmdb", raw)
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"extra_data": map[string]any{"match": "yes", "ip": "ip"}}
	result, err := p.Execute(t.Context(), input, input, "t", Sources{Instances: &identityReader{}})
	if err != nil || result.Status != domain.EnrichStatusPartial {
		t.Fatal(result, err)
	}
	effective, err := domain.ApplyEnrichPatches(input, result.Patches)
	if err != nil {
		t.Fatal(err)
	}
	if effective["extra_data"].(map[string]any)["owner"] != "second" || effective["labels"].(map[string]any)["model_inst_id"] != "101" {
		t.Fatal(effective)
	}
}
