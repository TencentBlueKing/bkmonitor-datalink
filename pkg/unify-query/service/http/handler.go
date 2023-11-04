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
	"encoding/json"
	"fmt"
	"time"
	"unsafe"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/model/labels"
	promPromql "github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/promql/parser"
	oleltrace "go.opentelemetry.io/otel/trace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/downsample"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	influxdbRouter "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/prometheus"
)

func queryExemplar(ctx context.Context, query *structured.QueryTs) (interface{}, error) {
	var (
		err  error
		span oleltrace.Span

		tablesCh = make(chan *influxdb.Tables, 1)
		recvDone = make(chan struct{})

		resp        = &PromData{}
		totalTables = influxdb.NewTables()
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "query-exemplar")
	if span != nil {
		defer span.End()
	}

	qStr, _ := json.Marshal(query)
	trace.InsertStringIntoSpan("query-ts", string(qStr), span)

	// 验证 queryList 限制长度
	if DefaultQueryListLimit > 0 && len(query.QueryList) > DefaultQueryListLimit {
		err = fmt.Errorf("the number of query lists cannot be greater than %d", DefaultQueryListLimit)
		log.Errorf(ctx, err.Error())
		return nil, err
	}

	start, end, _, timezone, err := structured.ToTime(query.Start, query.End, query.Step, query.Timezone)
	if err != nil {
		log.Errorf(ctx, err.Error())
		return nil, err
	}

	go func() {
		defer func() { recvDone <- struct{}{} }()
		var tableList []*influxdb.Tables
		for tables := range tablesCh {
			tableList = append(tableList, tables)
		}
		if len(tableList) == 0 {
			return
		}

		totalTables = influxdb.MergeTables(tableList, false)
	}()

	for _, qList := range query.QueryList {
		queryMetric, err := qList.ToQueryMetric(ctx, query.SpaceUid)
		if err != nil {
			return nil, err
		}
		for _, qry := range queryMetric.QueryList {
			qry.Timezone = timezone

			instance := prometheus.GetInstance(ctx, qry)
			if instance != nil {
				res, err := instance.QueryExemplar(ctx, qList.FieldList, qry, start, end)
				if err != nil {
					log.Errorf(ctx, "query exemplar: %s", err.Error())
					continue
				}
				if res.Err != "" {
					return nil, fmt.Errorf(res.Err)
				}
				tables := influxdb.NewTables()
				for _, result := range res.Results {
					if result.Err != "" {
						return nil, errors.New(result.Err)
					}

					for _, series := range result.Series {
						tables.Add(influxdb.NewTable(qry.Field, series, nil))
					}
				}

				if tables.Length() > 0 {
					tablesCh <- tables
				}
			}
		}
	}

	close(tablesCh)
	<-recvDone

	tables := &promql.Tables{
		Tables: make([]*promql.Table, 0, totalTables.Length()),
	}
	for _, table := range totalTables.Tables {
		tables.Add(&promql.Table{
			Name:        table.Name,
			MetricName:  table.MetricName,
			Headers:     table.Headers,
			Types:       table.Types,
			GroupKeys:   table.GroupKeys,
			GroupValues: table.GroupValues,
			Data:        table.Data,
		})
	}

	if err = resp.Fill(tables); err != nil {
		return nil, err
	}
	return resp, err
}

