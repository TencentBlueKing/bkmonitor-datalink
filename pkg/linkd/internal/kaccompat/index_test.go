// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kaccompat

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"linkd/internal/config"
)

func TestCompatibleMappingAllowsESImplicitObjectAndExtraFields(t *testing.T) {
	mapping := alarmMapping()
	fields := mapping["properties"].(map[string]any)
	fields["field_extra_info"] = map[string]any{"properties": map[string]any{"strategy_name": map[string]any{"properties": map[string]any{"url": map[string]any{"type": "text"}}}}}
	fields["external_business_field"] = map[string]any{"type": "keyword"}
	mapping["dynamic"] = "true"
	if !compatibleMapping(mapping) {
		t.Fatal("ES canonical object mapping was rejected")
	}
	fields["status"] = map[string]any{"type": "text"}
	if compatibleMapping(mapping) {
		t.Fatal("incompatible status mapping accepted")
	}
}

// TestKACAlarmEventSchemaComparison 从指定固定 KAC 源码读取字段声明，不依赖 Django 或 ES 环境。
func TestKACAlarmEventSchemaComparison(t *testing.T) {
	root := os.Getenv("LINKD_TEST_KAC_SOURCE_ROOT")
	if root == "" {
		t.Skip("set LINKD_TEST_KAC_SOURCE_ROOT for source comparison")
	}
	python := os.Getenv("LINKD_TEST_KAC_PYTHON")
	if python == "" {
		python = "python3"
	}
	script := filepath.Join("..", "..", "tests", "kac_behavior_comparison", "kac_alarm_event_schema.py")
	//nolint:gosec // G204: 显式集成测试使用开发者指定的 Python 和仓库内固定只读脚本。
	command := exec.CommandContext(t.Context(), python, script, root)
	raw, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("schema comparison: %v: %s", err, raw)
	}
	var expected map[string]any
	if err = json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(expected, alarmMapping()) {
		t.Fatal("KAC alarm_event mapping differs from fixed source")
	}
}

func TestMetadataIndexCannotMatchKACTemplate(t *testing.T) {
	for _, alias := range []string{"linkd", "linkd-kac-state", "cw_kac_saas_3.0_alarm_event"} {
		c := newClient(config.KACPluginConfig{AlarmEventIndex: alias}, nil, nil, nil)
		if strings.HasPrefix(c.stateIndex, alias) || !strings.HasPrefix(c.stateIndex, ".") {
			t.Fatal("metadata may inherit KAC rollover template", alias)
		}
	}
}

// 历史代际缺字段可追加，已有字段冲突仍不可用；追加失败不得宣称目标就绪。
func TestMaintainHistoricalMappings(t *testing.T) {
	for _, tc := range []struct {
		name     string
		missing  []string
		conflict bool
		denied   bool
	}{
		{name: "complete"},
		{name: "oldest", missing: []string{"strategy_id", "metric_unique_id", "bk_tenant_id", "strategy_config_uid", "strategy_config_version"}},
		{name: "v1", missing: []string{"metric_unique_id", "bk_tenant_id", "strategy_config_uid", "strategy_config_version"}},
		{name: "v2_v3", missing: []string{"strategy_config_uid", "strategy_config_version"}},
		{name: "conflict", missing: []string{"strategy_id"}, conflict: true},
		{name: "denied", missing: []string{"strategy_config_uid"}, denied: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			historical := alarmMapping()
			props := historical["properties"].(map[string]any)
			for _, name := range tc.missing {
				delete(props, name)
			}
			if tc.conflict {
				props["status"] = map[string]any{"type": "text"}
			}
			f := newFakeES()
			c := newClient(config.KACPluginConfig{AlarmEventIndex: f.alias}, nil, nil, nil)
			c.ready.Store(true)
			puts := 0
			c.transport = mappingTransport(func(req *http.Request) (*http.Response, error) {
				switch {
				case req.Method == http.MethodPut && strings.HasPrefix(req.URL.Path, "/_ilm/"):
					return jsonResponse(200, map[string]any{"acknowledged": true}), nil
				case req.URL.Path == "/_template/"+f.alias:
					return jsonResponse(200, map[string]any{f.alias: map[string]any{"mappings": alarmMapping(), "settings": map[string]any{"index": c.settings()}, "index_patterns": []string{f.alias + "*"}}}), nil
				case req.URL.Path == "/_alias/"+f.alias:
					return jsonResponse(200, map[string]any{f.index: map[string]any{"aliases": map[string]any{f.alias: map[string]any{"is_write_index": true}}}}), nil
				case req.URL.Path == "/"+f.alias+"/_mapping":
					return jsonResponse(200, map[string]any{f.index: map[string]any{"mappings": alarmMapping()}, f.alias + "_v1-000001": map[string]any{"mappings": historical}}), nil
				case req.URL.Path == "/"+f.alias+"_v1-000001/_mapping" && req.Method == http.MethodPut:
					puts++
					if tc.denied {
						return jsonResponse(403, nil), nil
					}
					var body map[string]map[string]any
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					patch := body["properties"]
					if len(body) != 1 || len(patch) != len(tc.missing) {
						t.Fatalf("unexpected patch: %#v", body)
					}
					for _, name := range tc.missing {
						if !reflect.DeepEqual(patch[name], alarmMapping()["properties"].(map[string]any)[name]) {
							t.Fatalf("wrong definition: %s", name)
						}
						props[name] = patch[name]
					}
					return jsonResponse(200, map[string]any{"acknowledged": true}), nil
				case req.URL.Path == "/"+c.stateIndex+"/_mapping":
					return jsonResponse(200, map[string]any{}), nil
				default:
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
					return nil, nil
				}
			})
			err := c.Maintain(t.Context())
			if tc.conflict || tc.denied {
				if err == nil || c.ready.Load() {
					t.Fatal("invalid mapping reported ready")
				}
				if tc.conflict && puts != 0 {
					t.Fatal("conflicting schema was modified")
				}
				return
			}
			if err != nil || !c.ready.Load() {
				t.Fatalf("maintain: %v", err)
			}
			want := 0
			if len(tc.missing) > 0 {
				want = 1
			}
			if puts != want {
				t.Fatalf("puts=%d want=%d", puts, want)
			}
			if err = c.Maintain(t.Context()); err != nil || puts != want {
				t.Fatalf("non-idempotent maintenance: %v puts=%d", err, puts)
			}
		})
	}
}

type mappingTransport func(*http.Request) (*http.Response, error)

func (f mappingTransport) Perform(r *http.Request) (*http.Response, error) { return f(r) }
