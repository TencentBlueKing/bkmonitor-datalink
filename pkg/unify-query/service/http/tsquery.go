// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang/gddo/httputil/header"
	oleltrace "go.opentelemetry.io/otel/trace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/downsample"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

const (
	BizHeader      = "X-Bk-Scope-Biz-Id"
	SpaceUIDHeader = "X-Bk-Scope-Space-Uid"
)

func HandleTSQueryRequest(c *gin.Context) {
	var (
		ctx  = c.Request.Context()
		span oleltrace.Span
	)

	// 这里开始context就使用trace生成的了
	ctx, span = trace.IntoContext(ctx, trace.TracerName, "handle-ts-request")
	if span != nil {
		defer span.End()
	}

	queryStmt, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Errorf(ctx, "read ts request body failed for->[%s]", err)
		c.JSON(400, ErrResponse{Err: err.Error()})
		return
	}

	// 如果header中有bkbizid，则以header中的值为最优先
	bizIDs := header.ParseList(c.Request.Header, BizHeader)
	spaceUid := c.Request.Header.Get(SpaceUIDHeader)

	log.Debugf(ctx, "X-Bk-Scope-Biz-Id:%v", bizIDs)

	trace.InsertStringIntoSpan("request-space-uid", spaceUid, span)
	trace.InsertStringSliceIntoSpan("request-biz-ids", bizIDs, span)

	trace.InsertStringIntoSpan("ts-request-data", string(queryStmt), span)
	trace.InsertIntIntoSpan("ts-request-data-size", len(queryStmt), span)
	trace.InsertStringIntoSpan("ts-request-header", fmt.Sprintf("%+v", c.Request.Header), span)

	respData, err := handleTSQuery(ctx, string(queryStmt), false, bizIDs, spaceUid)
	if err != nil {
		log.Warnf(ctx, "handle ts request failed for->[%s]", err)
		c.JSON(400, ErrResponse{Err: err.Error()})
		return
	}

	c.JSON(200, respData)
}

func HandleTSExemplarRequest(c *gin.Context) {
	var (
		ctx  = c.Request.Context()
		span oleltrace.Span
	)

	// 这里开始context就使用trace生成的了
	ctx, span = trace.IntoContext(ctx, trace.TracerName, "handle-ts-exemplar-request")
	if span != nil {
		defer span.End()
	}

	queryStmt, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Errorf(context.TODO(), "read ts request body failed for->[%s]", err)
		c.JSON(400, ErrResponse{Err: err.Error()})
		return
	}

	// 如果header中有bkbizid，则以header中的值为最优先
	bizIDs := header.ParseList(c.Request.Header, BizHeader)
	spaceUid := c.Request.Header.Get(SpaceUIDHeader)

	trace.InsertStringIntoSpan("request-space-uid", spaceUid, span)
	trace.InsertStringSliceIntoSpan("request-biz-ids", bizIDs, span)

	trace.InsertStringIntoSpan("request-header", fmt.Sprintf("%+v", c.Request.Header), span)
	trace.InsertStringIntoSpan("request-data", string(queryStmt), span)

	respData, err := handleTSExemplarQuery(ctx, string(queryStmt), bizIDs, spaceUid)
	if err != nil {
		log.Errorf(context.TODO(), "handle ts request failed for->[%s]", err)
		c.JSON(400, ErrResponse{Err: err.Error()})
		return
	}

	c.JSON(200, respData)
}

