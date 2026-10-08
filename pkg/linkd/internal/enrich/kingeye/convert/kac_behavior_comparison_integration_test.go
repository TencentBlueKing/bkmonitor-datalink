// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package convert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/enrich/custom"
	"linkd/internal/onemodel"
)

type comparisonInstances struct {
	rows   []map[string]any
	failed bool
}

func (r comparisonInstances) Search(_ context.Context, tenant string, q onemodel.Query) ([]onemodel.Instance, error) {
	if tenant != "t" || !q.First || q.Limit != 1 {
		return nil, fmt.Errorf("wrong query scope or selection")
	}
	if r.failed {
		return nil, errors.New("synthetic query failure")
	}
	rows := append([]map[string]any(nil), r.rows...)
	sort.Slice(rows, func(i, j int) bool { return fmt.Sprint(rows[i]["bk_host_id"]) < fmt.Sprint(rows[j]["bk_host_id"]) })
	var match func(onemodel.Filter, map[string]any) bool
	match = func(f onemodel.Filter, row map[string]any) bool {
		if f.Not != nil {
			return !match(*f.Not, row)
		}
		if len(f.All) > 0 {
			for _, child := range f.All {
				if !match(child, row) {
					return false
				}
			}
			return true
		}
		if len(f.Any) > 0 {
			for _, child := range f.Any {
				if match(child, row) {
					return true
				}
			}
			return false
		}
		field := strings.TrimPrefix(f.Field, "attributes.")
		value := row[field]
		if field == "model_inst_id" {
			value = row["bk_host_id"]
		}
		return fmt.Sprint(value) == fmt.Sprint(f.Value)
	}
	for _, row := range rows {
		if match(q.Where, row) {
			return []onemodel.Instance{{TenantID: tenant, ModelCode: q.ModelID, InstanceID: fmt.Sprint(row["bk_host_id"]), Attributes: row}}, nil
		}
	}
	return nil, nil
}

func (comparisonInstances) Related(context.Context, string, []onemodel.Instance, string, string, onemodel.Query) ([]onemodel.Instance, error) {
	return nil, errors.New("unexpected relation")
}

func (comparisonInstances) FindCMDBTopology(context.Context, string, string) (onemodel.ResourceTopology, bool, error) {
	return onemodel.ResourceTopology{}, false, nil
}

type comparisonDisplay struct{}

func (comparisonDisplay) Format(_ context.Context, _ string, _ string, _ string, v any) (any, error) {
	return v, nil
}

