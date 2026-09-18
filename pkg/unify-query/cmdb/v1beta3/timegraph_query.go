// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pkg/errors"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/prometheus"
)

const (
	timeGraphQueryMaxRouting = 2
	timeGraphQueryTimeout    = time.Minute
)

// buildTimeGraphFromRelations materializes relation metrics into a temporary
// in-memory TimeGraph for one query window.
func (m *Model) buildTimeGraphFromRelations(ctx context.Context, spaceUID string, start, end time.Time, step time.Duration, sourceInfo cmdb.Matcher, relations []cmdb.Relation, lookBackDelta string) (*TimeGraph, error) {
	var err error
	ctx, span := trace.NewSpan(ctx, "build-time-graph-from-relations")
	defer span.End(&err)
	span.Set("space-uid", spaceUID)
	span.Set("relation-count", len(relations))
	span.Set("query-start", start.Unix())
	span.Set("query-end", end.Unix())
	span.Set("query-step-seconds", step.Seconds())

	tg := NewTimeGraphWithConfig(m.timeGraphConfig(spaceUID))
	var lookBack time.Duration
	if lookBackDelta != "" {
		lookBack, err = time.ParseDuration(lookBackDelta)
		if err != nil {
			return nil, errors.WithMessage(err, "parse look back delta")
		}
	}

	baseQueryParams := metadata.GetQueryParams(ctx)
	var instance tsdb.Instance
	if baseQueryParams.IsDirectQuery() {
		instance = prometheus.GetTsDbInstance(ctx, &metadata.Query{
			StorageType: metadata.VictoriaMetricsStorageType,
		})
		if instance == nil {
			return nil, fmt.Errorf("%s storage get error", metadata.VictoriaMetricsStorageType)
		}
	} else {
		instance = prometheus.NewInstance(ctx, promql.GlobalEngine, &prometheus.QueryRangeStorage{
			QueryMaxRouting: timeGraphQueryMaxRouting,
			Timeout:         timeGraphQueryTimeout,
		}, lookBack, timeGraphQueryMaxRouting)
	}

	instant := start.Equal(end)
	for _, relation := range relations {
		if len(relation.V) != 2 {
			continue
		}

		relationCtx := metadata.InitHashID(ctx)
		metadata.GetQueryParams(relationCtx).SetIsSkipK8s(true)
		queryTs, err := tg.MakeQueryTs(relationCtx, spaceUID, sourceInfo, start, end, step, relation)
		if err != nil {
			return nil, errors.WithMessagef(err, "make query ts error for relation %v", relation)
		}
		if queryTs == nil {
			continue
		}

		queryRef, err := queryTs.ToQueryReference(relationCtx)
		if err != nil {
			return nil, errors.WithMessage(err, "to query reference")
		}
		metadata.SetExpand(relationCtx, query.ToVmExpand(relationCtx, queryRef))

		expr, err := queryTs.ToPromExpr(relationCtx, nil)
		if err != nil {
			return nil, errors.WithMessage(err, "to prom expr")
		}
		relationQueryParams := metadata.GetQueryParams(relationCtx)

		var matrix pl.Matrix
		if instant {
			vector, queryErr := instance.DirectQuery(relationCtx, expr.String(), relationQueryParams.End)
			if queryErr != nil {
				return nil, errors.WithMessage(queryErr, "direct query")
			}
			matrix = vectorToMatrix(vector)
		} else {
			var queryErr error
			matrix, _, queryErr = instance.DirectQueryRange(
				relationCtx,
				expr.String(),
				relationQueryParams.AlignStart,
				relationQueryParams.End,
				relationQueryParams.Step,
			)
			if queryErr != nil {
				return nil, errors.WithMessage(queryErr, "direct query range")
			}
		}

		for _, series := range matrix {
			info := make(cmdb.Matcher, len(series.Metric))
			for _, label := range series.Metric {
				info[label.Name] = label.Value
			}
			timestamps := make([]int64, len(series.Points))
			for i, point := range series.Points {
				timestamps[i] = point.T
			}
			if err = tg.AddTimeRelationWithRelation(relationCtx, relation, info, timestamps...); err != nil {
				return nil, errors.WithMessage(err, "add time relation")
			}
		}
	}

	return tg, nil
}

