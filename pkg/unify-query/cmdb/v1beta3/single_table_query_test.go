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

	"github.com/stretchr/testify/require"
)

func TestSingleTableLogicalEdgeIdentity(t *testing.T) {
	provider := newTableSchemaProvider(map[ResourceType]tableResourceDefinition{
		ResourceTypeHost: {primaryKeys: []string{"bk_host_id"}}, ResourceTypeModule: {primaryKeys: []string{"bk_module_id"}},
	}, []RelationSchema{
		{RelationType: "first_link", Category: RelationCategoryStatic, FromType: ResourceTypeHost, ToType: ResourceTypeModule},
		{RelationType: "second_link", Category: RelationCategoryStatic, FromType: ResourceTypeHost, ToType: ResourceTypeModule},
	})
	sql := NewSurrealQueryBuilderWithSchemaProvider(&QueryRequest{SourceType: ResourceTypeHost, SourceInfo: map[string]string{"bk_host_id": "1"}, TargetType: ResourceTypeModule, MaxHops: 1}, provider).Build()
	// History segments share the logical relation ID within a table, while IDs
	// from different relation tables must not overwrite one another in the graph.
	require.Contains(t, sql, "type::record('first_link', relation_id)")
	require.Contains(t, sql, "type::record('second_link', relation_id)")
	require.NotContains(t, sql, "relation_id: <string>id")
	require.NotContains(t, sql, "_liveness_record")
	require.NotContains(t, sql, "_active_edge_view")
}