func queryTs(ctx context.Context, query *structured.QueryTs) (interface{}, error) {
	var (
		err  error
		span oleltrace.Span

		instance tsdb.Instance
		ok       bool

		res any

		lookBackDelta time.Duration

		promQL parser.Expr
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "query-ts")
	if span != nil {
		defer span.End()
	}

	qStr, _ := json.Marshal(query)
	trace.InsertStringIntoSpan("query-ts", string(qStr), span)

	// 验证 queryList 限制长度
	if DefaultQueryListLimit > 0 && len(query.QueryList) > DefaultQueryListLimit {
		err = fmt.Errorf("the number of query lists cannot be greater than %d", DefaultQueryListLimit)
		log.Errorf(ctx, err.Error())
		return nil, err
	}

	// 是否打开对齐
	for _, q := range query.QueryList {
		q.AlignInfluxdbResult = AlignInfluxdbResult
	}

	if query.LookBackDelta != "" {
		lookBackDelta, err = time.ParseDuration(query.LookBackDelta)
		if err != nil {
			return nil, err
		}
	}

	queryReference, err := query.ToQueryReference(ctx)
	if err != nil {
		return nil, err
	}

	start, end, step, timezone, err := structured.ToTime(query.Start, query.End, query.Step, query.Timezone)
	if err != nil {
		return nil, err
	}
	query.Timezone = timezone

	qrStr, _ := json.Marshal(queryReference)
	trace.InsertStringIntoSpan("query-reference", string(qrStr), span)

	referenceNameMetric := make(map[string]string, len(query.QueryList))
	referenceNameLabelMatcher := make(map[string][]*labels.Matcher, len(query.QueryList))

	// 判断是否是直查
	ok, vmExpand, err := queryReference.CheckVmQuery(ctx)
	if err != nil {
		log.Errorf(ctx, fmt.Sprintf("check vm query: %s", err.Error()))
	}
	if ok {
		if err != nil {
			return nil, err
		}
		if !metadata.GetVMQueryOrFeatureFlag(ctx) {
			referenceNameMetric = vmExpand.MetricAliasMapping
			referenceNameLabelMatcher = vmExpand.LabelsMatcher
		}

		metadata.SetExpand(ctx, vmExpand)
		instance = prometheus.GetInstance(ctx, &metadata.Query{
			StorageID: consul.VictoriaMetricsStorageType,
		})
		if instance == nil {
			err = fmt.Errorf("%s storage get error", consul.VictoriaMetricsStorageType)
			return nil, err
		}
	} else {
		err = metadata.SetQueryReference(ctx, queryReference)

		if err != nil {
			return nil, err
		}

		trace.InsertIntIntoSpan("query-max-routing", QueryMaxRouting, span)
		trace.InsertStringIntoSpan("singleflight-timeout", SingleflightTimeout.String(), span)

		instance = prometheus.NewInstance(ctx, promql.GlobalEngine, &prometheus.QueryRangeStorage{
			QueryMaxRouting: QueryMaxRouting,
			Timeout:         SingleflightTimeout,
		}, lookBackDelta)
	}

	promQL, err = query.ToPromExpr(ctx, referenceNameMetric, referenceNameLabelMatcher)
	if err != nil {
		return nil, err
	}

	trace.InsertStringIntoSpan("storage-type", instance.GetInstanceType(), span)

	if query.Instant {
		res, err = instance.Query(ctx, promQL.String(), end)
	} else {
		res, err = instance.QueryRange(ctx, promQL.String(), start, end, step)
	}
	if err != nil {
		return nil, err
	}

	trace.InsertStringIntoSpan("promql", promQL.String(), span)
	trace.InsertStringIntoSpan("start", start.String(), span)
	trace.InsertStringIntoSpan("end", end.String(), span)
	trace.InsertStringIntoSpan("step", step.String(), span)

	tables := promql.NewTables()
	seriesNum := 0
	pointsNum := 0

	switch v := res.(type) {
	case promPromql.Matrix:
		for index, series := range v {
			tables.Add(promql.NewTable(index, series))

			seriesNum++
			pointsNum += len(series.Points)
		}
	case promPromql.Vector:
		for index, series := range v {
			tables.Add(promql.NewTableWithSample(index, series))

			seriesNum++
			pointsNum++
		}
	default:
		err = fmt.Errorf("data type wrong: %T", v)
		return nil, err
	}

	trace.InsertIntIntoSpan("resp-series-num", seriesNum, span)
	trace.InsertIntIntoSpan("resp-points-num", pointsNum, span)

	resp := NewPromData(query.ResultColumns)
	err = resp.Fill(tables)
	if err != nil {
		return nil, err
	}

	var factor float64
	if ok, factor, err = downsample.CheckDownSampleRange(query.Step, query.DownSampleRange); ok && err != nil {
		var info *TimeInfo
		if info, err = getTimeInfo(&structured.CombinedQueryParams{
			Start: query.Start,
			End:   query.End,
			Step:  query.DownSampleRange,
		}); err == nil {
			log.Debugf(context.TODO(), "respData to downsample: %+v", info)
			resp.Downsample(factor)
		}
	}

	resp.Status = metadata.GetStatus(ctx)
	return resp, nil
}

