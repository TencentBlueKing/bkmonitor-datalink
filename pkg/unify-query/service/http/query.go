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

	"github.com/jinzhu/copier"
	ants "github.com/panjf2000/ants/v2"
	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/model/labels"
	promPromql "github.com/prometheus/prometheus/promql"
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/downsample"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/promql_parser"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	redisUtil "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/prometheus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/redis"
	queryErrors "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/errors"
)

func queryExemplar(ctx context.Context, query *structured.QueryTs) (any, error) {
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
		log.Errorf(ctx, "%s [%s] | 操作: 查询列表验证 | 错误: %s | 解决: 减少查询数量至%d以下", queryErrors.ErrBusinessParamInvalid, queryErrors.GetErrorCode(queryErrors.ErrBusinessParamInvalid), err.Error(), DefaultQueryListLimit)
		return nil, err
	}

	_, startTime, endTime, err := function.QueryTimestamp(query.Start, query.End)
	if err != nil {
		log.Errorf(ctx, "%s [%s] | 操作: 时间参数解析 | 错误: %s | 解决: 检查开始和结束时间格式", queryErrors.ErrBusinessParamInvalid, queryErrors.GetErrorCode(queryErrors.ErrBusinessParamInvalid), err.Error())
		return nil, err
	}

	start, end, _, timezone, err := structured.AlignTime(startTime, endTime, query.Step, query.Timezone)
	if err != nil {
		log.Errorf(ctx, "%s [%s] | 操作: 时间对齐处理 | 错误: %s | 解决: 检查步长和时区设置", queryErrors.ErrBusinessParamInvalid, queryErrors.GetErrorCode(queryErrors.ErrBusinessParamInvalid), err.Error())
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
	ignoreDimensions := []string{metadata.KeyTableUUID}
	list = make([]map[string]any, 0)
	resultTableOptions = make(metadata.ResultTableOptions)

	ctx, span := trace.NewSpan(ctx, "query-raw-with-instance")
	defer span.End(&err)

	unit, start, end, timeErr := function.QueryTimestamp(queryTs.Start, queryTs.End)
	if timeErr != nil {
		err = timeErr
		return total, list, resultTableOptions, err
	}
	metadata.GetQueryParams(ctx).SetTime(start, end, unit)

	var (
		receiveWg sync.WaitGroup
		dataCh    = make(chan map[string]any)
		errCh     = make(chan error)

		message strings.Builder
		lock    sync.Mutex

		allLabelMap = make(map[string][]function.LabelMapValue)

		queryRef metadata.QueryReference
	)

	queryRef, err = queryTs.ToQueryReference(ctx)
	if err != nil {
		return total, list, resultTableOptions, err
	}

	receiveWg.Add(1)
	go func() {
		defer receiveWg.Done()
		for e := range errCh {
			message.WriteString(fmt.Sprintf("query error: %s ", e.Error()))
		}
		if message.Len() > 0 {
			err = errors.New(message.String())
		}
	}()

	receiveWg.Add(1)

	// 启动合并数据
	go func() {
		defer receiveWg.Done()

		var (
			data      []map[string]any
			fieldType = make(map[string]string)
		)
		for d := range dataCh {
			data = append(data, d)
		}

		for _, rto := range resultTableOptions {
			for k, v := range rto.FieldType {
				fieldType[k] = v
			}
		}

		span.Set("query-list-num", queryRef.Count())
		span.Set("result-data-num", len(data))

		queryTs.OrderBy.Orders().SortSliceList(data, fieldType)

		span.Set("query-scroll", queryTs.Scroll)
		span.Set("query-result-table", queryTs.ResultTableOptions)

		//  scroll 和 searchAfter 模式不进行裁剪
		if queryTs.Scroll == "" && !queryTs.IsSearchAfter && queryTs.ResultTableOptions.IsCrop() {
			// 判定是否启用 multi from 特性
			span.Set("query-multi-from", queryTs.IsMultiFrom)
			span.Set("data-length", len(data))
			span.Set("query-ts-from", queryTs.From)
			span.Set("query-ts-limit", queryTs.Limit)

			if queryTs.Limit > 0 {
				if queryTs.IsMultiFrom {
					if len(data) > 0 && len(data) > queryTs.Limit {
						data = data[0:queryTs.Limit]
					}
					for _, l := range data {
						tableUUID := l[metadata.KeyTableUUID].(string)

						option := resultTableOptions.GetOption(tableUUID)
						if option == nil || option.From == nil {
							resultTableOptions.SetOption(tableUUID, &metadata.ResultTableOption{From: function.IntPoint(1)})
						} else {
							*option.From++
						}
					}
				} else {
					// 只有合并数据才需要进行裁剪，否则原始数据里面就已经经过裁剪了
					if queryRef.Count() > 1 {
						// 只有长度符合的数据才进行裁剪
						if len(data) > queryTs.From {
							maxLength := queryTs.From + queryTs.Limit
							if len(data) < maxLength {
								maxLength = len(data)
							}

							data = data[queryTs.From:maxLength]
						} else {
							data = make([]map[string]any, 0)
						}
					}
				}
			}
		}

		span.Set("query-label-map", allLabelMap)
		span.Set("query-highlight", queryTs.HighLight)

		var hlF *function.HighLightFactory
		if queryTs.HighLight != nil && queryTs.HighLight.Enable && len(allLabelMap) > 0 {
			hlF = function.NewHighLightFactory(allLabelMap, queryTs.HighLight.MaxAnalyzedOffset)
		}

		for _, item := range data {
			if item == nil {
				continue
			}

			for _, ignoreDimension := range ignoreDimensions {
				delete(item, ignoreDimension)
			}

			if hlF != nil {
				if highlightResult := hlF.Process(item); len(highlightResult) > 0 {
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
			close(errCh)
		}()

		queryRef.Range("", func(qry *metadata.Query) {
			sendWg.Add(1)

			labelMap, err := qry.LabelMap()
			if err == nil {
				// 合并 labelMap
				for k, lm := range labelMap {
					if _, ok := allLabelMap[k]; !ok {
						allLabelMap[k] = make([]function.LabelMapValue, 0)
					}

					allLabelMap[k] = append(allLabelMap[k], lm...)
				}
			}

			// 如果是多数据合并，为了保证排序和Limit 的准确性，需要查询原始的所有数据，所以这里对 from 和 size 进行重写
			if queryRef.Count() > 1 {
				if !queryTs.IsMultiFrom {
					qry.Size += qry.From
					qry.From = 0
				}
			}

			_ = p.Submit(func() {
				defer func() {
					sendWg.Done()
				}()

				instance := prometheus.GetTsDbInstance(ctx, qry)
				if instance == nil {
					log.Warnf(ctx, "not instance in %s", qry.StorageID)
					return
				}

				_, size, option, queryErr := instance.QueryRawData(ctx, qry, start, end, dataCh)
				if queryErr != nil {
					errCh <- queryErr
					return
				}

				// 如果配置了 IsMultiFrom，则无需使用 scroll 和 searchAfter 配置
				lock.Lock()
				resultTableOptions.SetOption(qry.TableUUID(), option)
				lock.Unlock()

				total += size
			})
		})
	}()

	// 等待数据组装完毕
	receiveWg.Wait()
	return total, list, resultTableOptions, err
}

func queryRawWithScroll(ctx context.Context, queryTs *structured.QueryTs, session *redisUtil.ScrollSession) (total int64, list []map[string]any, resultTableOptions metadata.ResultTableOptions, err error) {
	var (
		receiveWg sync.WaitGroup
		dataCh    = make(chan map[string]any)
		errCh     = make(chan error)

		message strings.Builder
		lock    sync.Mutex

		queryRef metadata.QueryReference
	)

	list = make([]map[string]any, 0)
	resultTableOptions = make(metadata.ResultTableOptions)

	ctx, span := trace.NewSpan(ctx, "query-raw-with-scroll")
	defer span.End(&err)
	unit, start, end, timeErr := function.QueryTimestamp(queryTs.Start, queryTs.End)
	if timeErr != nil {
		err = timeErr
		return total, list, resultTableOptions, err
	}
	metadata.GetQueryParams(ctx).SetTime(start, end, unit)

	queryRef, err = queryTs.ToQueryReference(ctx)
	if err != nil {
		return total, list, resultTableOptions, err
	}

	receiveWg.Add(1)
	go func() {
		defer receiveWg.Done()
		for e := range errCh {
			message.WriteString(fmt.Sprintf("query error: %s ", e.Error()))
		}
		if message.Len() > 0 {
			err = errors.New(message.String())
		}
	}()

	receiveWg.Add(1)

	go func() {
		defer receiveWg.Done()

		for d := range dataCh {
			list = append(list, d)
		}
	}()

	// 多协程查询数据
	var (
		wg sync.WaitGroup
	)

	p, _ := ants.NewPool(QueryMaxRouting)
	defer p.Release()

	queryRef.Range("", func(qry *metadata.Query) {
		for i := 0; i < session.SliceLength(); i++ {
			wg.Add(1)

			err = p.Submit(func() {
				defer func() {
					wg.Done()
				}()

				newQry := &metadata.Query{}
				err = copier.CopyWithOption(newQry, qry, copier.Option{DeepCopy: true})
				if err != nil {
					log.Errorf(ctx, "copy query ts error: %s", err.Error())
					errCh <- err
					return
				}

				// 使用 slice 配置查询
				newQry.SliceID = cast.ToString(i)

				// slice info
				slice := session.Slice(newQry.TableUUID())
				defer func() {
					session.UpdateSliceStatus(newQry.TableUUID(), slice)
				}()

				if slice.Done() {
					return
				}

				from := slice.Offset + i*slice.Limit
				newQry.Size = slice.Limit
				newQry.ResultTableOption = &metadata.ResultTableOption{
					SliceIndex: i,
					ScrollID:   slice.ScrollID,
					SliceMax:   slice.SliceMax,
					From:       &from,
				}

				instance := prometheus.GetTsDbInstance(ctx, newQry)
				if instance == nil {
					log.Warnf(ctx, "not instance in %s", newQry.StorageID)
					return
				}

				size, _, option, err := instance.QueryRawData(ctx, newQry, start, end, dataCh)
				if err != nil {
					slice.FailedNum++
					errCh <- err
					return
				}

				// 如果配置了 IsMultiFrom，则无需使用 scroll 和 searchAfter 配置
				if option != nil {
					if option.ScrollID != "" {
						slice.ScrollID = option.ScrollID
					}
					slice.Offset = slice.Offset + slice.Limit*session.SliceLength()
					lock.Lock()
					resultTableOptions.SetOption(newQry.TableUUID(), option)
					lock.Unlock()
				}

				if size == 0 {
					slice.Status = redisUtil.StatusCompleted
				}
				total += size
			})
			if err != nil {
				errCh <- err
				wg.Done()
			}
		}
	})

	wg.Wait()

	close(dataCh)
	close(errCh)

	receiveWg.Wait()
	return total, list, resultTableOptions, err
}

func queryReferenceWithPromEngine(ctx context.Context, queryTs *structured.QueryTs) (*PromData, error) {
	var (
		res       any
		err       error
		resp      = NewPromData(queryTs.ResultColumns)
		isPartial bool
	)

	ctx, span := trace.NewSpan(ctx, "query-reference-with-prom-engine")
	defer func() {
		resp.TraceID = span.TraceID()
		resp.Status = metadata.GetStatus(ctx)
		span.End(&err)
	}()

	qStr, _ := json.Marshal(queryTs)
	span.Set("query-ts", string(qStr))

	for _, ql := range queryTs.QueryList {
		ql.NotPromFunc = true

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

	// 开启时间不对齐模式
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

	// 获取配置里面的最大时间聚合以及时区
	window, timezone := queryRef.GetMaxWindowAndTimezone()

	// 只有聚合场景需要对齐
	if window.Seconds() > 0 {
		// 移除按天整除逻辑，使用用户传过来的时区
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
		res, isPartial, err = instance.DirectQueryRange(ctx, queryTs.MetricMerge, startTime, endTime, step)
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

	resp.IsPartial = isPartial
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

	// 判定是否开启时间不对齐模式
	if queryTs.Reference {
		unit, startTime, endTime, timeErr := function.QueryTimestamp(queryTs.Start, queryTs.End)
		if timeErr != nil {
			err = timeErr
			return instance, stmt, err
		}

		metadata.GetQueryParams(ctx).SetTime(startTime, endTime, unit).SetIsReference(true)
	}

	// 判断是否打开对齐
	for _, ql := range queryTs.QueryList {
		ql.NotPromFunc = false
		// 只有时间对齐模式，才需要开启
		ql.AlignInfluxdbResult = AlignInfluxdbResult && !queryTs.Reference

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
			return instance, stmt, err
		}
	}

	// 如果 step 为空，则补充默认 step
	if queryTs.Step == "" {
		queryTs.Step = promql.GetDefaultStep().String()
	}

	// 转换成 queryRef
	queryRef, err := queryTs.ToQueryReference(ctx)
	if err != nil {
		return instance, stmt, err
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
		return instance, stmt, err
	}

	stmt = expr.String()

	if instance == nil {
		err = fmt.Errorf("storage get error")
		return instance, stmt, err
	}

	span.Set("storage-type", instance.InstanceType())
	span.Set("stmt", stmt)
	return instance, stmt, err
}

func queryTsWithPromEngine(ctx context.Context, query *structured.QueryTs) (any, error) {
	var (
		err error

		instance tsdb.Instance
		stmt     string

		res       any
		resp      = NewPromData(query.ResultColumns)
		isPartial bool
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
		res, isPartial, err = instance.DirectQueryRange(ctx, stmt, start, end, step)
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

	resp.IsPartial = isPartial
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
	var matchers []*labels.Matcher

	if queryPromQL == nil {
		return query, err
	}

	sp := structured.NewQueryPromQLExpr(queryPromQL.PromQL)
	query, err = sp.QueryTs()
	if err != nil {
		return query, err
	}

	query.Start = queryPromQL.Start
	query.End = queryPromQL.End
	query.Step = queryPromQL.Step
	query.Timezone = queryPromQL.Timezone
	query.LookBackDelta = queryPromQL.LookBackDelta
	query.Instant = queryPromQL.Instant
	query.DownSampleRange = queryPromQL.DownSampleRange
	query.Reference = queryPromQL.Reference

	if queryPromQL.Match != "" {
		matchers, err = promql_parser.ParseMetricSelector(queryPromQL.Match)
		if err != nil {
			return query, err
		}
	}

	decodeFunc := metadata.GetFieldFormat(ctx).DecodeFunc()
	if decodeFunc == nil {
		decodeFunc = func(q string) string {
			return q
		}
	}

	for _, q := range query.QueryList {
		// decode table id and field name
		q.TableID = structured.TableID(decodeFunc(string(q.TableID)))
		q.FieldName = decodeFunc(q.FieldName)

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
		verifyDimensions := func(key string) bool {
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

	return query, err
}

func QueryTsClusterMetrics(ctx context.Context, query *structured.QueryTs) (any, error) {
	var (
		err       error
		res       any
		isPartial bool
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
		res, isPartial, err = instance.DirectQueryRange(ctx, "", start, end, step)
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
	resp.IsPartial = isPartial
	err = resp.Fill(tables)
	if err != nil {
		return nil, err
	}
	return resp, nil
}
