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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/panjf2000/ants/v2"
	"github.com/pkg/errors"
	"github.com/prometheus/common/model"
	promPromql "github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/promql/parser"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/downsample"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
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

		resp        = NewPromData(query.ResultColumns)
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

	ref, err := query.ToQueryReference(ctx)
	if err != nil {
		return nil, err
	}
	ok, _, err := ref.CheckVmQuery(ctx)
	// 如果查询 vm 的情况下则直接退出，因为 vm 不支持 Exemplar 数据
	if ok {
		return resp, nil
	}

	for _, qList := range query.QueryList {
		queryMetric, err := qList.ToQueryMetric(ctx, query.SpaceUid)
		if err != nil {
			return nil, err
		}
		for _, qry := range queryMetric.QueryList {
			qry.Timezone = timezone

			instance := prometheus.GetTsDbInstance(ctx, qry)
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

func queryRawWithInstance(ctx context.Context, queryTs *structured.QueryTs) (total int64, list []map[string]any, err error) {
	ctx, span := trace.NewSpan(ctx, "query-raw-with-instance")
	defer span.End(&err)

	start, end, timeErr := queryTs.GetTime()
	if timeErr != nil {
		err = timeErr
		return
	}

	var (
		receiveWg sync.WaitGroup
		dataCh    = make(chan map[string]any)

		message strings.Builder
		lock    sync.Mutex
	)

	receiveWg.Add(1)
	list = make([]map[string]any, 0)
	// 启动合并数据
	go func() {
		defer receiveWg.Done()
		for d := range dataCh {
			list = append(list, d)
		}
	}()

	// 多协程查询数据
	var sendWg sync.WaitGroup
	p, _ := ants.NewPool(QueryMaxRouting)
	go func() {
		defer func() {
			sendWg.Wait()
			close(dataCh)
		}()

		for _, ql := range queryTs.QueryList {
			// 时间复用
			ql.Timezone = queryTs.Timezone
			ql.Start = queryTs.Start
			ql.End = queryTs.End

			// 排序复用
			ql.OrderBy = queryTs.OrderBy

			// 如果 qry.Step 不存在去外部统一的 step
			if ql.Step == "" {
				ql.Step = queryTs.Step
			}
			qm, qmErr := ql.ToQueryMetric(ctx, queryTs.SpaceUid)
			if qmErr != nil {
				err = qmErr
				return
			}

			for _, qry := range qm.QueryList {
				sendWg.Add(1)
				qry := qry
				err = p.Submit(func() {
					defer func() {
						sendWg.Done()
					}()

					instance := prometheus.GetTsDbInstance(ctx, qry)
					if instance == nil {
						log.Warnf(ctx, "not instance in %s", qry.StorageID)
						return
					}

					size, queryErr := instance.QueryRawData(ctx, qry, start, end, dataCh)
					if queryErr != nil {
						lock.Lock()
						message.WriteString(fmt.Sprintf("query %s:%s is error: %s ", qry.TableID, qry.Fields, queryErr.Error()))
						lock.Unlock()
					}
					atomic.AddInt64(&total, size)
				})
			}
		}
	}()

	// 等待数据组装完毕
	receiveWg.Wait()
	if message.Len() > 0 {
		err = errors.New(message.String())
	}

	return
}

func queryReferenceWithPromEngine(ctx context.Context, query *structured.QueryTs) (*PromData, error) {
	var (
		res  any
		err  error
		resp = NewPromData(query.ResultColumns)
	)

	ctx, span := trace.NewSpan(ctx, "query-reference")
	defer func() {
		resp.Status = metadata.GetStatus(ctx)
		span.End(&err)
	}()

	qStr, _ := json.Marshal(query)
	span.Set("query-ts", string(qStr))

	for _, q := range query.QueryList {
		q.IsReference = true
		if q.TableID == "" {
			err = fmt.Errorf("tableID is empty")
			return nil, err
		}
	}

	// 判断如果 step 为空，则补充默认 step
	if query.Step == "" {
		query.Step = promql.GetDefaultStep().String()
	}

	queryRef, err := query.ToQueryReference(ctx)
	startInt, err := strconv.ParseInt(query.Start, 10, 64)
	if err != nil {
		return nil, err
	}
	start := time.Unix(startInt, 0)

	endInt, err := strconv.ParseInt(query.End, 10, 64)
	if err != nil {
		return nil, err
	}
	end := time.Unix(endInt, 0)
	step, err := model.ParseDuration(query.Step)
	if err != nil {
		return nil, err
	}

	// es 需要使用自己的查询时间范围
	metadata.GetQueryParams(ctx).SetTime(start.Unix(), end.Unix()).SetIsReference(true)
	metadata.SetQueryReference(ctx, queryRef)

	var lookBackDelta time.Duration
	if query.LookBackDelta != "" {
		lookBackDelta, err = time.ParseDuration(query.LookBackDelta)
		if err != nil {
			return nil, err
		}
	}

	instance := prometheus.NewInstance(ctx, promql.GlobalEngine, &prometheus.QueryRangeStorage{
		QueryMaxRouting: QueryMaxRouting,
		Timeout:         SingleflightTimeout,
	}, lookBackDelta)

	if query.Instant {
		res, err = instance.Query(ctx, query.MetricMerge, start)
	} else {
		res, err = instance.QueryRange(ctx, query.MetricMerge, start, end, time.Duration(step))
	}
	if err != nil {
		return nil, err
	}

	tables := promql.NewTables()
	seriesNum := 0
	pointsNum := 0

	decodeFunc := metadata.GetPromDataFormat(ctx).DecodeFunc()

	switch v := res.(type) {
	case promPromql.Matrix:
		for index, series := range v {
			tables.Add(promql.NewTable(index, series, decodeFunc))

			seriesNum++
			pointsNum += len(series.Points)
		}
	case promPromql.Vector:
		for index, series := range v {
			// 层级需要转换
			tables.Add(promql.NewTableWithSample(index, series, decodeFunc))

			seriesNum++
			pointsNum++
		}
	default:
		err = fmt.Errorf("data type wrong: %T", v)
		return nil, err
	}

	span.Set("resp-series-num", seriesNum)
	span.Set("resp-points-num", pointsNum)

	err = resp.Fill(tables)
	if err != nil {
		return nil, err
	}

	return resp, err
}

func queryTsWithPromEngine(ctx context.Context, query *structured.QueryTs) (interface{}, error) {
	var (
		err error

		instance tsdb.Instance
		ok       bool

		res any

		lookBackDelta time.Duration

		promQL parser.Expr

		promExprOpt = &structured.PromExprOption{}

		resp = NewPromData(query.ResultColumns)
	)

	ctx, span := trace.NewSpan(ctx, "query-ts")
	defer func() {
		resp.Status = metadata.GetStatus(ctx)
		span.End(&err)
	}()

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
		q.IsReference = false
		q.AlignInfluxdbResult = AlignInfluxdbResult
	}

	if query.LookBackDelta != "" {
		lookBackDelta, err = time.ParseDuration(query.LookBackDelta)
		if err != nil {
			return nil, err
		}
	}
	// 判断如果 step 为空，则补充默认 step
	if query.Step == "" {
		query.Step = promql.GetDefaultStep().String()
	}

	queryRef, err := query.ToQueryReference(ctx)
	if err != nil {
		return nil, err
	}

	start, end, step, timezone, err := structured.ToTime(query.Start, query.End, query.Step, query.Timezone)
	if err != nil {
		return nil, err
	}
	query.Timezone = timezone

	// 写入查询时间到全局缓存
	metadata.GetQueryParams(ctx).SetTime(start.Unix(), end.Unix())

	// 判断是否是直查
	ok, vmExpand, err := queryRef.CheckVmQuery(ctx)
	if err != nil {
		log.Errorf(ctx, fmt.Sprintf("check vm query: %s", err.Error()))
	}
	if ok {
		if len(vmExpand.ResultTableList) == 0 {
			return resp, nil
		}

		// 函数替换逻辑有问题、暂时屏蔽
		// vm 跟 prom 的函数有差异，需要转换一下以完全适配 prometheus。
		// https://docs.victoriametrics.com/metricsql/#delta
		//promExprOpt.FunctionReplace = map[string]string{
		//	"increase": "increase_prometheus",
		//	"delta":    "delta_prometheus",
		//	"changes":  "changes_prometheus",
		//}
		//if err != nil {
		//	return nil, err
		//}
		metadata.SetExpand(ctx, vmExpand)
		instance = prometheus.GetTsDbInstance(ctx, &metadata.Query{
			// 兼容 storage 结构体，用于单元测试
			StorageID:   consul.VictoriaMetricsStorageType,
			StorageType: consul.VictoriaMetricsStorageType,
		})
		if instance == nil {
			err = fmt.Errorf("%s storage get error", consul.VictoriaMetricsStorageType)
			return nil, err
		}
	} else {
		// 非直查开启忽略时间聚合函数判断
		promExprOpt.IgnoreTimeAggregationEnable = true

		metadata.SetQueryReference(ctx, queryRef)
		span.Set("query-max-routing", QueryMaxRouting)
		span.Set("singleflight-timeout", SingleflightTimeout.String())

		instance = prometheus.NewInstance(ctx, promql.GlobalEngine, &prometheus.QueryRangeStorage{
			QueryMaxRouting: QueryMaxRouting,
			Timeout:         SingleflightTimeout,
		}, lookBackDelta)
	}

	// sum(count_over_time(a[1m])) => a
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

	decodeFunc := metadata.GetPromDataFormat(ctx).DecodeFunc()

	switch v := res.(type) {
	case promPromql.Matrix:
		for index, series := range v {
			tables.Add(promql.NewTable(index, series, decodeFunc))

			seriesNum++
			pointsNum += len(series.Points)
		}
	case promPromql.Vector:
		for index, series := range v {
			// 层级需要转换
			tables.Add(promql.NewTableWithSample(index, series, decodeFunc))

			seriesNum++
			pointsNum++
		}
	default:
		err = fmt.Errorf("data type wrong: %T", v)
		return nil, err
	}

	span.Set("resp-series-num", seriesNum)
	span.Set("resp-points-num", pointsNum)

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

	return resp, err
}

func structToPromQL(ctx context.Context, query *structured.QueryTs) (*structured.QueryPromQL, error) {
	if query == nil {
		return nil, nil
	}

	promQL, err := query.ToPromQL(ctx)
	if err != nil {
		log.Errorf(ctx, err.Error())
		return nil, err
	}

	return &structured.QueryPromQL{
		PromQL: promQL,
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

	decodeFunc := metadata.GetPromDataFormat(ctx).DecodeFunc()

	switch v := res.(type) {
	case promPromql.Matrix:
		for index, series := range v {
			tables.Add(promql.NewTable(index, series, decodeFunc))
			seriesNum++
			pointsNum += len(series.Points)
		}
	case promPromql.Vector:
		for index, series := range v {
			tables.Add(promql.NewTableWithSample(index, series, decodeFunc))
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
