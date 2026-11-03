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
	"cmp"
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// QueryPlan 分别保存执行查询与完整历史路由，路由数量不决定执行次数。
type QueryPlan struct {
	Queries metadata.QueryReference
	Routes  metadata.QueryReference
	Stats   QueryPlanStats
}

// QueryPlanStats 记录规划过程中实际发生的目标复用和数据库合并次数。
type QueryPlanStats struct {
	ReusedTargets int
	MergedQueries int
}

type queryMode uint8

const (
	timeSeriesQuery queryMode = iota
	rawQuery
)

type queryMetricPlan struct {
	queries *metadata.QueryMetric
	routes  *metadata.QueryMetric
	stats   QueryPlanStats
}

// ToRawQueryPlan 按完整查询时间生成执行目标，保留每个目标对应的历史分段。
func (q *QueryTs) ToRawQueryPlan(ctx context.Context) (plan QueryPlan, err error) {
	ctx, span := trace.NewSpan(ctx, "query-raw-plan")
	defer span.End(&err)

	plan, err = q.toQueryPlan(ctx, rawQuery)
	span.Set("raw_plan_route_count", plan.Routes.Count())
	span.Set("raw_plan_query_count", plan.Queries.Count())
	span.Set("raw_plan_reused_target_count", plan.Stats.ReusedTargets)
	span.Set("raw_plan_merge_db_count", plan.Stats.MergedQueries)
	if err != nil {
		return plan, err
	}
	metric.RawQueryDecisionAdd(ctx, metric.RawQueryDecisionHistoryDuplicate, plan.Stats.ReusedTargets)
	metric.RawQueryDecisionAdd(ctx, metric.RawQueryDecisionMergeDB, plan.Stats.MergedQueries)
	return plan, nil
}

// rawStorageTarget 只在同一个逻辑查询和同一个结果表的历史展开中使用。
// 查询条件、投影和分页属于该逻辑查询；历史记录只改变这里的存储定位信息。
type rawStorageTarget struct {
	storageType string
	storageID   string
	storageName string
	clusterName string
	db          string
	measurement string
	sourceType  string
}

func rawTarget(query *metadata.Query) rawStorageTarget {
	return rawStorageTarget{
		storageType: query.StorageType,
		storageID:   query.StorageID,
		storageName: query.StorageName,
		clusterName: query.ClusterName,
		db:          query.DB,
		measurement: query.Measurement,
		sourceType:  query.SourceType,
	}
}

type queryMergeTarget struct {
	storageUUID string
	clusterName string
	measurement string
	sourceType  string
}

func mergeTarget(query *metadata.Query, mode queryMode) queryMergeTarget {
	target := queryMergeTarget{storageUUID: query.StorageUUID()}
	if mode == rawQuery {
		target.clusterName = query.ClusterName
		target.measurement = query.Measurement
		target.sourceType = query.SourceType
	}
	return target
}

func compareMergeTargets(left, right queryMergeTarget) int {
	return cmp.Or(
		cmp.Compare(left.storageUUID, right.storageUUID),
		cmp.Compare(left.clusterName, right.clusterName),
		cmp.Compare(left.measurement, right.measurement),
		cmp.Compare(left.sourceType, right.sourceType),
	)
}
