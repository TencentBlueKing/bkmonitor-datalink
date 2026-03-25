// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta1

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

// mockSchemaProvider 简单 mock 实现 relation.SchemaProvider
type mockSchemaProvider struct {
	resources []*relation.ResourceDefinition
	relations []*relation.RelationDefinition
}

func (m *mockSchemaProvider) GetResourceDefinition(namespace, name string) (*relation.ResourceDefinition, error) {
	for _, r := range m.resources {
		if r.Namespace == namespace && r.Name == name {
			return r, nil
		}
	}
	return nil, relation.ErrResourceDefinitionNotFound
}

func (m *mockSchemaProvider) ListResourceDefinitions(namespace string) ([]*relation.ResourceDefinition, error) {
	result := make([]*relation.ResourceDefinition, 0)
	for _, r := range m.resources {
		if r.Namespace == namespace || namespace == "" {
			result = append(result, r)
		}
	}
	return result, nil
}

func (m *mockSchemaProvider) GetRelationDefinition(namespace, name string) (*relation.RelationDefinition, error) {
	for _, r := range m.relations {
		if r.Namespace == namespace && r.Name == name {
			return r, nil
		}
	}
	return nil, relation.ErrRelationDefinitionNotFound
}

func (m *mockSchemaProvider) ListRelationDefinitions(namespace string) ([]*relation.RelationDefinition, error) {
	result := make([]*relation.RelationDefinition, 0)
	for _, r := range m.relations {
		if r.Namespace == namespace || namespace == "" {
			result = append(result, r)
		}
	}
	return result, nil
}

func (m *mockSchemaProvider) GetResourcePrimaryKeys(resourceType relation.ResourceType) []string {
	return nil
}

func (m *mockSchemaProvider) GetRelationSchema(relationType relation.RelationName) (*relation.RelationSchema, error) {
	return nil, relation.ErrRelationDefinitionNotFound
}

func (m *mockSchemaProvider) ListRelationSchemas() []relation.RelationSchema {
	return nil
}

func (m *mockSchemaProvider) FindRelationByResourceTypes(namespace, fromResource, toResource string, directionType relation.DirectionType) (*relation.RelationDefinition, bool) {
	for _, r := range m.relations {
		if (r.Namespace == namespace || namespace == "") &&
			((r.FromResource == fromResource && r.ToResource == toResource) ||
				(r.FromResource == toResource && r.ToResource == fromResource)) {
			return r, true
		}
	}
	return nil, false
}

func (m *mockSchemaProvider) Subscribe(callback relation.SchemaChangeCallback) error {
	return nil
}

