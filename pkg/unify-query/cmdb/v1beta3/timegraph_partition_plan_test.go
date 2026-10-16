// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package v1beta3

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/prometheus/prometheus/model/labels"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestTopologyPartitionFanoutComplementsCoverUnknownValues(t *testing.T) {
	query := &structured.QueryTs{Start: "1700000000", End: "1700003540", Step: "60s", QueryList: []*structured.Query{{Conditions: structured.Conditions{
		FieldList: []structured.ConditionField{
			{DimensionName: "tenant", Operator: structured.ConditionEqual, Value: []string{"one"}},
			{DimensionName: "tenant", Operator: structured.ConditionEqual, Value: []string{"two"}},
		}, ConditionList: []string{structured.ConditionOr},
	}}}}
	before, err := json.Marshal(query)
	require.NoError(t, err)
	matrix := pl.Matrix{}
	values := []string{"", "雪", "line\nnext", "\\", "late-new-id"}
	for i := 0; i < 16; i++ {
		value := fmt.Sprintf("id-%02d", i)
		values = append(values, value)
		matrix = append(matrix, contractMatrix(map[string]string{"id": value}, 1700000000000)...)
	}
	parts, err := topologyPartitionQueries(query, matrix, "id", 4)
	require.NoError(t, err)
	require.Len(t, parts, 4)
	for _, tenant := range []string{"one", "two", "other"} {
		for _, value := range values {
			count := 0
			for _, partition := range parts {
				part := partition.query
				require.Equal(t, query.Start, part.Start)
				require.Equal(t, query.End, part.End)
				require.Equal(t, query.Step, part.Step)
				if partitionTestMatches(t, part, labels.FromMap(map[string]string{"tenant": tenant, "id": value})) {
					count++
				}
			}
			if tenant == "other" {
				require.Zero(t, count)
			} else {
				require.Equal(t, 1, count, "%s/%q must occur exactly once", tenant, value)
			}
		}
	}
	parts[0].query.QueryList[0].Conditions.FieldList[0].Value[0] = "changed"
	after, err := json.Marshal(query)
	require.NoError(t, err)
	require.Equal(t, before, after)
}
