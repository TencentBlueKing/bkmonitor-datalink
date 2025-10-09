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
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/influxdata/influxdb/prometheus/remote"
	"github.com/influxdata/influxql"
	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/prompb"
	promPromql "github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
	promRemote "github.com/prometheus/prometheus/storage/remote"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

const (
	ContentType     = "Accept"
	ContentEncoding = "Accept-Encoding"

	ContentTypeProtobuf = "application/x-protobuf"
	ContentTypeJson     = "application/json"
	ContentTypeMsgpack  = "application/x-msgpack"

	ContentEncodingSnappy = "snappy"
)

var (
	ErrorsHttpNotFound   = errors.New("404 Not Found")
	ErrorsNotDownSampled = errors.New("not downsampled")

	MapGrpcConn = make(map[string]*grpc.ClientConn)
)

// NewInstance 初始化引擎
func NewInstance(ctx context.Context, opt *Options) (*Instance, error) {
	if opt.Host == "" {
		return nil, fmt.Errorf("host is empty %+v", opt)
	}

	inst := &Instance{
		ctx:      ctx,
		host:     opt.Host,
		port:     opt.Port,
		grpcPort: opt.GrpcPort,

		username: opt.Username,
		password: opt.Password,

		contentType: opt.ContentType,
		chunkSize:   opt.ChunkSize,

		protocol:       opt.Protocol,
		rawUriPath:     opt.RawUriPath,
		accept:         opt.Accept,
		acceptEncoding: opt.AcceptEncoding,

		readRateLimit: opt.ReadRateLimit,
		maxLimit:      opt.MaxLimit,
		maxSLimit:     opt.MaxSlimit,
		tolerance:     opt.Tolerance,

		timeout: opt.Timeout,
		curl:    opt.Curl,
	}
	return inst, nil
}

var _ tsdb.Instance = (*Instance)(nil)

func (i *Instance) Check(ctx context.Context, promql string, start, end time.Time, step time.Duration) string {
	return ""
}

// GetInstanceType 获取引擎类型
func (i *Instance) InstanceType() string {
	return metadata.InfluxDBStorageType
}

func (i *Instance) QueryExemplar(ctx context.Context, fields []string, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) (*decoder.Response, error) {
	var (
		cancel context.CancelFunc

		startAnaylize time.Time

		sLimitStr string
		limitStr  string
		err       error

		res = new(decoder.Response)
	)
	ctx, span := trace.NewSpan(ctx, "influxdb-influxql-query-exemplar")
	defer span.End(&err)
	startAnaylize = time.Now()

	where := fmt.Sprintf("time > %d and time < %d", start.UnixNano(), end.UnixNano())
	if query.Condition != "" {
		where = fmt.Sprintf("%s and %s", where, query.Condition)
	}

	limit, slimit := i.getLimitAndSlimit(query.OffsetInfo.Limit, query.OffsetInfo.SLimit)
	if limit > 0 {
		sLimitStr = fmt.Sprintf(" slimit %d", slimit)
	}
	if slimit > 0 {
		limitStr = fmt.Sprintf(" limit %d", limit)
	}

	influxql := fmt.Sprintf(
		"select %s as %s, time as %s, %s from %s where %s and (bk_span_id != '' or bk_trace_id != '') %s%s",
		query.Field, influxdb.ResultColumnName, influxdb.TimeColumnName, strings.Join(fields, ", "),
		influxql.QuoteIdent(query.Measurement), where, limitStr, sLimitStr,
	)

	values := &url.Values{}
	values.Set("db", query.DB)
	values.Set("q", influxql)

	if i.chunkSize > 0 {
		values.Set("chunked", "true")
		values.Set("chunk_size", fmt.Sprintf("%d", i.chunkSize))
	}

	urlPath := fmt.Sprintf(
		"%s://%s:%d/%s?%s",
		"http", i.host, i.port, "query", values.Encode(),
	)

	span.Set("query-params", values.Encode())
	span.Set("http-url", urlPath)

	ctx, cancel = context.WithTimeout(ctx, i.timeout)
	defer cancel()

	user := metadata.GetUser(ctx)
	span.Set("query-space-uid", user.SpaceUID)
	span.Set("query-source", user.Source)
	span.Set("query-username", user.Name)
	span.Set("query-url-path", urlPath)
	span.Set("query-q", influxql)
	span.Set("query-db", query.DB)
	span.Set("query-measurement", query.Measurement)
	span.Set("query-field", query.Field)
	span.Set("query-url-path", urlPath)
	span.Set("query-where", where)
	span.Set("query-cost", time.Since(startAnaylize).String())

	log.Debugf(ctx,
		"influxdb query: %s, where: %s",
		urlPath, where,
	)

	dec, err := decoder.GetDecoder(i.contentType)
	if err != nil {
		return nil, err
	}

	i.curl.WithDecoder(func(ctx context.Context, reader io.Reader, resp any) (int, error) {
		dr := resp.(*decoder.Response)
		return dec.Decode(ctx, reader, dr)
	})

	size, err := i.curl.Request(
		ctx, curl.Get,
		curl.Options{
			UrlPath: urlPath,
			Headers: metadata.Headers(ctx, map[string]string{
				ContentType: i.contentType,
			}),
			UserName: i.username,
			Password: i.password,
		},
		res,
	)
	metric.TsDBRequestBytes(ctx, size, i.InstanceType())

	return res, nil
}