// makeInfluxdbQuery 生成 sql 语句
func makeInfluxdbQuery(
	ctx context.Context, query *structured.CombinedQueryParams, bizIDs []string, spaceUid string,
) ([]influxdb.SQLInfo, error) {
	var sqls []influxdb.SQLInfo
	ctx, span := trace.IntoContext(ctx, trace.TracerName, "promql-utils-makeInfluxdbQuery")
	if span != nil {
		defer span.End()
	}
	options, err := structured.GenerateOptions(query, false, bizIDs, spaceUid)
	if err != nil {
		log.Errorf(ctx, "generate options error ->[%s]", err)
		return nil, err
	}
	info, err := getTimeInfo(query)
	if err != nil {
		log.Errorf(ctx, "get time info: %s", err)
		return nil, err
	}
	trace.InsertStringIntoSpan("start-str", info.Start.String(), span)
	trace.InsertStringIntoSpan("end-str", info.Stop.String(), span)
	trace.InsertStringIntoSpan("interval", info.Interval.String(), span)

	for _, q := range query.QueryList {
		var (
			tableInfos []*consul.TableID
			whereList  = promql.NewWhereList()
			fields     []string
		)

		for _, v := range q.FieldList {
			fields = append(fields, string(v))
		}
		for _, v := range q.KeepColumns {
			fields = append(fields, v)
		}
		// 取代查询条件中的bk_biz_id
		if options.IsReplaceBizID {
			structured.ReplaceOrAddCondition(&q.Conditions, structured.BizID, bizIDs)
		}

		tableIDfilter, err1 := structured.NewTableIDFilter(string(q.FieldName), q.TableID, nil, q.Conditions)
		if err1 != nil {
			return nil, err1
		}

		queryInfo := new(promql.QueryInfo)
		if !tableIDfilter.IsAppointTableID() {
			queryInfo.DataIDList = tableIDfilter.DataIDList()
		} else {
			routes := tableIDfilter.GetRoutes()
			for _, route := range routes {
				queryInfo.DB = route.DB()
				queryInfo.Measurement = route.Measurement()
				queryInfo.ClusterID = route.ClusterID()
			}
		}
		queryInfo.IsPivotTable = influxdb.IsPivotTable(string(q.TableID))

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

		metricName := string(q.FieldName)

		// 增加查询条件
		q.Conditions.ConditionList = append([]string{"and"}, q.Conditions.ConditionList...)
		for idx, cond := range q.Conditions.FieldList {
			if len(cond.Value) <= 0 {
				continue
			}

			// 正则优化 有可能改变 Operator/Value 值
			cf := &structured.ConditionField{
				DimensionName: cond.DimensionName,
				Value:         cond.Value,
				Operator:      cond.Operator,
			}

			cf = cf.ContainsToPromReg()

			valueType := promql.RegexpType
			switch cf.Operator {
			case structured.ConditionEqual, structured.ConditionNotEqual:
				valueType = promql.StringType
			}

			operator, ok := promql.PromqlOperatorMapping[cf.ToPromOperator()]
			if !ok {
				continue
			}
			whereList.Append(
				q.Conditions.ConditionList[idx], promql.NewWhere(cf.DimensionName, cf.Value[0], operator, valueType),
			)
		}

		// 判断是否是行转列数据
		if queryInfo.IsPivotTable {
			whereList.Append(promql.AndOperator,
				promql.NewWhere(promql.StaticMetricName, metricName, promql.EqualOperator, promql.StringType),
			)
			metricName = promql.StaticMetricValue
		}
		if len(queryInfo.Conditions) != 0 {
			whereList.Append(promql.AndOperator, promql.NewTextWhere(promql.MakeOrExpression(queryInfo.Conditions)))
		}

		start := strconv.FormatInt(info.Start.UnixNano(), 10)
		end := strconv.FormatInt(info.Stop.UnixNano(), 10)
		whereList.Append(promql.AndOperator, promql.NewWhere("time", start, promql.UpperEqualOperator, promql.NumType))
		whereList.Append(promql.AndOperator, promql.NewWhere("time", end, promql.LowerOperator, promql.NumType))

		limit := int(q.Limit)
		sLimit := q.Slimit

		for i, tableInfo := range tableInfos {
			var (
				measurement string
				field       string
				db          = tableInfo.DB

				sLimitStr string
				limitStr  string
				sql       string
			)
			if tableInfo.IsSplit() {
				measurement = metricName
				field = promql.StaticField
			} else {
				measurement = tableInfo.Measurement
				field = metricName
			}
			trace.InsertStringIntoSpan(fmt.Sprintf("table-info-field-%d", i), field, span)
			if sLimit > 0 {
				sLimitStr = fmt.Sprintf(" slimit %d", sLimit)
			}
			if limit > 0 {
				limitStr = fmt.Sprintf(" limit %d", limit)
			}

			sql = fmt.Sprintf(
				`select %s as %s, time as %s, %s from %s where %s and (bk_span_id != '' or bk_trace_id != '') %s%s`,
				field, influxdb.ResultColumnName, influxdb.TimeColumnName, strings.Join(fields, ","),
				measurement, whereList.String(), limitStr, sLimitStr,
			)
			trace.InsertStringIntoSpan(fmt.Sprintf("table-info-db-%d", i), db, span)
			trace.InsertStringIntoSpan(fmt.Sprintf("table-info-sql-%d", i), sql, span)
			trace.InsertStringSliceIntoSpan(fmt.Sprintf("table-info-fields-%d", i), fields, span)

			// sql注入防范
			err = influxdb.CheckSelectSQL(ctx, sql)
			if err != nil {
				trace.InsertStringIntoSpan(fmt.Sprintf("table-info-error-%d", i), err.Error(), span)
				return nil, err
			}

			sqls = append(sqls, influxdb.SQLInfo{ClusterID: tableInfo.ClusterID, DB: db, SQL: sql, MetricName: metricName})
		}
	}
	return sqls, nil
}

