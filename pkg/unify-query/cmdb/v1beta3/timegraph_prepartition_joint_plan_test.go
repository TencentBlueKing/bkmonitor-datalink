// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package v1beta3

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/prometheus/prometheus/model/labels"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestTopologyJointPartitionSumsMarginalWeightsAndPreservesOR(t *testing.T) {
	query := &structured.QueryTs{Start: "1700000000", End: "1700003540", Step: "60s", QueryList: []*structured.Query{{Conditions: structured.Conditions{
		FieldList: []structured.ConditionField{
			{DimensionName: "tenant", Operator: structured.ConditionEqual, Value: []string{"one"}},
			{DimensionName: "tenant", Operator: structured.ConditionEqual, Value: []string{"two"}},
		}, ConditionList: []string{structured.ConditionOr},
	}}}}
	before, err := json.Marshal(query)
	require.NoError(t, err)
	matrix := pl.Matrix{}
	for _, sample := range []struct {
		a, b   string
		weight float64
	}{{"0", "0", 100}, {"0", "1", 100}, {"0", "2", 100}, {"1", "0", 100}, {"2", "0", 200}} {
		series := contractMatrix(map[string]string{"a_id": sample.a, "b_id": sample.b}, 1700000000000)[0]
		series.Points[0].V = sample.weight
		matrix = append(matrix, series)
	}
	// The sample intentionally omits the inherited tenant condition. Dividing
	// it must apply only the newly chosen matcher, preserving the full OR query.
	buckets, err := topologyPartitionBuckets(query, matrix, "a_id", 2, 0, true)
	require.NoError(t, err)
	require.Len(t, buckets, 2)
	weights := []float64{}
	for _, bucket := range buckets {
		weight := 0.0
		for _, series := range bucket.sample {
			weight += series.Points[0].V
		}
		weights = append(weights, weight)
	}
	sort.Float64s(weights)
	require.Equal(t, []float64{300, 300}, weights, "MAX would incorrectly choose a 400:200 split")
	for _, tenant := range []string{"one", "two", "other"} {
		for _, value := range []string{"", "0", "1", "2", "雪\nlate", "\\"} {
			matches := 0
			for _, bucket := range buckets {
				if partitionTestMatches(t, bucket.query, labels.FromMap(map[string]string{"tenant": tenant, "a_id": value})) {
					matches++
				}
			}
			if tenant == "other" {
				require.Zero(t, matches)
			} else {
				require.Equal(t, 1, matches, "%s/%q", tenant, value)
			}
		}
	}
	after, err := json.Marshal(query)
	require.NoError(t, err)
	require.Equal(t, before, after)
}
