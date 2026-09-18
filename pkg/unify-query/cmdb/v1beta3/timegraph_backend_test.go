// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"errors"
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
	instantPlan    []cmdb.RelationPath
	rangePlan      []cmdb.RelationPath
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

func (m *fakeTimeGraphModel) QueryRelationPathResources(
	ctx context.Context,
	lookBackDelta, spaceUID, timestamp string,
	sourceType cmdb.Resource,
	targetTypes []cmdb.Resource,
	paths []cmdb.RelationPath,
	matcher cmdb.Matcher,
) ([]cmdb.PathResourcesResult, error) {
	m.instantPlan = paths
	m.instantPaths = relationPathsToResources(paths)
	return m.QueryPathResources(ctx, lookBackDelta, spaceUID, timestamp, sourceType, targetTypes, m.instantPaths, matcher)
}

func (m *fakeTimeGraphModel) QueryRelationPathResourcesRange(
	ctx context.Context,
	lookBackDelta, spaceUID, step, start, end string,
	sourceType cmdb.Resource,
	targetTypes []cmdb.Resource,
	paths []cmdb.RelationPath,
	matcher cmdb.Matcher,
) ([]cmdb.PathResourcesResult, error) {
	m.rangePlan = paths
	m.rangePaths = relationPathsToResources(paths)
	return m.QueryPathResourcesRange(ctx, lookBackDelta, spaceUID, step, start, end, sourceType, targetTypes, m.rangePaths, matcher)
}

func relationPathsToResources(paths []cmdb.RelationPath) [][]cmdb.Resource {
	result := make([][]cmdb.Resource, 0, len(paths))
	for _, path := range paths {
		resources := make([]cmdb.Resource, 0, len(path.Steps))
		for _, step := range path.Steps {
			resources = append(resources, step.ResourceType)
		}
		result = append(result, resources)
	}
	return result
}

func TestQueryResourceMatcherUsesTimeGraphBackend(t *testing.T) {
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
	model.SetTimeGraphPrimary(true)
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
	require.Equal(t, "node_with_system", fake.instantPlan[0].Steps[1].RelationType)
}

func TestTimeGraphPrimaryDoesNotFallbackToLegacyExecutor(t *testing.T) {
	executor := &mockGraphQueryExecutor{}
	model := &Model{executor: executor, schemaProvider: timeGraphTestSchemaProvider{}}
	model.SetTimeGraphPrimary(true)
	model.SetTimeGraphResolver(func(context.Context, string) (cmdb.CMDB, error) {
		return nil, errors.New("timegraph unavailable")
	})

	_, _, _, _, _, err := model.QueryResourceMatcher(
		context.Background(), "", "bkcc__2", "1700000000",
		"system", "node", cmdb.Matcher{"node": "n1"}, nil, true, nil,
	)
	require.ErrorContains(t, err, "timegraph unavailable")
	require.Empty(t, executor.sqls)
}

func TestQueryResourceMatcherRangeUsesTimeGraphBackendAndNormalizesBuckets(t *testing.T) {
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
	model.SetTimeGraphPrimary(true)
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
	require.Equal(t, "node_with_system", fake.rangePlan[0].Steps[1].RelationType)
}
