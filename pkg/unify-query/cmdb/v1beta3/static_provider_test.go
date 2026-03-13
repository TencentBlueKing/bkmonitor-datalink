// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta3

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStaticSchemaProvider_GetResourceDefinition(t *testing.T) {
	provider := NewStaticSchemaProvider()

	t.Run("get existing resource", func(t *testing.T) {
		rd, err := provider.GetResourceDefinition("", "pod")
		assert.NoError(t, err)
		assert.NotNil(t, rd)
		assert.Equal(t, "pod", rd.Name)
		assert.Equal(t, "", rd.Namespace)
		assert.True(t, len(rd.Fields) > 0)

		// 验证主键字段
		keys := rd.GetPrimaryKeys()
		assert.Equal(t, []string{"bcs_cluster_id", "namespace", "pod"}, keys)
	})

	t.Run("get non-existing resource", func(t *testing.T) {
		rd, err := provider.GetResourceDefinition("", "non_existing")
		assert.Error(t, err)
		assert.Nil(t, rd)
		assert.Equal(t, ErrResourceDefinitionNotFound, err)
	})

	t.Run("get resource with namespace", func(t *testing.T) {
		// 静态提供器不支持带命名空间的资源
		rd, err := provider.GetResourceDefinition("bkcc__2", "pod")
		assert.Error(t, err)
		assert.Nil(t, rd)
	})
}

func TestStaticSchemaProvider_ListResourceDefinitions(t *testing.T) {
	provider := NewStaticSchemaProvider()

	t.Run("list global resources", func(t *testing.T) {
		resources, err := provider.ListResourceDefinitions("")
		assert.NoError(t, err)
		assert.NotNil(t, resources)
		assert.True(t, len(resources) > 0)

		// 验证包含已知的资源类型
		resourceNames := make(map[string]bool)
		for _, rd := range resources {
			resourceNames[rd.Name] = true
		}
		assert.True(t, resourceNames["pod"])
		assert.True(t, resourceNames["node"])
		assert.True(t, resourceNames["container"])
	})

	t.Run("list resources with namespace", func(t *testing.T) {
		// 静态提供器不支持带命名空间的资源,返回空列表
		resources, err := provider.ListResourceDefinitions("bkcc__2")
		assert.NoError(t, err)
		assert.Empty(t, resources)
	})
}

func TestStaticSchemaProvider_GetRelationDefinition(t *testing.T) {
	provider := NewStaticSchemaProvider()

	t.Run("get existing relation", func(t *testing.T) {
		rd, err := provider.GetRelationDefinition("", "pod_with_service")
		assert.NoError(t, err)
		assert.NotNil(t, rd)
		assert.Equal(t, "pod_with_service", rd.Name)
		assert.Equal(t, "", rd.Namespace)
		assert.Equal(t, "pod", rd.FromResource)
		assert.Equal(t, "service", rd.ToResource)
		assert.Equal(t, "static", rd.Category)
	})

	t.Run("get non-existing relation", func(t *testing.T) {
		rd, err := provider.GetRelationDefinition("", "non_existing")
		assert.Error(t, err)
		assert.Nil(t, rd)
		assert.Equal(t, ErrRelationDefinitionNotFound, err)
	})

	t.Run("get relation with namespace", func(t *testing.T) {
		// 静态提供器不支持带命名空间的关联
		rd, err := provider.GetRelationDefinition("bkcc__2", "pod_with_service")
		assert.Error(t, err)
		assert.Nil(t, rd)
	})
}

func TestStaticSchemaProvider_ListRelationDefinitions(t *testing.T) {
	provider := NewStaticSchemaProvider()

	t.Run("list global relations", func(t *testing.T) {
		relations, err := provider.ListRelationDefinitions("")
		assert.NoError(t, err)
		assert.NotNil(t, relations)
		assert.True(t, len(relations) > 0)

		// 验证包含已知的关联类型
		relationNames := make(map[string]bool)
		for _, rd := range relations {
			relationNames[rd.Name] = true
		}
		assert.True(t, relationNames["pod_with_service"])
		assert.True(t, relationNames["node_with_pod"])
	})

	t.Run("list relations with namespace", func(t *testing.T) {
		// 静态提供器不支持带命名空间的关联,返回空列表
		relations, err := provider.ListRelationDefinitions("bkcc__2")
		assert.NoError(t, err)
		assert.Empty(t, relations)
	})
}

func TestStaticSchemaProvider_GetResourcePrimaryKeys(t *testing.T) {
	provider := NewStaticSchemaProvider()

	t.Run("get primary keys for pod", func(t *testing.T) {
		keys := provider.GetResourcePrimaryKeys(ResourceTypePod)
		assert.Equal(t, []string{"bcs_cluster_id", "namespace", "pod"}, keys)
	})

	t.Run("get primary keys for node", func(t *testing.T) {
		keys := provider.GetResourcePrimaryKeys(ResourceTypeNode)
		assert.Equal(t, []string{"bcs_cluster_id", "node"}, keys)
	})

	t.Run("get primary keys for non-existing resource", func(t *testing.T) {
		keys := provider.GetResourcePrimaryKeys(ResourceType("non_existing"))
		assert.Empty(t, keys)
	})
}

