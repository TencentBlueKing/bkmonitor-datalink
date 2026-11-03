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
	"fmt"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/featureFlag"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// toBkDataQueryMetric 构建无需历史存储路由的计算平台查询，并保留业务空间校验。
func (q *Query) toBkDataQueryMetric(ctx context.Context, spaceUid string, aggregates metadata.Aggregates, allConditions AllConditions) (*metadata.QueryMetric, error) {
	var err error
	ctx, span := trace.NewSpan(ctx, "query-ts-to-bkdata-query-metric")
	defer span.End(&err)
	queryMetric := &metadata.QueryMetric{ReferenceName: q.ReferenceName, MetricName: q.FieldName}
	qp := metadata.GetQueryParams(ctx)
	tableID := string(q.TableID)
	// 判断空间跟业务是否匹配。
	isMatchBizID := func() bool {
		space := strings.Split(spaceUid, "__")
		if len(space) != 2 {
			return false
		}
		if space[0] != "bkcc" {
			return false
		}
		return strings.HasPrefix(tableID, space[1]+"_")
	}()

	ff := featureFlag.GetBkDataTableIDCheck(ctx, tableID)
	metric.BkDataRequestInc(ctx, spaceUid, tableID, fmt.Sprintf("%v", isMatchBizID), fmt.Sprintf("%v", ff))
	// 开启校验时，仅允许查询当前业务的表，避免越权。
	if ff && !isMatchBizID {
		return queryMetric, nil
	}

	route, err := MakeRouteFromTableID(q.TableID)
	if err != nil {
		return nil, err
	}
	if route.DB() == "" {
		err = metadata.NewMessage(metadata.MsgQueryBKSQL, "bkdata 的表名不能为空").Error(ctx, nil)
		return nil, err
	}

	costProfile := metadata.QueryCostProfile{
		SelectAllCandidate: len(aggregates) == 0 && len(q.KeepColumns) == 0 && q.SQL == "",
		ASTBranchCount:     q.ASTBranchCount,
		SQLPushdown:        q.IsDomSampled,
	}
	if costProfile.ASTBranchCount == 0 {
		costProfile.ASTBranchCount = 1
	}
	costProfile.RangeFunction, costProfile.Window, costProfile.Step = q.queryCostRangeProfile()
	costProfile.StepLessThanWindow = costProfile.Window > 0 && costProfile.Step > 0 && costProfile.Step < costProfile.Window
	span.Set("query-cost.select-all-candidate", costProfile.SelectAllCandidate)
	span.Set("query-cost.range-function", costProfile.RangeFunction)
	span.Set("query-cost.step-less-than-window", costProfile.StepLessThanWindow)
	span.Set("query-cost.ast-branches", costProfile.ASTBranchCount)
	span.Set("query-cost.sql-pushdown", costProfile.SQLPushdown)
	span.Set("query-cost.window", costProfile.Window)
	span.Set("query-cost.step", costProfile.Step)

	query := &metadata.Query{
		StorageType:   metadata.BkSqlStorageType,
		TableID:       tableID,
		DataSource:    q.DataSource,
		DB:            route.DB(),
		Measurement:   route.Measurement(),
		Field:         q.FieldName,
		Aggregates:    aggregates,
		AllConditions: allConditions.MetaDataAllConditions(),
		CostProfile:   costProfile,
	}

	// SaaS 将计算平台的 eq/neq 映射为 contains/ncontains，此处恢复多值精确匹配。
	for _, conds := range query.AllConditions {
		for i := range conds {
			if conds[i].IsWildcard || conds[i].IsPrefix || conds[i].IsSuffix {
				continue
			}
			switch conds[i].Operator {
			case metadata.ConditionContains:
				conds[i].Operator = metadata.ConditionEqual
			case metadata.ConditionNotContains:
				conds[i].Operator = metadata.ConditionNotEqual
			}
		}
	}

	query.SQL = q.SQL
	query.QueryString = q.QueryString
	query.IsPrefix = q.IsPrefix
	query.Source = q.KeepColumns
	query.Collapse = q.Collapse
	query.Scroll = q.Scroll
	query.DryRun = q.DryRun
	query.IsMergeDB = q.IsMergeDB
	query.Size = q.Limit
	query.From = q.From

	if query.SQL != "" {
		const sqlPreviewMax = 512
		preview := query.SQL
		if len(preview) > sqlPreviewMax {
			preview = preview[:sqlPreviewMax] + "...(truncated)"
		}
		span.Set("bkdata.metadata.sql.preview", preview)
	}

	if len(q.OrderBy) > 0 {
		query.Orders = q.OrderBy.Orders()
	}

	qp.SetStorageType(query.StorageType)
	queryMetric.QueryList = []*metadata.Query{query}
	return queryMetric, nil
}
