// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

type timeGraphTestSchemaProvider struct{}

func (timeGraphTestSchemaProvider) GetResourcePrimaryKeys(_ string, resourceType ResourceType) []string {
	switch resourceType {
	case "node":
		return []string{"node"}
	case "system":
		return []string{"ip"}
	default:
		return nil
	}
}

func (timeGraphTestSchemaProvider) GetResourceFields(namespace string, resourceType ResourceType) []string {
	if resourceType == "node" {
		return []string{"node", "region"}
	}
	return timeGraphTestSchemaProvider{}.GetResourcePrimaryKeys(namespace, resourceType)
}

func (timeGraphTestSchemaProvider) ListResourceTypes(string) []ResourceType {
	return []ResourceType{"node", "system"}
}

func (timeGraphTestSchemaProvider) ListRelationSchemas(string) []RelationSchema {
	return []RelationSchema{{
		RelationType:  "node_with_system",
		Category:      RelationCategoryStatic,
		FromType:      "node",
		ToType:        "system",
		IsDirectional: true,
	}}
}

func TestBuildTimeGraphRequestAppliesSourceExpandInfo(t *testing.T) {
	model := &Model{schemaProvider: timeGraphTestSchemaProvider{}}
	tests := []struct {
		name       string
		expand     cmdb.Matcher
		wantExpand map[string]string
		wantSource map[string]string
	}{
		{name: "known_field", expand: cmdb.Matcher{"region": "east"}, wantExpand: map[string]string{"region": "east"}, wantSource: map[string]string{"node": "n1", "region": "east"}},
		{name: "legacy_unknown_field", expand: cmdb.Matcher{"legacy_label": "value"}, wantExpand: map[string]string{"legacy_label": "value"}, wantSource: map[string]string{"node": "n1", "legacy_label": "value"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, _, err := model.buildTimeGraphRequest(context.Background(),
				"bkcc__2", "system", "node",
				cmdb.Matcher{"node": "n1"}, tc.expand, true, nil,
			)
			require.NoError(t, err)
			require.Equal(t, tc.wantExpand, req.SourceExpandInfo)
			require.Equal(t, tc.wantSource, req.SourceInfo)
		})
	}
}

func TestBuildTimeGraphRequestInfersLegacySourceAndImplicitTarget(t *testing.T) {
	model := &Model{schemaProvider: timeGraphTestSchemaProvider{}}
	req, paths, err := model.buildTimeGraphRequest(context.Background(),
		"bkcc__2", "", "",
		cmdb.Matcher{"node": "n1"}, nil, false, nil,
	)
	require.NoError(t, err)
	require.Equal(t, ResourceType("node"), req.SourceType)
	require.Equal(t, ResourceType("node"), req.TargetType)
	require.False(t, req.TargetTypeExplicit)
	require.Equal(t, []resourcePath{{Steps: []resourcePathStep{{ResourceType: "node"}}}}, paths)
}

func TestQueryResourceMatcherKeepsDistinctCompositePrimaryKeys(t *testing.T) {
	provider := contractSchemaProvider{
		resources: []ResourceType{"source", "target"},
		primary: map[ResourceType][]string{
			"source": {"id"},
			"target": {"a", "b"},
		},
		fields: map[ResourceType][]string{
			"source": {"id"},
			"target": {"a", "b"},
		},
		schemas: []RelationSchema{{
			RelationType: "source_to_target",
			Category:     RelationCategoryStatic,
			FromType:     "source",
			ToType:       "target",
			MetricName:   "source_to_target_flow",
		}},
	}
	model := legacyRegressionModel(provider)
	model.timeGraphVMQuery = func(_ context.Context, query *structured.QueryTs, _ string, _ bool, _, end time.Time, _ time.Duration) (pl.Matrix, error) {
		require.Equal(t, "source_to_target_flow", query.QueryList[0].FieldName)
		return append(
			contractMatrix(map[string]string{"id": "s1", "a": "x,b=y", "b": "z"}, end.UnixMilli()),
			contractMatrix(map[string]string{"id": "s1", "a": "x", "b": "y,b=z"}, end.UnixMilli())...,
		), nil
	}
	_, _, _, _, got, err := model.QueryResourceMatcher(
		initTimeGraphQueryTestEnvironment(), "10m", "space", "1700000000",
		"target", "source", cmdb.Matcher{"id": "s1"}, nil, false,
		[]cmdb.Resource{"source", "target"},
	)
	require.NoError(t, err)
	require.ElementsMatch(t, cmdb.Matchers{{"a": "x,b=y", "b": "z"}, {"a": "x", "b": "y,b=z"}}, got)
}

