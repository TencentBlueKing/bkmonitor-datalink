// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License").

package v1beta3

import (
	"context"
	"fmt"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestLegacyOrdinaryLongPathCandidates(t *testing.T) {
	oldHops := MaxAllowedHops
	MaxAllowedHops = 5
	t.Cleanup(func() { MaxAllowedHops = oldHops })
	for _, mode := range []string{"instant", "range"} {
		for _, tc := range []struct {
			name       string
			shortEmpty bool
		}{
			{name: "only twelve-hop candidate"},
			// 13 个节点的长路径不受图规模限制，照常命中。
			{name: "empty short candidate then twelve-hop hit", shortEmpty: true},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				provider := longPathTestProvider()
				if tc.shortEmpty {
					provider.schemas = append(provider.schemas, RelationSchema{
						RelationType: "short", FromType: "r0", ToType: "r12", Category: RelationCategoryStatic,
						IsDirectional: true, MetricName: "short",
					})
				}
				model := legacyRegressionModel(provider)
				var fields []string
				var deadline time.Time
				model.timeGraphVMQuery = func(ctx context.Context, query *structured.QueryTs, _ string, _ bool, start, end time.Time, step time.Duration) (pl.Matrix, error) {
					gotDeadline, ok := ctx.Deadline()
					require.True(t, ok, "ordinary candidate queries retain their deadline")
					if deadline.IsZero() {
						deadline = gotDeadline
					}
					require.Equal(t, deadline, gotDeadline, "all candidates share one query deadline")
					field := query.QueryList[0].FieldName
					fields = append(fields, field)
					if field == "short" {
						return nil, nil
					}
					var timestamps []int64
					for at := start; !at.After(end); at = at.Add(step) {
						timestamps = append(timestamps, at.UnixMilli())
					}
					return filterTopologyRegressionMatrix(query, contractMatrix(map[string]string{"id": "a"}, timestamps...)), nil
				}
				ctx := initTimeGraphQueryTestEnvironment()
				var path []string
				var results cmdb.Matchers
				var err error
				if mode == "instant" {
					_, _, path, _, results, err = model.QueryResourceMatcher(ctx, "24h", "space", "1700000040", "r12", "r0", cmdb.Matcher{"id": "a"}, nil, false, nil)
				} else {
					var buckets []cmdb.MatchersWithTimestamp
					_, _, path, _, buckets, err = model.QueryResourceMatcherRange(ctx, "24h", "space", "1m", "1700000040", "1700000100", "r12", "r0", cmdb.Matcher{"id": "a"}, nil, false, nil)
					for _, bucket := range buckets {
						results = append(results, bucket.Matchers...)
					}
				}
				if tc.shortEmpty {
					require.NotEmpty(t, fields)
					require.Equal(t, "short", fields[0], "short candidates are queried first")
				}
				require.NoError(t, err)
				var wantPath []string
				for i := 0; i <= 12; i++ {
					wantPath = append(wantPath, fmt.Sprintf("r%d", i))
				}
				require.Equal(t, wantPath, path)
				require.NotEmpty(t, results)
				for _, result := range results {
					require.Equal(t, cmdb.Matcher{"id": "a"}, result)
				}
				require.Nil(t, metadata.GetStatus(ctx), "long paths must not become partial results")
			})
		}
	}
}

func TestLegacyCandidateDiscoveryUsesQueryDeadline(t *testing.T) {
	oldTimeout := timeGraphQueryTimeout
	timeGraphQueryTimeout = -time.Nanosecond
	t.Cleanup(func() { timeGraphQueryTimeout = oldTimeout })
	for _, mode := range []string{"instant", "range"} {
		t.Run(mode, func(t *testing.T) {
			model := legacyRegressionModel(longPathTestProvider())
			model.timeGraphVMQuery = func(context.Context, *structured.QueryTs, string, bool, time.Time, time.Time, time.Duration) (pl.Matrix, error) {
				t.Fatal("expired candidate discovery must stop before querying")
				return nil, nil
			}
			ctx := initTimeGraphQueryTestEnvironment()
			var err error
			if mode == "instant" {
				_, _, _, _, _, err = model.QueryResourceMatcher(ctx, "24h", "space", "1700000040", "r12", "r0", nil, nil, false, nil)
			} else {
				_, _, _, _, _, err = model.QueryResourceMatcherRange(ctx, "24h", "space", "1m", "1700000040", "1700000100", "r12", "r0", nil, nil, false, nil)
			}
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.NoError(t, ctx.Err(), "the request keeps ownership of its parent context")
		})
	}
}

func TestLegacySameLengthCandidatesUseLexicalOrder(t *testing.T) {
	provider := legacyAlternativeProvider(true)
	provider.primary["z_middle"] = []string{"middle_id"}
	provider.fields["z_middle"] = []string{"middle_id"}
	provider.resources = append(provider.resources, "z_middle")
	provider.schemas = append([]RelationSchema{
		{RelationType: "z_first", FromType: "source", ToType: "z_middle", Category: RelationCategoryStatic, IsDirectional: true, MetricName: "z_first"},
		{RelationType: "z_second", FromType: "z_middle", ToType: "target", Category: RelationCategoryStatic, IsDirectional: true, MetricName: "z_second"},
	}, provider.schemas...)
	model := legacyRegressionModel(provider)
	model.timeGraphVMQuery = func(_ context.Context, query *structured.QueryTs, _ string, _ bool, start, _ time.Time, _ time.Duration) (pl.Matrix, error) {
		field := query.QueryList[0].FieldName
		require.NotContains(t, []string{"z_first", "z_second"}, field, "stop at the first nonempty candidate in lexical order")
		if field == "source_target_flow" {
			return nil, nil
		}
		return legacyAlternativeMatrix(field, start.UnixMilli()), nil
	}
	_, _, path, _, result, err := model.QueryResourceMatcher(initTimeGraphQueryTestEnvironment(), "24h", "space", "1700000040", "target", "source", cmdb.Matcher{"source_id": "a"}, nil, false, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"source", "middle", "target"}, path)
	require.Equal(t, cmdb.Matchers{{"target_id": "indirect"}}, result)
}
