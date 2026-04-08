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

func (m *mockSchemaProvider) Name() string {
	return "mock"
}

func (m *mockSchemaProvider) ListNamespaces() ([]string, error) {
	seen := make(map[string]struct{})
	for _, r := range m.resources {
		ns := r.Namespace
		if ns == "" {
			ns = relation.NamespaceAll
		}
		seen[ns] = struct{}{}
	}
	namespaces := make([]string, 0, len(seen))
	for ns := range seen {
		namespaces = append(namespaces, ns)
	}
	return namespaces, nil
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
	if namespace == "" {
		namespace = relation.NamespaceAll
	}
	result := make([]*relation.ResourceDefinition, 0)
	for _, r := range m.resources {
		ns := r.Namespace
		if ns == "" {
			ns = relation.NamespaceAll
		}
		if ns == namespace {
			result = append(result, r)
		}
	}
	return result, nil
}

func (m *mockSchemaProvider) ListAllResourceDefinitions() (map[string][]*relation.ResourceDefinition, error) {
	result := make(map[string][]*relation.ResourceDefinition)
	for _, r := range m.resources {
		ns := r.Namespace
		if ns == "" {
			ns = relation.NamespaceAll
		}
		result[ns] = append(result[ns], r)
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
	if namespace == "" {
		namespace = relation.NamespaceAll
	}
	result := make([]*relation.RelationDefinition, 0)
	for _, r := range m.relations {
		ns := r.Namespace
		if ns == "" {
			ns = relation.NamespaceAll
		}
		if ns == namespace {
			result = append(result, r)
		}
	}
	return result, nil
}

func (m *mockSchemaProvider) ListAllRelationDefinitions() (map[string][]*relation.RelationDefinition, error) {
	result := make(map[string][]*relation.RelationDefinition)
	for _, r := range m.relations {
		ns := r.Namespace
		if ns == "" {
			ns = relation.NamespaceAll
		}
		result[ns] = append(result[ns], r)
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

func TestConfigAdapter_GetConfigs(t *testing.T) {
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
	configs, err := adapter.GetConfigs(context.Background())
	require.NoError(t, err)
	require.NotNil(t, configs)

	// mock 数据 namespace 为 ""，归到 __all__
	cfg := configs[relation.NamespaceAll]
	require.NotNil(t, cfg)

	assert.Len(t, cfg.Resource, 3)

	resourceMap := make(map[cmdb.Resource]ResourceConf)
	for _, r := range cfg.Resource {
		resourceMap[r.Name] = r
	}

	podConf := resourceMap["pod"]
	assert.Equal(t, cmdb.Resource("pod"), podConf.Name)
	assert.Equal(t, cmdb.Index{"bcs_cluster_id", "namespace", "pod"}, podConf.Index)
	assert.Nil(t, podConf.Info)

	nodeConf := resourceMap["node"]
	assert.Equal(t, cmdb.Resource("node"), nodeConf.Name)
	assert.Equal(t, cmdb.Index{"bcs_cluster_id", "node"}, nodeConf.Index)
	assert.Nil(t, nodeConf.Info)

	hostConf := resourceMap["host"]
	assert.Equal(t, cmdb.Resource("host"), hostConf.Name)
	assert.Equal(t, cmdb.Index{"bk_host_id"}, hostConf.Index)
	assert.Equal(t, cmdb.Index{"version", "env_name"}, hostConf.Info)

	assert.Len(t, cfg.Relation, 2)
}

func TestConfigAdapter_GetConfigs_EmptyProvider(t *testing.T) {
	provider := &mockSchemaProvider{
		resources: []*relation.ResourceDefinition{},
		relations: []*relation.RelationDefinition{},
	}

	adapter := NewConfigAdapter(provider)
	_, err := adapter.GetConfigs(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no namespaces found")
}

func TestConfigAdapter_GetConfigs_SkipInvalidRelation(t *testing.T) {
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
	configs, err := adapter.GetConfigs(context.Background())
	require.NoError(t, err)
	cfg := configs[relation.NamespaceAll]
	require.NotNil(t, cfg)
	// invalid relation should be skipped
	assert.Len(t, cfg.Relation, 1)
	assert.Equal(t, cmdb.Resource("pod"), cfg.Relation[0].Resources[0])
}

func TestConfigAdapter_GetConfigs_MultiNamespace(t *testing.T) {
	// 模拟多 namespace 的场景，每个 namespace 独立构建 Config
	provider := &mockSchemaProvider{
		resources: []*relation.ResourceDefinition{
			{Namespace: "bkcc__2", Name: "pod", Fields: []relation.FieldDefinition{{Name: "pod", Required: true}}},
			{Namespace: "bkcc__2", Name: "node", Fields: []relation.FieldDefinition{{Name: "node", Required: true}}},
			{Namespace: "__all__", Name: "host", Fields: []relation.FieldDefinition{{Name: "bk_host_id", Required: true}}},
		},
		relations: []*relation.RelationDefinition{
			{Namespace: "bkcc__2", Name: "node_with_pod", FromResource: "node", ToResource: "pod"},
			{Namespace: "__all__", Name: "host_with_system", FromResource: "host", ToResource: "system"},
		},
	}

	adapter := NewConfigAdapter(provider)
	configs, err := adapter.GetConfigs(context.Background())
	require.NoError(t, err)

	// 应有 2 个 namespace
	assert.Len(t, configs, 2)

	// bkcc__2 有 pod, node 两个资源和 1 个关联
	cfg2 := configs["bkcc__2"]
	require.NotNil(t, cfg2)
	assert.Len(t, cfg2.Resource, 2)
	assert.Len(t, cfg2.Relation, 1)

	// __all__ 有 host 1 个资源和 1 个关联
	cfgAll := configs[relation.NamespaceAll]
	require.NotNil(t, cfgAll)
	assert.Len(t, cfgAll.Resource, 1)
	assert.Len(t, cfgAll.Relation, 1)
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

		conf, ok := convertRelationDefinition(rd)
		assert.True(t, ok)
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

		_, ok := convertRelationDefinition(rd)
		assert.False(t, ok)
	})

	t.Run("empty to_resource", func(t *testing.T) {
		rd := &relation.RelationDefinition{
			Namespace:    "",
			Name:         "bad_rel",
			FromResource: "node",
			ToResource:   "",
		}

		_, ok := convertRelationDefinition(rd)
		assert.False(t, ok)
	})
}
