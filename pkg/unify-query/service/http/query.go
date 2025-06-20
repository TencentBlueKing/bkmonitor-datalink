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
	"strings"
	"sync"
	"time"

	ants "github.com/panjf2000/ants/v2"
	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/model/labels"
	promPromql "github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/promql/parser"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/downsample"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/elasticsearch"
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

	_, startTime, endTime, err := function.QueryTimestamp(query.Start, query.End)
	if err != nil {
		log.Errorf(ctx, err.Error())
		return nil, err
	}

	start, end, _, timezone, err := structured.AlignTime(startTime, endTime, query.Step, query.Timezone)
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

	_, err = query.ToQueryReference(ctx)
	if err != nil {
		return nil, err
	}
	// 如果查询 vm 的情况下则直接退出，因为 vm 不支持 Exemplar 数据
	if metadata.GetQueryParams(ctx).IsDirectQuery() {
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

func queryRawWithInstance(ctx context.Context, queryTs *structured.QueryTs) (total int64, list []map[string]any, resultTableOptions metadata.ResultTableOptions, err error) {
	ignoreDimensions := []string{elasticsearch.KeyAddress}

	ctx, span := trace.NewSpan(ctx, "query-raw-with-instance")
	defer span.End(&err)

	unit, start, end, timeErr := function.QueryTimestamp(queryTs.Start, queryTs.End)
	if timeErr != nil {
		err = timeErr
		return
	}
	metadata.GetQueryParams(ctx).SetTime(start, end, unit)

	var (
		receiveWg sync.WaitGroup
		dataCh    = make(chan map[string]any)

		message   strings.Builder
		queryList []*metadata.Query
		lock      sync.Mutex
	)

	list = make([]map[string]any, 0)

	// 构建查询路由列表
	if queryTs.SpaceUid == "" {
		queryTs.SpaceUid = metadata.GetUser(ctx).SpaceUID
	}
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

		if queryTs.ResultTableOptions != nil {
			ql.ResultTableOptions = queryTs.ResultTableOptions
		}

		// 如果 Limit / From 没有单独指定的话，同时外部指定了的话，使用外部的
		if ql.Limit == 0 && queryTs.Limit > 0 {
			ql.Limit = queryTs.Limit
		}

		// 在使用 multiFrom 模式下，From 需要保持为 0，因为 from 存放在 resultTableOptions 里面
		if queryTs.IsMultiFrom {
			queryTs.From = 0
		}

		if ql.From == 0 && queryTs.From > 0 {
			ql.From = queryTs.From
		}

		// 复用 scroll 配置，如果配置了 scroll 优先使用 scroll
		if queryTs.Scroll != "" {
			ql.Scroll = queryTs.Scroll
			queryTs.IsMultiFrom = false
		}

		// 复用字段配置，没有特殊配置的情况下使用公共配置
		if len(ql.KeepColumns) == 0 && len(queryTs.ResultColumns) != 0 {
			ql.KeepColumns = queryTs.ResultColumns
		}

		qm, qmErr := ql.ToQueryMetric(ctx, queryTs.SpaceUid)
		if qmErr != nil {
			err = qmErr
			return
		}

		for _, qry := range qm.QueryList {
			if qry != nil {
				queryList = append(queryList, qry)
			}
		}
	}

	receiveWg.Add(1)

	// 启动合并数据
	go func() {
		defer receiveWg.Done()

		var data []map[string]any
		for d := range dataCh {
			data = append(data, d)
		}

		span.Set("query-list-num", len(queryList))
		span.Set("result-data-num", len(data))

		if len(queryList) > 1 {
			queryTs.OrderBy.Orders().SortSliceList(data)

			span.Set("query-scroll", queryTs.Scroll)
			span.Set("query-result-table", queryTs.ResultTableOptions)

			// scroll 和 searchAfter 模式不进行裁剪
			if queryTs.Scroll == "" && queryTs.ResultTableOptions.IsCrop() {
				// 判定是否启用 multi from 特性
				span.Set("query-multi-from", queryTs.IsMultiFrom)
				span.Set("data-length", len(data))
				span.Set("query-ts-from", queryTs.From)
				span.Set("query-ts-limit", queryTs.Limit)

				if len(data) > queryTs.Limit {
					if queryTs.IsMultiFrom {
						resultTableOptions = queryTs.ResultTableOptions
						if resultTableOptions == nil {
							resultTableOptions = make(metadata.ResultTableOptions)
						}

						data = data[0:queryTs.Limit]
						for _, l := range data {
							tableID := l[elasticsearch.KeyTableID].(string)
							address := l[elasticsearch.KeyAddress].(string)

							option := resultTableOptions.GetOption(tableID, address)
							if option == nil {
								resultTableOptions.SetOption(tableID, address, &metadata.ResultTableOption{From: function.IntPoint(1)})
							} else {
								*option.From++
							}
						}
					} else {
						data = data[queryTs.From : queryTs.From+queryTs.Limit]
					}
				}
			}
		}

		labelMap, lbErr := queryTs.LabelMap()
		if lbErr != nil {
			err = lbErr
			return
		}

		span.Set("query-label-map", labelMap)
		span.Set("query-highlight", queryTs.HighLight)
		skipHighlight := false
		lmf, exist := function.LabelMapFactory(ctx)
		if !exist {
			log.Warnf(ctx, "label map factory not found in context")
			skipHighlight = true
		}
		for _, item := range data {
			if item == nil {
				continue
			}

			for _, ignoreDimension := range ignoreDimensions {
				delete(item, ignoreDimension)
			}

			if !skipHighlight && lmf != nil {
				var maxAnalyzedOffset int
				if queryTs.HighLight != nil && queryTs.HighLight.MaxAnalyzedOffset > 0 {
					maxAnalyzedOffset = queryTs.HighLight.MaxAnalyzedOffset
				}
				if highlightResult := lmf.ProcessHighlight(item, maxAnalyzedOffset); len(highlightResult) > 0 {
					item[function.KeyHighLight] = highlightResult
				}
			}

			list = append(list, item)
		}

		span.Set("result-list-num", len(list))
		span.Set("result-option", resultTableOptions)
	}()

	// 多协程查询数据
	var (
		sendWg sync.WaitGroup
	)

	p, _ := ants.NewPool(QueryMaxRouting)
	defer p.Release()

	go func() {
		defer func() {
			sendWg.Wait()
			close(dataCh)
		}()
		for _, qry := range queryList {
			sendWg.Add(1)
			qry := qry

			// 如果是多数据合并，为了保证排序和Limit 的准确性，需要查询原始的所有数据，所以这里对 from 和 size 进行重写
			if len(queryList) > 1 {
				if !queryTs.IsMultiFrom {
					qry.Size += qry.From
					qry.From = 0
				}
			}

			err = p.Submit(func() {
				defer func() {
					sendWg.Done()
				}()

				instance := prometheus.GetTsDbInstance(ctx, qry)
				if instance == nil {
					log.Warnf(ctx, "not instance in %s", qry.StorageID)
					return
				}

				size, options, queryErr := instance.QueryRawData(ctx, qry, start, end, dataCh)
				if queryErr != nil {
					message.WriteString(fmt.Sprintf("query %s:%s is error: %s ", qry.TableID, qry.Fields, queryErr.Error()))
					return
				}

				// 如果配置了 IsMultiFrom，则无需使用 scroll 和 searchAfter 配置
				if !queryTs.IsMultiFrom {
					if resultTableOptions == nil {
						resultTableOptions = make(metadata.ResultTableOptions)
					}
					lock.Lock()
					resultTableOptions.MergeOptions(options)
					lock.Unlock()
				}

				total += size
			})
		}
	}()

	// 等待数据组装完毕
	receiveWg.Wait()
	if message.Len() > 0 {
		err = errors.New(message.String())
	}

	return
}