// buildRelationsFromPaths extracts and de-duplicates adjacent edges from paths.
func (m *Model) buildRelationsFromPaths(paths [][]cmdb.Resource) []cmdb.Relation {
	return m.buildRelationsFromRelationPathsForNamespace("", cmdb.RelationPathsFromResourcePaths(paths))
}

func (m *Model) buildRelationsFromRelationPaths(paths []cmdb.RelationPath) []cmdb.Relation {
	return m.buildRelationsFromRelationPathsForNamespace("", paths)
}

func (m *Model) buildRelationsFromRelationPathsForNamespace(namespace string, paths []cmdb.RelationPath) []cmdb.Relation {
	type relationKey struct {
		source       cmdb.Resource
		target       cmdb.Resource
		relationType string
		metricName   string
	}
	seen := make(map[relationKey]struct{})
	relations := make([]cmdb.Relation, 0)
	for _, path := range paths {
		for i := 1; i < len(path.Steps); i++ {
			source := path.Steps[i-1].ResourceType
			target := path.Steps[i].ResourceType
			if source == "" || target == "" {
				continue
			}
			candidates := m.timeGraphRelationCandidates(namespace, source, target, path.Steps[i])
			for _, candidate := range candidates {
				key := relationKey{
					source:       source,
					target:       target,
					relationType: candidate.RelationType,
					metricName:   candidate.MetricName,
				}
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				relations = append(relations, cmdb.Relation{
					V:            []cmdb.Resource{source, target},
					RelationType: candidate.RelationType,
					MetricName:   candidate.MetricName,
				})
			}
		}
	}
	return relations
}

func (m *Model) timeGraphRelationCandidates(
	namespace string,
	source, target cmdb.Resource,
	step cmdb.RelationPathStep,
) []cmdb.Relation {
	cfg := m.timeGraphConfig(namespace)

	result := make([]cmdb.Relation, 0)
	for _, configured := range cfg.Relation {
		if len(configured.Resources) != 2 {
			continue
		}
		if step.RelationType != "" && !sameRelationType(configured.RelationType, step.RelationType) {
			continue
		}
		if !sameResourcePair(configured.Resources, source, target) {
			continue
		}
		metricName := configured.MetricName
		if step.MetricName != "" {
			metricName = step.MetricName
		}
		result = append(result, cmdb.Relation{
			V:            []cmdb.Resource{source, target},
			RelationType: configured.RelationType,
			MetricName:   metricName,
		})
	}
	if len(result) > 0 {
		return result
	}
	return []cmdb.Relation{{
		V:            []cmdb.Resource{source, target},
		RelationType: step.RelationType,
		MetricName:   step.MetricName,
	}}
}

func sameRelationType(left, right string) bool {
	if left == right {
		return true
	}
	if separator := strings.IndexByte(left, ':'); separator >= 0 {
		return left[separator+1:] == right
	}
	return false
}

func sameResourcePair(resources []cmdb.Resource, source, target cmdb.Resource) bool {
	if len(resources) != 2 {
		return false
	}
	return (resources[0] == source && resources[1] == target) || (resources[0] == target && resources[1] == source)
}