func TestBuildTimeGraphRequestPreservesSourceIdentity(t *testing.T) {
	provider := contractSchemaProvider{
		resources: []ResourceType{"source"},
		primary:   map[ResourceType][]string{"source": {"scope", "id"}},
		fields:    map[ResourceType][]string{"source": {"scope", "id", "version"}},
	}
	tests := []struct {
		name     string
		source   cmdb.Matcher
		expand   cmdb.Matcher
		want     map[string]string
		conflict bool
	}{
		{name: "conflicting_identity", source: cmdb.Matcher{"scope": "s1", "id": "n1"}, expand: cmdb.Matcher{"id": "n2"}, conflict: true},
		{name: "same_identity_and_attribute", source: cmdb.Matcher{"scope": "s1", "id": "n1"}, expand: cmdb.Matcher{"id": "n1", "version": "v2"}, want: map[string]string{"scope": "s1", "id": "n1", "version": "v2"}},
		{name: "complete_partial_identity", source: cmdb.Matcher{"scope": "s1"}, expand: cmdb.Matcher{"id": "n1", "version": "v2"}, want: map[string]string{"scope": "s1", "id": "n1", "version": "v2"}},
		{name: "attribute_only", expand: cmdb.Matcher{"version": "v2"}, want: map[string]string{"version": "v2"}},
		{name: "empty_identity_is_unspecified", source: cmdb.Matcher{"id": ""}, expand: cmdb.Matcher{"id": "n2"}, want: map[string]string{"id": "n2"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			model := &Model{schemaProvider: provider}
			req, _, err := model.buildTimeGraphRequest(context.Background(), "test-space", "", "source", tc.source, tc.expand, false, nil)
			if tc.conflict {
				require.ErrorContains(t, err, "source_expand_info conflicts with source_info")
				require.Nil(t, req)
				require.NotContains(t, err.Error(), "n2")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, req.SourceInfo)
			require.Equal(t, matcherToMap(tc.expand), req.SourceExpandInfo)
		})
	}
}

func TestTimeGraphLegacyQueriesRejectConflictingSourceIdentityBeforeQuery(t *testing.T) {
	for _, rangeQuery := range []bool{false, true} {
		name := "instant"
		if rangeQuery {
			name = "range"
		}
		t.Run(name, func(t *testing.T) {
			model := legacyRegressionModel(timeGraphTestSchemaProvider{})
			model.timeGraphVMQuery = func(context.Context, *structured.QueryTs, string, bool, time.Time, time.Time, time.Duration) (pl.Matrix, error) {
				t.Fatal("conflicting source identity must fail before querying")
				return nil, nil
			}
			source := cmdb.Matcher{"node": "n1"}
			expand := cmdb.Matcher{"node": "n2"}
			var err error
			if rangeQuery {
				_, _, _, _, _, err = model.QueryResourceMatcherRange(context.Background(), "", "test-space", "30s", "1700000000", "1700000030", "system", "node", source, expand, false, nil)
			} else {
				_, _, _, _, _, err = model.QueryResourceMatcher(context.Background(), "", "test-space", "1700000000", "system", "node", source, expand, false, nil)
			}
			require.ErrorContains(t, err, "source_expand_info conflicts with source_info")
			require.Equal(t, cmdb.Matcher{"node": "n1"}, source)
			require.Equal(t, cmdb.Matcher{"node": "n2"}, expand)
		})
	}
}
