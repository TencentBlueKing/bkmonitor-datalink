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
	sourceExpand   cmdb.Matcher
	rangeExpand    cmdb.Matcher
}

type mockCMDB struct{}

func (mockCMDB) QueryResourceMatcher(
	context.Context, string, string, string, cmdb.Resource, cmdb.Resource,
	cmdb.Matcher, cmdb.Matcher, bool, []cmdb.Resource,
) (cmdb.Resource, cmdb.Matcher, []string, cmdb.Resource, cmdb.Matchers, error) {
	return "", nil, nil, "", nil, nil
}

func (mockCMDB) QueryResourceMatcherRange(
	context.Context, string, string, string, string, string, cmdb.Resource, cmdb.Resource,
	cmdb.Matcher, cmdb.Matcher, bool, []cmdb.Resource,
) (cmdb.Resource, cmdb.Matcher, []string, cmdb.Resource, []cmdb.MatchersWithTimestamp, error) {
	return "", nil, nil, "", nil, nil
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

func (m *fakeTimeGraphModel) queryRelationPathResourcesWithSourceExpand(
	ctx context.Context,
	lookBackDelta, spaceUID, timestamp string,
	sourceType cmdb.Resource,
	targetTypes []cmdb.Resource,
	paths []cmdb.RelationPath,
	matcher, expand cmdb.Matcher,
) ([]cmdb.PathResourcesResult, error) {
	m.sourceExpand = expand
	return m.QueryRelationPathResources(ctx, lookBackDelta, spaceUID, timestamp, sourceType, targetTypes, paths, matcher)
}

func (m *fakeTimeGraphModel) queryRelationPathResourcesRangeWithSourceExpand(
	ctx context.Context,
	lookBackDelta, spaceUID, step, start, end string,
	sourceType cmdb.Resource,
	targetTypes []cmdb.Resource,
	paths []cmdb.RelationPath,
	matcher, expand cmdb.Matcher,
) ([]cmdb.PathResourcesResult, error) {
	m.rangeExpand = expand
	return m.QueryRelationPathResourcesRange(ctx, lookBackDelta, spaceUID, step, start, end, sourceType, targetTypes, paths, matcher)
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
	model := &Model{schemaProvider: timeGraphTestSchemaProvider{}}
	model.SetTimeGraphResolver(func(context.Context, string) (cmdb.CMDB, error) {
		return nil, errors.New("timegraph unavailable")
	})

	_, _, _, _, _, err := model.QueryResourceMatcher(
		context.Background(), "", "bkcc__2", "1700000000",
		"system", "node", cmdb.Matcher{"node": "n1"}, nil, true, nil,
	)
	require.ErrorContains(t, err, "timegraph unavailable")
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
			req, _, err := model.buildTimeGraphRequest(
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
	req, paths, err := model.buildTimeGraphRequest(
		"bkcc__2", "", "",
		cmdb.Matcher{"node": "n1"}, nil, false, nil,
	)
	require.NoError(t, err)
	require.Equal(t, ResourceType("node"), req.SourceType)
	require.Equal(t, ResourceType("node"), req.TargetType)
	require.False(t, req.TargetTypeExplicit)
	require.Equal(t, []resourcePath{{Steps: []resourcePathStep{{ResourceType: "node"}}}}, paths)
}

func TestQueryResourceMatcherPassesSourceExpandInfoToTimeGraph(t *testing.T) {
	fake := &fakeTimeGraphModel{}
	model := &Model{schemaProvider: timeGraphTestSchemaProvider{}}
	model.SetTimeGraphResolver(func(context.Context, string) (cmdb.CMDB, error) {
		return fake, nil
	})

	_, _, _, _, _, err := model.QueryResourceMatcher(
		context.Background(), "", "bkcc__2", "1700000000",
		"system", "node", cmdb.Matcher{"node": "n1"}, cmdb.Matcher{"region": "east"}, false, nil,
	)
	require.NoError(t, err)
	require.Equal(t, cmdb.Matcher{"region": "east"}, fake.sourceExpand)
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
	tests := []struct {
		name         string
		instant      []cmdb.PathResourcesResult
		wantMatchers cmdb.Matchers
	}{
		{
			name: "values_containing_identity_delimiters_remain_distinct",
			instant: []cmdb.PathResourcesResult{
				{TargetType: "target", Path: []cmdb.PathNode{
					{ResourceType: "source", Dimensions: cmdb.Matcher{"id": "s1"}},
					{ResourceType: "target", Dimensions: cmdb.Matcher{"a": "x,b=y", "b": "z"}},
				}},
				{TargetType: "target", Path: []cmdb.PathNode{
					{ResourceType: "source", Dimensions: cmdb.Matcher{"id": "s1"}},
					{ResourceType: "target", Dimensions: cmdb.Matcher{"a": "x", "b": "y,b=z"}},
				}},
			},
			wantMatchers: cmdb.Matchers{
				{"a": "x,b=y", "b": "z"},
				{"a": "x", "b": "y,b=z"},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeTimeGraphModel{instantResults: tc.instant}
			model := &Model{schemaProvider: provider}
			model.SetTimeGraphResolver(func(context.Context, string) (cmdb.CMDB, error) {
				return fake, nil
			})

			_, _, _, _, got, err := model.QueryResourceMatcher(
				initTimeGraphQueryTestEnvironment(), "10m", "space", "1700000000",
				"target", "source", cmdb.Matcher{"id": "s1"}, nil, false,
				[]cmdb.Resource{"source", "target"},
			)
			require.NoError(t, err)
			require.ElementsMatch(t, tc.wantMatchers, got)
		})
	}
}

func TestQueryResourceMatcherRangeDefaultsEmptyStep(t *testing.T) {
	tests := []struct {
		name     string
		step     string
		wantStep string
	}{
		{name: "empty_step_uses_one_minute", step: "", wantStep: "1m0s"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeTimeGraphModel{}
			model := &Model{schemaProvider: timeGraphTestSchemaProvider{}}
			model.SetTimeGraphResolver(func(context.Context, string) (cmdb.CMDB, error) {
				return fake, nil
			})

			_, _, _, _, _, err := model.QueryResourceMatcherRange(
				initTimeGraphQueryTestEnvironment(), "10m", "bkcc__2", tc.step,
				"1700000000", "1700000060", "system", "node", cmdb.Matcher{"node": "n1"}, nil, false, nil,
			)
			require.NoError(t, err)
			require.Equal(t, tc.wantStep, fake.rangeStep)
		})
	}
}