func TestConfigAdapter_GetConfig(t *testing.T) {
	provider := &mockSchemaProvider{
		resources: []*relation.ResourceDefinition{
			{
				Namespace: "",
				Name:      "pod",
				Fields: []relation.FieldDefinition{
					{Name: "bcs_cluster_id", Required: true},
					{Name: "namespace", Required: true},
					{Name: "pod", Required: true},
				},
			},
			{
				Namespace: "",
				Name:      "node",
				Fields: []relation.FieldDefinition{
					{Name: "bcs_cluster_id", Required: true},
					{Name: "node", Required: true},
				},
			},
			{
				Namespace: "",
				Name:      "host",
				Fields: []relation.FieldDefinition{
					{Name: "bk_host_id", Required: true},
					{Name: "version", Required: false},
					{Name: "env_name", Required: false},
				},
			},
		},
		relations: []*relation.RelationDefinition{
			{
				Namespace:    "",
				Name:         "node_with_pod",
				FromResource: "node",
				ToResource:   "pod",
				Category:     "static",
			},
			{
				Namespace:    "",
				Name:         "host_with_system",
				FromResource: "host",
				ToResource:   "system",
				Category:     "static",
			},
		},
	}

	adapter := NewConfigAdapter(provider)
	cfg, err := adapter.GetConfig(context.Background(), "")
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// 验证资源数量
	assert.Len(t, cfg.Resource, 3)

	// 验证资源转换
	resourceMap := make(map[cmdb.Resource]ResourceConf)
	for _, r := range cfg.Resource {
		resourceMap[r.Name] = r
	}

	// pod: 3 个 required 字段 → 3 个 Index, 0 个 Info
	podConf := resourceMap["pod"]
	assert.Equal(t, cmdb.Resource("pod"), podConf.Name)
	assert.Equal(t, cmdb.Index{"bcs_cluster_id", "namespace", "pod"}, podConf.Index)
	assert.Nil(t, podConf.Info)

	// node: 2 个 required 字段
	nodeConf := resourceMap["node"]
	assert.Equal(t, cmdb.Resource("node"), nodeConf.Name)
	assert.Equal(t, cmdb.Index{"bcs_cluster_id", "node"}, nodeConf.Index)
	assert.Nil(t, nodeConf.Info)

	// host: 1 个 required + 2 个 optional → Index + Info
	hostConf := resourceMap["host"]
	assert.Equal(t, cmdb.Resource("host"), hostConf.Name)
	assert.Equal(t, cmdb.Index{"bk_host_id"}, hostConf.Index)
	assert.Equal(t, cmdb.Index{"version", "env_name"}, hostConf.Info)

	// 验证关联数量和内容
	assert.Len(t, cfg.Relation, 2)

	relMap := make(map[string]RelationConf)
	for _, r := range cfg.Relation {
		key := string(r.Resources[0]) + "_" + string(r.Resources[1])
		relMap[key] = r
	}

	nodePodRel := relMap["node_pod"]
	assert.Equal(t, cmdb.Resource("node"), nodePodRel.Resources[0])
	assert.Equal(t, cmdb.Resource("pod"), nodePodRel.Resources[1])
}

func TestConfigAdapter_GetConfig_EmptyProvider(t *testing.T) {
	provider := &mockSchemaProvider{
		resources: []*relation.ResourceDefinition{},
		relations: []*relation.RelationDefinition{},
	}

	adapter := NewConfigAdapter(provider)
	_, err := adapter.GetConfig(context.Background(), "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no resource or relation definitions found")
}

func TestConfigAdapter_GetConfig_SkipInvalidRelation(t *testing.T) {
	provider := &mockSchemaProvider{
		resources: []*relation.ResourceDefinition{
			{
				Namespace: "",
				Name:      "pod",
				Fields:    []relation.FieldDefinition{{Name: "pod", Required: true}},
			},
		},
		relations: []*relation.RelationDefinition{
			{
				Namespace:    "",
				Name:         "valid_rel",
				FromResource: "pod",
				ToResource:   "node",
			},
			{
				Namespace:    "",
				Name:         "invalid_rel",
				FromResource: "",
				ToResource:   "node",
			},
		},
	}

	adapter := NewConfigAdapter(provider)
	cfg, err := adapter.GetConfig(context.Background(), "")
	require.NoError(t, err)
	// invalid relation should be skipped
	assert.Len(t, cfg.Relation, 1)
	assert.Equal(t, cmdb.Resource("pod"), cfg.Relation[0].Resources[0])
}

func TestConfigAdapter_GetConfig_DeduplicateByName(t *testing.T) {
	// 模拟跨 namespace 出现同名资源和同边关联的场景（Redis 返回多个 namespace 的数据）
	provider := &mockSchemaProvider{
		resources: []*relation.ResourceDefinition{
			{Namespace: "bkcc__2", Name: "pod", Fields: []relation.FieldDefinition{{Name: "pod", Required: true}}},
			{Namespace: "bkcc__3", Name: "pod", Fields: []relation.FieldDefinition{{Name: "pod", Required: true}}},
			{Namespace: "__all__", Name: "pod", Fields: []relation.FieldDefinition{{Name: "pod", Required: true}}},
			{Namespace: "bkcc__2", Name: "node", Fields: []relation.FieldDefinition{{Name: "node", Required: true}}},
			{Namespace: "__all__", Name: "node", Fields: []relation.FieldDefinition{{Name: "node", Required: true}}},
		},
		relations: []*relation.RelationDefinition{
			{Namespace: "bkcc__2", Name: "node_with_pod", FromResource: "node", ToResource: "pod"},
			{Namespace: "bkcc__3", Name: "node_with_pod", FromResource: "node", ToResource: "pod"},
			{Namespace: "__all__", Name: "node_with_pod", FromResource: "node", ToResource: "pod"},
		},
	}

	adapter := NewConfigAdapter(provider)
	cfg, err := adapter.GetConfig(context.Background(), "")
	require.NoError(t, err)

	// 应该只有 2 个去重后的资源
	assert.Len(t, cfg.Resource, 2)
	// 应该只有 1 个去重后的关联
	assert.Len(t, cfg.Relation, 1)
}