func (i *Instance) getRawData(columns []string, data []any) (time.Time, float64, error) {
	var (
		t        time.Time
		err      error
		ok       bool
		v        float64
		timeItem string
		tf       float64

		timeColumnIndex  int
		valueColumnIndex int

		hasTimeColumnIndex  bool
		hasValueColumnIndex bool
	)

	for index, column := range columns {
		switch column {
		case influxdb.TimeColumnName:
			timeColumnIndex = index
			hasTimeColumnIndex = true
		case influxdb.ResultColumnName:
			valueColumnIndex = index
			hasValueColumnIndex = true
		}
	}

	if !hasTimeColumnIndex {
		return t, v, fmt.Errorf("columns have not time column: %v", columns)
	}

	if !hasValueColumnIndex {
		return t, v, fmt.Errorf("columns have not value column: %v", columns)
	}

	// 基于不同的通信协议(json/x-msgpack)会有不同的时间解析结果
	switch data[timeColumnIndex].(type) {
	case string:
		timeItem, ok = data[timeColumnIndex].(string)
		if !ok {
			err = fmt.Errorf("parse time type failed,data: %#v", data[timeColumnIndex])
			return t, v, err
		}
		if t, err = time.Parse(time.RFC3339Nano, timeItem); err != nil {
			err = fmt.Errorf(
				"failed to transfer datetime->[%s] for err->[%s], will return empty data", data[timeColumnIndex], err,
			)
			return t, v, err
		}
	case time.Time:
		t, ok = data[timeColumnIndex].(time.Time)
		if !ok {
			err = fmt.Errorf("parse time type failed,data: %#v", data[timeColumnIndex])
			return t, v, err
		}
	case float64:
		tf, ok = data[timeColumnIndex].(float64)
		if !ok {
			err = fmt.Errorf("parse time type failed,data: %#v", data[timeColumnIndex])
			return t, v, err
		}

		t = time.Unix(0, int64(tf))
	}

	switch value := data[valueColumnIndex].(type) {
	case float64:
		v = value
	case int:
		v = float64(value)
	case int64:
		v = float64(value)
	case json.Number:
		result, err := value.Float64()
		if err != nil {
			err = fmt.Errorf("parse value from string failed,data:%#v", data[valueColumnIndex])
			return t, v, err
		}
		v = result
	case string:
		result, err := strconv.ParseFloat(value, 64)
		if err != nil {
			err = fmt.Errorf("parse value from string failed,data:%#v", data[valueColumnIndex])
			return t, 0, err
		}
		v = result
	case nil:
		return t, 0, errors.New("invalid value")
	}

	return t, v, nil
}

// getLimitAndSlimit 获取真实的 limit 和 slimit
func (i *Instance) getLimitAndSlimit(limit, slimit int) (int64, int64) {
	var resultLimit, resultSLimit int

	if limit > 0 {
		resultLimit = limit
	}
	if limit == 0 || limit > i.maxLimit {
		if i.maxLimit > 0 {
			resultLimit = i.maxLimit + i.tolerance
		}
	}

	if slimit > 0 {
		resultSLimit = slimit
	}
	if slimit == 0 || slimit > i.maxSLimit {
		if i.maxSLimit > 0 {
			resultSLimit = i.maxSLimit + i.tolerance
		}
	}

	return int64(resultLimit), int64(resultSLimit)
}