func (m *Model) resolveTimeGraphPaths(ctx context.Context, spaceUID string, sourceType cmdb.Resource, targetTypes []cmdb.Resource, requested [][]cmdb.Resource) ([][]cmdb.Resource, error) {
	if len(requested) > 0 {
		paths := make([][]cmdb.Resource, 0, len(requested))
		for _, path := range requested {
			if len(path) >= 2 {
				paths = append(paths, path)
			}
		}
		if len(paths) > 0 {
			return paths, nil
		}
	}

	pathFinder := NewPathFinder(
		WithSchemaProvider(m.getSchemaProvider()),
		WithNamespace(spaceUID),
		WithMaxHops(DefaultMaxHops),
	)
	var paths [][]cmdb.Resource
	for _, targetType := range targetTypes {
		graphPaths, err := pathFinder.FindAllPaths(ResourceType(sourceType), ResourceType(targetType), nil)
		if err != nil {
			continue
		}
		for _, graphPath := range graphPaths {
			path := make([]cmdb.Resource, 0, len(graphPath.Steps))
			for _, step := range graphPath.Steps {
				path = append(path, cmdb.Resource(step.ResourceType))
			}
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return nil, errors.New("no paths found")
	}
	return paths, nil
}

func (m *Model) queryTimeGraph(ctx context.Context, lookBackDelta, spaceUID string, start, end time.Time, step time.Duration, sourceType cmdb.Resource, targetTypes []cmdb.Resource, pathResources [][]cmdb.Resource, matcher cmdb.Matcher) ([]cmdb.PathResourcesResult, error) {
	paths, err := m.resolveTimeGraphPaths(ctx, spaceUID, sourceType, targetTypes, pathResources)
	if err != nil {
		return nil, err
	}
	return m.queryRelationTimeGraph(ctx, lookBackDelta, spaceUID, start, end, step, sourceType, targetTypes, cmdb.RelationPathsFromResourcePaths(paths), matcher)
}

func (m *Model) queryRelationTimeGraph(ctx context.Context, lookBackDelta, spaceUID string, start, end time.Time, step time.Duration, sourceType cmdb.Resource, targetTypes []cmdb.Resource, paths []cmdb.RelationPath, matcher cmdb.Matcher) (results []cmdb.PathResourcesResult, err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-query-relation")
	defer span.End(&err)
	span.Set("space-uid", spaceUID)
	span.Set("source-type", sourceType)
	span.Set("target-types", targetTypes)
	span.Set("path-count", len(paths))
	span.Set("query-start", start.Unix())
	span.Set("query-end", end.Unix())
	span.Set("query-step-seconds", step.Seconds())
	relations := m.buildRelationsFromRelationPathsForNamespace(spaceUID, paths)
	span.Set("relation-count", len(relations))

	tg, err := m.buildTimeGraphFromRelations(ctx, spaceUID, start, end, step, matcher, relations, lookBackDelta)
	if err != nil {
		return nil, errors.WithMessage(err, "build time graph")
	}
	defer tg.Clean(ctx)

	results = make([]cmdb.PathResourcesResult, 0)
	pathResults, queryErr := tg.FindRelationPathResources(ctx, sourceType, targetTypes, matcher, paths)
	if queryErr != nil {
		return nil, errors.WithMessagef(queryErr, "find path from %s", sourceType)
	}
	for _, result := range pathResults {
		results = append(results, cmdb.PathResourcesResult{
			Timestamp:  result.Timestamp,
			TargetType: result.TargetType,
			Path:       result.Path,
		})
	}
	span.Set("raw-result-count", len(results))
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Timestamp == results[j].Timestamp {
			return results[i].TargetType < results[j].TargetType
		}
		return results[i].Timestamp < results[j].Timestamp
	})
	return results, nil
}

func (m *Model) QueryPathResources(ctx context.Context, lookBackDelta, spaceUID, timestamp string, sourceType cmdb.Resource, targetTypes []cmdb.Resource, pathResources [][]cmdb.Resource, matcher cmdb.Matcher) ([]cmdb.PathResourcesResult, error) {
	if spaceUID == "" {
		return nil, errors.New("space uid is empty")
	}
	if timestamp == "" {
		return nil, errors.New("timestamp is empty")
	}
	if sourceType == "" {
		return nil, errors.New("source type is empty")
	}
	if len(targetTypes) == 0 {
		return nil, errors.New("target types is empty")
	}
	timestampValue, err := cast.ToInt64E(timestamp)
	if err != nil {
		return nil, errors.WithMessage(err, "parse timestamp")
	}
	return m.queryTimeGraph(ctx, lookBackDelta, spaceUID, time.Unix(timestampValue, 0), time.Unix(timestampValue, 0), 5*time.Minute, sourceType, targetTypes, pathResources, matcher)
}

