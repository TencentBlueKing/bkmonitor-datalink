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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/redis"
)

func queryExemplar(ctx context.Context, query *structured.QueryTs) (interface{}, error) {
	var (
		err error

		tablesCh = make(chan *influxdb.Tables, 1)
		recvDone = make(chan struct{})

		resp        = &PromData{}
		totalTables = influxdb.NewTables()
	)

	ctx, span := trace.NewSpan(ctx, "query-exemplar")
	defer span.End(&err)

	qStr, _ := json.Marshal(query)
	span.Set("query-ts", string(qStr))

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
		err error

		instance tsdb.Instance
		ok       bool

		res any

		lookBackDelta time.Duration

		promQL parser.Expr

		promExprOpt = &structured.PromExprOption{}
	)

	ctx, span := trace.NewSpan(ctx, "query-ts")
	defer span.End(&err)

	qStr, _ := json.Marshal(query)
	span.Set("query-ts", string(qStr))

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

	// 写入查询缓存
	metadata.SetQueryParams(ctx, &metadata.QueryParams{
		Start: start.Unix(),
		End:   end.Unix(),
	})

	// 判断是否是直查
	ok, vmExpand, err := queryReference.CheckVmQuery(ctx)
	if err != nil {
		log.Errorf(ctx, fmt.Sprintf("check vm query: %s", err.Error()))
	}
	if ok {
		// vm 跟 prom 的函数有差异，需要转换一下以完全适配 prometheus。
		// https://docs.victoriametrics.com/metricsql/#delta
		promExprOpt.FunctionReplace = map[string]string{
			"increase": "increase_prometheus",
			"delta":    "delta_prometheus",
			"changes":  "changes_prometheus",
		}

		if err != nil {
			return nil, err
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

		span.Set("query-max-routing", QueryMaxRouting)
		span.Set("singleflight-timeout", SingleflightTimeout.String())

		instance = prometheus.NewInstance(ctx, promql.GlobalEngine, &prometheus.QueryRangeStorage{
			QueryMaxRouting: QueryMaxRouting,
			Timeout:         SingleflightTimeout,
		}, lookBackDelta)
	}

	promQL, err = query.ToPromExpr(ctx, promExprOpt)
	if err != nil {
		return nil, err
	}

	span.Set("storage-type", instance.GetInstanceType())

	if query.Instant {
		res, err = instance.Query(ctx, promQL.String(), end)
	} else {
		res, err = instance.QueryRange(ctx, promQL.String(), start, end, step)
	}
	if err != nil {
		return nil, err
	}

	span.Set("promql", promQL.String())
	span.Set("start", start.String())
	span.Set("end", end.String())
	span.Set("step", step.String())

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

	span.Set("resp-series-num", seriesNum)
	span.Set("resp-points-num", pointsNum)

	resp := NewPromData(query.ResultColumns)
	err = resp.Fill(tables)
	if err != nil {
		return nil, err
	}

	var (
		factor          float64
		downSampleError error
	)
	if ok, factor, downSampleError = downsample.CheckDownSampleRange(query.Step, query.DownSampleRange); ok {
		if downSampleError == nil {
			var info *TimeInfo
			if info, downSampleError = getTimeInfo(&structured.CombinedQueryParams{
				Start: query.Start,
				End:   query.End,
				Step:  query.DownSampleRange,
			}); downSampleError == nil {
				log.Debugf(context.TODO(), "respData to down sample: %+v", info)
				resp.Downsample(factor)
			}
		}
	}

	resp.Status = metadata.GetStatus(ctx)
	return resp, err
}