func (i *Instance) makeSQL(
	ctx context.Context, query *metadata.Query, start, end time.Time,
) (string, error) {
	var (
		selectList = make([]string, 0)
		groupList  = make([]string, 0)

		sqlBuilder strings.Builder
		err        error
	)
	ctx, span := trace.NewSpan(ctx, "influxdb-make-sqlBuilder")
	defer span.End(&err)

	if len(query.Aggregates) > 1 {
		return "", fmt.Errorf("influxdb 不支持多函数聚合查询, %+v", query.Aggregates)
	}

	if len(query.Aggregates) == 1 {
		agg := query.Aggregates[0]
		if len(agg.Dimensions) > 0 {
			for _, dim := range agg.Dimensions {
				group := dim
				if group == "" || group == labels.MetricName {
					continue
				}

				if group != "*" {
					group = fmt.Sprintf(`"%s"`, group)
				}
				groupList = append(groupList, group)
			}
		}

		if agg.Window > 0 && !agg.Without {
			groupList = append(groupList, "time("+agg.Window.String()+")")
		}

		// avg => mean
		if agg.Name == structured.AVG {
			agg.Name = structured.MEAN
		}

		selectList = append(selectList, fmt.Sprintf(`%s("%s") AS %s`, agg.Name, query.Field, influxdb.ResultColumnName))
	} else {
		selectList = append(selectList, fmt.Sprintf(`"%s" AS %s`, query.Field, influxdb.ResultColumnName))
		selectList = append(selectList, "*::tag")
	}
	selectList = append(selectList, fmt.Sprintf(`"time" AS %s`, influxdb.TimeColumnName))

	sqlBuilder.WriteString("SELECT ")
	sqlBuilder.WriteString(strings.Join(selectList, ", ") + " ")
	sqlBuilder.WriteString("FROM " + influxql.QuoteIdent(query.Measurement) + " ")
	sqlBuilder.WriteString("WHERE " + fmt.Sprintf("time > %d and time < %d", start.UnixNano(), end.UnixNano()))
	if query.Condition != "" {
		sqlBuilder.WriteString(" AND (" + query.Condition + ")")
	}
	if len(groupList) > 0 {
		sqlBuilder.WriteString(" GROUP BY " + strings.Join(groupList, ", "))
	}

	limit, slimit := i.getLimitAndSlimit(query.OffsetInfo.Limit, query.OffsetInfo.SLimit)
	if limit > 0 {
		sqlBuilder.WriteString(fmt.Sprintf(` LIMIT %d`, limit))
	}
	if slimit > 0 {
		sqlBuilder.WriteString(fmt.Sprintf(` SLIMIT %d`, slimit))
	}
	if query.Timezone != "" {
		sqlBuilder.WriteString(fmt.Sprintf(` TZ('%s')`, query.Timezone))
	}

	return sqlBuilder.String(), nil
}

