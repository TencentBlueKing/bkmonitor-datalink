// Copyright (C) 2022 THL A29 Limited, a Tencent company.
// Licensed under the MIT License.

package v1beta3

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

// reversedRelationProvider lists relation definitions in reverse, as a Redis
// provider may return them in any order.
type reversedRelationProvider struct {
	*relation.StaticSchemaProvider
}

func (p reversedRelationProvider) ListRelationDefinitions(namespace string) ([]*relation.RelationDefinition, error) {
	definitions, err := p.StaticSchemaProvider.ListRelationDefinitions(namespace)
	definitions = slices.Clone(definitions)
	slices.Reverse(definitions)
	return definitions, err
}

// reversedSchemaList hands the path search its relation schemas in reverse.
type reversedSchemaList struct {
	SchemaProvider
}

func (p reversedSchemaList) ListRelationSchemas(namespace string) []RelationSchema {
	schemas := slices.Clone(p.SchemaProvider.ListRelationSchemas(namespace))
	slices.Reverse(schemas)
	return schemas
}

func TestRelationSchemasDoNotDependOnProviderOrder(t *testing.T) {
	base := relation.NewDefaultStaticSchemaProvider()
	forward := NewSchemaProviderFromRelation(base)
	reversed := NewSchemaProviderFromRelation(reversedRelationProvider{base})

	schemas := forward.ListRelationSchemas(relation.NamespaceAll)
	require.Equal(t, schemas, reversed.ListRelationSchemas(relation.NamespaceAll))

	want := make(map[RelationType]struct{})
	for _, definition := range relation.DefaultRelationDefinitions() {
		want[RelationType(definition.Name)] = struct{}{}
	}
	listed := make(map[RelationType]struct{})
	for _, schema := range schemas {
		listed[schema.RelationType] = struct{}{}
	}
	require.Equal(t, want, listed)

	candidates := func(t *testing.T, provider SchemaProvider, source, target ResourceType, categories ...RelationCategory) []string {
		finder := NewPathFinder(WithSchemaProvider(provider), WithAllowedCategories(categories...), WithMaxHops(3), WithLegacyPathConstraints())
		paths, err := finder.FindAllPaths(source, target, nil)
		require.NoError(t, err)
		keys := make([]string, 0, len(paths))
		for _, path := range sortPathsForQuery(paths) {
			keys = append(keys, resourcePathSortKey(path))
		}
		return keys
	}
	for _, c := range []struct {
		source, target ResourceType
		categories     []RelationCategory
	}{
		{source: "pod", target: "system", categories: []RelationCategory{RelationCategoryStatic}},
		{source: "pod", target: "system", categories: []RelationCategory{RelationCategoryStatic, RelationCategoryDynamic}},
		{source: "apm_service_instance", target: "system", categories: []RelationCategory{RelationCategoryStatic}},
		{source: "datasource", target: "system", categories: []RelationCategory{RelationCategoryStatic}},
		{source: "job", target: "system", categories: []RelationCategory{RelationCategoryStatic}},
	} {
		t.Run(string(c.source)+"->"+string(c.target), func(t *testing.T) {
			// 候选路径的优先级只由排序决定，与关系列表的先后无关。
			got := candidates(t, forward, c.source, c.target, c.categories...)
			require.NotEmpty(t, got)
			require.Equal(t, got, candidates(t, reversedSchemaList{forward}, c.source, c.target, c.categories...))
		})
	}
}

func TestSchemaAdapterPreservesConfiguredMetricNames(t *testing.T) {
	for _, directional := range []bool{false, true} {
		for _, metric := range []string{"", "custom_flow"} {
			provider := NewSchemaProviderFromRelation(relation.NewStaticSchemaProvider(relation.StaticProviderConfig{
				RelationSchemas: []relation.RelationSchema{{RelationName: "calls", Category: relation.RelationCategoryStatic, FromType: "service", ToType: "database", IsDirectional: directional, MetricName: metric}},
			}))
			schemas := provider.ListRelationSchemas(relation.NamespaceAll)
			require.Len(t, schemas, 1)
			want := metric
			if directional && metric == "" {
				want = "service_to_database_flow"
			}
			require.Equal(t, want, schemas[0].MetricName)
		}
	}
}
