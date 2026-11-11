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
	ReusedTargets int // 因重复历史目标而复用的查询项数。
	MergedQueries int // 启用 IsMergeDB 后合并的查询项数。
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
// 完整规划成功后统一发布目标复用与 DB 合并指标。
func (q *QueryTs) ToRawQueryPlan(ctx context.Context) (plan QueryPlan, err error) {
	ctx, span := trace.NewSpan(ctx, "query-raw-plan")
	defer span.End(&err)

	plan, err = q.toQueryPlan(ctx, rawQuery)
	span.Set("raw.plan.route_count", plan.Routes.Count())
	span.Set("raw.plan.query_count", plan.Queries.Count())
	span.Set("raw.plan.reused_target_count", plan.Stats.ReusedTargets)
	span.Set("raw.plan.merge_db_count", plan.Stats.MergedQueries)
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
	backend     string
	physical    string
	storageType string
	storageID   string
	storageName string
	clusterName string
	db          string
	measurement string
	sourceType  string
}

// rawTarget 提取已完成默认值补齐和历史覆盖的存储定位字段。
func rawTarget(query *metadata.Query) rawStorageTarget {
	physical := ""
	if query.QueryBackend == metadata.DorisStorageType {
		physical = metadata.PhysicalTableSQL(query.PhysicalTables["main"])
	}
	return rawStorageTarget{
		backend: query.QueryBackend, physical: physical,
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
	backend     string
	storageUUID string
	clusterName string
	measurement string
	sourceType  string
}

// mergeTarget 生成 DB 合并分组键，Raw 查询同时区分集群、Measurement 和 SourceType。
func mergeTarget(query *metadata.Query, mode queryMode) queryMergeTarget {
	target := queryMergeTarget{storageUUID: query.StorageUUID(), backend: query.QueryBackend}
	// 未显式选后端时执行器使用 StorageType，续页的显式标记不应拆分同一组查询。
	if target.backend == "" {
		target.backend = query.StorageType
	}
	if mode == rawQuery {
		target.clusterName = query.ClusterName
		target.measurement = query.Measurement
		target.sourceType = query.SourceType
	}
	return target
}

func compareMergeTargets(left, right queryMergeTarget) int {
	return cmp.Or(
		cmp.Compare(left.backend, right.backend),
		cmp.Compare(left.storageUUID, right.storageUUID),
		cmp.Compare(left.clusterName, right.clusterName),
		cmp.Compare(left.measurement, right.measurement),
		cmp.Compare(left.sourceType, right.sourceType),
	)
}