func (i *Instance) query(
	ctx context.Context,
	query *metadata.Query,
	metricName string,
	start time.Time,
	end time.Time,
	withFieldTag bool,
) (*prompb.QueryResult, error) {
	var (
		cancel context.CancelFunc

		startAnaylize time.Time

		seriesNum = 0
		pointNum  = 0

		err error

		res = new(decoder.Response)
	)
	ctx, span := trace.NewSpan(ctx, "influxdb-influxql-query-raw")
	defer span.End(&err)

	if len(query.Aggregates) > 1 {
		return nil, fmt.Errorf("influxdb 不支持多函数聚合查询, %+v", query.Aggregates)
	}

	sql, err := i.makeSQL(ctx, query, start, end)
	if err != nil {
		return nil, err
	}

	values := &url.Values{}
	values.Set("db", query.DB)
	values.Set("q", sql)

	if i.chunkSize > 0 {
		values.Set("chunked", "true")
		values.Set("chunk_size", fmt.Sprintf("%d", i.chunkSize))
	}

	urlPath := fmt.Sprintf(
		"%s://%s:%d/%s?%s",
		"http", i.host, i.port, "query", values.Encode(),
	)

	span.Set("query-params", values.Encode())
	span.Set("http-url", urlPath)

	ctx, cancel = context.WithTimeout(ctx, i.timeout)
	defer cancel()
	startAnaylize = time.Now()

	user := metadata.GetUser(ctx)
	span.Set("query-space-uid", user.SpaceUID)
	span.Set("query-source", user.Source)
	span.Set("query-username", user.Name)
	span.Set("query-url-path", urlPath)
	span.Set("query-q", sql)
	span.Set("query-db", query.DB)
	span.Set("query-measurement", query.Measurement)
	span.Set("query-field", query.Field)
	span.Set("query-url-path", urlPath)

	dec, err := decoder.GetDecoder(i.contentType)
	if err != nil {
		return nil, metadata.Sprintf(
			metadata.MsgQueryInfluxDB,
			"解析器 %s 解析异常",
			i.contentType,
		).Error(ctx, err)
	}

	i.curl.WithDecoder(func(ctx context.Context, reader io.Reader, resp any) (int, error) {
		dr := resp.(*decoder.Response)
		return dec.Decode(ctx, reader, dr)
	})

	size, err := i.curl.Request(
		ctx, curl.Get,
		curl.Options{
			UrlPath: urlPath,
			Headers: map[string]string{
				ContentType: i.contentType,
			},
			UserName: i.username,
			Password: i.password,
		},
		res,
	)
	if err != nil {
		return nil, err
	}

	queryCost := time.Since(startAnaylize)
	span.Set("query-cost", queryCost.String())

	rangeLeftTime := end.Sub(start)
	metric.TsDBRequestRangeMinute(ctx, rangeLeftTime, i.InstanceType())
	metric.TsDBRequestSecond(
		ctx, queryCost, fmt.Sprintf("%s_http", i.InstanceType()), i.host,
	)
	metric.TsDBRequestBytes(ctx, size, i.InstanceType())

	series := make([]*decoder.Row, 0)
	for _, r := range res.Results {
		if r.Err != "" {
			return nil, errors.New(r.Err)
		}
		series = append(series, r.Series...)
	}

	// 合并排序数据
	series = influxdb.GroupBySeries(ctx, series)
	seriesNum += len(series)

	result := &prompb.QueryResult{
		Timeseries: make([]*prompb.TimeSeries, 0, len(series)),
	}

	for _, s := range series {
		pointNum += len(s.Values)

		lbs := make([]prompb.Label, 0, len(s.Tags)+1)

		// 拼接指标名
		if metricName != "" {
			lbs = append(lbs, prompb.Label{
				Name:  labels.MetricName,
				Value: metricName,
			})
		}

		for k, v := range s.Tags {
			// BkExporter 静态指标名需要替换为真实指标名
			if query.MeasurementType == redis.BkExporter && k == promql.StaticMetricName {
				k = labels.MetricName
				metrics := function.GetRealMetricName(query.DataSource, query.TableID, v)
				if len(metrics) > 0 {
					v = metrics[0]
				}
			}

			lbs = append(lbs, prompb.Label{
				Name:  k,
				Value: v,
			})

		}

		samples := make([]prompb.Sample, 0, len(s.Values))
		for _, sv := range s.Values {
			t, v, rawErr := i.getRawData(s.Columns, sv)
			if rawErr != nil {
				continue
			}
			samples = append(samples, prompb.Sample{
				Value:     v,
				Timestamp: t.UnixMilli(),
			})
		}

		result.Timeseries = append(result.Timeseries, &prompb.TimeSeries{
			Labels:  lbs,
			Samples: samples,
		})
	}

	if pointNum > i.maxLimit {
		metadata.SetStatus(ctx, metadata.ExceedsMaximumLimit, fmt.Sprintf("query points > max: %d", i.maxLimit))
	}
	if seriesNum > i.maxSLimit {
		metadata.SetStatus(ctx, metadata.ExceedsMaximumSlimit, fmt.Sprintf("query series > max: %d", i.maxSLimit))
	}

	span.Set("resp-series-num", seriesNum)
	span.Set("resp-point-num", pointNum)
	return result, nil
}

