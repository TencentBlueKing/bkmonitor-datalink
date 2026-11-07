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

// These pin the differences from the old engine that docs/api/timegraph.md lists
// as part of the migration contract.

func TestLegacyKeptDifferenceTargetWithoutInfoData(t *testing.T) {
	model := legacyRegressionModel(publicSourceExpandProvider())
	model.timeGraphVMQuery = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, start, end time.Time, step time.Duration) (pl.Matrix, error) {
		if q.QueryList[0].FieldName != "node_to_middle_flow" {
			return nil, nil
		}
		var timestamps []int64
		for at := start; !at.After(end); at = at.Add(step) {
			timestamps = append(timestamps, at.UnixMilli())
		}
		return contractMatrix(map[string]string{"node_id": "n1", "middle_id": "m1"}, timestamps...), nil
	}
	source := cmdb.Matcher{"node_id": "n1"}

	t.Run("instant", func(t *testing.T) {
		_, _, _, _, targets, err := model.QueryResourceMatcher(
			initTimeGraphQueryTestEnvironment(), "", "space", "1700000040", "middle", "node", source, nil, true, nil,
		)
		require.NoError(t, err)
		require.Equal(t, cmdb.Matchers{{"middle_id": "m1"}}, targets)
	})
	t.Run("range", func(t *testing.T) {
		_, _, _, _, buckets, err := model.QueryResourceMatcherRange(
			initTimeGraphQueryTestEnvironment(), "", "space", "1m", "1700000040", "1700000100", "middle", "node", source, nil, true, nil,
		)
		require.NoError(t, err)
		require.NotEmpty(t, buckets)
		for _, bucket := range buckets {
			require.Equal(t, []cmdb.Matcher{{"middle_id": "m1"}}, bucket.Matchers)
		}
	})
}

func TestLegacyKeptDifferencePartialPathFailure(t *testing.T) {
	const hitMS int64 = 1700000040000
	backendFailure := errors.New("backend failed")
	for _, tc := range []struct {
		name      string
		indirect  pl.Matrix
		wantPath  []string
		wantError string
	}{
		{
			name:      "no other path hits",
			wantError: "relation paths failed without an alternative hit",
		},
		{
			name:     "another path hits",
			indirect: legacyAlternativeMatrix("middle_target_flow", hitMS),
			wantPath: []string{"source", "middle", "target"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := legacyRegressionModel(legacyAlternativeProvider(true))
			model.timeGraphVMQuery = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
				switch name := q.QueryList[0].FieldName; {
				case name == "source_target_flow":
					return nil, backendFailure
				case tc.indirect == nil:
					return nil, nil
				default:
					return legacyAlternativeMatrix(name, hitMS), nil
				}
			}
			_, _, path, _, _, err := model.QueryResourceMatcher(
				initTimeGraphQueryTestEnvironment(), "1m", "space", "1700000040", "target", "source", cmdb.Matcher{"source_id": "a"}, nil, false, nil,
			)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				require.ErrorIs(t, err, backendFailure)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantPath, path)
		})
	}
}

func TestLegacyKeptDifferenceEmptyRangeStep(t *testing.T) {
	model := legacyRegressionModel(publicSourceExpandProvider())
	var calls []string
	model.timeGraphVMQuery = func(_ context.Context, q *structured.QueryTs, expr string, _ bool, _, _ time.Time, step time.Duration) (pl.Matrix, error) {
		require.Equal(t, time.Minute, step)
		require.Equal(t, structured.Window("1m0s"), q.QueryList[0].TimeAggregation.Window)
		require.Contains(t, expr, "count_over_time")
		calls = append(calls, q.QueryList[0].FieldName)
		return nil, nil
	}
	_, _, _, _, _, err := model.QueryResourceMatcherRange(
		initTimeGraphQueryTestEnvironment(), "10m", "space", "", "1700000040", "1700000100", "middle", "node", cmdb.Matcher{"node_id": "n1"}, nil, false, nil,
	)
	require.NoError(t, err)
	require.NotEmpty(t, calls)
}
