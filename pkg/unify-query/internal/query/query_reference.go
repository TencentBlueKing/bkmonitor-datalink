// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package query

import (
	"context"
	"sort"
	"strings"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// ToVmExpandForExpression limits routing and coverage to the selectors actually
// executed. A named output must not inherit another output's physical tables.
// The expression must be rendered with reference names, as in QueryTs direct queries.
func ToVmExpandForExpression(ctx context.Context, qr metadata.QueryReference, expr parser.Expr) *metadata.VmExpand {
	used := make(metadata.QueryReference)
	selectors := 0
	parser.Inspect(expr, func(node parser.Node, _ []parser.Node) error {
		if selector, ok := node.(*parser.VectorSelector); ok {
			selectors++
			name := selector.Name
			if name == "" {
				// Query.IsRegexp renders its reference as {__name__=~"a"}.
				// Resolve the literal reference identity, not a regex over references.
				for _, matcher := range selector.LabelMatchers {
					if matcher.Name == labels.MetricName && (matcher.Type == labels.MatchEqual || matcher.Type == labels.MatchRegexp) {
						name = matcher.Value
						break
					}
				}
			}
			if references, exists := qr[name]; exists {
				used[name] = references
			}
		}
		return nil
	})
	if selectors == 0 {
		// Keep the existing VM execution envelope for constants such as vector(1),
		// which still require a table to dispatch through the gateway. They read no
		// physical metric, so those dispatch tables are not coverage requirements.
		expand := ToVmExpand(ctx, qr)
		if expand != nil {
			expand.RtDetailList = nil
		}
		return expand
	}
	return ToVmExpand(ctx, used)
}

func ToVmExpand(ctx context.Context, qr metadata.QueryReference) *metadata.VmExpand {
	vmClusterNames := set.New[string]()
	vmResultTable := set.New[string]()
	metricFilterCondition := make(map[string]string)
	rtDetailList := make(map[string]metadata.RtDetail)

	for referenceName, references := range qr {
		if len(references) == 0 {
			continue
		}

		// 因为是直查，reference 还需要承担聚合语法生成，所以 vm 不支持同指标的拼接，所以这里只取第一个 reference
		reference := references[0]
		if 0 < len(reference.QueryList) {
			vmConditions := set.New[string]()
			for _, query := range reference.QueryList {
				if query.VmRt == "" {
					continue
				}

				vmResultTable.Add(query.VmRt)
				vmConditions.Add(string(query.VmCondition))
				vmClusterNames.Add(query.StorageName)
				rtDetailList[query.VmRt] = metadata.RtDetail{
					TableID:     query.TableID,
					StorageName: query.StorageName,
				}
			}

			filterCondition := ""
			if vmConditions.Size() > 0 {
				conditions := vmConditions.ToArray()
				sort.Strings(conditions)
				filterCondition = strings.Join(conditions, ` or `)
			}

			metricFilterCondition[referenceName] = filterCondition
		}
	}

	if vmResultTable.Size() == 0 {
		return nil
	}

	vmExpand := &metadata.VmExpand{
		MetricFilterCondition: metricFilterCondition,
		ResultTableList:       vmResultTable.ToArray(),
		RtDetailList:          rtDetailList,
	}
	sort.Strings(vmExpand.ResultTableList)

	// 当所有的 vm 集群都一样的时候，才进行传递
	if vmClusterNames.Size() == 1 {
		vmExpand.ClusterName = vmClusterNames.First()
	}

	return vmExpand
}