func (i *Instance) grpcStream(
	ctx context.Context,
	db, rp, measurement, field, where, metricName string,
	slimit, limit int64,
) storage.SeriesSet {
	var (
		client remote.QueryTimeSeriesServiceClient
		err    error
	)

	ctx, span := trace.NewSpan(ctx, "influxdb-query-grpc-stream")
	defer span.End(&err)

	urlPath := fmt.Sprintf("%s:%d", i.host, i.grpcPort)

	user := metadata.GetUser(ctx)
	span.Set("grpc-query-space-uid", user.SpaceUID)
	span.Set("grpc-query-source", user.Source)
	span.Set("grpc-query-username", user.Name)
	span.Set("grpc-query-url-path", urlPath)
	span.Set("grpc-query-db", db)
	span.Set("grpc-query-rp", rp)
	span.Set("grpc-query-measurement", measurement)
	span.Set("grpc-query-field", field)
	span.Set("grpc-query-metric-name", metricName)
	span.Set("grpc-query-where", where)
	span.Set("grpc-query-slimit", int(slimit))
	span.Set("grpc-query-limit", int(limit))

	client = influxdb.GetInfluxDBRouter().TimeSeriesClient(ctx, i.protocol, urlPath)
	if client == nil {
		err = ErrorsHttpNotFound
		return storage.ErrSeriesSet(ErrorsHttpNotFound)
	}

	req := &remote.FilterRequest{
		Db:          db,
		Rp:          rp,
		Measurement: measurement,
		Field:       field,
		Where:       where,
		Slimit:      slimit,
		Limit:       limit,
	}

	filterRequest, _ := json.Marshal(req)
	span.Set("grpc-query-filter-request", string(filterRequest))

	stream, err := client.Raw(ctx, req)
	if err != nil {
		_ = metadata.Sprintf(
			metadata.MsgQueryInfluxDB,
			"查询异常 %+v",
			req,
		).Error(ctx, err)
		return storage.EmptySeriesSet()
	}
	limiter := rate.NewLimiter(rate.Limit(i.readRateLimit), int(i.readRateLimit))

	name := fmt.Sprintf("%s://%s", i.protocol, i.host)

	span.Set("grpc-start-stream-series-set", name)

	seriesSet := StartStreamSeriesSet(
		ctx, name, &StreamSeriesSetOption{
			Span:       span,
			Stream:     stream,
			Limiter:    limiter,
			Timeout:    i.timeout,
			MetricName: metricName,
		},
	)

	return seriesSet
}

// QuerySeriesSet 给 PromEngine 提供查询接口
func (i *Instance) QuerySeriesSet(
	ctx context.Context,
	query *metadata.Query,
	start time.Time,
	end time.Time,
) storage.SeriesSet {
	var err error

	ctx, span := trace.NewSpan(ctx, "influxdb-query-raw")
	defer span.End(&err)

	where := fmt.Sprintf("time > %d and time < %d", start.UnixNano(), end.UnixNano())
	if query.Condition != "" {
		where = fmt.Sprintf("%s and %s", where, query.Condition)
	}

	limit, slimit := i.getLimitAndSlimit(query.OffsetInfo.Limit, query.OffsetInfo.SLimit)

	user := metadata.GetUser(ctx)
	span.Set("query-space-uid", user.SpaceUID)
	span.Set("query-source", user.Source)
	span.Set("query-username", user.Name)

	span.Set("query-storage-id", query.StorageID)
	span.Set("query-cluster-name", query.ClusterName)
	span.Set("query-tag-keys", query.TagsKey)

	span.Set("query-protocol", i.protocol)
	span.Set("query-rate-limit", int(i.readRateLimit))

	span.Set("query-max-limit", i.maxLimit)
	span.Set("query-max-slimit", i.maxSLimit)

	span.Set("query-host", i.host)
	span.Set("query-db", query.DB)
	span.Set("query-measurement", query.Measurement)
	span.Set("query-measurements", strings.Join(query.Measurements, ","))
	span.Set("query-field", query.Field)
	span.Set("query-fields", strings.Join(query.Fields, ","))
	span.Set("query-where", where)

	var sets []storage.SeriesSet
	// 在指标模糊匹配的情况下，需要检索符合条件的 Measures + Fields，这时候会有多个，最后合并结果输出
	multiFieldsFlag := len(query.Measurements) > 1 || len(query.Fields) > 1

	var metricIdx int
	for _, measurement := range query.Measurements {
		for _, field := range query.Fields {
			var (
				set        storage.SeriesSet
				metricName string
			)

			if len(query.MetricNames) > metricIdx {
				metricName = query.MetricNames[metricIdx]
			}
			metricIdx++

			// 判断是否进入降采样逻辑：sum(sum_over_time), count(count_over_time) 等等
			if len(query.Aggregates) == 0 && i.protocol == influxdb.GRPC {
				set = i.grpcStream(ctx, query.DB, query.RetentionPolicy, measurement, field, where, metricName, slimit, limit)
			} else {
				// 复制 ToVmExpand 对象，简化 field、measure 取值，传入查询方法
				mq := &metadata.Query{
					DataSource:      query.DataSource,
					TableID:         query.TableID,
					RetentionPolicy: query.RetentionPolicy,
					DB:              query.DB,
					Measurement:     measurement,
					Field:           field,
					MeasurementType: query.MeasurementType,
					Timezone:        query.Timezone,
					IsHasOr:         query.IsHasOr,
					Aggregates:      query.Aggregates,
					Condition:       query.Condition,
					Filters:         query.Filters,
					OffsetInfo:      query.OffsetInfo,
					SegmentedEnable: query.SegmentedEnable,
				}
				res, err := i.query(ctx, mq, metricName, start, end, multiFieldsFlag)
				if err != nil {
					_ = metadata.Sprintf(
						metadata.MsgQueryInfluxDB,
						"查询异常 %+v",
						mq,
					).Error(ctx, err)
					continue
				}
				set = promRemote.FromQueryResult(true, res)
			}
			sets = append(sets, set)
		}
	}
	if len(sets) == 0 {
		return storage.EmptySeriesSet()
	} else {
		return storage.NewMergeSeriesSet(sets, storage.ChainedSeriesMerge)
	}
}