func TestConfigAdapter_GetConfig_DeduplicateUndirectedEdge(t *testing.T) {
	// v1beta1 使用无向图，"pod->node" 和 "node->pod" 是同一条边
	// 模拟 Redis 有 pod->node，Static 有 node->pod 的情况
	provider := &mockSchemaProvider{
		resources: []*relation.ResourceDefinition{
			{Namespace: "", Name: "pod", Fields: []relation.FieldDefinition{{Name: "pod", Required: true}}},
			{Namespace: "", Name: "node", Fields: []relation.FieldDefinition{{Name: "node", Required: true}}},
		},
		relations: []*relation.RelationDefinition{
			{Namespace: "__all__", Name: "global_pod_to_node", FromResource: "pod", ToResource: "node"},
			{Namespace: "", Name: "node_with_pod", FromResource: "node", ToResource: "pod"},
		},
	}

	adapter := NewConfigAdapter(provider)
	cfg, err := adapter.GetConfig(context.Background(), "")
	require.NoError(t, err)

	// pod->node 和 node->pod 在无向图中是同一条边，应只保留 1 个
	assert.Len(t, cfg.Relation, 1)
}

func TestConvertResourceDefinition(t *testing.T) {
	rd := &relation.ResourceDefinition{
		Namespace: "",
		Name:      "container",
		Fields: []relation.FieldDefinition{
			{Name: "bcs_cluster_id", Required: true},
			{Name: "namespace", Required: true},
			{Name: "pod", Required: true},
			{Name: "container", Required: true},
			{Name: "version", Required: false},
		},
	}

	conf := convertResourceDefinition(rd)
	assert.Equal(t, cmdb.Resource("container"), conf.Name)
	assert.Equal(t, cmdb.Index{"bcs_cluster_id", "namespace", "pod", "container"}, conf.Index)
	assert.Equal(t, cmdb.Index{"version"}, conf.Info)
}

func TestConvertRelationDefinition(t *testing.T) {
	t.Run("valid relation", func(t *testing.T) {
		rd := &relation.RelationDefinition{
			Namespace:    "",
			Name:         "node_with_system",
			FromResource: "node",
			ToResource:   "system",
		}

		conf := convertRelationDefinition(rd)
		assert.Len(t, conf.Resources, 2)
		assert.Equal(t, cmdb.Resource("node"), conf.Resources[0])
		assert.Equal(t, cmdb.Resource("system"), conf.Resources[1])
	})

	t.Run("empty from_resource", func(t *testing.T) {
		rd := &relation.RelationDefinition{
			Namespace:    "",
			Name:         "bad_rel",
			FromResource: "",
			ToResource:   "node",
		}

		conf := convertRelationDefinition(rd)
		// empty from_resource still produces a RelationConf (no error)
		assert.Equal(t, cmdb.Resource(""), conf.Resources[0])
		assert.Equal(t, cmdb.Resource("node"), conf.Resources[1])
	})

	t.Run("empty to_resource", func(t *testing.T) {
		rd := &relation.RelationDefinition{
			Namespace:    "",
			Name:         "bad_rel",
			FromResource: "node",
			ToResource:   "",
		}

		conf := convertRelationDefinition(rd)
		assert.Equal(t, cmdb.Resource("node"), conf.Resources[0])
		assert.Equal(t, cmdb.Resource(""), conf.Resources[1])
	})
}
