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
	"sort"
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
							"expands": {
								"host": {
									"bk_host_id": "3001"
								}
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
					name: "host_info_relation",
					labels: map[string]string{
						"bk_biz_id":  "2",
						"bk_host_id": "3001",
					},
				},
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
							"expands": {
								"host": {
									"bk_host_id": "3001"
								}
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
			expected: []expectedMetric{
				{
					name: "host_info_relation",
					labels: map[string]string{
						"bk_biz_id":  "2",
						"bk_host_id": "3001",
					},
				},
			}, // version 字段缺失，不生成 app_version_with_host_relation 指标
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
							"expands": {
								"host": {
									"bk_host_id": "3001"
								}
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
					name: "host_info_relation",
					labels: map[string]string{
						"bk_biz_id":  "2",
						"bk_host_id": "3001",
					},
				},
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
		{
			name:  "继承场景：host 构建 RelationConfig 时字段从 set 的 Expands 获取",
			bizID: 2,
			resources: `{
				"set": {
					"name": "set",
					"data": {
						"3001": {
							"id": "3001",
							"resource": "set",
							"label": {
								"bk_set_id": "3001"
							},
							"expands": {
								"host": {
									"bk_host_id": "1001",
									"env_type": "prod",
									"deploy_version": "v2.0"
								}
							}
						}
					}
				},
				"module": {
					"name": "module",
					"data": {
						"2001": {
							"id": "2001",
							"resource": "module",
							"label": {
								"bk_module_id": "2001"
							}
						}
					}
				},
				"host": {
					"name": "host",
					"data": {
						"1001": {
							"id": "1001",
							"resource": "host",
							"label": {
								"bk_host_id": "1001"
							},
							"relation_config": {
								"deploy_config": {
									"app_name": "my-service"
								}
							},
							"links": [
								[
									{
										"id": "2001",
										"resource": "module",
										"label": {"bk_module_id": "2001"}
									},
									{
										"id": "3001",
										"resource": "set",
										"label": {"bk_set_id": "3001"}
									}
								]
							]
						}
					}
				}
			}`,
			schema: mockSchemaConfig{
				resources: map[string]*service.ResourceDefinition{
					"deploy_config": {
						Name: "deploy_config",
						Fields: []service.FieldDefinition{
							{Name: "app_name", Required: true},
							{Name: "env_type", Required: true},
							{Name: "deploy_version", Required: true},
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
					"deploy_config_with_host": {
						Name:         "deploy_config_with_host",
						FromResource: "deploy_config",
						ToResource:   "host",
						Category:     "static",
					},
				},
			},
			// host 配置了 relation_config.deploy_config，只有 app_name
			// env_type 和 deploy_version 需要从 host 所属的 set 的 expands.host 中获取
			expected: []expectedMetric{
				{
					name: "host_with_module_relation",
					labels: map[string]string{
						"bk_biz_id":    "2",
						"bk_host_id":   "1001",
						"bk_module_id": "2001",
					},
				},
				{
					name: "module_with_set_relation",
					labels: map[string]string{
						"bk_biz_id":    "2",
						"bk_module_id": "2001",
						"bk_set_id":    "3001",
					},
				},
				{
					name: "host_info_relation",
					labels: map[string]string{
						"bk_biz_id":      "2",
						"bk_host_id":     "1001",
						"env_type":       "prod",
						"deploy_version": "v2.0",
					},
				},
				{
					name: "deploy_config_with_host_relation",
					labels: map[string]string{
						"bk_biz_id":      "2",
						"bk_host_id":     "1001",
						"app_name":       "my-service",
						"env_type":       "prod",
						"deploy_version": "v2.0",
					},
				},
			},
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

			assert.ElementsMatch(t, tt.expected, actualResults, "生成的指标与预期不符")
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
	m.AddRelationDefinitionWithDirection(namespace, fromResource, toResource, false)
}

// AddRelationDefinitionWithDirection 添加关系定义，支持指定方向性
// isBelongsTo=true: 单向关系，使用 _to_ 连接，key 按 from_to_to 格式
// isBelongsTo=false: 双向关系，使用 _with_ 连接，key 按字母序排序
func (m *MockSchemaProvider) AddRelationDefinitionWithDirection(namespace, fromResource, toResource string, isBelongsTo bool) {
	var name, key string
	if isBelongsTo {
		// 单向关系：使用 _to_，不排序
		name = fmt.Sprintf("%s_to_%s", fromResource, toResource)
		key = fmt.Sprintf("%s:%s", namespace, name)
	} else {
		// 双向关系：按字母序排序，与 RedisSchemaProvider 保持一致
		resources := []string{fromResource, toResource}
		sort.Strings(resources)
		name = fmt.Sprintf("%s_with_%s", resources[0], resources[1])
		key = fmt.Sprintf("%s:%s", namespace, name)
	}
	m.relationDefs[key] = &service.RelationDefinition{
		Namespace:    namespace,
		Name:         name,
		FromResource: fromResource,
		ToResource:   toResource,
		Category:     "static",
		IsBelongsTo:  isBelongsTo,
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
	// 先尝试单向关系 key（from_to_to 格式）
	directionalName := fmt.Sprintf("%s_to_%s", fromResource, toResource)
	directionalKey := fmt.Sprintf("%s:%s", namespace, directionalName)
	if def, ok := m.relationDefs[directionalKey]; ok {
		return def, nil
	}

	// 再尝试双向关系 key（按字母序排序）
	resources := []string{fromResource, toResource}
	sort.Strings(resources)
	bidirectionalName := fmt.Sprintf("%s_with_%s", resources[0], resources[1])
	bidirectionalKey := fmt.Sprintf("%s:%s", namespace, bidirectionalName)
	if def, ok := m.relationDefs[bidirectionalKey]; ok {
		return def, nil
	}

	return nil, fmt.Errorf("not found")
}

func (m *MockSchemaProvider) ListRelationDefinitions(namespace string) ([]*service.RelationDefinition, error) {
	return nil, nil
}
