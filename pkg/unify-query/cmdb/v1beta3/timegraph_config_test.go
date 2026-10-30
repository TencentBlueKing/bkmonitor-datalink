// Copyright (C) 2022 THL A29 Limited, a Tencent company.
// Licensed under the MIT License.

package v1beta3

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

func TestTimeGraphConfigPreservesSchemaAndFieldOwnership(t *testing.T) {
	provider := relation.NewStaticSchemaProvider(relation.StaticProviderConfig{
		ResourceDefinitions: []*relation.ResourceDefinition{
			{Name: "service", Fields: []relation.FieldDefinition{{Name: "tenant", Required: true}, {Name: "id", Required: true}, {Name: "version"}}},
			{Name: "database", Fields: []relation.FieldDefinition{{Name: "name", Required: true}, {Name: "zone"}}},
		},
		RelationSchemas: []relation.RelationSchema{{RelationName: "calls", FromType: "service", ToType: "database", Category: relation.RelationCategoryDynamic, IsDirectional: true, MetricName: "calls_metric"}},
	})
	previous := GetUnderlyingProvider(GetSchemaProvider())
	InitSchemaProvider(provider)
	t.Cleanup(func() { InitSchemaProvider(previous) })
	config := buildTimeGraphConfig(GetSchemaProvider(), "space")
	require.Equal(t, []TimeGraphResourceConfig{
		{Name: "database", Index: cmdb.Index{"name"}, Info: cmdb.Index{"zone"}},
		{Name: "service", Index: cmdb.Index{"tenant", "id"}, Info: cmdb.Index{"version"}},
	}, config.Resource)
	require.Equal(t, []TimeGraphRelationConfig{{Resources: []cmdb.Resource{"service", "database"}, RelationType: "calls", MetricName: "calls_metric", Category: string(RelationCategoryDynamic), IsDirectional: true}}, config.Relation)
	index := ResourcesIndex("service", "database", "unknown")
	require.Equal(t, cmdb.Index{"tenant", "id", "name"}, index)
	index[0] = "changed"
	require.Equal(t, cmdb.Index{"tenant", "id"}, ResourcesIndex("service"))
	info := ResourcesInfo("service")
	require.Equal(t, cmdb.Index{"version"}, info)
	info[0] = "changed"
	require.Equal(t, cmdb.Index{"version"}, ResourcesInfo("service"))
	require.Equal(t, cmdb.Index{"zone"}, ResourcesInfo("database"))
	require.Nil(t, ResourcesInfo("unknown"))
	require.Nil(t, ResourcesIndex())

	InitSchemaProvider(relation.NewStaticSchemaProvider(relation.StaticProviderConfig{}))
	empty := buildTimeGraphConfig(GetSchemaProvider(), "space")
	require.Empty(t, empty.Resource)
	require.Empty(t, empty.Relation)
	require.Nil(t, ResourcesIndex("service"))
	require.Nil(t, ResourcesInfo("service"))
}