// handleTSExemplarQuery
func handleTSExemplarQuery(ctx context.Context, queryStmt string, bizIDs []string, spaceUid string) (*PromData, error) {
	var (
		span oleltrace.Span
		sqls []influxdb.SQLInfo
	)
	ctx, span = trace.IntoContext(ctx, trace.TracerName, "handle-ts-exemplar-query")
	if span != nil {
		defer span.End()
	}
	query, err := structured.AnalysisQuery(queryStmt)
	if err != nil {
		log.Errorf(ctx, "anaylize combined query info failed for->[%s]", err)
		return nil, err
	}

	sqls, err = makeInfluxdbQuery(ctx, query, bizIDs, spaceUid)
	if err != nil {
		log.Errorf(ctx, "get sqls failed for->[%s]", err)
		return nil, err
	}

	// 返回第一个 error
	tables, errs := influxdb.QueryInfosAsync(ctx, sqls, "", 0)
	if errs != nil {
		return nil, errs[0]
	}

	// 数据格式转换 保持跟 /query/ts 接口兼容
	promqlTables := &promql.Tables{}
	var ret []*promql.Table
	for _, table := range tables.Tables {

		ret = append(ret, &promql.Table{
			Name:        table.Name,
			MetricName:  table.MetricName,
			Headers:     table.Headers,
			Types:       table.Types,
			GroupKeys:   table.GroupKeys,
			GroupValues: table.GroupValues,
			Data:        table.Data,
		})
	}
	promqlTables.Tables = ret

	promData := &PromData{}
	if err = promData.Fill(promqlTables); err != nil {
		return nil, err
	}

	return promData, nil
}

// HandleTsQueryStructToPromQLRequest 结构化查询转 PromQL 接口
func HandleTsQueryStructToPromQLRequest(c *gin.Context) {
	var (
		ctx  = c.Request.Context()
		span oleltrace.Span
	)

	// 这里开始context就使用trace生成的了
	ctx, span = trace.IntoContext(ctx, trace.TracerName, "handle-ts-struct-to-promql")
	if span != nil {
		defer span.End()
	}

	queryStmt, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Errorf(context.TODO(), "read ts request body failed for->[%s]", err)
		c.JSON(400, ErrResponse{Err: err.Error()})
		return
	}

	trace.InsertStringIntoSpan("request_data", string(queryStmt), span)

	_, stmt, _, err := handleTsQueryParams(ctx, string(queryStmt), true, nil, "")
	if err != nil {
		log.Errorf(context.TODO(), "handle ts params failed for->[%s]", err)
		c.JSON(400, ErrResponse{Err: err.Error()})
		return
	}

	c.JSON(200, gin.H{"promql": stmt})
}

