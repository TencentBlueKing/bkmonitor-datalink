// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package promql

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	oleltrace "go.opentelemetry.io/otel/trace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

// QueryInfo
type QueryInfo struct {
	TsDBs       []*query.TsDBV2
	ClusterID   string
	DB          string
	Measurement string
	DataIDList  []consul.DataID

	// 是否为行转列表
	IsPivotTable bool

	// 判断是否是 Count 查询
	IsCount bool

	// limit等偏移量配置
	OffsetInfo OffSetInfo

	// 复杂额外条件信息
	Conditions [][]ConditionField

	// 聚合方法，由querier判断是否能够做降采样
	AggregateMethodList AggrMethods
}

type AggrMethods []AggrMethod

// AggrMethod 聚合方法
type AggrMethod struct {
	Name       string
	Dimensions []string
	Without    bool
}

// 通过 clusterID 获取查询状态
func clusterIDToSourceType(clusterID string) (string, error) {
	// 从 VM 类型开始查
	ins, err := tsdb.GetStorage(clusterID)
	if err != nil {
		// 默认返回 influxdb 存储类型
		return consul.InfluxDBStorageType, nil
	}
	return ins.Type, nil
}

// tsDBToMetadataQuery tsDBs 结构转换为 metadata.Query 结构体
func tsDBToMetadataQuery(ctx context.Context, metricName string, queryInfo *QueryInfo) (metadata.QueryList, error) {

	var (
		span oleltrace.Span
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "ts-db-metadata-query")
	if span != nil {
		defer span.End()
	}

	trace.InsertIntIntoSpan("result_table_num", len(queryInfo.TsDBs), span)

	queryList := make(metadata.QueryList, 0, len(queryInfo.TsDBs))
	for i, tsDB := range queryInfo.TsDBs {
		var (
			field     string
			whereList = NewWhereList()
			err       error
			query     = &metadata.Query{
				SegmentedEnable: tsDB.SegmentedEnable,
				Filters:         make([]map[string]string, len(tsDB.Filters)),
				OffsetInfo: metadata.OffSetInfo{
					OffSet:  queryInfo.OffsetInfo.OffSet,
					Limit:   queryInfo.OffsetInfo.Limit,
					SOffSet: queryInfo.OffsetInfo.SOffSet,
					SLimit:  queryInfo.OffsetInfo.SLimit,
				},
			}
		)

		tsDBStr, _ := json.Marshal(tsDB)
		trace.InsertStringIntoSpan(fmt.Sprintf("result_table_%d", i), string(tsDBStr), span)

		db := tsDB.DB
		measurement := tsDB.Measurement
		storageID := tsDB.StorageID
		vmRt := tsDB.VmRt

		switch tsDB.MeasurementType {
		case redis.BKTraditionalMeasurement:
			field = metricName
		// 多指标单表，单列多指标，维度: metric_name 为指标名，metric_value 为指标值
		case redis.BkExporter:
			whereList.Append(AndOperator, NewWhere(StaticMetricName, metricName, EqualOperator, StringType))
			field = StaticMetricValue
		// 多指标单表，字段名为指标名
		case redis.BkStandardV2TimeSeries:
			field = metricName
		// 单指标单表，指标名为表名，值为指定字段 value
		case redis.BkSplitMeasurement:
			measurement = metricName
			field = StaticField
		default:
			err = fmt.Errorf("%s: %s 类型异常", tsDB.TableID, tsDB.MeasurementType)
			log.Errorf(ctx, err.Error())
			return nil, err
		}

		// 增加聚合方法
		query.AggregateMethodList = make([]metadata.AggrMethod, len(queryInfo.AggregateMethodList))
		for i, aggr := range queryInfo.AggregateMethodList {
			query.AggregateMethodList[i] = metadata.AggrMethod{
				Name:       aggr.Name,
				Dimensions: aggr.Dimensions,
				Without:    aggr.Without,
			}
		}

		// 如果有额外condition，则录入where语句中
		if len(queryInfo.Conditions) != 0 {
			whereList.Append(AndOperator, NewTextWhere(MakeOrExpression(queryInfo.Conditions)))
			if len(queryInfo.Conditions) > 1 {
				query.IsHasOr = true
			}
		}

		// 拼入空间自带过滤条件
		var conditions [][]ConditionField
		for fi, filter := range tsDB.Filters {
			var (
				condition []ConditionField
				tmpFilter = make(map[string]string)
			)

			for k, v := range filter {
				if v != "" {
					condition = append(condition, ConditionField{
						DimensionName: k,
						Value:         []string{v},
						Operator:      EqualOperator,
					})
					tmpFilter[k] = v
				}
			}
			if len(condition) > 0 {
				conditions = append(conditions, condition)
			}

			if len(tmpFilter) > 0 {
				query.Filters[fi] = tmpFilter
			}
		}

		if len(conditions) > 0 {
			whereList.Append(AndOperator, NewTextWhere(MakeOrExpression(conditions)))
		}

		query.ClusterID = storageID
		query.SourceType, err = clusterIDToSourceType(query.ClusterID)

		log.Debugf(ctx, "tsdb: %s", tsDB.String())

		if err != nil {
			log.Errorf(ctx, err.Error())
			return nil, err
		}

		query.TableID = tsDB.TableID
		query.DB = db
		query.Measurement = measurement
		query.Field = field
		query.Condition = whereList.String()
		query.VmRt = vmRt

		queryList = append(queryList, query)
	}

	return queryList, nil
}

