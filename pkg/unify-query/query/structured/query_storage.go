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
	if len(tsDB.StorageClusterRecords) > 0 {
		query.PhysicalTables = route.PhysicalTables
		// 历史记录不得继承当前表的 BKBase ID。
		if route.PhysicalTables != nil {
			query.DB = route.DB
		}
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
	// 先以旧标识查找游标，随后按本次选定的后端和物理目标查找。
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
	// 后续页保留首次选择的后端，不因 Header 改变而重用另一种游标。
	if query.Measurement == metadata.DorisStorageType {
		if query.ResultTableOption == nil && len(query.PhysicalTables) > 0 {
			direct := *query
			direct.QueryBackend = metadata.DorisStorageType
			direct.DirectTables = direct.DorisTables()
			query.ResultTableOption = q.ResultTableOptions.GetOption(direct.TableUUID())
		}
		// 合并查询的游标列出了多个目标，仅用它匹配当前授权路由；不能从游标构造物理 SQL 目标。
		if query.ResultTableOption == nil {
			target := query.PhysicalTables["main"]
			prefix := query.TableUUID() + "|doris|"
			for key, option := range q.ResultTableOptions {
				if option == nil || option.QueryBackend != metadata.DorisStorageType || !strings.HasPrefix(key, prefix) {
					continue
				}
				for _, saved := range option.DirectTables {
					if target.Database != "" && target.Database == saved.Database && target.Table == saved.Table {
						query.ResultTableOption = option
						break
					}
				}
				if query.ResultTableOption != nil {
					break
				}
			}
		}
		if option := query.ResultTableOption; option != nil {
			switch option.QueryBackend {
			case metadata.DorisStorageType:
				t := query.PhysicalTables["main"]
				if t.Database == "" || t.Table == "" {
					return nil, fmt.Errorf("Doris pagination target is no longer configured")
				}
				query.QueryBackend = metadata.DorisStorageType
				query.DirectTables = []metadata.PhysicalTable{t}
			case "", metadata.BkSqlStorageType:
				// 升级前的游标没有后端标记，必须继续使用生成它的 BKBase。
				if query.DB == "" {
					return nil, fmt.Errorf("BKBase pagination target is no longer configured")
				}
				query.QueryBackend = metadata.BkSqlStorageType
			default:
				return nil, fmt.Errorf("unknown pagination backend %q", option.QueryBackend)
			}
		}
	}
	if err := query.ResolveDorisBackend(ctx); err != nil {
		return nil, err
	}
	if option := q.ResultTableOptions.GetOption(query.TableUUID()); option != nil {
		query.ResultTableOption = option
	}
	metadata.GetQueryParams(ctx).SetStorageType(query.StorageType)
	return query, nil
}