// 根据语句返回结果
func handleTSQuery(
	ctx context.Context, queryStmt string, onlyParse bool, bizIDs []string, spaceUid string,
) (interface{}, error) {
	ctx, stmt, query, err := handleTsQueryParams(ctx, queryStmt, onlyParse, bizIDs, spaceUid)
	// 由于监控在刚接入容器监控时，没有任何容器指标，导致容器查询方式中：
	// 当tableID为空时，此时匹配到dataIdList为空，则应该直接返回空数据，而不是返回错误wrong table_id
	if err != nil && err != structured.ErrEmptyTableID {
		return nil, err
	}

	var respData *PromData
	respData, err = HandleRawPromQuery(ctx, stmt, query)
	if err != nil {
		return nil, err
	}

	// 降采样逻辑，根据DownSampleRange，如果大于Step则进行降采样处理
	var ok bool
	var factor float64
	if ok, factor, err = downsample.CheckDownSampleRange(query.Step, query.DownSampleRange); ok && err != nil {
		var info *TimeInfo
		if info, err = getTimeInfo(&structured.CombinedQueryParams{
			Start: query.Start,
			End:   query.End,
			Step:  query.DownSampleRange,
		}); err == nil {
			log.Debugf(context.TODO(), "respData to downsample: %+v", info)
			respData.Downsample(factor)
		}
	}

	respData.Status = metadata.GetStatus(ctx)
	return respData, nil
}

// handleTsQueryParams: structure -> promql
// onlyParse: 仅解析structure -> promql，不将查询信息填充到ctx等
// 函数最终返回的有三种promql:
// onlyParse为true: 将结构化转promql，并将查询参数填充到ctx中，同时取消与influxdb对齐操作(取消offset)
// onlyParse为false: 将结构化转promql，同时判断是否需要请求argus
// - 如果请求argus，则将与influxdb对齐操作取消
// - 如果不请求argus(即请求influxdb)，则将与influxdb对齐操作打开
func handleTsQueryParams(ctx context.Context, queryStmt string, onlyParse bool, bizIDs []string, spaceUid string) (
	context.Context, string, *structured.CombinedQueryParams, error) {
	ctx, span := trace.IntoContext(ctx, trace.TracerName, "handle-ts-query")
	if span != nil {
		defer span.End()
	}

	query, err := structured.AnalysisQuery(queryStmt)
	if err != nil {
		log.Errorf(ctx, "anaylize combined query info failed for->[%s]", err)
		return ctx, "", nil, err
	}

	if DefaultQueryListLimit > 0 && len(query.QueryList) > DefaultQueryListLimit {
		err = fmt.Errorf("the number of query lists cannot be greater than %d", DefaultQueryListLimit)
		log.Errorf(ctx, err.Error())
		return ctx, "", nil, err
	}

	options, err := structured.GenerateOptions(query, onlyParse, bizIDs, spaceUid)
	if err != nil {
		log.Errorf(ctx, "handle ts query params err: %s", err)
		return ctx, "", nil, err
	}

	// 是否打开对齐
	if options.IsAlignInfluxdb {
		for _, innerQuery := range query.QueryList {
			innerQuery.AlignInfluxdbResult = AlignInfluxdbResult
		}
	}

	// 是否取代查询条件中的bk_biz_id
	if options.IsReplaceBizID && len(bizIDs) != 0 {
		log.Debugf(context.TODO(), "bizIDs:[%v] will replace conditions", bizIDs)
		for _, q := range query.QueryList {
			q.Conditions = *structured.ReplaceOrAddCondition(&q.Conditions, structured.BizID, bizIDs)
		}
	}

	si, _ := strconv.ParseInt(query.Start, 10, 64)
	ei, _ := strconv.ParseInt(query.End, 10, 64)
	startStr := time.Unix(si, 0).String()
	endStr := time.Unix(ei, 0).String()

	trace.InsertStringIntoSpan("start-str", startStr, span)
	trace.InsertStringIntoSpan("end-str", endStr, span)

	// 进行promql查询
	ctx, stmt, err := structured.QueryProm(ctx, query, options)
	if err != nil {
		log.Errorf(context.TODO(), "anaylize prom ql body failed for->[%s]", err)
		return ctx, "", nil, err
	}

	trace.InsertStringIntoSpan("promql-stmt", stmt, span)

	// tsquery解析后直接走flux的处理流程
	return ctx, stmt, query, nil
}
