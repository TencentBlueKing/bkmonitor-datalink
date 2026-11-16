// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"errors"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// The old engine reports the primary keys of source_info and uses the expand
// predicates only to filter, on success and when the range entry fails. Its
// instant entry returns no source on failure. The empty-result parity with the
// old handlers lives in the HTTP tests; these rows cover real targets.
func TestLegacyTimeGraphSourceInfoExcludesExpandPredicates(t *testing.T) {
	const instantStamp, rangeStart, rangeEnd = "1700000040", "1700000040", "1700000100"
	source := cmdb.Matcher{"node_id": "n1"}
	backendFailure := errors.New("backend failed")
	for _, tc := range []struct {
		name        string
		expand      cmdb.Matcher
		failure     error
		wantTargets []string
	}{
		{name: "matching expand still reaches the target", expand: cmdb.Matcher{"zone": "east"}, wantTargets: []string{"m1"}},
		{name: "other expand value still filters the source", expand: cmdb.Matcher{"zone": "west"}},
		{name: "no expand", wantTargets: []string{"m1"}},
		{name: "backend failure", expand: cmdb.Matcher{"zone": "east"}, failure: backendFailure},
	} {
		model := legacyRegressionModel(publicSourceExpandProvider())
		matrices := map[string]map[string]string{
			"node_info_relation":   {"node_id": "n1", "zone": "east"},
			"node_to_middle_flow":  {"node_id": "n1", "middle_id": "m1"},
			"middle_info_relation": {"middle_id": "m1", "version": "v1"},
		}
		model.timeGraphVMQuery = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, start, end time.Time, step time.Duration) (pl.Matrix, error) {
			if tc.failure != nil {
				return nil, tc.failure
			}
			var timestamps []int64
			for at := start; !at.After(end); at = at.Add(step) {
				timestamps = append(timestamps, at.UnixMilli())
			}
			query := q.QueryList[0]
			return filteredTimeGraphMatrix(t, contractMatrix(matrices[query.FieldName], timestamps...), query.Conditions), nil
		}

		t.Run("instant/"+tc.name, func(t *testing.T) {
			_, gotSource, _, _, targets, err := model.QueryResourceMatcher(
				initTimeGraphQueryTestEnvironment(), "", "space", instantStamp, "middle", "node", source, tc.expand, false, nil,
			)
			if tc.failure != nil {
				require.ErrorContains(t, err, tc.failure.Error())
				require.Nil(t, gotSource)
				return
			}
			require.NoError(t, err)
			require.Equal(t, source, gotSource)
			got := make([]string, 0, len(targets))
			for _, target := range targets {
				got = append(got, target["middle_id"])
			}
			require.ElementsMatch(t, tc.wantTargets, got)
		})

		t.Run("range/"+tc.name, func(t *testing.T) {
			_, gotSource, _, _, buckets, err := model.QueryResourceMatcherRange(
				initTimeGraphQueryTestEnvironment(), "", "space", "1m", rangeStart, rangeEnd, "middle", "node", source, tc.expand, false, nil,
			)
			require.Equal(t, source, gotSource)
			if tc.failure != nil {
				require.ErrorContains(t, err, tc.failure.Error())
				return
			}
			require.NoError(t, err)
			got := make(map[string]bool)
			for _, bucket := range buckets {
				for _, target := range bucket.Matchers {
					got[target["middle_id"]] = true
				}
			}
			require.Len(t, got, len(tc.wantTargets))
			for _, want := range tc.wantTargets {
				require.True(t, got[want], want)
			}
		})
	}
}

// "zoned" would win the inference if expand could complete its longer primary key.
// The predicate only filters the source that source_info identifies.
func TestLegacyTimeGraphInferredSourceKeepsExpandAsFilter(t *testing.T) {
	const instantStamp, rangeStart, rangeEnd = "1700000040", "1700000040", "1700000100"
	source := cmdb.Matcher{"node_id": "n1"}
	provider := contractSchemaProvider{
		resources: []ResourceType{"node", "zoned", "middle"},
		primary:   map[ResourceType][]string{"node": {"node_id"}, "zoned": {"node_id", "zone"}, "middle": {"middle_id"}},
		fields:    map[ResourceType][]string{"node": {"node_id", "zone"}, "zoned": {"node_id", "zone"}, "middle": {"middle_id"}},
		schemas: []RelationSchema{
			{RelationType: "node_to_middle", Category: RelationCategoryStatic, FromType: "node", ToType: "middle", MetricName: "node_to_middle_flow"},
		},
	}
	for _, tc := range []struct {
		name        string
		expand      cmdb.Matcher
		wantTargets []string
	}{
		{name: "matching expand", expand: cmdb.Matcher{"zone": "east"}, wantTargets: []string{"m1"}},
		{name: "other expand value", expand: cmdb.Matcher{"zone": "west"}},
	} {
		model := legacyRegressionModel(provider)
		matrices := map[string]map[string]string{
			"node_info_relation":  {"node_id": "n1", "zone": "east"},
			"node_to_middle_flow": {"node_id": "n1", "middle_id": "m1"},
		}
		model.timeGraphVMQuery = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, start, end time.Time, step time.Duration) (pl.Matrix, error) {
			var timestamps []int64
			for at := start; !at.After(end); at = at.Add(step) {
				timestamps = append(timestamps, at.UnixMilli())
			}
			query := q.QueryList[0]
			return filteredTimeGraphMatrix(t, contractMatrix(matrices[query.FieldName], timestamps...), query.Conditions), nil
		}

		t.Run("instant/"+tc.name, func(t *testing.T) {
			gotType, _, _, _, targets, err := model.QueryResourceMatcher(
				initTimeGraphQueryTestEnvironment(), "", "space", instantStamp, "middle", "", source, tc.expand, false, nil,
			)
			require.NoError(t, err)
			require.Equal(t, cmdb.Resource("node"), gotType)
			got := make([]string, 0, len(targets))
			for _, target := range targets {
				got = append(got, target["middle_id"])
			}
			require.ElementsMatch(t, tc.wantTargets, got)
		})

		t.Run("range/"+tc.name, func(t *testing.T) {
			gotType, _, _, _, buckets, err := model.QueryResourceMatcherRange(
				initTimeGraphQueryTestEnvironment(), "", "space", "1m", rangeStart, rangeEnd, "middle", "", source, tc.expand, false, nil,
			)
			require.NoError(t, err)
			require.Equal(t, cmdb.Resource("node"), gotType)
			got := make(map[string]bool)
			for _, bucket := range buckets {
				for _, target := range bucket.Matchers {
					got[target["middle_id"]] = true
				}
			}
			require.Len(t, got, len(tc.wantTargets))
			for _, want := range tc.wantTargets {
				require.True(t, got[want], want)
			}
		})
	}
}