func structToPromQL(ctx context.Context, query *structured.QueryTs) (*structured.QueryPromQL, error) {
	if query == nil {
		return nil, nil
	}

	promExprOpt := &structured.PromExprOption{}

	promExprOpt.ReferenceNameMetric = make(map[string]string, len(query.QueryList))
	promExprOpt.ReferenceNameLabelMatcher = make(map[string][]*labels.Matcher, len(query.QueryList))

	for _, q := range query.QueryList {
		// 保留查询条件
		matcher, _, err := q.Conditions.ToProm()
		if err != nil {
			return nil, err
		}
		promExprOpt.ReferenceNameLabelMatcher[q.ReferenceName] = matcher

		router, err := q.ToRouter()
		if err != nil {
			return nil, err
		}
		promExprOpt.ReferenceNameMetric[q.ReferenceName] = router.RealMetricName()
	}

	promQL, err := query.ToPromExpr(ctx, promExprOpt)
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
	query.DownSampleRange = queryPromQL.DownSampleRange

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
// @ID       transform_promql_to_struct
// @Produce  json
// @Param    traceparent            header    string                          false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    Bk-Query-Source   		header    string                          false  "来源" default(username:goodman)
// @Param    X-Bk-Scope-Space-Uid   header    string                          false  "空间UID" default(bkcc__2)
// @Param    data                  	body      structured.QueryPromQL  		  true   "json data"
// @Success  200                   	{object}  structured.QueryTs
// @Failure  400                   	{object}  ErrResponse
// @Router   /query/ts/promql_to_struct [post]
func HandlerPromQLToStruct(c *gin.Context) {
	var (
		ctx  = c.Request.Context()
		resp = &response{
			c: c,
		}

		err error
	)

	// 这里开始context就使用trace生成的了
	ctx, span := trace.NewSpan(ctx, "handle-promql-to-struct")
	defer span.End(&err)

	// 解析请求 body
	promQL := &structured.QueryPromQL{}
	err = json.NewDecoder(c.Request.Body).Decode(promQL)
	if err != nil {
		log.Errorf(ctx, err.Error())
		resp.failed(ctx, err)
		return
	}

	promQLStr, _ := json.Marshal(promQL)
	span.Set("promql-body", string(promQLStr))

	query, err := promQLToStruct(ctx, promQL)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	queryStr, _ := json.Marshal(query)
	span.Set("query-body", string(queryStr))

	resp.success(ctx, gin.H{"data": query})
}

// HandlerStructToPromQL
// @Summary  query struct to promql
// @ID       transform_struct_to_promql
// @Produce  json
// @Param    traceparent            header    string                          false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    Bk-Query-Source   		header    string                          false  "来源" default(username:goodman)
// @Param    X-Bk-Scope-Space-Uid   header    string                          false  "空间UID" default(bkcc__2)
// @Param    data                  	body      structured.QueryTs  			  true   "json data"
// @Success  200                   	{object}  structured.QueryPromQL
// @Failure  400                   	{object}  ErrResponse
// @Router   /query/ts/struct_to_promql [post]
func HandlerStructToPromQL(c *gin.Context) {
	var (
		ctx  = c.Request.Context()
		resp = &response{
			c: c,
		}

		err error
	)

	// 这里开始context就使用trace生成的了
	ctx, span := trace.NewSpan(ctx, "handle-struct-to-promql")
	defer span.End(&err)

	// 解析请求 body
	query := &structured.QueryTs{}
	err = json.NewDecoder(c.Request.Body).Decode(query)
	if err != nil {
		log.Errorf(ctx, err.Error())
		resp.failed(ctx, err)
		return
	}

	queryStr, _ := json.Marshal(query)
	span.Set("query-body", string(queryStr))

	promQL, err := structToPromQL(ctx, query)
	if err != nil {
		log.Errorf(ctx, err.Error())
		resp.failed(ctx, err)
		return
	}

	promQLStr, _ := json.Marshal(promQL)
	span.Set("promql-body", string(promQLStr))

	resp.success(ctx, promQL)
}

// HandlerQueryExemplar 查询时序 exemplar 数据
// @Summary  query monitor by ts exemplar
// @ID       query_ts_exemplar
// @Produce  json
// @Param    traceparent            header    string                        false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    Bk-Query-Source   		header    string                        false  "来源" default(username:goodman)
// @Param    X-Bk-Scope-Space-Uid   header    string                        false  "空间UID" default(bkcc__2)
// @Param	 X-Bk-Scope-Skip-Space  header	  string						false  "是否跳过空间验证" default()
// @Param    data                   body      structured.QueryTs  			true   "json data"
// @Success  200                    {object}  PromData
// @Failure  400                    {object}  ErrResponse
// @Router   /query/ts/exemplar [post]
func HandlerQueryExemplar(c *gin.Context) {
	var (
		ctx = c.Request.Context()

		resp = &response{
			c: c,
		}
		user = metadata.GetUser(ctx)

		err error
	)

	ctx, span := trace.NewSpan(ctx, "handler-query-exemplar")
	defer span.End(&err)

	span.Set("request-url", c.Request.URL.String())
	span.Set("request-header", fmt.Sprintf("%+v", c.Request.Header))

	span.Set("query-source", user.Key)
	span.Set("query-space-uid", user.SpaceUid)

	// 解析请求 body
	query := &structured.QueryTs{}
	err = json.NewDecoder(c.Request.Body).Decode(query)
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
	span.Set("query-body", string(queryStr))

	log.Infof(ctx, fmt.Sprintf("header: %+v, body: %s", c.Request.Header, queryStr))

	res, err := queryExemplar(ctx, query)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	span.Set("resp-size", fmt.Sprint(unsafe.Sizeof(res)))
	resp.success(ctx, res)
}

// HandlerQueryTs
// @Summary  query monitor by ts
// @ID       query_ts
// @Produce  json
// @Param    traceparent            header    string                        false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    Bk-Query-Source   		header    string                        false  "来源" default(username:goodman)
// @Param    X-Bk-Scope-Space-Uid   header    string                        false  "空间UID" default(bkcc__2)
// @Param	 X-Bk-Scope-Skip-Space  header	  string						false  "是否跳过空间验证" default()
// @Param    data                  	body      structured.QueryTs  			true   "json data"
// @Success  200                   	{object}  PromData
// @Failure  400                   	{object}  ErrResponse
// @Router   /query/ts [post]
func HandlerQueryTs(c *gin.Context) {
	var (
		ctx = c.Request.Context()

		resp = &response{
			c: c,
		}
		user = metadata.GetUser(ctx)

		err error
	)

	ctx, span := trace.NewSpan(ctx, "handler-query-ts")
	defer span.End(&err)

	span.Set("request-url", c.Request.URL.String())
	span.Set("request-header", fmt.Sprintf("%+v", c.Request.Header))

	span.Set("query-source", user.Key)
	span.Set("query-space-uid", user.SpaceUid)

	// 解析请求 body
	query := &structured.QueryTs{}
	err = json.NewDecoder(c.Request.Body).Decode(query)
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
	span.Set("query-body", string(queryStr))
	span.Set("query-body-size", len(queryStr))

	log.Infof(ctx, fmt.Sprintf("header: %+v, body: %s", c.Request.Header, queryStr))

	res, err := queryTs(ctx, query)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	span.Set("resp-size", fmt.Sprint(unsafe.Sizeof(res)))

	resp.success(ctx, res)
}

// HandlerQueryPromQL
// @Summary  query monitor by promql
// @ID       query_promql
// @Produce  json
// @Param    traceparent            header    string                        false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    Bk-Query-Source   		header    string                        false  "来源" default(username:goodman)
// @Param    X-Bk-Scope-Space-Uid   header    string                        false  "空间UID" default(bkcc__2)
// @Param	 X-Bk-Scope-Skip-Space  header	  string						false  "是否跳过空间验证" default()
// @Param    data                  	body      structured.QueryPromQL  		true   "json data"
// @Success  200                   	{object}  PromData
// @Failure  400                   	{object}  ErrResponse
// @Router   /query/promql [post]
func HandlerQueryPromQL(c *gin.Context) {
	var (
		ctx = c.Request.Context()

		resp = &response{
			c: c,
		}
		user = metadata.GetUser(ctx)

		err error
	)

	ctx, span := trace.NewSpan(ctx, "handler-query-promql")
	defer span.End(&err)

	span.Set("headers", fmt.Sprintf("%+v", c.Request.Header))
	span.Set("query-source", user.Key)
	span.Set("query-space-uid", user.SpaceUid)

	// 解析请求 body
	queryPromQL := &structured.QueryPromQL{}
	err = json.NewDecoder(c.Request.Body).Decode(queryPromQL)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	queryStr, _ := json.Marshal(queryPromQL)
	span.Set("query-body", string(queryStr))
	span.Set("query-promql", queryPromQL.PromQL)

	log.Infof(ctx, fmt.Sprintf("header: %+v, body: %s", c.Request.Header, queryStr))

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

func HandlerQueryTsClusterMetrics(c *gin.Context) {
	var (
		ctx = c.Request.Context()

		resp = &response{c: c}

		err error
	)
	ctx, span := trace.NewSpan(ctx, "handler-query-ts-cluster-metrics")
	defer span.End(&err)

	span.Set("request-url", c.Request.URL.String())
	span.Set("request-header", fmt.Sprintf("%+v", c.Request.Header))
	query := &structured.QueryTs{}
	err = json.NewDecoder(c.Request.Body).Decode(query)
	if err != nil {
		resp.failed(ctx, err)
		return
	}
	queryStr, _ := json.Marshal(query)

	log.Infof(ctx, fmt.Sprintf("header: %+v, body: %s", c.Request.Header, queryStr))

	span.Set("query-body", string(queryStr))
	res, err := QueryTsClusterMetrics(ctx, query)
	if err != nil {
		resp.failed(ctx, err)
		return
	}
	resp.success(ctx, res)
}

func QueryTsClusterMetrics(ctx context.Context, query *structured.QueryTs) (interface{}, error) {
	var (
		err error
		res any
	)
	ctx, span := trace.NewSpan(ctx, "query-ts-cluster-metrics")
	defer span.End(&err)
	start, end, step, timezone, err := structured.ToTime(query.Start, query.End, query.Step, query.Timezone)
	if err != nil {
		return nil, err
	}
	query.Timezone = timezone
	queryCM, err := query.ToQueryClusterMetric(ctx)
	if err != nil {
		return nil, err
	}
	err = metadata.SetQueryClusterMetric(ctx, queryCM)
	if err != nil {
		return nil, err
	}
	instance := redis.Instance{Ctx: ctx, Timeout: ClusterMetricQueryTimeout, ClusterMetricPrefix: ClusterMetricQueryPrefix}
	if query.Instant {
		res, err = instance.Query(ctx, "", end)
	} else {
		res, err = instance.QueryRange(ctx, "", start, end, step)
	}
	if err != nil {
		return nil, err
	}

	span.Set("start", start.String())
	span.Set("end", end.String())
	span.Set("step", step.String())
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

	span.Set("resp-series-num", seriesNum)
	span.Set("resp-points-num", pointsNum)

	resp := NewPromData(query.ResultColumns)
	err = resp.Fill(tables)
	if err != nil {
		return nil, err
	}
	return resp, nil
}
