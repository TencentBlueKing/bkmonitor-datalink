// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

import (
	"context"
	"testing"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestQueryTsValidateNamedOutputs(t *testing.T) {
	valid := func() *QueryTs {
		return &QueryTs{
			QueryList: []*Query{
				{ReferenceName: "A"},
				{ReferenceName: "B"},
			},
			MetricMerge:      "A / B * 100",
			ResponseContract: NamedOutputsV1,
			LegacyOutputRef:  "C",
			OutputList: []QueryOutput{
				{ReferenceName: "A", Expression: "A"},
				{ReferenceName: "B", Expression: "B"},
				{ReferenceName: "C", Expression: "A/B*100"},
			},
		}
	}

	tests := map[string]struct {
		mutate  func(*QueryTs)
		max     int
		wantErr string
	}{
		"valid": {max: 4},
		"output without contract": {
			mutate: func(q *QueryTs) { q.ResponseContract = "" }, max: 4,
			wantErr: "response_contract",
		},
		"unknown contract": {
			mutate: func(q *QueryTs) { q.ResponseContract = "named_outputs/v2" }, max: 4,
			wantErr: "unsupported response_contract",
		},
		"empty metric merge": {
			mutate: func(q *QueryTs) { q.MetricMerge = "" }, max: 4,
			wantErr: "metric_merge",
		},
		"empty legacy reference": {
			mutate: func(q *QueryTs) { q.LegacyOutputRef = "" }, max: 4,
			wantErr: "legacy_output_ref",
		},
		"too many outputs": {
			mutate: func(q *QueryTs) {
				q.OutputList = append(q.OutputList, QueryOutput{ReferenceName: "D", Expression: "A"})
			},
			max: 3, wantErr: "output_list",
		},
		"duplicate output reference": {
			mutate: func(q *QueryTs) { q.OutputList[1].ReferenceName = "A" }, max: 4,
			wantErr: "duplicate output reference",
		},
		"invalid output reference": {
			mutate: func(q *QueryTs) { q.OutputList[0].ReferenceName = "A-B" }, max: 4,
			wantErr: "invalid output reference",
		},
		"missing legacy output": {
			mutate: func(q *QueryTs) { q.LegacyOutputRef = "D" }, max: 4,
			wantErr: "legacy_output_ref",
		},
		"legacy expression mismatch": {
			mutate: func(q *QueryTs) { q.OutputList[2].Expression = "A / B" }, max: 4,
			wantErr: "metric_merge",
		},
		"non legacy must be identity": {
			mutate: func(q *QueryTs) { q.OutputList[0].Expression = "A + B" }, max: 4,
			wantErr: "identity",
		},
		"identity rejects offset modifier": {
			mutate: func(q *QueryTs) { q.OutputList[0].Expression = "A offset 1m" }, max: 4,
			wantErr: "identity",
		},
		"identity rejects at modifier": {
			mutate: func(q *QueryTs) { q.OutputList[0].Expression = "A @ 123" }, max: 4,
			wantErr: "identity",
		},
		"identity rejects explicit selector": {
			mutate: func(q *QueryTs) { q.OutputList[0].Expression = `{__name__="A"}` }, max: 4,
			wantErr: "identity",
		},
		"identity must reference query list": {
			mutate: func(q *QueryTs) { q.OutputList[0].Expression = "D" }, max: 4,
			wantErr: "query reference",
		},
		"invalid expression": {
			mutate: func(q *QueryTs) { q.OutputList[0].Expression = "(" }, max: 4,
			wantErr: "expression",
		},
		"nil query item": {
			mutate: func(q *QueryTs) { q.QueryList[0] = nil }, max: 4,
			wantErr: "query_list",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			q := valid()
			if tc.mutate != nil {
				tc.mutate(q)
			}
			err := q.ValidateNamedOutputs(tc.max)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestQueryTsToPromExprForDoesNotMutateSharedQueryOrMatchers(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	matcher := labels.MustNewMatcher(labels.MatchEqual, "service", "api")
	q := &QueryTs{
		QueryList: []*Query{{
			ReferenceName:       "A",
			FieldName:           "metric_a",
			IsDomSampled:        true,
			AggregateMethodList: AggregateMethodList{},
			TimeAggregation: TimeAggregation{
				Function: "count_over_time",
				Window:   "1m",
			},
		}},
		MetricMerge: "A",
	}
	option := &PromExprOption{
		ReferenceNameLabelMatcher:   map[string][]*labels.Matcher{"A": {matcher}},
		IgnoreTimeAggregationEnable: true,
	}

	_, err := q.ToPromExprFor(ctx, "A", option)
	require.NoError(t, err)
	require.Equal(t, "count_over_time", q.QueryList[0].TimeAggregation.Function)
	require.Equal(t, "service", matcher.Name)
}

func TestQueryTsToPromExprForDoesNotChangeLegacyExpression(t *testing.T) {
	q := &QueryTs{
		QueryList: []*Query{
			{ReferenceName: "A", FieldName: "metric_a"},
			{ReferenceName: "B", FieldName: "metric_b"},
		},
		MetricMerge: "A / B * 100",
	}

	legacy, err := q.ToPromExpr(context.Background(), &PromExprOption{})
	require.NoError(t, err)
	named, err := q.ToPromExprFor(context.Background(), q.MetricMerge, &PromExprOption{})
	require.NoError(t, err)
	require.Equal(t, legacy.String(), named.String())
}