// QueryRange 查询范围数据
func (i *Instance) DirectQueryRange(
	ctx context.Context, promql string,
	start, end time.Time, step time.Duration,
) (promPromql.Matrix, bool, error) {
	return nil, false, nil
}

// Query instant 查询
func (i *Instance) DirectQuery(
	ctx context.Context, promql string,
	end time.Time,
) (promPromql.Vector, error) {
	return nil, nil
}

func (i *Instance) QueryLabelNames(ctx context.Context, query *metadata.Query, start, end time.Time) ([]string, error) {
	var (
		err    error
		cancel context.CancelFunc

		lbMap = make(map[string]struct{})
	)

	ctx, span := trace.NewSpan(ctx, "influxdb-label-names")
	defer span.End(&err)

	if query != nil {
		var (
			db          = query.DB
			measurement = query.Measurement
			field       = query.Field
			condition   = query.Condition

			res = new(decoder.Response)
		)
		where := fmt.Sprintf("time > %d and time < %d", start.UnixNano(), end.UnixNano())
		if condition != "" {
			where = fmt.Sprintf("%s and %s", where, condition)
		}

		influxql := fmt.Sprintf(
			"show tag keys from %s", measurement,
		)
		if where != "" {
			influxql = fmt.Sprintf("%s where %s", influxql, where)
		}
		values := &url.Values{}
		values.Set("db", db)
		values.Set("q", influxql)

		urlPath := fmt.Sprintf(
			"%s://%s:%d/%s?%s",
			"http", i.host, i.port, "query", values.Encode(),
		)

		span.Set("query-params", values.Encode())
		span.Set("http-url", urlPath)

		ctx, cancel = context.WithTimeout(ctx, i.timeout)
		defer cancel()
		startAnaylize := time.Now()

		user := metadata.GetUser(ctx)
		span.Set("query-space-uid", user.SpaceUID)
		span.Set("query-source", user.Source)
		span.Set("query-username", user.Name)
		span.Set("query-url-path", urlPath)
		span.Set("query-db", db)
		span.Set("query-measurement", measurement)
		span.Set("query-field", field)
		span.Set("query-url-path", urlPath)
		span.Set("query-where", where)

		log.Debugf(ctx,
			"influxdb query: %s, where: %s",
			urlPath, where,
		)
		dec, err := decoder.GetDecoder(i.contentType)
		if err != nil {
			_ = metadata.Sprintf(
				metadata.MsgQueryInfluxDB,
				"解析器 %s 解析异常",
				i.contentType,
			).Error(ctx, err)
			return nil, err
		}

		i.curl.WithDecoder(func(ctx context.Context, reader io.Reader, resp any) (int, error) {
			dr := resp.(*decoder.Response)
			return dec.Decode(ctx, reader, dr)
		})

		size, err := i.curl.Request(
			ctx, curl.Get,
			curl.Options{
				UrlPath: urlPath,
				Headers: map[string]string{
					ContentType: i.contentType,
				},
				UserName: i.username,
				Password: i.password,
			},
			res,
		)
		metric.TsDBRequestBytes(ctx, size, i.InstanceType())

		span.Set("query-cost", time.Since(startAnaylize).String())

		if res.Err != "" {
			return nil, fmt.Errorf(res.Err)
		}

		respNum := 0
		for _, r := range res.Results {
			for _, s := range r.Series {
				for _, v := range s.Values {
					if len(v) > 0 {
						value := v[0].(string)
						if value != "" {
							lbMap[value] = struct{}{}
							respNum++
						}
					}
				}
			}
		}

		span.Set("resp-num", respNum)
	}

	lbs := make([]string, 0, len(lbMap))
	for k := range lbMap {
		lbs = append(lbs, k)
	}

	return lbs, err
}