// queryInfoMetadataQuery queryInfo 结构转换为 metadata.Query 结构体
func queryInfoMetadataQuery(ctx context.Context, metricName string, queryInfo *QueryInfo) (metadata.QueryList, error) {
	var (
		tableInfos []*consul.TableID
		whereList  = NewWhereList()
		isHasOr    = false
		span       oleltrace.Span
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "query-info-metadata-query")
	if span != nil {
		defer span.End()
	}

	if queryInfo.DB != "" && queryInfo.Measurement != "" {
		tableInfos = append(tableInfos, influxdb.GetTableIDByDBAndMeasurement(
			queryInfo.DB, queryInfo.Measurement,
		))
	} else {
		for _, dataID := range queryInfo.DataIDList {
			tableInfo := influxdb.GetTableIDsByDataID(dataID)
			if len(tableInfo) == 0 {
				continue
			}
			tableInfos = append(tableInfos, tableInfo...)
		}
	}
	// 如果是行转列表，则要特殊处理查询信息
	if queryInfo.IsPivotTable {
		whereList.Append(AndOperator, NewWhere(StaticMetricName, metricName, EqualOperator, StringType))
		metricName = StaticMetricValue
	}
	// 如果有额外condition，则录入where语句中
	if len(queryInfo.Conditions) != 0 {
		whereList.Append(AndOperator, NewTextWhere(MakeOrExpression(queryInfo.Conditions)))
		if len(queryInfo.Conditions) > 1 {
			isHasOr = true
		}
	}

	trace.InsertIntIntoSpan("result_table_num", len(tableInfos), span)

	queryList := make(metadata.QueryList, 0, len(tableInfos))
	for i, tableInfo := range tableInfos {
		var (
			db          = tableInfo.DB
			clusterID   = tableInfo.ClusterID
			measurement string
			field       string
			query       = &metadata.Query{
				SegmentedEnable: influxdb.SegmentedQueryEnable(db, measurement),
				IsHasOr:         isHasOr,
				OffsetInfo: metadata.OffSetInfo{
					OffSet:  queryInfo.OffsetInfo.OffSet,
					Limit:   queryInfo.OffsetInfo.Limit,
					SOffSet: queryInfo.OffsetInfo.SOffSet,
					SLimit:  queryInfo.OffsetInfo.SLimit,
				},
			}
			err error
		)

		if db == "" {
			log.Errorf(ctx, "db is empty, tableInfo: %v", tableInfo)
			continue
		}

		if tableInfo.IsSplit() {
			measurement = metricName
			field = StaticField
		} else {
			measurement = tableInfo.Measurement
			field = metricName
		}

		// 增加聚合方法
		query.AggregateMethodList = make([]metadata.AggrMethod, len(queryInfo.AggregateMethodList))
		for j, aggr := range queryInfo.AggregateMethodList {
			query.AggregateMethodList[j] = metadata.AggrMethod{
				Name:       aggr.Name,
				Dimensions: aggr.Dimensions,
				Without:    aggr.Without,
			}
		}

		query.ClusterID = clusterID
		query.SourceType, err = clusterIDToSourceType(query.ClusterID)
		if err != nil {
			log.Errorf(ctx, err.Error())
			return nil, err
		}

		query.TableID = tableInfo.String()
		query.DB = db
		query.Measurement = measurement
		query.Field = field
		query.Condition = whereList.String()

		queryStr, _ := json.Marshal(query)
		trace.InsertStringIntoSpan(fmt.Sprintf("result_table_%d", i), string(queryStr), span)

		log.Debugf(ctx, "query_info: %+v", query)
		queryList = append(queryList, query)
	}

	return queryList, nil
}

// QueryInfoIntoContext 获取 queryInfo 的数据进行解析，存入 ctx 缓存中，给后续的请求使用
func QueryInfoIntoContext(ctx context.Context, referenceName, metricName string, queryInfo *QueryInfo) (context.Context, error) {
	// 查询列表，里面包含该次查询对应的所有实例，实现跨 DB 查询
	var (
		err         error
		queryMetric = &metadata.QueryMetric{
			ReferenceName: referenceName,
			MetricName:    metricName,
			IsCount:       queryInfo.IsCount,
		}
		span oleltrace.Span
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "query-info-into-context")
	if span != nil {
		defer span.End()
	}

	// 空间内容解析
	if queryInfo.TsDBs != nil {
		queryMetric.QueryList, err = tsDBToMetadataQuery(ctx, metricName, queryInfo)
	} else {
		queryMetric.QueryList, err = queryInfoMetadataQuery(ctx, metricName, queryInfo)
	}
	if err != nil {
		return ctx, err
	}
	queries := metadata.GetQueries(ctx)
	if queries == nil {
		queries = &metadata.Queries{
			Query: make(metadata.QueryReference),
		}
	}

	queries.Query[referenceName] = queryMetric

	queryMetricStr, _ := json.Marshal(queryMetric)
	trace.InsertStringIntoSpan(fmt.Sprintf("reference_%s", referenceName), string(queryMetricStr), span)

	err = metadata.SetQueries(ctx, queries)
	return ctx, err
}

// QueryInfoFromContext 通过 ctx 获取查询信息
func QueryInfoFromContext(ctx context.Context, referenceName string) (*metadata.QueryMetric, error) {
	queries := metadata.GetQueries(ctx)
	if queryMetric, ok := queries.Query[referenceName]; ok {
		return queryMetric, nil
	} else {
		return nil, fmt.Errorf("metadata query is empty, with referenceName: %s", referenceName)
	}
}

// OffSetInfo Offset的信息存储，供promql查询转换为influxdb查询语句时使用
type OffSetInfo struct {
	OffSet  time.Duration
	Limit   int
	SOffSet int
	SLimit  int
}

// ConditionField
type ConditionField struct {
	DimensionName string   `json:"field_name"`
	Value         []string `json:"value"`
	Operator      string   `json:"op"`
}
