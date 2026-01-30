// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package relation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestToResourceMap_EndToEnd 测试完整数据流：CMDB -> Builder -> Metrics
func TestToResourceMap_EndToEnd(t *testing.T) {
	builder := newRelationMetricsBuilder()

	// 模拟主机数据（包含 ToResourceMap）
	hostInfos := []*Info{
		{
			ID:       "135",
			Resource: "host",
			Label: map[string]string{
				"bk_host_id": "135",
			},
			Links: []Link{
				{
					{ID: "109", Resource: "module", Label: map[string]string{"bk_module_id": "109"}},
					{ID: "21", Resource: "set", Label: map[string]string{"bk_set_id": "21"}},
					{ID: "7", Resource: "biz", Label: map[string]string{"bk_biz_id": "7"}},
				},
			},
			ToResourceMap: map[string]map[string]map[string]any{
				"host": {
					"app_version": {
						"app_name": "user-service",
						"version":  "2.0.1",
						"build_id": "12345",
					},
					"service_name": {
						"name":      "user-svc",
						"namespace": "production",
					},
				},
			},
		},
	}

	// 构建缓存
	err := builder.BuildInfosCache(context.Background(), 7, "host", hostInfos)
	require.NoError(t, err)

	// 获取生成的指标
	metrics := builder.getCMDBMetrics(7)

	// 验证指标
	metricNames := make(map[string]bool)
	for _, m := range metrics {
		metricNames[m.Name] = true
	}

	// 应该包含传统的拓扑关系指标
	assert.True(t, metricNames["host_with_module_relation"], "missing host_with_module_relation")
	assert.True(t, metricNames["module_with_set_relation"], "missing module_with_set_relation")

	// 应该包含 ToResourceMap 生成的指标
	assert.True(t, metricNames["app_version_with_host_relation"], "missing app_version_with_host_relation")
	assert.True(t, metricNames["service_name_with_host_relation"], "missing service_name_with_host_relation")

	// 详细验证 app_version 指标
	var appVersionMetric *Metric
	for i := range metrics {
		if metrics[i].Name == "app_version_with_host_relation" {
			appVersionMetric = &metrics[i]
			break
		}
	}

	require.NotNil(t, appVersionMetric, "app_version metric not found")

	labelMap := make(map[string]string)
	for _, l := range appVersionMetric.Labels {
		labelMap[l.Name] = l.Value
	}

	assert.Equal(t, "user-service", labelMap["app_name"])
	assert.Equal(t, "2.0.1", labelMap["version"])
	assert.Equal(t, "12345", labelMap["build_id"])
	assert.Equal(t, "135", labelMap["bk_host_id"])
	assert.Equal(t, "7", labelMap["bk_biz_id"])

	// 详细验证 service_name 指标
	var serviceMetric *Metric
	for i := range metrics {
		if metrics[i].Name == "service_name_with_host_relation" {
			serviceMetric = &metrics[i]
			break
		}
	}

	require.NotNil(t, serviceMetric, "service_name metric not found")

	serviceLabelMap := make(map[string]string)
	for _, l := range serviceMetric.Labels {
		serviceLabelMap[l.Name] = l.Value
	}

	assert.Equal(t, "user-svc", serviceLabelMap["name"])
	assert.Equal(t, "production", serviceLabelMap["namespace"])
	assert.Equal(t, "135", serviceLabelMap["bk_host_id"])
	assert.Equal(t, "7", serviceLabelMap["bk_biz_id"])
}

// TestToResourceMap_SetLevel 测试 set 层级的 ToResourceMap
func TestToResourceMap_SetLevel(t *testing.T) {
	builder := newRelationMetricsBuilder()

	setInfos := []*Info{
		{
			ID:       "21",
			Resource: "set",
			Label: map[string]string{
				"bk_set_id": "21",
			},
			Links: []Link{
				{
					{ID: "7", Resource: "biz", Label: map[string]string{"bk_biz_id": "7"}},
				},
			},
			ToResourceMap: map[string]map[string]map[string]any{
				"set": {
					"app_version": {
						"app_name": "api-gateway",
						"version":  "1.0.0",
					},
				},
			},
		},
	}

	err := builder.BuildInfosCache(context.Background(), 7, "set", setInfos)
	require.NoError(t, err)

	metrics := builder.getCMDBMetrics(7)

	// 验证 ToResourceMap 生成的指标
	metricNames := make(map[string]bool)
	for _, m := range metrics {
		metricNames[m.Name] = true
	}

	assert.True(t, metricNames["app_version_with_set_relation"], "missing app_version_with_set_relation")

	// 验证标签
	var appVersionMetric *Metric
	for i := range metrics {
		if metrics[i].Name == "app_version_with_set_relation" {
			appVersionMetric = &metrics[i]
			break
		}
	}

	require.NotNil(t, appVersionMetric)
	labelMap := make(map[string]string)
	for _, l := range appVersionMetric.Labels {
		labelMap[l.Name] = l.Value
	}

	assert.Equal(t, "api-gateway", labelMap["app_name"])
	assert.Equal(t, "1.0.0", labelMap["version"])
	assert.Equal(t, "21", labelMap["bk_set_id"])
	assert.Equal(t, "7", labelMap["bk_biz_id"])
}

// TestToResourceMap_EmptyToResourceMap 测试空 ToResourceMap 不影响传统指标
func TestToResourceMap_EmptyToResourceMap(t *testing.T) {
	builder := newRelationMetricsBuilder()

	hostInfo := &Info{
		ID:       "135",
		Resource: "host",
		Label: map[string]string{
			"bk_host_id": "135",
		},
		Links: []Link{
			{
				{ID: "109", Resource: "module", Label: map[string]string{"bk_module_id": "109"}},
				{ID: "21", Resource: "set", Label: map[string]string{"bk_set_id": "21"}},
			},
		},
		ToResourceMap: nil, // 空
	}

	err := builder.BuildInfosCache(context.Background(), 7, "host", []*Info{hostInfo})
	require.NoError(t, err)

	metrics := builder.getCMDBMetrics(7)

	// 应该仍然包含传统的拓扑关系指标
	metricNames := make(map[string]bool)
	for _, m := range metrics {
		metricNames[m.Name] = true
	}

	assert.True(t, metricNames["host_with_module_relation"], "missing host_with_module_relation")
	// host_with_set_relation 需要通过 link 中的 set 创建，这个测试没有包含 set 资源
	assert.False(t, metricNames["host_with_set_relation"], "host_with_set_relation should not exist without set resource")
	assert.True(t, metricNames["module_with_set_relation"], "missing module_with_set_relation")

	// 不应该包含 ToResourceMap 指标
	assert.False(t, metricNames["app_version_with_host_relation"])
}