func (i *Instance) metrics(ctx context.Context, query *metadata.Query) ([]string, error) {
	var (
		err    error
		cancel context.CancelFunc

		db = query.DB

		measurement = query.Measurement
		field       = query.Field

		sql string
		res = new(decoder.Response)
	)
	ctx, span := trace.NewSpan(ctx, "influxdb-metrics")
	defer span.End(&err)

	if field == "value" {
		sql = "show measurements"
	} else if field == "metric_value" {
		sql = fmt.Sprintf(`show tag values from %s with key="metric_name"`, influxql.QuoteIdent(measurement))
	} else {
		sql = fmt.Sprintf(`show field keys from %s`, influxql.QuoteIdent(measurement))
	}

	values := &url.Values{}
	values.Set("db", db)
	values.Set("q", sql)

	urlPath := fmt.Sprintf(
		"%s://%s:%d/%s?%s",
		"http", i.host, i.port, "query", values.Encode(),
	)

	span.Set("query-params", values.Encode())
	span.Set("http-url", urlPath)

	ctx, cancel = context.WithTimeout(ctx, i.timeout)
	defer cancel()
	startAnaylize := time.Now()

	user := metadata.GetUser(ctx)
	span.Set("query-space-uid", user.SpaceUID)
	span.Set("query-source", user.Source)
	span.Set("query-username", user.Name)
	span.Set("query-url-path", urlPath)
	span.Set("query-q", sql)
	span.Set("query-db", db)
	span.Set("query-measurement", measurement)
	span.Set("query-field", field)
	span.Set("query-url-path", urlPath)

	log.Debugf(ctx,
		"influxdb query: %s", urlPath,
	)
	dec, err := decoder.GetDecoder(i.contentType)
	if err != nil {
		_ = metadata.Sprintf(
			metadata.MsgQueryInfluxDB,
			"解析器 %s 解析异常",
			i.contentType,
		).Error(ctx, err)
		return nil, err
	}

	i.curl.WithDecoder(func(ctx context.Context, reader io.Reader, resp any) (int, error) {
		dr := resp.(*decoder.Response)
		return dec.Decode(ctx, reader, dr)
	})

	size, err := i.curl.Request(
		ctx, curl.Get,
		curl.Options{
			UrlPath: urlPath,
			Headers: map[string]string{
				ContentType: i.contentType,
			},
			UserName: i.username,
			Password: i.password,
		},
		res,
	)
	metric.TsDBRequestBytes(ctx, size, i.InstanceType())

	span.Set("query-cost", time.Since(startAnaylize).String())

	if res.Err != "" {
		return nil, fmt.Errorf(res.Err)
	}
	lbs := make([]string, 0)
	for _, r := range res.Results {
		for _, s := range r.Series {
			for _, v := range s.Values {
				if len(v) < 1 {
					continue
				}

				value := v[0].(string)
				if value == "" {
					continue
				}

				// metric_name 结构取后面的 values 数值
				if value == "metric_name" {
					if len(v) < 2 {
						continue
					}
					if val := v[1].(string); val != "" {
						lbs = append(lbs, val)
					}
				} else {
					lbs = append(lbs, value)
				}
			}
		}
	}

	span.Set("resp-num", len(lbs))

	return lbs, err
}

