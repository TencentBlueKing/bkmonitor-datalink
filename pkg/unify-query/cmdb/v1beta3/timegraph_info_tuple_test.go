// Copyright (C) 2022 THL A29 Limited, a Tencent company.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	uqquery "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

func TestResourceInfoPrimaryTuplesApplyAttributesToEveryGroup(t *testing.T) {
	first := cmdb.Matcher{"cluster": "c1", "pod": "p1"}
	second := cmdb.Matcher{"cluster": "c2", "pod": "p2"}
	for _, tc := range []struct {
		name           string
		primary        []cmdb.Matcher
		source, expand cmdb.Matcher
		want           []bool
	}{
		{"single_tuple", []cmdb.Matcher{first}, nil, nil, []bool{true, false, true, false, true, false, false, false}},
		{"multiple_tuples", []cmdb.Matcher{first, second}, nil, nil, []bool{true, true, true, true, true, true, false, false}},
		{"single_tuple_attribute", []cmdb.Matcher{first}, nil, cmdb.Matcher{"version": "wanted"}, []bool{true, false, false, false, true, false, false, false}},
		{"multiple_tuples_attribute", []cmdb.Matcher{first, second}, nil, cmdb.Matcher{"version": "wanted"}, []bool{true, true, false, false, true, true, false, false}},
		{"multiple_tuples_attributes", []cmdb.Matcher{first, second}, nil, cmdb.Matcher{"version": "wanted", "zone": "east"}, []bool{true, true, false, false, false, false, false, false}},
		{"source_attributes", nil, first, cmdb.Matcher{"version": "wanted", "zone": "east"}, []bool{true, false, false, false, false, false, false, false}},
		{"primary_attribute_ignored", []cmdb.Matcher{first, second}, nil, cmdb.Matcher{"cluster": "unused", "pod": "unused", "version": "wanted"}, []bool{true, true, false, false, true, true, false, false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := initTimeGraphQueryTestEnvironment()
			graph := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{{
				Name: "pod", Index: cmdb.Index{"cluster", "pod"}, Info: cmdb.Index{"version", "zone"},
			}}})
			t.Cleanup(func() { graph.Clean(ctx) })
			query, err := graph.MakeResourceInfoQueryTsWithWindow("space", "pod", tc.source,
				tc.expand, tc.primary,
				time.Unix(1700000000, 0), time.Unix(1700000000, 0), time.Minute, time.Minute)
			require.NoError(t, err)
			query.TsDBMap = map[string]structured.TsDBs{metadata.DefaultReferenceName: {&uqquery.TsDBV2{
				TableID: "review.pod_info_relation", Field: []string{"pod_info_relation"},
				MeasurementType: redis.BkSplitMeasurement, ExpandMetricNames: []string{"a"},
				StorageID: "1", StorageType: metadata.VictoriaMetricsStorageType,
				DB: "review", Measurement: "pod_info_relation", VmRt: "review",
			}}}
			reference, err := query.ToQueryReference(ctx)
			require.NoError(t, err)
			expr, err := query.ToPromExpr(ctx, nil)
			require.NoError(t, err)
			t.Logf("expression=%s", expr.String())
			for _, metric := range reference[metadata.DefaultReferenceName] {
				for _, physical := range metric.QueryList {
					t.Logf("physical selector=%s", physical.VmCondition)
				}
			}
			groups, err := query.QueryList[0].Conditions.AnalysisConditions()
			require.NoError(t, err)
			for index, sample := range []struct {
				name, cluster, pod, version, zone string
			}{
				{"first_matching", "c1", "p1", "wanted", "east"},
				{"second_matching", "c2", "p2", "wanted", "east"},
				{"first_wrong_version", "c1", "p1", "other", "east"},
				{"second_wrong_version", "c2", "p2", "other", "east"},
				{"first_wrong_zone", "c1", "p1", "wanted", "west"},
				{"second_wrong_zone", "c2", "p2", "wanted", "west"},
				{"cross_tuple_first", "c1", "p2", "wanted", "east"},
				{"cross_tuple_second", "c2", "p1", "wanted", "east"},
			} {
				t.Run(sample.name, func(t *testing.T) {
					matched, err := groups.MatchResultTableLabels(map[string]string{
						"cluster": sample.cluster, "pod": sample.pod, "version": sample.version, "zone": sample.zone,
					})
					require.NoError(t, err)
					require.Equal(t, tc.want[index], matched)
				})
			}
		})
	}
}
