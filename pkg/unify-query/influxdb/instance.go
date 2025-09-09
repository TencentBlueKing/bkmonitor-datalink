// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/client"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// Params
type Params struct {
	Timeout              time.Duration
	ContentType          string
	PerQueryMaxGoroutine int
	ChunkSize            int

	MaxLimit  int
	MaxSLimit int
	Tolerance int
}

// Instance
type Instance struct {
	ctx     context.Context
	cli     client.Client
	timeout time.Duration

	maxLimit  int
	maxSLimit int
	tolerance int
}

var NewClient = func(address, username, password, contentType string, chunkSize int) client.Client {
	return client.NewBasicClient(address, username, password, contentType, chunkSize)
}

// NewInstance
func NewInstance(ctx context.Context, params *Params, client client.Client) (*Instance, error) {
	// Create a new HTTPClient
	return &Instance{
		ctx:       ctx,
		cli:       client,
		timeout:   params.Timeout,
		maxLimit:  params.MaxLimit,
		maxSLimit: params.MaxSLimit,
		tolerance: params.Tolerance,
	}, nil
}

// query
func (i *Instance) query(
	ctx context.Context, db, sql, precision, contentType string, chunked bool,
) (*decoder.Response, error) {
	var err error
	ctx, span := trace.NewSpan(ctx, "raw-query")
	defer span.End(&err)

	user := metadata.GetUser(ctx)
	span.Set("query-source", user.Source)
	span.Set("query-username", user.Name)
	span.Set("query-db", db)
	span.Set("query-sql", sql)
	span.Set("query-content-type", contentType)
	span.Set("query-chunked", chunked)

	start := time.Now()
	resp, err := i.cli.Query(ctx, db, sql, precision, contentType, chunked)

	// 即使超时也需要打点
	left := time.Since(start)
	span.Set("query-cost", left)

	if err != nil {
		return nil, err
	}

	return resp, nil
}

// QueryInfos: 请求json格式的数据
func (i *Instance) QueryInfos(ctx context.Context, metricName, db, stmt, precision string, _ int) (*Tables, error) {
	var (
		cancel context.CancelFunc

		resp   *decoder.Response
		err    error
		tables *Tables

		startQuery, startAnaylize time.Time

		resultsNum int
		seriesNum  int
		pointsNum  int
	)

	ctx, span := trace.NewSpan(ctx, "query-info-influxdb-query-select")
	defer span.End(&err)

	ctx, cancel = context.WithTimeout(ctx, i.timeout)
	defer cancel()

	startQuery = time.Now()

	// resp, err = i.cli.QueryCtx(ctx, base.NewQuery(stmt, db, precision))
	resp, err = i.query(ctx, db, stmt, precision, "application/json", false)
	if err != nil {
		log.Errorf(ctx, "inner query:%s failed,error:%s", stmt, err)
		return nil, err
	}

	startAnaylize = time.Now()
	user := metadata.GetUser(ctx)
	span.Set("query-source", user.Source)
	span.Set("query-username", user.Name)
	span.Set("query-db", db)
	span.Set("query-metric-name", metricName)
	span.Set("query-sql", stmt)
	span.Set("query-cost", startAnaylize.Sub(startQuery))

	log.Debugf(ctx,
		fmt.Sprintf("influxdb query:[%s][%s], query cost:%s", db, stmt, startAnaylize.Sub(startQuery)),
	)
	if resp == nil {
		log.Warnf(ctx, "query:%s get nil response", stmt)
		return nil, errors.New("get nil response")
	}
	if resp.Err != "" {
		return nil, errors.New(resp.Err)
	}

	// 将查询到的结果，转换为输出结构
	tables = new(Tables)
	for _, result := range resp.Results {
		resultsNum++
		if result.Err != "" {
			log.Errorf(ctx, "query:%s get err result:%s", stmt, result.Err)
			return nil, errors.New(result.Err)
		}

		for _, series := range result.Series {
			seriesNum++
			pointsNum += len(series.Values)
			tables.Add(NewTable(metricName, series, nil))
		}
	}

	span.Set("results-num", resultsNum)
	span.Set("series-num", seriesNum)
	span.Set("points-num", pointsNum)

	span.Set("analyzer-cost", time.Since(startAnaylize))

	log.Debugf(ctx,
		"influxdb query:[%s][%s], result anaylize cost:%s", db, stmt, time.Since(startAnaylize),
	)

	return tables, nil
}

// limit
func (i *Instance) limit(limit int) int {
	max := 0
	if i.maxLimit > 0 {
		max = i.maxLimit + i.tolerance
	}

	// 开启 limit 限制并且指定数量超过限制，则返回最大限制
	if max > 0 && limit > max {
		return max
	}

	if limit > 0 {
		return limit
	}
	return max
}