func structToPromQL(ctx context.Context, query *structured.QueryTs) (*structured.QueryPromQL, error) {
	if query == nil {
		return nil, nil
	}

	// 是否打开对齐
	referenceNameMetric := make(map[string]string, len(query.QueryList))
	referenceNameLabelMatcher := make(map[string][]*labels.Matcher, len(query.QueryList))

	for _, q := range query.QueryList {
		// 保留查询条件
		matcher, _, err := q.Conditions.ToProm()
		if err != nil {
			return nil, err
		}
		referenceNameLabelMatcher[q.ReferenceName] = matcher

		router, err := q.ToRouter()
		if err != nil {
			return nil, err
		}
		referenceNameMetric[q.ReferenceName] = router.RealMetricName()
	}

	promQL, err := query.ToPromExpr(ctx, referenceNameMetric, referenceNameLabelMatcher)
	if err != nil {
		log.Errorf(ctx, err.Error())
		return nil, err
	}

	return &structured.QueryPromQL{
		PromQL: promQL.String(),
		Start:  query.Start,
		End:    query.End,
		Step:   query.Step,
	}, nil
}

func promQLToStruct(ctx context.Context, queryPromQL *structured.QueryPromQL) (*structured.QueryTs, error) {
	var (
		user = metadata.GetUser(ctx)
	)

	if queryPromQL == nil {
		return nil, nil
	}

	sp := structured.NewQueryPromQLExpr(queryPromQL.PromQL)
	query, err := sp.QueryTs()
	if err != nil {
		return nil, err
	}

	// metadata 中的 spaceUid 是从 header 头信息中获取
	if user.SpaceUid != "" {
		query.SpaceUid = user.SpaceUid
	}

	query.Start = queryPromQL.Start
	query.End = queryPromQL.End
	query.Step = queryPromQL.Step
	query.Timezone = queryPromQL.Timezone
	query.LookBackDelta = queryPromQL.LookBackDelta
	query.Instant = queryPromQL.Instant

	// 补充业务ID
	if len(queryPromQL.BKBizIDs) > 0 {
		for _, q := range query.QueryList {
			q.Conditions.Append(structured.ConditionField{
				DimensionName: structured.BizID,
				Value:         queryPromQL.BKBizIDs,
				Operator:      structured.Contains,
			}, structured.ConditionAnd)
		}
	}

	if queryPromQL.Match != "" {
		matchers, err := parser.ParseMetricSelector(queryPromQL.Match)
		if err != nil {
			return nil, err
		}

		if len(matchers) > 0 {
			for _, m := range matchers {
				for _, q := range query.QueryList {
					q.Conditions.Append(structured.ConditionField{
						DimensionName: m.Name,
						Value:         []string{m.Value},
						Operator:      structured.PromOperatorToConditions(m.Type),
					}, structured.ConditionAnd)
				}
			}
		}
	}

	return query, nil
}

// HandlerPromQLToStruct
// @Summary  promql to struct
// @ID       promql-to-struct
// @Produce  json
// @Param    traceparent            header    string                          false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    Bk-Query-Source   		header    string                          false  "来源" default(username:goodman)
// @Param    X-Bk-Scope-Space-Uid   header    string                          false  "空间UID" default(bkcc__2)
// @Param    data                  	body      structured.QueryPromQL  		  true   "json data"
// @Success  200                   	{object}  PromData
// @Failure  400                   	{object}  ErrResponse
// @Router   /query/ts/promql_to_struct [post]
func HandlerPromQLToStruct(c *gin.Context) {
	var (
		ctx  = c.Request.Context()
		resp = &response{
			c: c,
		}
		span oleltrace.Span
	)

	// 这里开始context就使用trace生成的了
	ctx, span = trace.IntoContext(ctx, trace.TracerName, "handle-promql-to-struct")
	if span != nil {
		defer span.End()
	}

	// 解析请求 body
	promql := &structured.QueryPromQL{}
	err := json.NewDecoder(c.Request.Body).Decode(promql)
	if err != nil {
		log.Errorf(ctx, err.Error())
		resp.failed(ctx, err)
		return
	}

	query, err := promQLToStruct(ctx, promql)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	resp.success(ctx, gin.H{"data": query})
}

