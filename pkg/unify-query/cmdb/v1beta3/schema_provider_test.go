// Copyright (C) 2022 THL A29 Limited, a Tencent company.
// Licensed under the MIT License.

package v1beta3

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

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
