// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

const (
	wideFrontierFirstMS  int64 = 1700000040000
	wideFrontierSecondMS int64 = 1700000100000
)

func wideFrontierProvider() contractSchemaProvider {
	return contractSchemaProvider{
		resources: []ResourceType{"root", "pod", "target"},
		primary: map[ResourceType][]string{
			"root":   {"root_id"},
			"pod":    {"cluster", "namespace", "pod"},
			"target": {"target_id"},
		},
		fields: map[ResourceType][]string{
			"root":   {"root_id"},
			"pod":    {"cluster", "namespace", "pod"},
			"target": {"target_id"},
		},
		schemas: []RelationSchema{
			{RelationType: "root_to_pod", Category: RelationCategoryStatic, FromType: "root", ToType: "pod", MetricName: "root_to_pod_flow"},
			{RelationType: "pod_to_target", Category: RelationCategoryStatic, FromType: "pod", ToType: "target", MetricName: "pod_to_target_flow"},
		},
	}
}

// wideFrontierEdge is one series with the timestamps it reports.
type wideFrontierEdge struct {
	labels map[string]string
	times  []int64
}

// wideFrontierBackend evaluates each selector against the edges, the way a
// storage does, and records what the second hop asked for.
type wideFrontierBackend struct {
	edges       map[string][]wideFrontierEdge
	hopQueries  []structured.Conditions
	hopBudgets  []int64
	rejectWide  func(ctx context.Context) error
	wideQueries int
}