func queryReferenceWithPromEngine(ctx context.Context, queryTs *structured.QueryTs) (*PromData, error) {
	var (
		res  any
		err  error
		resp = NewPromData(queryTs.ResultColumns)
	)

	ctx, span := trace.NewSpan(ctx, "query-reference")
	defer func() {
		resp.TraceID = span.TraceID()
		resp.Status = metadata.GetStatus(ctx)
		span.End(&err)
	}()

	qStr, _ := json.Marshal(queryTs)
	span.Set("query-ts", string(qStr))

	for _, ql := range queryTs.QueryList {
		ql.IsReference = true

		// 排序复用
		ql.OrderBy = queryTs.OrderBy

		// 如果 qry.Step 不存在去外部统一的 step
		if ql.Step == "" {
			ql.Step = queryTs.Step
		}

		// 如果 Limit / From 没有单独指定的话，同时外部指定了的话，使用外部的
		if ql.Limit == 0 && queryTs.Limit > 0 {
			ql.Limit = queryTs.Limit
		}
		if ql.From == 0 && queryTs.From > 0 {
			ql.From = queryTs.From
		}

		if ql.TableID == "" {
			err = fmt.Errorf("tableID is empty")
			return nil, err
		}
	}

	queryRef, err := queryTs.ToQueryReference(ctx)
	if err != nil {
		return nil, err
	}

	unit, startTime, endTime, err := function.QueryTimestamp(queryTs.Start, queryTs.End)
	if err != nil {
		return nil, err
	}

	// es 需要使用自己的查询时间范围
	metadata.GetQueryParams(ctx).SetTime(startTime, endTime, unit).SetIsReference(true)
	metadata.SetQueryReference(ctx, queryRef)

	var lookBackDelta time.Duration
	if queryTs.LookBackDelta != "" {
		lookBackDelta, err = time.ParseDuration(queryTs.LookBackDelta)
		if err != nil {
			return nil, err
		}
	} else {
		// reference 接口背后都使用了存储引擎计算，所以在不特殊指定的情况下，使用 1s 补点逻辑，防止出的数据异常
		lookBackDelta = time.Second
	}

	instance := prometheus.NewInstance(ctx, promql.GlobalEngine, &prometheus.QueryRangeStorage{
		QueryMaxRouting: QueryMaxRouting,
		Timeout:         SingleflightTimeout,
	}, lookBackDelta, QueryMaxRouting)

	// 根据 step 重新对齐开始时间，因为 prometheus engine 中时间如果不能覆盖源数据，则会丢弃，而源数据是通过聚合而来
	var (
		step time.Duration
	)

	// 只有聚合场景需要对齐
	if window, windowErr := queryTs.GetMaxWindow(); windowErr == nil && window.Hours() > 0 {
		timezone := "UTC"
		// 只有按天聚合的时候才启用时区对齐偏移量，否则一律使用 UTC
		if window.Milliseconds()%(24*time.Hour).Milliseconds() == 0 {
			timezone = queryTs.Timezone
		}

		startTime, endTime, step, _, err = structured.AlignTime(startTime, endTime, queryTs.Step, timezone)
		if err != nil {
			return nil, err
		}
	} else {
		step = structured.StepParse(queryTs.Step)
	}

	if queryTs.Instant {
		res, err = instance.DirectQuery(ctx, queryTs.MetricMerge, startTime)
	} else {
		res, err = instance.DirectQueryRange(ctx, queryTs.MetricMerge, startTime, endTime, step)
	}
	if err != nil {
		return nil, err
	}

	tables := promql.NewTables()
	seriesNum := 0
	pointsNum := 0

	decodeFunc := metadata.GetFieldFormat(ctx).DecodeFunc()

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

// queryTsToInstanceAndStmt query 结构体转换为 instance 以及 stmt
func queryTsToInstanceAndStmt(ctx context.Context, queryTs *structured.QueryTs) (instance tsdb.Instance, stmt string, err error) {
	var (
		lookBackDelta time.Duration
		promExprOpt   = &structured.PromExprOption{}
	)

	ctx, span := trace.NewSpan(ctx, "query-ts-to-instance")
	defer func() {
		span.End(&err)
	}()

	queryString, _ := json.Marshal(queryTs)
	span.Set("query-ts", queryString)

	// 限制 queryList 是否过长
	if DefaultQueryListLimit > 0 {
		if len(queryTs.QueryList) > DefaultQueryListLimit {
			err = fmt.Errorf("the number of query lists cannot be greater than %d", DefaultQueryListLimit)
		}
	}

	// 判断是否打开对齐
	for _, ql := range queryTs.QueryList {
		ql.IsReference = false
		ql.AlignInfluxdbResult = AlignInfluxdbResult

		// 排序复用
		ql.OrderBy = queryTs.OrderBy

		// 如果 qry.Step 不存在去外部统一的 step
		if ql.Step == "" {
			ql.Step = queryTs.Step
		}

		// 如果 Limit / From 没有单独指定的话，同时外部指定了的话，使用外部的
		if ql.Limit == 0 && queryTs.Limit > 0 {
			ql.Limit = queryTs.Limit
		}
		if ql.From == 0 && queryTs.From > 0 {
			ql.From = queryTs.From
		}
	}

	// 判断是否指定 LookBackDelta
	if queryTs.LookBackDelta != "" {
		lookBackDelta, err = time.ParseDuration(queryTs.LookBackDelta)
		if err != nil {
			return
		}
	}

	// 如果 step 为空，则补充默认 step
	if queryTs.Step == "" {
		queryTs.Step = promql.GetDefaultStep().String()
	}

	// 转换成 queryRef
	queryRef, err := queryTs.ToQueryReference(ctx)
	if err != nil {
		return
	}

	if metadata.GetQueryParams(ctx).IsDirectQuery() {
		// 判断是否是直查
		vmExpand := queryRef.ToVmExpand(ctx)
		metadata.SetExpand(ctx, vmExpand)
		instance = prometheus.GetTsDbInstance(ctx, &metadata.Query{
			// 兼容 storage 结构体，用于单元测试
			StorageID:   consul.VictoriaMetricsStorageType,
			StorageType: consul.VictoriaMetricsStorageType,
		})
	} else {
		// 非直查开启忽略时间聚合函数判断
		promExprOpt.IgnoreTimeAggregationEnable = true

		metadata.SetQueryReference(ctx, queryRef)

		span.Set("query-max-routing", QueryMaxRouting)
		span.Set("singleflight-timeout", SingleflightTimeout.String())

		instance = prometheus.NewInstance(ctx, promql.GlobalEngine, &prometheus.QueryRangeStorage{
			QueryMaxRouting: QueryMaxRouting,
			Timeout:         SingleflightTimeout,
		}, lookBackDelta, QueryMaxRouting)
	}

	expr, err := queryTs.ToPromExpr(ctx, promExprOpt)
	if err != nil {
		return
	}

	stmt = expr.String()

	if instance == nil {
		err = fmt.Errorf("storage get error")
		return
	}

	span.Set("storage-type", instance.InstanceType())
	span.Set("stmt", stmt)
	return
}

func queryTsWithPromEngine(ctx context.Context, query *structured.QueryTs) (any, error) {
	var (
		err error

		instance tsdb.Instance
		stmt     string

		res  any
		resp = NewPromData(query.ResultColumns)
	)

	ctx, span := trace.NewSpan(ctx, "query-ts")
	defer func() {
		resp.TraceID = span.TraceID()
		resp.Status = metadata.GetStatus(ctx)
		span.End(&err)
	}()

	unit, startTime, endTime, err := function.QueryTimestamp(query.Start, query.End)
	if err != nil {
		log.Errorf(ctx, err.Error())
		return nil, err
	}

	start, end, step, timezone, err := structured.AlignTime(startTime, endTime, query.Step, query.Timezone)
	if err != nil {
		return nil, err
	}
	query.Timezone = timezone

	// 写入查询时间到全局缓存
	metadata.GetQueryParams(ctx).SetTime(start, end, unit)
	instance, stmt, err = queryTsToInstanceAndStmt(ctx, query)
	if err != nil {
		return nil, err
	}

	span.Set("storage-type", instance.InstanceType())

	if query.Instant {
		res, err = instance.DirectQuery(ctx, stmt, end)
	} else {
		res, err = instance.DirectQueryRange(ctx, stmt, start, end, step)
	}
	if err != nil {
		return nil, err
	}

	span.Set("stmt", stmt)
	span.Set("start", start)
	span.Set("end", end)
	span.Set("step", step)

	tables := promql.NewTables()
	seriesNum := 0
	pointsNum := 0

	decodeFunc := metadata.GetFieldFormat(ctx).DecodeFunc()

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

	if ok, factor, downSampleError := downsample.CheckDownSampleRange(step.String(), query.DownSampleRange); ok {
		if downSampleError == nil {
			resp.Downsample(factor)
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

func promQLToStruct(ctx context.Context, queryPromQL *structured.QueryPromQL) (query *structured.QueryTs, err error) {
	var (
		matchers []*labels.Matcher
	)

	if queryPromQL == nil {
		return
	}

	sp := structured.NewQueryPromQLExpr(queryPromQL.PromQL)
	query, err = sp.QueryTs()
	if err != nil {
		return
	}

	query.Start = queryPromQL.Start
	query.End = queryPromQL.End
	query.Step = queryPromQL.Step
	query.Timezone = queryPromQL.Timezone
	query.LookBackDelta = queryPromQL.LookBackDelta
	query.Instant = queryPromQL.Instant
	query.DownSampleRange = queryPromQL.DownSampleRange

	if queryPromQL.Match != "" {
		matchers, err = parser.ParseMetricSelector(queryPromQL.Match)
		if err != nil {
			return
		}
	}

	decodeFunc := metadata.GetFieldFormat(ctx).DecodeFunc()

	for _, q := range query.QueryList {
		// decode table id and field name
		q.TableID = structured.TableID(decodeFunc(string(q.TableID)))

		// decode condition
		for i, d := range q.Conditions.FieldList {
			q.Conditions.FieldList[i].DimensionName = decodeFunc(d.DimensionName)
		}

		// decode agg
		for aggIdx, agg := range q.AggregateMethodList {
			for i, d := range agg.Dimensions {
				q.AggregateMethodList[aggIdx].Dimensions[i] = decodeFunc(d)
			}
		}

		// 补充业务ID
		if len(queryPromQL.BKBizIDs) > 0 {
			q.Conditions.Append(structured.ConditionField{
				DimensionName: structured.BizID,
				Value:         queryPromQL.BKBizIDs,
				Operator:      structured.Contains,
			}, structured.ConditionAnd)
		}

		// 补充 Match
		var verifyDimensions = func(key string) bool {
			return true
		}
		if len(matchers) > 0 {
			if queryPromQL.IsVerifyDimensions {
				dimSet := set.New[string]()
				for _, a := range q.AggregateMethodList {
					dimSet.Add(a.Dimensions...)
				}

				verifyDimensions = func(key string) bool {
					return dimSet.Existed(key)
				}
			}

			for _, m := range matchers {
				if !verifyDimensions(m.Name) {
					continue
				}

				q.Conditions.Append(structured.ConditionField{
					DimensionName: m.Name,
					Value:         []string{m.Value},
					Operator:      structured.PromOperatorToConditions(m.Type),
				}, structured.ConditionAnd)
			}
		}
	}

	return
}

func QueryTsClusterMetrics(ctx context.Context, query *structured.QueryTs) (interface{}, error) {
	var (
		err error
		res any
	)
	ctx, span := trace.NewSpan(ctx, "query-ts-cluster-metrics")
	defer span.End(&err)

	_, startTime, endTime, err := function.QueryTimestamp(query.Start, query.End)
	if err != nil {
		log.Errorf(ctx, err.Error())
		return nil, err
	}

	start, end, step, timezone, err := structured.AlignTime(startTime, endTime, query.Step, query.Timezone)
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
		res, err = instance.DirectQuery(ctx, "", end)
	} else {
		res, err = instance.DirectQueryRange(ctx, "", start, end, step)
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

	decodeFunc := metadata.GetFieldFormat(ctx).DecodeFunc()

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
