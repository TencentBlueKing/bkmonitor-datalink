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
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/service"
)

func TestBuildRelationConfigMetrics_TableDriven(t *testing.T) {
	tests := []struct {
		name      string           // 测试场景描述
		bizID     int              // 业务 ID
		resources string           // 输入的 resources JSON
		schema    mockSchemaConfig // schema 配置
		expected  []expectedMetric // 期望的指标
	}{
		{
			name:  "成功场景：host 关联 app_version",
			bizID: 2,
			resources: `{
				"host": {
					"name": "host",
					"data": {
						"3001": {
							"id": "3001",
							"resource": "host",
							"label": {
								"bk_host_id": "3001"
							},
							"relation_config": {
								"app_version": {
									"app_name": "my-service",
									"version": "v1.0"
								}
							}
						}
					}
				}
			}`,
			schema: mockSchemaConfig{
				resources: map[string]*service.ResourceDefinition{
					"app_version": {
						Name: "app_version",
						Fields: []service.FieldDefinition{
							{Name: "app_name", Required: true},
							{Name: "version", Required: true},
						},
					},
					"host": {
						Name: "host",
						Fields: []service.FieldDefinition{
							{Name: "bk_host_id", Required: true},
						},
					},
				},
				relations: map[string]*service.RelationDefinition{
					"app_version_with_host": {
						Name:         "app_version_with_host",
						FromResource: "app_version",
						ToResource:   "host",
						Category:     "static",
					},
				},
			},
			expected: []expectedMetric{
				{
					name: "app_version_with_host_relation",
					labels: map[string]string{
						"bk_biz_id":  "2",
						"bk_host_id": "3001",
						"app_name":   "my-service",
						"version":    "v1.0",
					},
				},
			},
		},
		{
			name:  "字段缺失场景：缺少 version 字段",
			bizID: 2,
			resources: `{
				"host": {
					"name": "host",
					"data": {
						"3001": {
							"id": "3001",
							"resource": "host",
							"label": {
								"bk_host_id": "3001"
							},
							"relation_config": {
								"app_version": {
									"app_name": "my-service"
								}
							}
						}
					}
				}
			}`,
			schema: mockSchemaConfig{
				resources: map[string]*service.ResourceDefinition{
					"app_version": {
						Name: "app_version",
						Fields: []service.FieldDefinition{
							{Name: "app_name", Required: true},
							{Name: "version", Required: true},
						},
					},
					"host": {
						Name: "host",
						Fields: []service.FieldDefinition{
							{Name: "bk_host_id", Required: true},
						},
					},
				},
				relations: map[string]*service.RelationDefinition{
					"app_version_with_host": {
						Name:         "app_version_with_host",
						FromResource: "app_version",
						ToResource:   "host",
						Category:     "static",
					},
				},
			},
			expected: []expectedMetric{}, // 字段缺失，不生成指标
		},
		{
			name:  "多个关系场景：host 同时关联 app_version 和 git_commit",
			bizID: 2,
			resources: `{
				"host": {
					"name": "host",
					"data": {
						"3001": {
							"id": "3001",
							"resource": "host",
							"label": {
								"bk_host_id": "3001"
							},
							"relation_config": {
								"app_version": {
									"app_name": "my-service",
									"version": "v1.0"
								},
								"git_commit": {
									"git_repo": "https://github.com/example/repo",
									"commit_id": "abc123"
								}
							}
						}
					}
				}
			}`,
			schema: mockSchemaConfig{
				resources: map[string]*service.ResourceDefinition{
					"app_version": {
						Name: "app_version",
						Fields: []service.FieldDefinition{
							{Name: "app_name", Required: true},
							{Name: "version", Required: true},
						},
					},
					"git_commit": {
						Name: "git_commit",
						Fields: []service.FieldDefinition{
							{Name: "git_repo", Required: true},
							{Name: "commit_id", Required: true},
						},
					},
					"host": {
						Name: "host",
						Fields: []service.FieldDefinition{
							{Name: "bk_host_id", Required: true},
						},
					},
				},
				relations: map[string]*service.RelationDefinition{
					"app_version_with_host": {
						Name:         "app_version_with_host",
						FromResource: "app_version",
						ToResource:   "host",
						Category:     "static",
					},
					"git_commit_with_host": {
						Name:         "git_commit_with_host",
						FromResource: "git_commit",
						ToResource:   "host",
						Category:     "static",
					},
				},
			},
			expected: []expectedMetric{
				{
					name: "app_version_with_host_relation",
					labels: map[string]string{
						"bk_biz_id":  "2",
						"bk_host_id": "3001",
						"app_name":   "my-service",
						"version":    "v1.0",
					},
				},
				{
					name: "git_commit_with_host_relation",
					labels: map[string]string{
						"bk_biz_id":  "2",
						"bk_host_id": "3001",
						"git_repo":   "https://github.com/example/repo",
						"commit_id":  "abc123",
					},
				},
			},
		},
		{
			name:  "关系未定义场景：schema 中没有定义该关系",
			bizID: 2,
			resources: `{
				"host": {
					"name": "host",
					"data": {
						"3001": {
							"id": "3001",
							"resource": "host",
							"label": {
								"bk_host_id": "3001"
							},
							"relation_config": {
								"unknown_resource": {
									"foo": "bar"
								}
							}
						}
					}
				}
			}`,
			schema: mockSchemaConfig{
				resources: map[string]*service.ResourceDefinition{
					"host": {
						Name: "host",
						Fields: []service.FieldDefinition{
							{Name: "bk_host_id", Required: true},
						},
					},
				},
				relations: map[string]*service.RelationDefinition{},
			},
			expected: []expectedMetric{}, // 关系未定义，不生成指标
		},
		{
			name:  "空 RelationConfig 场景：没有配置任何关系",
			bizID: 2,
			resources: `{
				"host": {
					"name": "host",
					"data": {
						"3001": {
							"id": "3001",
							"resource": "host",
							"label": {
								"bk_host_id": "3001"
							}
						}
					}
				}
			}`,
			schema: mockSchemaConfig{
				resources: map[string]*service.ResourceDefinition{
					"host": {
						Name: "host",
						Fields: []service.FieldDefinition{
							{Name: "bk_host_id", Required: true},
						},
					},
				},
				relations: map[string]*service.RelationDefinition{},
			},
			expected: []expectedMetric{}, // 没有配置，不生成指标
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 1. 解析 resources JSON
			var resourcesMap map[string]*ResourceInfo
			err := json.Unmarshal([]byte(tt.resources), &resourcesMap)
			assert.NoError(t, err, "resources JSON 解析失败")

			// 2. 创建 Mock SchemaProvider
			mockProvider := newMockSchemaProviderFromConfig(tt.bizID, tt.schema)

			// 3. 创建 MetricsBuilder 并注入 SchemaProvider
			builder := newRelationMetricsBuilder()
			builder.WithSchemaProvider(mockProvider)
			builder.resources[tt.bizID] = resourcesMap

			// 4. 执行构建
			actualMetrics := builder.getCMDBMetrics(tt.bizID)

			// 5. 转换实际指标为可比较的格式
			actualResults := make([]expectedMetric, 0, len(actualMetrics))
			for _, metric := range actualMetrics {
				labels := make(map[string]string)
				for _, label := range metric.Labels {
					labels[label.Name] = label.Value
				}
				actualResults = append(actualResults, expectedMetric{
					name:   metric.Name,
					labels: labels,
				})
			}

			// 6. 整体断言
			assert.Equal(t, tt.expected, actualResults, "生成的指标与预期不符")
		})
	}
}

