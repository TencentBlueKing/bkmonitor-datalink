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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultStaticProviderConfigKeepsBusinessAlias(t *testing.T) {
	config := DefaultStaticProviderConfig()

	assert.Equal(t, []string{"bk_biz_id"}, config.ResourcePrimaryKeys["business"])
	assert.Equal(t, []string{"bk_biz_id"}, config.ResourcePrimaryKeys["biz"])

	provider := NewStaticSchemaProvider(config)
	business, err := provider.GetResourceDefinition(NamespaceAll, "business")
	require.NoError(t, err)
	assert.Equal(t, []string{"bk_biz_id"}, business.GetPrimaryKeys())

	relationDef, err := provider.GetRelationDefinition(NamespaceAll, "business_set")
	require.NoError(t, err)
	assert.Equal(t, "business", relationDef.FromResource)
	assert.Equal(t, "set", relationDef.ToResource)
}

func TestDefaultStaticProviderConfigKeepsInfoFields(t *testing.T) {
	config := DefaultStaticProviderConfig()
	provider := NewStaticSchemaProvider(config)

	container, err := provider.GetResourceDefinition(NamespaceAll, "container")
	require.NoError(t, err)
	assert.Equal(t, []string{"bcs_cluster_id", "namespace", "pod", "container"}, container.GetPrimaryKeys())
	assert.Contains(t, container.Fields, FieldDefinition{Name: "version", Required: false})

	host, err := provider.GetResourceDefinition(NamespaceAll, "host")
	require.NoError(t, err)
	assert.Contains(t, host.Fields, FieldDefinition{Name: "env_name", Required: false})
}
