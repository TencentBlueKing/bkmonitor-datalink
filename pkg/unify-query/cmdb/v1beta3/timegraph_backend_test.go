// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

type fakeTimeGraphModel struct {
	mockCMDB
	instantResults []cmdb.PathResourcesResult
	rangeResults   []cmdb.PathResourcesResult
	instantTs      string
	rangeStart     string
	rangeEnd       string
	rangeStep      string
	instantPaths   [][]cmdb.Resource
	rangePaths     [][]cmdb.Resource
}

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

func (m *fakeTimeGraphModel) QueryPathResources(
	_ context.Context,
	_ string,
	_ string,
	timestamp string,
	_ cmdb.Resource,
	_ []cmdb.Resource,
	paths [][]cmdb.Resource,
	_ cmdb.Matcher,
) ([]cmdb.PathResourcesResult, error) {
	m.instantTs = timestamp
	m.instantPaths = paths
	return m.instantResults, nil
}

func (m *fakeTimeGraphModel) QueryPathResourcesRange(
	_ context.Context,
	_ string,
	_ string,
	step, start, end string,
	_ cmdb.Resource,
	_ []cmdb.Resource,
	paths [][]cmdb.Resource,
	_ cmdb.Matcher,
) ([]cmdb.PathResourcesResult, error) {
	m.rangeStep = step
	m.rangeStart = start
	m.rangeEnd = end
	m.rangePaths = paths
	return m.rangeResults, nil
}

func TestQueryResourceMatcherUsesTimeGraphBackend(t *testing.T) {
	previousBackend := RelationBackend
	RelationBackend = RelationBackendTimeGraph
	t.Cleanup(func() { RelationBackend = previousBackend })

	fake := &fakeTimeGraphModel{
		instantResults: []cmdb.PathResourcesResult{{
			Timestamp:  1700000000000,
			TargetType: "system",
			Path: []cmdb.PathNode{
				{ResourceType: "node", Dimensions: cmdb.Matcher{"node": "n1"}},
				{ResourceType: "system", Dimensions: cmdb.Matcher{"ip": "10.0.0.1"}},
			},
		}},
	}
	model := &Model{schemaProvider: timeGraphTestSchemaProvider{}}
	model.SetTimeGraphResolver(func(context.Context, string) (cmdb.CMDB, error) {
		return fake, nil
	})

	source, sourceInfo, path, target, matchers, err := model.QueryResourceMatcher(
		context.Background(), "", "bkcc__2", "1700000000",
		"system", "node", cmdb.Matcher{"node": "n1"}, nil, true, nil,
	)
	require.NoError(t, err)
	require.Equal(t, cmdb.Resource("node"), source)
	require.Equal(t, cmdb.Matcher{"node": "n1"}, sourceInfo)
	require.Equal(t, []string{"node", "system"}, path)
	require.Equal(t, cmdb.Resource("system"), target)
	require.Equal(t, cmdb.Matchers{cmdb.Matcher{"ip": "10.0.0.1"}}, matchers)
	require.Equal(t, "1700000000", fake.instantTs)
	require.Equal(t, [][]cmdb.Resource{{"node", "system"}}, fake.instantPaths)
}

func TestQueryResourceMatcherRangeUsesTimeGraphBackendAndNormalizesBuckets(t *testing.T) {
	previousBackend := RelationBackend
	RelationBackend = RelationBackendTimeGraph
	t.Cleanup(func() { RelationBackend = previousBackend })

	fake := &fakeTimeGraphModel{
		rangeResults: []cmdb.PathResourcesResult{
			{
				Timestamp:  1700000000000,
				TargetType: "system",
				Path: []cmdb.PathNode{
					{ResourceType: "node", Dimensions: cmdb.Matcher{"node": "n1"}},
					{ResourceType: "system", Dimensions: cmdb.Matcher{"ip": "10.0.0.1"}},
				},
			},
			{
				Timestamp:  1700000030000,
				TargetType: "system",
				Path: []cmdb.PathNode{
					{ResourceType: "node", Dimensions: cmdb.Matcher{"node": "n1"}},
					{ResourceType: "system", Dimensions: cmdb.Matcher{"ip": "10.0.0.2"}},
				},
			},
		},
	}
	model := &Model{schemaProvider: timeGraphTestSchemaProvider{}}
	model.SetTimeGraphResolver(func(context.Context, string) (cmdb.CMDB, error) {
		return fake, nil
	})

	source, sourceInfo, path, target, series, err := model.QueryResourceMatcherRange(
		context.Background(), "", "bkcc__2", "30s", "1700000000", "1700000030",
		"system", "node", cmdb.Matcher{"node": "n1"}, nil, true, nil,
	)
	require.NoError(t, err)
	require.Equal(t, cmdb.Resource("node"), source)
	require.Equal(t, cmdb.Matcher{"node": "n1"}, sourceInfo)
	require.Equal(t, []string{"node", "system"}, path)
	require.Equal(t, cmdb.Resource("system"), target)
	require.Equal(t, []cmdb.MatchersWithTimestamp{
		{Timestamp: 1700000000000, Matchers: cmdb.Matchers{cmdb.Matcher{"ip": "10.0.0.1"}}},
		{Timestamp: 1700000030000, Matchers: cmdb.Matchers{cmdb.Matcher{"ip": "10.0.0.2"}}},
	}, series)
	require.Equal(t, "30s", fake.rangeStep)
	require.Equal(t, "1700000000", fake.rangeStart)
	require.Equal(t, "1700000030", fake.rangeEnd)
	require.Equal(t, [][]cmdb.Resource{{"node", "system"}}, fake.rangePaths)
}

func TestNormalizeRelationBackendFallsBackToSurrealDB(t *testing.T) {
	require.Equal(t, RelationBackendSurrealDB, normalizeRelationBackend("unknown"))
	require.Equal(t, RelationBackendTimeGraph, normalizeRelationBackend(RelationBackendTimeGraph))
	require.Equal(t, RelationBackendAuto, normalizeRelationBackend(RelationBackendAuto))
}