func wideFrontierMatches(t *testing.T, conditions structured.Conditions, labels map[string]string) bool {
	t.Helper()
	groups, err := conditions.AnalysisConditions()
	require.NoError(t, err)
	if len(groups) == 0 {
		return true
	}
	for _, group := range groups {
		matched := true
		for _, field := range group {
			value := labels[field.DimensionName]
			switch field.Operator {
			case structured.ConditionEqual, structured.ConditionContains:
				found := false
				for _, want := range field.Value {
					found = found || want == value
				}
				matched = matched && found
			case structured.ConditionNotEqual:
				matched = matched && value != field.Value[0]
			default:
				t.Fatalf("unexpected operator %s", field.Operator)
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func (b *wideFrontierBackend) query(t *testing.T) func(context.Context, *structured.QueryTs, string, bool, time.Time, time.Time, time.Duration) (pl.Matrix, error) {
	return func(ctx context.Context, q *structured.QueryTs, _ string, _ bool, start, end time.Time, step time.Duration) (pl.Matrix, error) {
		query := q.QueryList[0]
		if query.FieldName == "pod_to_target_flow" {
			b.hopQueries = append(b.hopQueries, query.Conditions)
			b.hopBudgets = append(b.hopBudgets, metadata.BackendResponseLimit(ctx))
			if metadata.BackendResponseLimit(ctx) > 0 {
				b.wideQueries++
				if b.rejectWide != nil {
					if err := b.rejectWide(ctx); err != nil {
						return nil, err
					}
				}
			}
		}
		var matrix pl.Matrix
		for _, edge := range b.edges[query.FieldName] {
			if !wideFrontierMatches(t, query.Conditions, edge.labels) {
				continue
			}
			var times []int64
			for _, at := range edge.times {
				if at >= start.UnixMilli() && at <= end.UnixMilli() {
					times = append(times, at)
				}
			}
			if len(times) > 0 {
				matrix = append(matrix, contractMatrix(edge.labels, times...)...)
			}
		}
		_ = step
		return matrix, nil
	}
}

// wideFrontierScenario has count reachable pods, each with one target, plus edges a
// correct read must not return.
func wideFrontierScenario(count int, namespaces, clusters int) *wideFrontierBackend {
	both := []int64{wideFrontierFirstMS, wideFrontierSecondMS}
	b := &wideFrontierBackend{edges: map[string][]wideFrontierEdge{}}
	pod := func(i int) map[string]string {
		return map[string]string{"cluster": fmt.Sprintf("c%d", i%clusters), "namespace": fmt.Sprintf("n%d", i%namespaces), "pod": fmt.Sprintf("p%04d", i)}
	}
	for i := 0; i < count; i++ {
		root := pod(i)
		root["root_id"] = "r1"
		b.edges["root_to_pod_flow"] = append(b.edges["root_to_pod_flow"], wideFrontierEdge{labels: root, times: both})
		edge := pod(i)
		edge["target_id"] = fmt.Sprintf("t%04d", i)
		b.edges["pod_to_target_flow"] = append(b.edges["pod_to_target_flow"], wideFrontierEdge{labels: edge, times: both})
	}
	// A pod outside the frontier, in a namespace the scoped read covers.
	outsider := pod(0)
	outsider["pod"], outsider["target_id"] = "outsider", "outsider"
	// A combination of values that are each in the frontier but never together.
	cross := pod(0)
	cross["pod"], cross["target_id"] = pod(1)["pod"], "cross"
	b.edges["pod_to_target_flow"] = append(b.edges["pod_to_target_flow"],
		wideFrontierEdge{labels: outsider, times: both}, wideFrontierEdge{labels: cross, times: both})
	// Reachable only in the first bucket, while its edge reports in both.
	late := pod(count)
	late["pod"] = "late"
	lateRoot := map[string]string{"root_id": "r1", "cluster": late["cluster"], "namespace": late["namespace"], "pod": "late"}
	b.edges["root_to_pod_flow"] = append(b.edges["root_to_pod_flow"], wideFrontierEdge{labels: lateRoot, times: both[:1]})
	late["target_id"] = "late"
	b.edges["pod_to_target_flow"] = append(b.edges["pod_to_target_flow"], wideFrontierEdge{labels: late, times: both})
	return b
}

func wideFrontierRange(t *testing.T, b *wideFrontierBackend) map[int64][]string {
	t.Helper()
	model := legacyRegressionModel(wideFrontierProvider())
	model.timeGraphVMQuery = b.query(t)
	_, _, path, _, buckets, err := model.QueryResourceMatcherRange(
		initTimeGraphQueryTestEnvironment(), "", "space", "1m", "1700000040", "1700000100",
		"target", "root", cmdb.Matcher{"root_id": "r1"}, nil, false, nil,
	)
	require.NoError(t, err)
	require.Equal(t, []string{"root", "pod", "target"}, path)
	got := make(map[int64][]string, len(buckets))
	for _, bucket := range buckets {
		for _, target := range bucket.Matchers {
			got[bucket.Timestamp] = append(got[bucket.Timestamp], target["target_id"])
		}
		sort.Strings(got[bucket.Timestamp])
	}
	return got
}

func wideFrontierWant(count int) map[int64][]string {
	all := make([]string, 0, count)
	for i := 0; i < count; i++ {
		all = append(all, fmt.Sprintf("t%04d", i))
	}
	first := append([]string{"late"}, all...)
	sort.Strings(first)
	return map[int64][]string{wideFrontierFirstMS: first, wideFrontierSecondMS: all}
}

func wideFrontierField(t *testing.T, conditions structured.Conditions, name string) structured.ConditionField {
	t.Helper()
	for _, field := range conditions.FieldList {
		if field.DimensionName == name {
			return field
		}
	}
	t.Fatalf("no condition on %s", name)
	return structured.ConditionField{}
}

func TestLegacyWideFrontierKeepsTheResultWithOneRead(t *testing.T) {
	b := wideFrontierScenario(300, 3, 2)
	require.Equal(t, wideFrontierWant(300), wideFrontierRange(t, b))

	require.Len(t, b.hopQueries, 1, "the whole frontier takes one read")
	require.Positive(t, b.hopBudgets[0], "a wide read is budgeted")
	require.Equal(t, []string{"n0", "n1", "n2"}, wideFrontierField(t, b.hopQueries[0], "namespace").Value)
	require.Equal(t, []string{"c0", "c1"}, wideFrontierField(t, b.hopQueries[0], "cluster").Value)
	pod := wideFrontierField(t, b.hopQueries[0], "pod")
	require.Equal(t, structured.ConditionNotEqual, pod.Operator, "a field with many values is not listed")
}

func TestLegacyWideFrontierLeavesHighCardinalityFieldsOpen(t *testing.T) {
	b := wideFrontierScenario(300, 200, 2)
	require.Equal(t, wideFrontierWant(300), wideFrontierRange(t, b))
	require.Len(t, b.hopQueries, 1)
	require.Equal(t, structured.ConditionNotEqual, wideFrontierField(t, b.hopQueries[0], "namespace").Operator)
	require.Equal(t, []string{"c0", "c1"}, wideFrontierField(t, b.hopQueries[0], "cluster").Value)
}

func TestLegacyWideFrontierThreshold(t *testing.T) {
	for _, tc := range []struct {
		name         string
		pods         int
		wantWide     int
		wantHopReads int
	}{
		// The "late" pod is part of the frontier as well.
		{"at the threshold batches", timeGraphWideFrontierThreshold - 1, 0, 2},
		{"over the threshold reads wide", timeGraphWideFrontierThreshold, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := wideFrontierScenario(tc.pods, 3, 2)
			require.Equal(t, wideFrontierWant(tc.pods), wideFrontierRange(t, b))
			require.Equal(t, tc.wantWide, b.wideQueries)
			require.Len(t, b.hopQueries, tc.wantHopReads)
		})
	}
}

func TestLegacyWideFrontierFallsBackToBatches(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reject func(ctx context.Context) error
	}{
		{"storage reports too large", func(context.Context) error { return metadata.ErrBackendResponseTooLarge }},
		{"budget marked exceeded", func(ctx context.Context) error {
			metadata.MarkBackendResponseLimitExceeded(ctx)
			return nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := wideFrontierScenario(300, 3, 2)
			b.rejectWide = tc.reject
			require.Equal(t, wideFrontierWant(300), wideFrontierRange(t, b))
			require.Equal(t, 1, b.wideQueries)
			// 301 identities in batches of 64, without a budget of their own.
			require.Len(t, b.hopQueries, 1+5)
			for _, budget := range b.hopBudgets[1:] {
				require.Zero(t, budget)
			}
		})
	}
}

func TestLegacyWideFrontierPropagatesOtherFailures(t *testing.T) {
	b := wideFrontierScenario(300, 3, 2)
	b.rejectWide = func(context.Context) error { return fmt.Errorf("backend failed") }
	model := legacyRegressionModel(wideFrontierProvider())
	model.timeGraphVMQuery = b.query(t)
	_, _, _, _, _, err := model.QueryResourceMatcher(
		initTimeGraphQueryTestEnvironment(), "1m", "space", "1700000040",
		"target", "root", cmdb.Matcher{"root_id": "r1"}, nil, false, nil,
	)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "backend failed"), err.Error())
	require.Len(t, b.hopQueries, 1, "only an oversized response falls back")
}