// HandlerStructToPromQL
// @Summary  query struct to promql
// @ID       struct-to-promql
// @Produce  json
// @Param    traceparent            header    string                          false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    Bk-Query-Source   		header    string                          false  "来源" default(username:goodman)
// @Param    X-Bk-Scope-Space-Uid   header    string                          false  "空间UID" default(bkcc__2)
// @Param    data                  	body      structured.QueryTs  			  true   "json data"
// @Success  200                   	{object}  PromData
// @Failure  400                   	{object}  ErrResponse
// @Router   /query/ts/struct_to_promql [post]
func HandlerStructToPromQL(c *gin.Context) {
	var (
		ctx  = c.Request.Context()
		resp = &response{
			c: c,
		}
		span oleltrace.Span
	)

	// 这里开始context就使用trace生成的了
	ctx, span = trace.IntoContext(ctx, trace.TracerName, "handle-struct-to-promql")
	if span != nil {
		defer span.End()
	}

	// 解析请求 body
	query := &structured.QueryTs{}
	err := json.NewDecoder(c.Request.Body).Decode(query)
	if err != nil {
		log.Errorf(ctx, err.Error())
		resp.failed(ctx, err)
		return
	}
	promQL, err := structToPromQL(ctx, query)
	if err != nil {
		log.Errorf(ctx, err.Error())
		resp.failed(ctx, err)
		return
	}

	resp.success(ctx, promQL)
}

// HandlerQueryExemplar 查询时序 exemplar 数据
// @Summary  query monitor by ts exemplar
// @ID       ts-query-exemplar-request
// @Produce  json
// @Param    traceparent            header    string                          false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    Bk-Query-Source   		header    string                          false  "来源" default(username:goodman)
// @Param    X-Bk-Scope-Space-Uid   header    string                          false  "空间UID" default(bkcc__2)
// @Param    data                   body      structured.QueryTs  			  true   "json data"
// @Success  200                    {object}  PromData
// @Failure  400                    {object}  ErrResponse
// @Router   /query/ts/exemplar [post]
func HandlerQueryExemplar(c *gin.Context) {
	var (
		ctx  = c.Request.Context()
		span oleltrace.Span
		resp = &response{
			c: c,
		}
		user = metadata.GetUser(ctx)
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "handler-query-exemplar")
	if span != nil {
		defer span.End()
	}

	trace.InsertStringIntoSpan("request-url", c.Request.URL.String(), span)
	trace.InsertStringIntoSpan("request-header", fmt.Sprintf("%+v", c.Request.Header), span)

	trace.InsertStringIntoSpan("query-source", user.Key, span)
	trace.InsertStringIntoSpan("query-space-uid", user.SpaceUid, span)

	// 解析请求 body
	query := &structured.QueryTs{}
	err := json.NewDecoder(c.Request.Body).Decode(query)
	if err != nil {
		log.Errorf(ctx, err.Error())
		resp.failed(ctx, err)
		return
	}

	// metadata 中的 spaceUid 是从 header 头信息中获取
	if user.SpaceUid != "" {
		query.SpaceUid = user.SpaceUid
	}

	queryStr, _ := json.Marshal(query)
	trace.InsertStringIntoSpan("query-body", string(queryStr), span)

	res, err := queryExemplar(ctx, query)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	trace.InsertStringIntoSpan("resp-size", fmt.Sprint(unsafe.Sizeof(res)), span)
	resp.success(ctx, res)
}