func TestStaticSchemaProvider_GetRelationSchema(t *testing.T) {
	provider := NewStaticSchemaProvider()

	t.Run("get schema for existing relation", func(t *testing.T) {
		schema, err := provider.GetRelationSchema(RelationPodWithService)
		assert.NoError(t, err)
		assert.NotNil(t, schema)
		assert.Equal(t, RelationPodWithService, schema.RelationType)
		assert.Equal(t, RelationCategoryStatic, schema.Category)
		assert.Equal(t, ResourceTypePod, schema.FromType)
		assert.Equal(t, ResourceTypeService, schema.ToType)
	})

	t.Run("get schema for non-existing relation", func(t *testing.T) {
		schema, err := provider.GetRelationSchema(RelationType("non_existing"))
		assert.Error(t, err)
		assert.Nil(t, schema)
		assert.Equal(t, ErrRelationDefinitionNotFound, err)
	})
}

func TestStaticSchemaProvider_ListRelationSchemas(t *testing.T) {
	provider := NewStaticSchemaProvider()

	schemas := provider.ListRelationSchemas()
	assert.NotNil(t, schemas)
	assert.True(t, len(schemas) > 0)

	// 验证包含已知的关联
	schemaMap := make(map[RelationType]RelationSchema)
	for _, schema := range schemas {
		schemaMap[schema.RelationType] = schema
	}

	// 验证静态关联
	assert.Contains(t, schemaMap, RelationPodWithService)
	assert.Contains(t, schemaMap, RelationNodeWithPod)

	// 验证动态关联
	assert.Contains(t, schemaMap, RelationPodToPod)
	assert.Equal(t, RelationCategoryDynamic, schemaMap[RelationPodToPod].Category)
}

func TestResourceDefinition_GetPrimaryKeys(t *testing.T) {
	rd := &ResourceDefinition{
		Name: "test_resource",
		Fields: []FieldDefinition{
			{Name: "id", Required: true},
			{Name: "name", Required: true},
			{Name: "optional_field", Required: false},
		},
	}

	keys := rd.GetPrimaryKeys()
	assert.Equal(t, []string{"id", "name"}, keys)
}

func TestRelationDefinition_ToRelationType(t *testing.T) {
	t.Run("global relation", func(t *testing.T) {
		rd := &RelationDefinition{
			Namespace: "",
			Name:      "pod_with_service",
		}
		assert.Equal(t, RelationType("pod_with_service"), rd.ToRelationType())
	})

	t.Run("namespaced relation", func(t *testing.T) {
		rd := &RelationDefinition{
			Namespace: "bkcc__2",
			Name:      "app_version_with_git_commit",
		}
		assert.Equal(t, RelationType("bkcc__2:app_version_with_git_commit"), rd.ToRelationType())
	})
}

func TestRelationDefinition_ToRelationSchema(t *testing.T) {
	t.Run("static relation", func(t *testing.T) {
		rd := &RelationDefinition{
			Namespace:    "bkcc__2",
			Name:         "app_version_with_container",
			FromResource: "app_version",
			ToResource:   "container",
			Category:     "static",
			IsBelongsTo:  false,
		}

		schema := rd.ToRelationSchema()
		assert.Equal(t, RelationType("bkcc__2:app_version_with_container"), schema.RelationType)
		assert.Equal(t, RelationCategoryStatic, schema.Category)
		assert.Equal(t, ResourceType("app_version"), schema.FromType)
		assert.Equal(t, ResourceType("container"), schema.ToType)
		assert.False(t, schema.IsBelongsTo)
	})

	t.Run("dynamic relation", func(t *testing.T) {
		rd := &RelationDefinition{
			Namespace:    "",
			Name:         "pod_to_pod",
			FromResource: "pod",
			ToResource:   "pod",
			Category:     "dynamic",
			IsBelongsTo:  false,
		}

		schema := rd.ToRelationSchema()
		assert.Equal(t, RelationType("pod_to_pod"), schema.RelationType)
		assert.Equal(t, RelationCategoryDynamic, schema.Category)
	})
}

// TestStaticSchemaProvider_Compatibility 测试与现有代码的兼容性
func TestStaticSchemaProvider_Compatibility(t *testing.T) {
	provider := NewStaticSchemaProvider()

	// 测试所有已有的资源类型都能正确获取主键
	testCases := []struct {
		resourceType ResourceType
		expectedKeys []string
	}{
		{ResourceTypePod, []string{"bcs_cluster_id", "namespace", "pod"}},
		{ResourceTypeNode, []string{"bcs_cluster_id", "node"}},
		{ResourceTypeContainer, []string{"bcs_cluster_id", "namespace", "pod", "container"}},
		{ResourceTypeSystem, []string{"bk_cloud_id", "bk_target_ip"}},
		{ResourceTypeAppVersion, []string{"bcs_cluster_id", "namespace", "app_version"}},
		{ResourceTypeGitCommit, []string{"git_repo", "commit_id"}},
	}

	for _, tc := range testCases {
		t.Run("compatibility_"+string(tc.resourceType), func(t *testing.T) {
			// 测试新的 Provider 接口
			keys := provider.GetResourcePrimaryKeys(tc.resourceType)
			assert.Equal(t, tc.expectedKeys, keys)

			// 测试与旧的全局函数返回值一致
			oldKeys := GetResourcePrimaryKeys(tc.resourceType)
			assert.Equal(t, oldKeys, keys)
		})
	}

	// 测试所有已有的关联类型都能正确获取 Schema
	relationTypes := []RelationType{
		RelationPodWithService,
		RelationNodeWithPod,
		RelationPodToPod,
		RelationAppVersionWithGitCommit,
	}

	for _, relationType := range relationTypes {
		t.Run("compatibility_"+string(relationType), func(t *testing.T) {
			schema, err := provider.GetRelationSchema(relationType)
			assert.NoError(t, err)
			assert.NotNil(t, schema)
			assert.Equal(t, relationType, schema.RelationType)
		})
	}
}