func TestLegacyWideFrontierInstant(t *testing.T) {
	b := wideFrontierScenario(300, 3, 2)
	model := legacyRegressionModel(wideFrontierProvider())
	model.timeGraphVMQuery = b.query(t)
	_, _, _, _, targets, err := model.QueryResourceMatcher(
		initTimeGraphQueryTestEnvironment(), "1m", "space", "1700000040",
		"target", "root", cmdb.Matcher{"root_id": "r1"}, nil, false, nil,
	)
	require.NoError(t, err)
	got := make([]string, 0, len(targets))
	for _, target := range targets {
		got = append(got, target["target_id"])
	}
	sort.Strings(got)
	require.Equal(t, wideFrontierWant(300)[wideFrontierFirstMS], got)
	require.Len(t, b.hopQueries, 1)
}

// A scoped read returns edges outside the frontier. They must not reach the next
// hop, whose reads are sized by the frontier the filter leaves behind.
func TestLegacyWideFrontierFiltersTheNextFrontier(t *testing.T) {
	provider := wideFrontierProvider()
	provider.resources = append(provider.resources, "leaf")
	provider.primary["leaf"] = []string{"leaf_id"}
	provider.fields["leaf"] = []string{"leaf_id"}
	provider.schemas = append(provider.schemas, RelationSchema{RelationType: "target_to_leaf", Category: RelationCategoryStatic, FromType: "target", ToType: "leaf", MetricName: "target_to_leaf_flow"})

	b := wideFrontierScenario(300, 3, 2)
	for i, edge := range b.edges["pod_to_target_flow"] {
		if id := edge.labels["target_id"]; strings.HasPrefix(id, "t") {
			b.edges["pod_to_target_flow"][i].labels["target_id"] = fmt.Sprintf("t%d", i%3)
		}
	}
	for _, id := range []string{"t0", "t1", "t2", "late", "outsider", "cross"} {
		b.edges["target_to_leaf_flow"] = append(b.edges["target_to_leaf_flow"], wideFrontierEdge{
			labels: map[string]string{"target_id": id, "leaf_id": "leaf-" + id},
			times:  []int64{wideFrontierFirstMS},
		})
	}

	var read []string
	backend := b.query(t)
	model := legacyRegressionModel(provider)
	model.timeGraphVMQuery = func(ctx context.Context, q *structured.QueryTs, expr string, instant bool, start, end time.Time, step time.Duration) (pl.Matrix, error) {
		if q.QueryList[0].FieldName == "target_to_leaf_flow" {
			for _, field := range q.QueryList[0].Conditions.FieldList {
				if field.DimensionName == "target_id" {
					read = append(read, field.Value...)
				}
			}
		}
		return backend(ctx, q, expr, instant, start, end, step)
	}
	_, _, _, _, targets, err := model.QueryResourceMatcher(
		initTimeGraphQueryTestEnvironment(), "1m", "space", "1700000040",
		"leaf", "root", cmdb.Matcher{"root_id": "r1"}, nil, false, []cmdb.Resource{"root", "pod", "target", "leaf"},
	)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"t0", "t1", "t2", "late"}, read)
	got := make([]string, 0, len(targets))
	for _, target := range targets {
		got = append(got, target["leaf_id"])
	}
	require.ElementsMatch(t, []string{"leaf-t0", "leaf-t1", "leaf-t2", "leaf-late"}, got)
}