// HandlerQueryTs
// @Summary  query monitor by ts
// @ID       ts-query-request
// @Produce  json
// @Param    traceparent            header    string                          false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    Bk-Query-Source   		header    string                          false  "来源" default(username:goodman)
// @Param    X-Bk-Scope-Space-Uid   header    string                          false  "空间UID" default(bkcc__2)
// @Param    data                  	body      structured.QueryTs  			  true   "json data"
// @Success  200                   	{object}  PromData
// @Failure  400                   	{object}  ErrResponse
// @Router   /query/ts [post]
func HandlerQueryTs(c *gin.Context) {
	var (
		ctx  = c.Request.Context()
		span oleltrace.Span
		resp = &response{
			c: c,
		}
		user = metadata.GetUser(ctx)
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "handler-query-ts")
	if span != nil {
		defer span.End()
	}

	trace.InsertStringIntoSpan("request-url", c.Request.URL.String(), span)
	trace.InsertStringIntoSpan("request-header", fmt.Sprintf("%+v", c.Request.Header), span)

	trace.InsertStringIntoSpan("query-source", user.Key, span)
	trace.InsertStringIntoSpan("query-space-uid", user.SpaceUid, span)

	// 解析请求 body
	query := &structured.QueryTs{}
	err := json.NewDecoder(c.Request.Body).Decode(query)
	if err != nil {
		log.Errorf(ctx, err.Error())
		resp.failed(ctx, err)
		return
	}

	// metadata 中的 spaceUid 是从 header 头信息中获取
	if user.SpaceUid != "" {
		query.SpaceUid = user.SpaceUid
	}

	queryStr, _ := json.Marshal(query)
	trace.InsertStringIntoSpan("query-body", string(queryStr), span)
	trace.InsertIntIntoSpan("query-body-size", len(queryStr), span)

	res, err := queryTs(ctx, query)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	trace.InsertStringIntoSpan("resp-size", fmt.Sprint(unsafe.Sizeof(res)), span)

	resp.success(ctx, res)
}

// HandlerQueryPromQL
// @Summary  query monitor by promql
// @ID       ts-query-request-promql
// @Produce  json
// @Param    traceparent            header    string                          false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    Bk-Query-Source   		header    string                          false  "来源" default(username:goodman)
// @Param    X-Bk-Scope-Space-Uid   header    string                          false  "空间UID" default(bkcc__2)
// @Param    data                  	body      structured.QueryPromQL  		  true   "json data"
// @Success  200                   	{object}  PromData
// @Failure  400                   	{object}  ErrResponse
// @Router   /query/ts/promql [post]
func HandlerQueryPromQL(c *gin.Context) {
	var (
		ctx  = c.Request.Context()
		span oleltrace.Span
		resp = &response{
			c: c,
		}
		user = metadata.GetUser(ctx)
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "handler-query-promql")
	if span != nil {
		defer span.End()
	}

	trace.InsertStringIntoSpan("headers", fmt.Sprintf("%+v", c.Request.Header), span)
	trace.InsertStringIntoSpan("query-source", user.Key, span)
	trace.InsertStringIntoSpan("query-space-uid", user.SpaceUid, span)

	// 解析请求 body
	queryPromQL := &structured.QueryPromQL{}
	err := json.NewDecoder(c.Request.Body).Decode(queryPromQL)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	queryStr, _ := json.Marshal(queryPromQL)
	trace.InsertStringIntoSpan("query-body", string(queryStr), span)
	trace.InsertStringIntoSpan("query-promql", queryPromQL.PromQL, span)

	if queryPromQL.PromQL == "" {
		resp.failed(ctx, fmt.Errorf("promql is empty"))
		return
	}

	// promql to struct
	query, err := promQLToStruct(ctx, queryPromQL)
	if err != nil {
		log.Errorf(ctx, err.Error())
		resp.failed(ctx, err)
		return
	}

	res, err := queryTs(ctx, query)
	if err != nil {
		log.Errorf(ctx, err.Error())
		resp.failed(ctx, err)
		return
	}
	resp.success(ctx, res)
}

// HandleInfluxDBPrint  打印 InfluxDB 路由信息
func HandleInfluxDBPrint(c *gin.Context) {
	ctx := c.Request.Context()
	refresh := c.Query("refresh")

	res := influxdbRouter.GetInfluxDBRouter().Print(ctx, refresh != "")
	c.String(200, res)
}