func (m *Model) QueryRelationPathResources(ctx context.Context, lookBackDelta, spaceUID, timestamp string, sourceType cmdb.Resource, targetTypes []cmdb.Resource, paths []cmdb.RelationPath, matcher cmdb.Matcher) ([]cmdb.PathResourcesResult, error) {
	if spaceUID == "" {
		return nil, errors.New("space uid is empty")
	}
	if timestamp == "" {
		return nil, errors.New("timestamp is empty")
	}
	if sourceType == "" {
		return nil, errors.New("source type is empty")
	}
	if len(targetTypes) == 0 {
		return nil, errors.New("target types is empty")
	}
	timestampValue, err := cast.ToInt64E(timestamp)
	if err != nil {
		return nil, errors.WithMessage(err, "parse timestamp")
	}
	return m.queryRelationTimeGraph(ctx, lookBackDelta, spaceUID, time.Unix(timestampValue, 0), time.Unix(timestampValue, 0), 5*time.Minute, sourceType, targetTypes, paths, matcher)
}

func (m *Model) QueryPathResourcesRange(ctx context.Context, lookBackDelta, spaceUID, step, startTimestamp, endTimestamp string, sourceType cmdb.Resource, targetTypes []cmdb.Resource, pathResources [][]cmdb.Resource, matcher cmdb.Matcher) ([]cmdb.PathResourcesResult, error) {
	if spaceUID == "" {
		return nil, errors.New("space uid is empty")
	}
	if startTimestamp == "" || endTimestamp == "" {
		return nil, errors.New("timestamp is empty")
	}
	if sourceType == "" {
		return nil, errors.New("source type is empty")
	}
	if len(targetTypes) == 0 {
		return nil, errors.New("target types is empty")
	}
	start, err := cast.ToInt64E(startTimestamp)
	if err != nil {
		return nil, errors.WithMessage(err, "parse start timestamp")
	}
	end, err := cast.ToInt64E(endTimestamp)
	if err != nil {
		return nil, errors.WithMessage(err, "parse end timestamp")
	}
	stepDuration, err := time.ParseDuration(step)
	if err != nil {
		return nil, errors.WithMessage(err, "parse step")
	}
	if stepDuration <= 0 {
		return nil, errors.New("step must be positive")
	}
	return m.queryTimeGraph(ctx, lookBackDelta, spaceUID, time.Unix(start, 0), time.Unix(end, 0), stepDuration, sourceType, targetTypes, pathResources, matcher)
}

func (m *Model) QueryRelationPathResourcesRange(ctx context.Context, lookBackDelta, spaceUID, step, startTimestamp, endTimestamp string, sourceType cmdb.Resource, targetTypes []cmdb.Resource, paths []cmdb.RelationPath, matcher cmdb.Matcher) ([]cmdb.PathResourcesResult, error) {
	if spaceUID == "" {
		return nil, errors.New("space uid is empty")
	}
	if startTimestamp == "" || endTimestamp == "" {
		return nil, errors.New("timestamp is empty")
	}
	if sourceType == "" {
		return nil, errors.New("source type is empty")
	}
	if len(targetTypes) == 0 {
		return nil, errors.New("target types is empty")
	}
	start, err := cast.ToInt64E(startTimestamp)
	if err != nil {
		return nil, errors.WithMessage(err, "parse start timestamp")
	}
	end, err := cast.ToInt64E(endTimestamp)
	if err != nil {
		return nil, errors.WithMessage(err, "parse end timestamp")
	}
	stepDuration, err := time.ParseDuration(step)
	if err != nil {
		return nil, errors.WithMessage(err, "parse step")
	}
	if stepDuration <= 0 {
		return nil, errors.New("step must be positive")
	}
	return m.queryRelationTimeGraph(ctx, lookBackDelta, spaceUID, time.Unix(start, 0), time.Unix(end, 0), stepDuration, sourceType, targetTypes, paths, matcher)
}

func vectorToMatrix(vector pl.Vector) pl.Matrix {
	var matrix pl.Matrix
	for _, sample := range vector {
		matrix = append(matrix, pl.Series{
			Metric: sample.Metric,
			Points: []pl.Point{sample.Point},
		})
	}
	return matrix
}
