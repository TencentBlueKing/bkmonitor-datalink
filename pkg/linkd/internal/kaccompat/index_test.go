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