// mockSchemaConfig schema 配置结构
type mockSchemaConfig struct {
	resources map[string]*service.ResourceDefinition // key: 资源名称
	relations map[string]*service.RelationDefinition // key: 关系名称
}

// expectedMetric 期望的指标
type expectedMetric struct {
	name   string            // 指标名称
	labels map[string]string // 标签
}

// newMockSchemaProviderFromConfig 从配置创建 Mock SchemaProvider
func newMockSchemaProviderFromConfig(bizID int, config mockSchemaConfig) *MockSchemaProvider {
	provider := NewMockSchemaProvider()
	namespace := fmt.Sprintf("bkcc__%d", bizID)

	// 添加资源定义
	for _, resDef := range config.resources {
		provider.AddResourceDefinition(namespace, resDef.Name, resDef.Fields)
	}

	// 添加关系定义
	for _, relDef := range config.relations {
		provider.AddRelationDefinition(namespace, relDef.FromResource, relDef.ToResource)
	}

	return provider
}

// MockSchemaProvider 用于测试的 Mock SchemaProvider
type MockSchemaProvider struct {
	resourceDefs map[string]*service.ResourceDefinition
	relationDefs map[string]*service.RelationDefinition
}

func NewMockSchemaProvider() *MockSchemaProvider {
	return &MockSchemaProvider{
		resourceDefs: make(map[string]*service.ResourceDefinition),
		relationDefs: make(map[string]*service.RelationDefinition),
	}
}

func (m *MockSchemaProvider) AddResourceDefinition(namespace, resourceType string, fields []service.FieldDefinition) {
	key := fmt.Sprintf("%s:%s", namespace, resourceType)
	m.resourceDefs[key] = &service.ResourceDefinition{
		Namespace: namespace,
		Name:      resourceType,
		Fields:    fields,
	}
}

func (m *MockSchemaProvider) AddRelationDefinition(namespace, fromResource, toResource string) {
	name := fmt.Sprintf("%s_with_%s", fromResource, toResource)
	key := fmt.Sprintf("%s:%s", namespace, name)
	m.relationDefs[key] = &service.RelationDefinition{
		Namespace:    namespace,
		Name:         name,
		FromResource: fromResource,
		ToResource:   toResource,
		Category:     "static",
	}
}

func (m *MockSchemaProvider) GetResourceDefinition(namespace, resourceType string) (*service.ResourceDefinition, error) {
	key := fmt.Sprintf("%s:%s", namespace, resourceType)
	if def, ok := m.resourceDefs[key]; ok {
		return def, nil
	}
	return nil, fmt.Errorf("not found")
}

func (m *MockSchemaProvider) GetRelationDefinition(namespace, fromResource, toResource string) (*service.RelationDefinition, error) {
	name := fmt.Sprintf("%s_with_%s", fromResource, toResource)
	key := fmt.Sprintf("%s:%s", namespace, name)
	if def, ok := m.relationDefs[key]; ok {
		return def, nil
	}
	return nil, fmt.Errorf("not found")
}

func (m *MockSchemaProvider) ListRelationDefinitions(namespace string) ([]*service.RelationDefinition, error) {
	return nil, nil
}
