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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	queryMod "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

// buildStorageQuery 将结果表默认值与历史定位字段解析为完整的存储查询。
func (q *Query) buildStorageQuery(
	ctx context.Context,
	tsDB *queryMod.TsDBV2,
	route queryMod.StorageIDRange,
	aggregates metadata.Aggregates,
	allConditions AllConditions,
) (*metadata.Query, error) {
	query := q.BuildMetadataQuery(ctx, tsDB, allConditions)
	if query == nil {
		return nil, nil
	}
	query.Aggregates = aggregates.Copy()
	query.Timezone = metadata.GetQueryParams(ctx).Timezone
	query.StorageID = route.StorageID
	// 历史记录指定的物理目标覆盖结果表当前定位。
	if route.StorageType != "" {
		query.StorageType = route.StorageType
	}
	if route.StorageName != "" {
		query.StorageName = route.StorageName
	}
	if route.ClusterName != "" {
		query.ClusterName = route.ClusterName
	}
	if route.DB != "" {
		query.DB = route.DB
	}
	if route.Measurement != "" {
		query.Measurement = route.Measurement
		query.Measurements = []string{route.Measurement}
	}
	if route.HasSourceType {
		query.SourceType = route.SourceType
	}
	query.ResultTableOption = q.ResultTableOptions.GetOption(query.TableUUID())

	if query.StorageType == "" {
		storage, _ := tsdb.GetStorage(ctx, query.StorageID)
		if storage != nil {
			query.StorageType = storage.Type
		}
	}
	if query.StorageType == "" {
		return nil, fmt.Errorf("storageType is empty with %v", query.StorageID)
	}

	if query.VmRt != "" {
		isVmQuery := func() bool {
			dims := set.New[string]()
			for _, aggregate := range q.AggregateMethodList {
				dims.Add(aggregate.Dimensions...)
			}
			if query.CheckDruidQuery(ctx, dims) {
				return true
			}
			return featureFlag.GetMustVmQueryFeatureFlag(ctx, tsDB.TableID)
		}()
		if isVmQuery {
			query.StorageType = metadata.VictoriaMetricsStorageType
		} else {
			query.StorageType = metadata.InfluxDBStorageType
		}
	}
	if query.StorageType == metadata.VictoriaMetricsStorageType {
		// VM 指标名称包含字段名，相关函数参数和 __name__ 条件同步转换。
		for _, aggregate := range q.AggregateMethodList {
			if aggregate.Method == "label_replace" && len(aggregate.VArgsList) == 4 && aggregate.VArgsList[2] == promql.MetricLabelName {
				if strings.LastIndex(fmt.Sprintf("%s", aggregate.VArgsList[3]), query.Field) < 0 {
					aggregate.VArgsList[3] = fmt.Sprintf("%s_%s", aggregate.VArgsList[3], query.Field)
				}
			}
		}
		for _, conditions := range allConditions {
			for _, condition := range conditions {
				if condition.DimensionName == promql.MetricLabelName {
					for index, value := range condition.Value {
						if strings.LastIndex(value, query.Field) < 0 {
							condition.Value[index] = fmt.Sprintf("%s_%s", value, query.Field)
						}
					}
				}
			}
		}
	}
	metadata.GetQueryParams(ctx).SetStorageType(query.StorageType)
	return query, nil
}
