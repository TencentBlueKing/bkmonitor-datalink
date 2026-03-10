// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package service

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
)

func TestTimeSeriesScope_isMatchAutoRules(t *testing.T) {
	t.Run("empty rules", func(t *testing.T) {
		assert.False(t, isMatchAutoRules("", "cpu_usage"))
		assert.False(t, isMatchAutoRules("[]", "cpu_usage"))
	})

	t.Run("invalid json", func(t *testing.T) {
		assert.False(t, isMatchAutoRules("not_json", "cpu_usage"))
	})

	t.Run("match and not match", func(t *testing.T) {
		rules := `["^cpu_.*", "^mem_.*"]`
		assert.True(t, isMatchAutoRules(rules, "cpu_usage"))
		assert.False(t, isMatchAutoRules(rules, "disk_usage"))
	})

	t.Run("contains invalid regex", func(t *testing.T) {
		rules := `["[invalid", "^net_.*"]`
		assert.True(t, isMatchAutoRules(rules, "net_rx"))
	})
}

func TestTimeSeriesScope_getDimensionKeysFromMetricInfo(t *testing.T) {
	t.Run("from tag_value_list", func(t *testing.T) {
		item := map[string]any{
			"tag_value_list": map[string]any{
				"target": map[string]any{},
				"module": map[string]any{},
			},
		}

		dims := getDimensionKeysFromMetricInfo(item)
		assert.ElementsMatch(t, []string{"target", "module"}, dims)
	})

	t.Run("from tag_list", func(t *testing.T) {
		item := map[string]any{
			"tag_list": []any{
				map[string]any{"field_name": "target"},
				map[string]any{"field_name": "module"},
				map[string]any{"field_name": ""},
				"invalid",
			},
		}

		dims := getDimensionKeysFromMetricInfo(item)
		assert.ElementsMatch(t, []string{"target", "module"}, dims)
	})

	t.Run("prefer tag_value_list", func(t *testing.T) {
		item := map[string]any{
			"tag_value_list": map[string]any{
				"target": map[string]any{},
			},
			"tag_list": []any{
				map[string]any{"field_name": "module"},
			},
		}

		dims := getDimensionKeysFromMetricInfo(item)
		assert.ElementsMatch(t, []string{"target"}, dims)
	})

	t.Run("no tags", func(t *testing.T) {
		item := map[string]any{}
		dims := getDimensionKeysFromMetricInfo(item)
		assert.Empty(t, dims)
	})
}

func TestTimeSeriesScope_determineScopeNameForNewMetric(t *testing.T) {
	svc := NewTimeSeriesGroupSvc(&customreport.TimeSeriesGroup{
		MetricGroupDimensions: `[{"default_value":"default"}]`,
	})

	allScopes := []customreport.TimeSeriesScope{
		{
			ScopeName: "env||cpu",
			AutoRules: `["^cpu_.*"]`,
		},
		{
			ScopeName: "env||memory",
			AutoRules: `["^mem_.*"]`,
		},
	}

	t.Run("default scope matched by auto rules", func(t *testing.T) {
		scopeName, createFromDefault := determineScopeNameForNewMetric(&svc, "cpu_usage", "env||default", allScopes)
		assert.Equal(t, "env||cpu", scopeName)
		assert.False(t, createFromDefault)
	})

	t.Run("default scope fallback to default", func(t *testing.T) {
		scopeName, createFromDefault := determineScopeNameForNewMetric(&svc, "disk_usage", "env||default", allScopes)
		assert.Equal(t, "env||default", scopeName)
		assert.True(t, createFromDefault)
	})

	t.Run("non default scope keeps original", func(t *testing.T) {
		scopeName, createFromDefault := determineScopeNameForNewMetric(&svc, "cpu_usage", "env||cpu", allScopes)
		assert.Equal(t, "env||cpu", scopeName)
		assert.False(t, createFromDefault)
	})
}