func (i *Instance) QueryLabelValues(ctx context.Context, query *metadata.Query, name string, start, end time.Time) ([]string, error) {
	var (
		err    error
		cancel context.CancelFunc

		lbMap = make(map[string]struct{})
	)

	ctx, span := trace.NewSpan(ctx, "influxdb-label-values")
	defer span.End(&err)

	var (
		db          = query.DB
		measurement = query.Measurement
		field       = query.Field
		condition   = query.Condition

		res = new(decoder.Response)
	)

	if name == labels.MetricName {
		lbs, err := i.metrics(ctx, query)
		if err != nil {
			return nil, err
		}

		for _, lb := range lbs {
			lbMap[lb] = struct{}{}
		}
	} else {
		where := fmt.Sprintf("time > %d and time < %d", start.UnixNano(), end.UnixNano())
		if condition != "" {
			where = fmt.Sprintf("%s and %s", where, condition)
		}

		if field == "" {
			field = "*"
		}
		sql := fmt.Sprintf(
			"select count(%s) from %s", field, influxql.QuoteIdent(measurement),
		)
		if where != "" {
			sql = fmt.Sprintf("%s where %s", sql, where)
		}
		sql = fmt.Sprintf("%s group by %s", sql, influxql.QuoteIdent(name))

		values := &url.Values{}
		values.Set("db", db)
		values.Set("q", sql)

		urlPath := fmt.Sprintf(
			"%s://%s:%d/%s?%s",
			"http", i.host, i.port, "query", values.Encode(),
		)

		span.Set("query-params", values.Encode())
		span.Set("http-url", urlPath)

		ctx, cancel = context.WithTimeout(ctx, i.timeout)
		defer cancel()
		startAnaylize := time.Now()

		user := metadata.GetUser(ctx)

		span.Set("query-space-uid", user.SpaceUID)
		span.Set("query-source", user.Source)
		span.Set("query-username", user.Name)
		span.Set("query-url-path", urlPath)
		span.Set("query-q", sql)
		span.Set("query-db", db)
		span.Set("query-measurement", measurement)
		span.Set("query-field", field)
		span.Set("query-url-path", urlPath)
		span.Set("query-where", where)

		log.Debugf(ctx,
			"influxdb query: %s, where: %s",
			urlPath, where,
		)
		dec, err := decoder.GetDecoder(i.contentType)
		if err != nil {
			_ = metadata.Sprintf(
				metadata.MsgQueryInfluxDB,
				"解析器 %s 解析异常",
				i.contentType,
			).Error(ctx, err)
			return nil, err
		}

		i.curl.WithDecoder(func(ctx context.Context, reader io.Reader, resp any) (int, error) {
			dr := resp.(*decoder.Response)
			return dec.Decode(ctx, reader, dr)
		})

		size, err := i.curl.Request(
			ctx, curl.Get,
			curl.Options{
				UrlPath: urlPath,
				Headers: map[string]string{
					ContentType: i.contentType,
				},
				UserName: i.username,
				Password: i.password,
			},
			res,
		)
		metric.TsDBRequestBytes(ctx, size, i.InstanceType())

		span.Set("query-cost", time.Since(startAnaylize).String())
		span.Set("response-size", size)

		if res.Err != "" {
			return nil, fmt.Errorf(res.Err)
		}

		respNum := 0
		for _, r := range res.Results {
			for _, s := range r.Series {
				if v, ok := s.Tags[name]; ok {
					if v != "" {
						lbMap[v] = struct{}{}
						respNum++
					}
				}
			}
		}

		span.Set("resp-num", respNum)
	}

	lbs := make([]string, 0, len(lbMap))
	for k := range lbMap {
		lbs = append(lbs, k)
	}

	return lbs, err
}

func (i *Instance) QuerySeries(ctx context.Context, query *metadata.Query, start, end time.Time) (series []map[string]string, err error) {
	ss := i.QuerySeriesSet(ctx, query, start, end)

	if ss.Err() != nil {
		err = ss.Err()
		return series, err
	}

	series = make([]map[string]string, 0)
	for ss.Next() {
		seriesMap := make(map[string]string)
		for _, lb := range ss.At().Labels() {
			seriesMap[lb.Name] = lb.Value
		}
		series = append(series, seriesMap)
	}

	return series, nil
}

func (i *Instance) DirectLabelNames(ctx context.Context, start, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
	// TODO implement me
	panic("implement me")
}

func (i *Instance) DirectLabelValues(ctx context.Context, name string, start, end time.Time, limit int, matchers ...*labels.Matcher) ([]string, error) {
	// TODO implement me
	panic("implement me")
}
