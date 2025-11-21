// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta1

import (
	"context"
	"sort"
	"time"

	"github.com/dominikbraun/graph"
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

type TimeGraph struct {
	id    int64
	nodes map[int64]cmdb.Matchers
	gm    map[int64]graph.Graph[int, int]
}

func NewTimeGraph() *TimeGraph {
	return &TimeGraph{
		nodes: make(map[int64]cmdb.Matchers),
		gm:    make(map[int64]graph.Graph[int, int]),
	}
}

func (q *TimeGraph) MakeQueryTsList(ctx context.Context, spaceUID string, labels map[string]string, start time.Time, end time.Time, step time.Duration, relations ...cmdb.Relation) ([]*structured.QueryTs, error) {
	queryTsList := make([]*structured.QueryTs, 0, len(relations))
	for _, relation := range relations {
		source, target, metric := relation.Info()
		if metric == "" {
			continue
		}

		indexSet := set.New[string](ResourcesIndex(source, target)...)

		var fieldList []structured.ConditionField
		for k, v := range labels {
			if indexSet.Existed(k) {
				fieldList = append(fieldList, structured.ConditionField{
					DimensionName: k,
					Value:         []string{v},
					Operator:      structured.ConditionEqual,
				})
			}
		}
		dimensions := indexSet.ToArray()
		sort.Strings(dimensions)

		var conditionList []string
		for i := 1; i < len(fieldList); i++ {
			conditionList = append(conditionList, structured.ConditionAnd)
		}

		query := &structured.Query{
			FieldName: metric,
			TimeAggregation: structured.TimeAggregation{
				Function: structured.CountOT,
				Window:   structured.Window(step.String()),
			},
			AggregateMethodList: structured.AggregateMethodList{
				{
					Method:     structured.COUNT,
					Dimensions: dimensions,
				},
			},
			Conditions: structured.Conditions{
				FieldList:     fieldList,
				ConditionList: conditionList,
			},
			ReferenceName: metadata.DefaultReferenceName,
		}

		queryTsList = append(queryTsList, &structured.QueryTs{
			SpaceUid:    spaceUID,
			QueryList:   []*structured.Query{query},
			MetricMerge: metadata.DefaultReferenceName,
			Start:       cast.ToString(start.Unix()),
			End:         cast.ToString(end.Unix()),
			Step:        step.String(),
		})
	}

	return queryTsList, nil
}