// slimit
func (i *Instance) slimit(slimit int) int {
	max := 0
	if i.maxSLimit > 0 {
		max = i.maxSLimit + i.tolerance
	}

	// 开启 slimit 限制并且指定数量超过限制，则返回最大限制
	if max > 0 && slimit > max {
		return max
	}

	if slimit > 0 {
		return slimit
	}
	return max
}

// setLimitAndSLimit
func (i *Instance) setLimitAndSLimit(stmt string, limit, slimit int) string {
	var (
		key       string
		newLimit  int
		newSLimit int
	)
	key = ` limit `
	newLimit = i.limit(limit)
	if newLimit > 0 && !strings.Contains(stmt, key) {
		stmt = fmt.Sprintf("%s%s%d", stmt, key, newLimit)
	}

	key = ` slimit `
	newSLimit = i.slimit(slimit)
	if newSLimit > 0 && !strings.Contains(stmt, key) {
		stmt = fmt.Sprintf("%s%s%d", stmt, key, newSLimit)
	}
	return stmt
}

// Query 请求influxdb，返回默认格式(msgpack)的数据
func (i *Instance) Query(
	ctx context.Context, metricName, db, stmt, precision string,
	withGroupBy bool, isCountGroup bool, expandTag map[string]string, limit, slimit int,
) (*Tables, error) {
	var (
		cancel context.CancelFunc

		resp   *decoder.Response
		tables *Tables

		err error

		startQuery, startAnaylize time.Time

		series []*decoder.Row

		resultNum = 0
		seriesNum = 0
		pointNum  = 0

		message string
	)

	ctx, span := trace.NewSpan(ctx, "influxdb-query-select")
	defer span.End(&err)

	ctx, cancel = context.WithTimeout(ctx, i.timeout)
	defer cancel()

	startQuery = time.Now()

	user := metadata.GetUser(ctx)
	span.Set("query-source", user.Source)
	span.Set("query-username", user.Name)
	span.Set("query-db", db)
	span.Set("query-metricName", metricName)
	span.Set("query-sql", stmt)
	span.Set("expand-tag", expandTag)

	stmt = i.setLimitAndSLimit(stmt, limit, slimit)
	resp, err = i.query(ctx, db, stmt, precision, "", true)
	if err != nil {
		log.Errorf(ctx, "db: %s inner query:%s failed,error:%s", db, stmt, err)
		return nil, err
	}

	startAnaylize = time.Now()

	span.Set("query-cost", startAnaylize.Sub(startQuery))
	log.Debugf(ctx, "influxdb query:%s, query cost:%s", stmt, startAnaylize.Sub(startQuery))
	if resp == nil {
		log.Warnf(ctx, "query:%s get nil response", stmt)
		return nil, errors.New("get nil response")
	}
	if resp.Err != "" {
		return nil, errors.New(resp.Err)
	}

	// 将查询到的结果，转换为输出结构
	tables = new(Tables)

	// 合并 Results
	series = make([]*decoder.Row, 0)
	for _, result := range resp.Results {
		resultNum++
		if result.Err != "" {
			log.Errorf(ctx, "query:%s get err result:%s", stmt, result.Err)
			return nil, errors.New(result.Err)
		}

		series = append(series, result.Series...)
	}

	// 如果sql中含有group by，则这里不需要再手动group by了
	if withGroupBy {
		seriesNum += len(series)
		for _, s := range series {
			pointNum += len(s.Values)
			tables.Add(NewTable(metricName, s, expandTag))
		}
	} else {
		// 替代influxdb操作group by *
		resultSeries := GroupBySeries(ctx, series)

		seriesNum += len(resultSeries)
		for _, s := range resultSeries {
			pointNum += len(s.Values)
			tables.Add(NewTable(metricName, s, expandTag))
		}
	}

	span.Set("resp-result-num", resultNum)
	span.Set("resp-series-num", seriesNum)
	span.Set("resp-point-num", pointNum)

	// 由于 ctx 信息无法向上传递，所以增加一个全局 cache 存放异常信息
	if i.maxLimit > 0 && pointNum > i.maxLimit {
		message = fmt.Sprintf("%s: %d", ErrPointBeyondLimit.Error(), i.maxLimit)
		metadata.SetStatus(ctx, metadata.ExceedsMaximumLimit, message)
		log.Warnf(ctx, message)
	}
	// 只有聚合场景下 slimit才会有效
	if withGroupBy && i.maxSLimit > 0 && seriesNum > i.maxSLimit {
		message = fmt.Sprintf("%s: %d", ErrSeriesBeyondSLimit.Error(), i.maxSLimit)
		metadata.SetStatus(ctx, metadata.ExceedsMaximumSlimit, message)
		log.Warnf(ctx, message)
	}

	span.Set("analyzer_cost", time.Since(startAnaylize))

	log.Debugf(ctx, fmt.Sprintf(
		"influxdb query:%s, result anaylize cost:%s, result num: %d, series num: %d, point num: %d",
		stmt, time.Since(startAnaylize), resultNum, seriesNum, pointNum,
	))

	return tables, nil
}