// TestKACCMDBBehaviorComparison 只在显式提供源码目录时读取固定 KAC 源码；不导入应用或连接外部服务。
func TestKACCMDBBehaviorComparison(t *testing.T) {
	root := os.Getenv("LINKD_KAC_SOURCE_DIR")
	if root == "" {
		t.Skip("set LINKD_KAC_SOURCE_DIR for fixed KAC source comparison")
	}
	python := os.Getenv("LINKD_KAC_PYTHON")
	if python == "" {
		python = "python3"
	}
	baseRule := map[string]any{"name": "host", "alarm_object_id": 1, "obj_rules": map[string]any{"expression": "A", "A": map[string]any{"field": "name", "value": "CPU", "condition": "term"}}, "inst_rules": map[string]any{"expression": "A", "A": map[string]any{"field": "ip", "value": "ip", "condition": "term"}}, "enrich_fields": []any{map[string]any{"key": "owner", "key_display": "负责人", "value": "owner"}}}
	instances := []map[string]any{{"bk_obj_id": "host", "bk_host_id": 202, "ip": "same", "owner": "bob"}, {"bk_obj_id": "host", "bk_host_id": 101, "ip": "same", "owner": "alice"}}
	cases := []map[string]any{
		{"alarm": map[string]any{"name": "CPU", "ip": "same"}},
		{"alarm": map[string]any{"name": "other", "ip": "same", "bk_obj_id": "host", "bk_inst_id": 202}},
		{"alarm": map[string]any{"name": "other", "ip": "same", "bk_obj_id": "host"}},
		{"alarm": map[string]any{"name": "CPU", "ip": "missing"}},
		{"alarm": map[string]any{"name": "CPU", "ip": "same"}, "query_error": true},
		{"alarm": map[string]any{"name": "CPU", "ip": "same", "bk_obj_id": "switch"}},
		{"alarm": map[string]any{"cw_object_model_code": "cw-Host", "cw_object_model_inst_id": 202}},
		{"alarm": map[string]any{"name": "CPU", "ip": "same"}, "empty_fields": true},
	}
	for _, input := range cases {
		encoded, _ := json.Marshal(baseRule)
		var rule map[string]any
		_ = json.Unmarshal(encoded, &rule)
		if input["empty_fields"] == true {
			rule["enrich_fields"] = []any{}
		}
		input["rules"] = []any{rule}
		input["instances"] = instances
	}
	encoded, _ := json.Marshal(cases)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	//nolint:gosec // G204: 显式本地源码对照，参数独立传递，不经过 shell。
	command := exec.CommandContext(ctx, python, "-B", filepath.Join("..", "..", "..", "..", "tests", "kac_behavior_comparison", "kac_enrich_behavior_comparison.py"), root)
	command.Stdin = bytes.NewReader(encoded)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("KAC comparison: %v %s", err, out)
	}
	var response struct {
		Sources map[string]string `json:"source_sha256"`
		Results []struct {
			KAC    map[string]any `json:"kac"`
			Config map[string]any `json:"config"`
		} `json:"results"`
	}
	decoder := json.NewDecoder(bytes.NewReader(out))
	decoder.UseNumber()
	if err = decoder.Decode(&response); err != nil {
		t.Fatal(err, string(out))
	}
	if len(response.Sources) != 2 || len(response.Results) != len(cases) {
		t.Fatal("incomplete comparison")
	}
	for index, expected := range response.Results {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			program, err := custom.Compile("cmdb", expected.Config)
			if err != nil {
				t.Fatal(err)
			}
			rawRule, _ := json.Marshal(cases[index]["rules"].([]any)[0])
			offline, err := Convert(Input{CMDBRules: []json.RawMessage{rawRule}, Models: map[string]Model{"1": {ModelID: "cw-Host", BKObjID: "host", Name: "主机", Attributes: map[string]onemodel.InstanceAttributeType{"ip": onemodel.InstanceAttributeKeyword}}}, FieldMappings: map[string]string{"ip": "$.extra_data.ip", "owner": "$.extra_data.owner"}})
			if err != nil || len(offline.Enrich.Processors) == 0 || offline.Report[0].Status != "converted" {
				t.Fatal(offline, err)
			}
			offlineProgram, err := custom.Compile("cmdb", offline.Enrich.Processors[0].Config)
			if err != nil {
				t.Fatal(err)
			}
			alarm := cases[index]["alarm"].(map[string]any)
			extra := map[string]any{"bk_obj_id": "", "bk_inst_id": "", "model_id": "", "model_name": "", "model_inst_id": "", "owner": ""}
			for key, value := range alarm {
				extra[key] = value
			}
			doc := map[string]any{"title": alarm["name"], "extra_data": extra}
			if doc["title"] == nil {
				doc["title"] = ""
			}
			for origin, program := range map[string]*custom.Program{"KAC publish": program, "offline": offlineProgram} {
				t.Run(origin, func(t *testing.T) {
					result, err := program.Execute(t.Context(), doc, doc, "t", custom.Sources{Instances: comparisonInstances{rows: instances, failed: cases[index]["query_error"] == true}, Display: comparisonDisplay{}})
					if err != nil {
						t.Fatal(err)
					}
					effective, err := domain.ApplyEnrichPatches(doc, result.Patches)
					if err != nil {
						t.Fatal(err)
					}
					flat := effective["extra_data"].(map[string]any)
					labels, _ := effective["labels"].(map[string]any)
					for key, value := range labels {
						flat[key] = value
					}
					for _, key := range []string{"bk_obj_id", "bk_inst_id", "model_id", "model_inst_id", "model_name", "owner"} {
						if fmt.Sprint(flat[key]) != fmt.Sprint(expected.KAC[key]) {
							t.Errorf("%s Linkd=%v KAC=%v trace=%+v", key, flat[key], expected.KAC[key], result.Trace)
						}
					}
				})
			}
		})
	}
}
