// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.
package main

import (
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

func TestSchemaCatalogMatchesPublishedArtifacts(t *testing.T) {
	wantJSON, err := os.ReadFile("../../api/timegraph-schema.json")
	require.NoError(t, err)
	wantMarkdown, err := os.ReadFile("../../api/timegraph-schema.md")
	require.NoError(t, err)
	t.Chdir(t.TempDir())
	require.NoError(t, os.MkdirAll("docs/api", 0o755))
	main()
	gotJSON, err := os.ReadFile("docs/api/timegraph-schema.json")
	require.NoError(t, err)
	require.JSONEq(t, string(wantJSON), string(gotJSON))
	gotMarkdown, err := os.ReadFile("docs/api/timegraph-schema.md")
	require.NoError(t, err)
	require.Equal(t, string(wantMarkdown), string(gotMarkdown))
}

func TestSchemaCatalogFieldAndRelationForms(t *testing.T) {
	resource := relation.DefaultResourceDefinitions()[0]
	definition := relation.DefaultRelationDefinitions()[0]
	originalResource, originalRelation := *resource, *definition
	t.Cleanup(func() { *resource, *definition = originalResource, originalRelation })
	*resource = relation.ResourceDefinition{Name: "contract_resource", Fields: []relation.FieldDefinition{
		{Name: "id", Required: true, Type: relation.FieldTypeString},
		{Name: "label"}, {Name: "count", Type: relation.FieldTypeInteger},
		{Name: "ratio", Type: relation.FieldTypeNumber}, {Name: "ready", Type: relation.FieldTypeBoolean},
	}}
	for _, category := range []string{"", "static", "dynamic"} {
		for _, directional := range []bool{false, true} {
			for _, override := range []string{"", "explicit_metric"} {
				t.Run(fmt.Sprintf("%s/%t/%s", category, directional, override), func(t *testing.T) {
					t.Chdir(t.TempDir())
					require.NoError(t, os.MkdirAll("docs/api", 0o755))
					*definition = relation.RelationDefinition{Name: "contract_relation", FromResource: "node", ToResource: "pod", Category: category, IsDirectional: directional, Labels: map[string]string{"metric_name": override}}
					main()
					body, err := os.ReadFile("docs/api/timegraph-schema.md")
					require.NoError(t, err)
					require.Contains(t, string(body), "| contract_resource | id | label, count, ratio, ready | count:integer, ratio:number, ready:boolean |")
					wantCategory, wantDirection, wantMetric := category, "bidirectional", "node_with_pod_relation"
					if category == "" {
						wantCategory = "static"
					}
					if directional {
						wantDirection, wantMetric = "outbound", "node_to_pod_flow"
					}
					if override != "" {
						wantMetric = override
					}
					require.Contains(t, string(body), fmt.Sprintf("| contract_relation | node | pod | %s | %s | %s |", wantCategory, wantDirection, wantMetric))
				})
			}
		}
	}
}

func TestSchemaCatalogReportsEncodingAndFileErrors(t *testing.T) {
	t.Run("invalid-spec", func(t *testing.T) {
		t.Chdir(t.TempDir())
		require.NoError(t, os.MkdirAll("docs/api", 0o755))
		resource := relation.DefaultResourceDefinitions()[0]
		original := resource.Spec
		t.Cleanup(func() { resource.Spec = original })
		resource.Spec = map[string]interface{}{"invalid-number": math.NaN()}
		require.Panics(t, main)
		_, err := os.Stat("docs/api/timegraph-schema.json")
		require.ErrorIs(t, err, os.ErrNotExist)
	})
	for _, path := range []string{"docs/api/timegraph-schema.json", "docs/api/timegraph-schema.md"} {
		t.Run(path, func(t *testing.T) {
			t.Chdir(t.TempDir())
			require.NoError(t, os.MkdirAll(path, 0o755))
			require.Panics(t, main)
		})
	}
}
