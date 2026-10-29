// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// Use the old QueryFactory and ToTime as the parsing oracle, then apply the
// actual VM wire conversion: Unix timestamps and integer seconds for step.
func TestLegacyTimeGraphMatchesVMTimePrecision(t *testing.T) {
	oldYolo := yoloMode
	t.Cleanup(func() { yoloMode = oldYolo })
	for _, yolo := range []bool{false, true} {
		yoloMode = yolo
		for _, tc := range []struct {
			name, start, end, step string
			instant                bool
		}{
			{name: "instant seconds", start: "1700000070", instant: true},
			{name: "instant aligned milliseconds", start: "1700000070000", instant: true},
			{name: "instant milliseconds", start: "1700000070123", instant: true},
			{name: "instant microseconds", start: "1700000070123456", instant: true},
			{name: "instant nanoseconds", start: "1700000070123456789", instant: true},
			{name: "range milliseconds", start: "1700000070123", end: "1700000190987", step: "1m"},
			{name: "range microseconds", start: "1700000070123456", end: "1700000190987654", step: "1m"},
			{name: "range nanoseconds", start: "1700000070123456789", end: "1700000190987654321", step: "1m"},
			{name: "fractional step retains window", start: "1700000070123", end: "1700000073987", step: "1500ms"},
			{name: "same wire second", start: "1700000070123", end: "1700000070987", step: "1m"},
			{name: "day window", start: "1700000070123", end: "1700086470987", step: "1d"},
		} {
			for _, quality := range []string{"complete", "empty", "partial", "partial empty"} {
				t.Run(fmt.Sprintf("yolo=%t/%s/%s", yolo, tc.name, quality), func(t *testing.T) {
					ctx := initTimeGraphQueryTestEnvironment()
					oldEnd := tc.end
					if tc.instant {
						oldEnd = tc.start
					}
					factory := v1beta1.QueryFactory{Path: []string{"pod", "node"}, Source: "pod", Target: "node", Start: tc.start, End: oldEnd, Step: tc.step}
					oldQuery, err := factory.MakeQueryTs()
					require.NoError(t, err)
					oldCtx := newTimeGraphSubqueryContext(ctx)
					require.NoError(t, oldQuery.ToTime(oldCtx))
					oldParams := metadata.GetQueryParams(oldCtx)
					start := time.Unix(oldParams.AlignStart.Unix(), 0)
					end := time.Unix(oldParams.End.Unix(), 0)
					wireStep := time.Duration(int64(oldParams.Step.Seconds())) * time.Second
					window := time.Minute
					if !tc.instant {
						window, err = oldQuery.QueryList[0].TimeAggregation.Window.Duration()
						require.NoError(t, err)
					}
					var timestamps []int64
					if tc.instant {
						timestamps = []int64{end.UnixMilli()}
					} else {
						for at := start; !at.After(end); at = at.Add(wireStep) {
							timestamps = append(timestamps, at.UnixMilli())
						}
					}
					provider := sharedTopologyQueryProvider().(contractSchemaProvider)
					provider.schemas = provider.schemas[:1]
					model := legacyRegressionModel(provider)
					calls := 0
					model.timeGraphVMQuery = func(qctx context.Context, q *structured.QueryTs, _ string, instant bool, gotStart, gotEnd time.Time, step time.Duration) (pl.Matrix, error) {
						calls++
						require.Equal(t, end.UnixMilli(), gotEnd.UnixMilli())
						require.Equal(t, start.Equal(end), instant)
						if !tc.instant {
							require.Equal(t, start.UnixMilli(), gotStart.UnixMilli())
							require.Equal(t, wireStep, step)
							require.True(t, q.NotTimeAlign)
						}
						gotWindow, err := q.QueryList[0].TimeAggregation.Window.Duration()
						require.NoError(t, err)
						require.Equal(t, window, gotWindow)
						if quality == "partial" || quality == "partial empty" {
							metadata.SetStatus(qctx, metadata.QueryTsPartial, "fixture_partial")
						}
						if quality == "empty" || quality == "partial empty" {
							return nil, nil
						}
						return contractMatrix(map[string]string{"source_id": "a", "middle_id": "b"}, timestamps...), nil
					}
					want := cmdb.Matchers{{"middle_id": "b"}}
					if quality == "empty" || quality == "partial empty" {
						want = nil
					}
					if tc.instant {
						_, _, _, _, actual, err := model.QueryResourceMatcher(ctx, "1m", "space", tc.start, "middle", "source", cmdb.Matcher{"source_id": "a"}, nil, false, nil)
						require.NoError(t, err)
						if want == nil {
							require.Empty(t, actual)
						} else {
							require.Equal(t, want, actual)
						}
					} else {
						_, _, _, _, actual, err := model.QueryResourceMatcherRange(ctx, "1m", "space", tc.step, tc.start, tc.end, "middle", "source", cmdb.Matcher{"source_id": "a"}, nil, false, nil)
						require.NoError(t, err)
						if want == nil {
							require.Empty(t, actual)
						} else {
							expected := make([]cmdb.MatchersWithTimestamp, 0, len(timestamps))
							for _, at := range timestamps {
								expected = append(expected, cmdb.MatchersWithTimestamp{Timestamp: at, Matchers: want})
							}
							require.Equal(t, expected, actual)
						}
					}
					require.Positive(t, calls)
					if quality == "partial" || quality == "partial empty" {
						require.Equal(t, metadata.QueryTsPartial, metadata.GetStatus(ctx).Code)
					} else {
						require.Nil(t, metadata.GetStatus(ctx))
					}
				})
			}
		}
	}
}

func TestLegacyTimeGraphRejectsInvalidWireGrid(t *testing.T) {
	for _, tc := range []struct{ start, end, step string }{
		{"1700000070123", "1700000190", "1m"},
		{"1700000070", "1700000190", "500ms"},
		{"1700000070", "1700000190", "1.000001s"},
		{"1700000070", "1700000190", "0s"},
		{"1700000070", "1700000190", "invalid"},
		{"1700000190", "1700000070", "1m"},
	} {
		t.Run(tc.start+"/"+tc.end+"/"+tc.step, func(t *testing.T) {
			model := legacyRegressionModel(sharedTopologyQueryProvider())
			model.timeGraphVMQuery = func(context.Context, *structured.QueryTs, string, bool, time.Time, time.Time, time.Duration) (pl.Matrix, error) {
				t.Fatal("invalid input must fail before VM")
				return nil, nil
			}
			_, _, _, _, _, err := model.QueryResourceMatcherRange(initTimeGraphQueryTestEnvironment(), "1m", "space", tc.step, tc.start, tc.end, "middle", "source", cmdb.Matcher{"source_id": "a"}, nil, false, nil)
			require.Error(t, err)
		})
	}
	timestamp, err := timeGraphQueryTimestamp("")
	require.NoError(t, err)
	require.InDelta(t, time.Now().Unix(), timestamp.Unix(), 2)
}
