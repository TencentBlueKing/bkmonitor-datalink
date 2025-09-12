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
	"encoding/json"
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
	"github.com/prometheus/prometheus/promql"
	promPromql "github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
	promRemote "github.com/prometheus/prometheus/storage/remote"
	oleltrace "go.opentelemetry.io/otel/trace"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
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

	BKTaskIndex = "bk_task_index"
)

var (
	ErrorsHttpNotFound   = errors.New("404 Not Found")
	ErrorsNotDownSampled = errors.New("not downsampled")

	MapGrpcConn = make(map[string]*grpc.ClientConn)
)

// NewInstance 初始化引擎
func NewInstance(ctx context.Context, opt Options) *Instance {
	headers := map[string]string{}
	if opt.Accept != "" {
		headers[ContentType] = opt.Accept
	}
	if opt.AcceptEncoding != "" {
		headers[ContentEncoding] = opt.AcceptEncoding
	}

	return &Instance{
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
}

var _ tsdb.Instance = (*Instance)(nil)

// GetInstanceType 获取引擎类型
func (i *Instance) GetInstanceType() string {
	return consul.InfluxDBStorageType
}

func (i *Instance) QueryExemplar(ctx context.Context, fields []string, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) (*decoder.Response, error) {
	var (
		cancel        context.CancelFunc
		span          oleltrace.Span
		startAnaylize time.Time

		sLimitStr string
		limitStr  string
	)
	ctx, span = trace.IntoContext(ctx, trace.TracerName, "influxdb-influxql-query-exemplar")
	if span != nil {
		defer span.End()
	}
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
		i.protocol, i.host, i.port, "query", values.Encode(),
	)

	trace.InsertStringIntoSpan("query-params", values.Encode(), span)
	trace.InsertStringIntoSpan("http-url", urlPath, span)

	ctx, cancel = context.WithTimeout(ctx, i.timeout)
	defer cancel()

	user := metadata.GetUser(ctx)
	trace.InsertStringIntoSpan("query-space-uid", user.SpaceUid, span)
	trace.InsertStringIntoSpan("query-source", user.Source, span)
	trace.InsertStringIntoSpan("query-username", user.Name, span)
	trace.InsertStringIntoSpan("query-url-path", urlPath, span)
	trace.InsertStringIntoSpan("query-q", influxql, span)
	trace.InsertStringIntoSpan("query-db", query.DB, span)
	trace.InsertStringIntoSpan("query-measurement", query.Measurement, span)
	trace.InsertStringIntoSpan("query-field", query.Field, span)
	trace.InsertStringIntoSpan("query-url-path", urlPath, span)
	trace.InsertStringIntoSpan("query-where", where, span)
	trace.InsertStringIntoSpan("query-cost", time.Since(startAnaylize).String(), span)

	log.Infof(ctx,
		"influxdb query: %s, where: %s",
		urlPath, where,
	)

	resp, err := i.curl.Request(
		ctx, curl.Get,
		curl.Options{
			UrlPath: urlPath,
			Headers: map[string]string{
				ContentType: i.contentType,
			},
			UserName: i.username,
			Password: i.password,
		},
	)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respContentType := resp.Header.Get("Content-type")
	dec, err := decoder.GetDecoder(respContentType)
	if err != nil {
		data, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			log.Errorf(ctx, "get decoder:%s error:%s and read error:%s", respContentType, err, readErr)
			return nil, err
		}
		log.Errorf(ctx, "get decoder:%s error:%s,data in body:%s", respContentType, err, data)
		return nil, err
	}
	res, err := dec.Decode(ctx, resp.Body)
	if err != nil {
		log.Errorf(ctx, "decoder:%s decode error:%s", respContentType, err)
		return nil, err
	}
	return res, nil
}

func (i *Instance) getRawData(columns []string, data []interface{}) (time.Time, float64, error) {
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
	default:
		log.Errorf(context.TODO(),
			"get time type failed, type: %T, data: %+v, timeColumnIndex: %d",
			data[timeColumnIndex], data, timeColumnIndex,
		)
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
	default:
		log.Errorf(context.TODO(),
			"get value type failed, type: %T, data: %+v, resultColumnIndex: %d",
			data[valueColumnIndex], data, valueColumnIndex,
		)
	}

	return t, v, nil
}

// getLimitAndSlimit 获取真实的 limit 和 slimit
func (i *Instance) getLimitAndSlimit(limit, slimit int) (int64, int64) {
	var (
		resultLimit, resultSLimit int
	)

	if limit > 0 {
		resultLimit = limit
	}
	if limit == 0 || limit > i.maxLimit {
		resultLimit = i.maxLimit + i.tolerance
	}

	if slimit > 0 {
		resultSLimit = slimit
	}
	if slimit == 0 || slimit > i.maxSLimit {
		resultSLimit = i.maxSLimit + i.tolerance
	}

	return int64(resultLimit), int64(resultSLimit)
}

func (i *Instance) downSampleCheck(
	ctx context.Context,
	query *metadata.Query,
	hints *storage.SelectHints,
	matchers ...*labels.Matcher,
) bool {
	newFuncName, _, _ := query.GetDownSampleFunc(hints)
	return newFuncName != ""
}

func (i *Instance) query(
	ctx context.Context,
	query *metadata.Query,
	hints *storage.SelectHints,
	matchers ...*labels.Matcher,
) (*prompb.QueryResult, error) {
	var (
		cancel        context.CancelFunc
		span          oleltrace.Span
		startAnaylize time.Time

		seriesNum = 0
		pointNum  = 0

		isCount   bool
		sLimitStr string
		limitStr  string
		timezone  string

		withTag     = ",*::tag"
		aggField    string
		groupingStr string

		expandTag []prompb.Label
	)
	ctx, span = trace.IntoContext(ctx, trace.TracerName, "influxdb-influxql-query-raw")
	if span != nil {
		defer span.End()
	}

	newFuncName, window, dims := query.GetDownSampleFunc(hints)
	if newFuncName != "" {
		groupList := make([]string, 0, len(dims)+1)
		if len(dims) > 0 {
			for _, dim := range dims {
				group := dim
				if group != "*" {
					group = fmt.Sprintf(`"%s"`, group)
				}
				groupList = append(groupList, group)
			}
		}

		if window > 0 {
			groupList = append(groupList, "time("+window.String()+")")
		}
		if len(groupList) > 0 {
			groupingStr = " group by " + strings.Join(groupList, ", ")
		}

		isCount = newFuncName == metadata.COUNT
		withTag = ""
		aggField = fmt.Sprintf(`%s("%s")`, newFuncName, query.Field)

		bkTaskIndex := query.TableID
		if bkTaskIndex == "" {
			bkTaskIndex = fmt.Sprintf("%s_%s", query.DB, query.Measurement)
		}
		expandTag = []prompb.Label{
			{
				Name:  BKTaskIndex,
				Value: bkTaskIndex,
			},
		}
	} else {
		aggField = fmt.Sprintf(`"%s"`, query.Field)
	}

	where := fmt.Sprintf("time > %d and time < %d", hints.Start*1e6, hints.End*1e6)
	if query.Condition != "" {
		where = fmt.Sprintf("%s and %s", where, query.Condition)
	}

	limit, slimit := i.getLimitAndSlimit(query.OffsetInfo.Limit, query.OffsetInfo.SLimit)

	if limit > 0 {
		sLimitStr = fmt.Sprintf(` slimit %d`, slimit)
	}
	if slimit > 0 {
		limitStr = fmt.Sprintf(` limit %d`, limit)
	}
	if query.Timezone != "" {
		timezone = fmt.Sprintf(` tz('%s')`, query.Timezone)
	}

	sql := fmt.Sprintf(
		"select %s as %s, time as %s%s from %s where %s %s%s%s%s",
		aggField, influxdb.ResultColumnName, influxdb.TimeColumnName, withTag, influxql.QuoteIdent(query.Measurement),
		where, groupingStr, limitStr, sLimitStr, timezone,
	)

	values := &url.Values{}
	values.Set("db", query.DB)
	values.Set("q", sql)

	if i.chunkSize > 0 {
		values.Set("chunked", "true")
		values.Set("chunk_size", fmt.Sprintf("%d", i.chunkSize))
	}

	urlPath := fmt.Sprintf(
		"%s://%s:%d/%s?%s",
		i.protocol, i.host, i.port, "query", values.Encode(),
	)

	trace.InsertStringIntoSpan("query-params", values.Encode(), span)
	trace.InsertStringIntoSpan("http-url", urlPath, span)

	ctx, cancel = context.WithTimeout(ctx, i.timeout)
	defer cancel()
	startAnaylize = time.Now()

	user := metadata.GetUser(ctx)
	trace.InsertStringIntoSpan("query-space-uid", user.SpaceUid, span)
	trace.InsertStringIntoSpan("query-source", user.Source, span)
	trace.InsertStringIntoSpan("query-username", user.Name, span)
	trace.InsertStringIntoSpan("query-url-path", urlPath, span)
	trace.InsertStringIntoSpan("query-q", sql, span)
	trace.InsertStringIntoSpan("query-db", query.DB, span)
	trace.InsertStringIntoSpan("query-measurement", query.Measurement, span)
	trace.InsertStringIntoSpan("query-field", query.Field, span)
	trace.InsertStringIntoSpan("query-url-path", urlPath, span)
	trace.InsertStringIntoSpan("query-where", where, span)

	log.Infof(ctx,
		"influxdb query: %s, where: %s",
		urlPath, where,
	)

	resp, err := i.curl.Request(
		ctx, curl.Get,
		curl.Options{
			UrlPath: urlPath,
			Headers: map[string]string{
				ContentType: i.contentType,
			},
			UserName: i.username,
			Password: i.password,
		},
	)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respContentType := resp.Header.Get("Content-type")
	dec, err := decoder.GetDecoder(respContentType)
	if err != nil {
		data, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			log.Errorf(ctx, "get decoder:%s error:%s and read error:%s", respContentType, err, readErr)
			return nil, err
		}
		log.Errorf(ctx, "get decoder:%s error:%s,data in body:%s", respContentType, err, data)
		return nil, err
	}

	res, err := dec.Decode(ctx, resp.Body)
	if err != nil {
		log.Errorf(ctx, "decoder:%s decode error:%s", respContentType, err)
		return nil, err
	}

	queryCost := time.Since(startAnaylize)
	trace.InsertStringIntoSpan("query-cost", queryCost.String(), span)

	metric.TsDBRequestSecond(
		ctx, queryCost, user.SpaceUid, fmt.Sprintf("%s_http", consul.InfluxDBStorageType),
	)

	series := make([]*decoder.Row, 0)
	for _, r := range res.Results {
		if r.Err != "" {
			return nil, err
		}
		series = append(series, r.Series...)
	}

	// 合并排序数据
	series = influxdb.GroupBySeries(ctx, series)
	seriesNum += len(series)

	result := &prompb.QueryResult{
		Timeseries: make([]*prompb.TimeSeries, 0, len(series)),
	}

	trace.InsertStringIntoSpan("expand-tag", fmt.Sprintf("%+v", expandTag), span)

	for _, s := range series {
		pointNum += len(s.Values)

		lbs := make([]prompb.Label, 0, len(s.Tags)+len(expandTag))
		for k, v := range s.Tags {
			lbs = append(lbs, prompb.Label{
				Name:  k,
				Value: v,
			})
		}

		if len(expandTag) > 0 {
			lbs = append(lbs, expandTag...)
		}

		samples := make([]prompb.Sample, 0, len(s.Values))
		for _, sv := range s.Values {
			t, v, err := i.getRawData(s.Columns, sv)
			if err != nil {
				continue
			}
			repNum := 1
			if isCount {
				repNum = int(v)
			}
			for j := 0; j < repNum; j++ {
				samples = append(samples, prompb.Sample{
					Value:     v,
					Timestamp: t.UnixMilli(),
				})
			}
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

	trace.InsertIntIntoSpan("resp-series-num", seriesNum, span)
	trace.InsertIntIntoSpan("resp-point-num", pointNum, span)
	return result, nil
}

func (i *Instance) grpcStream(
	ctx context.Context,
	db, rp, measurement, field, where string,
	slimit, limit int64,
) storage.SeriesSet {
	var (
		span   oleltrace.Span
		client remote.QueryTimeSeriesServiceClient
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "influxdb-query-raw-grpc-stream")

	urlPath := fmt.Sprintf("%s:%d", i.host, i.grpcPort)

	user := metadata.GetUser(ctx)
	trace.InsertStringIntoSpan("query-space-uid", user.SpaceUid, span)
	trace.InsertStringIntoSpan("query-source", user.Source, span)
	trace.InsertStringIntoSpan("query-username", user.Name, span)
	trace.InsertStringIntoSpan("query-url-path", urlPath, span)
	trace.InsertStringIntoSpan("query-db", db, span)
	trace.InsertStringIntoSpan("query-rp", rp, span)
	trace.InsertStringIntoSpan("query-measurement", measurement, span)
	trace.InsertStringIntoSpan("query-field", field, span)
	trace.InsertStringIntoSpan("query-where", where, span)
	trace.InsertIntIntoSpan("query-slimit", int(slimit), span)
	trace.InsertIntIntoSpan("query-limit", int(limit), span)

	client = influxdb.GetInfluxDBRouter().TimeSeriesClient(ctx, i.protocol, urlPath)
	if client == nil {
		log.Errorf(ctx, ErrorsHttpNotFound.Error())
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
	trace.InsertStringIntoSpan("query-filter-request", string(filterRequest), span)

	stream, err := client.Raw(ctx, req)
	if err != nil {
		log.Errorf(ctx, err.Error())
		return storage.EmptySeriesSet()
	}
	limiter := rate.NewLimiter(rate.Limit(i.readRateLimit), int(i.readRateLimit))

	name := fmt.Sprintf("%s://%s", i.protocol, i.host)

	trace.InsertStringIntoSpan("start-stream-series-set", name, span)
	seriesSet := StartStreamSeriesSet(
		ctx, name, &StreamSeriesSetOption{
			Span:    span,
			Stream:  stream,
			Limiter: limiter,
			Timeout: i.timeout,
		},
	)

	return seriesSet
}

// QueryRaw 查询原始数据
func (i *Instance) QueryRaw(
	ctx context.Context,
	query *metadata.Query,
	hints *storage.SelectHints,
	matchers ...*labels.Matcher,
) storage.SeriesSet {
	var (
		span oleltrace.Span
		err  error
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "influxdb-query-raw")
	if span != nil {
		defer span.End()
	}

	where := fmt.Sprintf("time > %d and time < %d", hints.Start*1e6, hints.End*1e6)
	if query.Condition != "" {
		where = fmt.Sprintf("%s and %s", where, query.Condition)
	}

	limit, slimit := i.getLimitAndSlimit(query.OffsetInfo.Limit, query.OffsetInfo.SLimit)

	user := metadata.GetUser(ctx)
	trace.InsertStringIntoSpan("query-space-uid", user.SpaceUid, span)
	trace.InsertStringIntoSpan("query-source", user.Source, span)
	trace.InsertStringIntoSpan("query-username", user.Name, span)

	trace.InsertStringIntoSpan("query-storage-id", query.StorageID, span)
	trace.InsertStringIntoSpan("query-cluster-name", query.ClusterName, span)
	trace.InsertStringIntoSpan("query-tag-keys", fmt.Sprintf("%+v", query.TagsKey), span)

	trace.InsertStringIntoSpan("query-protocol", i.protocol, span)
	trace.InsertIntIntoSpan("query-rate-limit", int(i.readRateLimit), span)

	trace.InsertIntIntoSpan("query-max-limit", i.maxLimit, span)
	trace.InsertIntIntoSpan("query-max-slimit", i.maxSLimit, span)

	trace.InsertStringIntoSpan("query-host", i.host, span)
	trace.InsertStringIntoSpan("query-db", query.DB, span)
	trace.InsertStringIntoSpan("query-measurement", query.Measurement, span)
	trace.InsertStringIntoSpan("query-field", query.Field, span)
	trace.InsertStringIntoSpan("query-where", where, span)

	// 判断是否进入降采样逻辑：sum(sum_over_time), count(count_over_time) 等等
	if !i.downSampleCheck(ctx, query, hints) {
		if i.protocol == influxdb.GRPC {
			return i.grpcStream(ctx, query.DB, query.RetentionPolicy, query.Measurement, query.Field, where, slimit, limit)
		}
	}

	res, err := i.query(ctx, query, hints, matchers...)
	if err != nil {
		log.Errorf(ctx, err.Error())
		return storage.EmptySeriesSet()
	}
	return promRemote.FromQueryResult(true, res)
}

// QueryRange 查询范围数据
func (i *Instance) QueryRange(
	ctx context.Context, promql string,
	start, end time.Time, step time.Duration,
) (promPromql.Matrix, error) {
	return nil, nil
}

// Query instant 查询
func (i *Instance) Query(
	ctx context.Context, promql string,
	end time.Time,
) (promql.Vector, error) {
	return nil, nil
}

func (i *Instance) LabelNames(ctx context.Context, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
	var (
		span   oleltrace.Span
		err    error
		cancel context.CancelFunc

		lbMap = make(map[string]struct{})
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "influxdb-label-names")
	if span != nil {
		defer span.End()
	}

	if query != nil {
		var (
			db          = query.DB
			measurement = query.Measurement
			field       = query.Field
			condition   = query.Condition
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
			i.protocol, i.host, i.port, "query", values.Encode(),
		)

		trace.InsertStringIntoSpan("query-params", values.Encode(), span)
		trace.InsertStringIntoSpan("http-url", urlPath, span)

		ctx, cancel = context.WithTimeout(ctx, i.timeout)
		defer cancel()
		startAnaylize := time.Now()

		user := metadata.GetUser(ctx)
		trace.InsertStringIntoSpan("query-space-uid", user.SpaceUid, span)
		trace.InsertStringIntoSpan("query-source", user.Source, span)
		trace.InsertStringIntoSpan("query-username", user.Name, span)
		trace.InsertStringIntoSpan("query-url-path", urlPath, span)
		trace.InsertStringIntoSpan("query-db", db, span)
		trace.InsertStringIntoSpan("query-measurement", measurement, span)
		trace.InsertStringIntoSpan("query-field", field, span)
		trace.InsertStringIntoSpan("query-url-path", urlPath, span)
		trace.InsertStringIntoSpan("query-where", where, span)

		log.Infof(ctx,
			"influxdb query: %s, where: %s",
			urlPath, where,
		)

		resp, err := i.curl.Request(
			ctx, curl.Get,
			curl.Options{
				UrlPath: urlPath,
				Headers: map[string]string{
					ContentType: i.contentType,
				},
				UserName: i.username,
				Password: i.password,
			},
		)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		respContentType := resp.Header.Get("Content-type")
		dec, err := decoder.GetDecoder(respContentType)
		if err != nil {
			data, readErr := io.ReadAll(resp.Body)
			if readErr != nil {
				log.Errorf(ctx, "get decoder:%s error:%s and read error:%s", respContentType, err, readErr)
				return nil, err
			}
			log.Errorf(ctx, "get decoder:%s error:%s,data in body:%s", respContentType, err, data)
			return nil, err
		}
		res, err := dec.Decode(ctx, resp.Body)
		if err != nil {
			log.Errorf(ctx, "decoder:%s decode error:%s", respContentType, err)
			return nil, err
		}

		trace.InsertStringIntoSpan("query-cost", time.Since(startAnaylize).String(), span)

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

		trace.InsertIntIntoSpan("resp-num", respNum, span)
	}

	lbs := make([]string, 0, len(lbMap))
	for k := range lbMap {
		lbs = append(lbs, k)
	}

	return lbs, err
}

func (i *Instance) metrics(ctx context.Context, query *metadata.Query) ([]string, error) {
	var (
		span   oleltrace.Span
		err    error
		cancel context.CancelFunc

		db = query.DB

		measurement = query.Measurement
		field       = query.Field

		sql string
	)
	ctx, span = trace.IntoContext(ctx, trace.TracerName, "influxdb-metrics")
	if span != nil {
		defer span.End()
	}

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
		i.protocol, i.host, i.port, "query", values.Encode(),
	)

	trace.InsertStringIntoSpan("query-params", values.Encode(), span)
	trace.InsertStringIntoSpan("http-url", urlPath, span)

	ctx, cancel = context.WithTimeout(ctx, i.timeout)
	defer cancel()
	startAnaylize := time.Now()

	user := metadata.GetUser(ctx)
	trace.InsertStringIntoSpan("query-space-uid", user.SpaceUid, span)
	trace.InsertStringIntoSpan("query-source", user.Source, span)
	trace.InsertStringIntoSpan("query-username", user.Name, span)
	trace.InsertStringIntoSpan("query-url-path", urlPath, span)
	trace.InsertStringIntoSpan("query-q", sql, span)
	trace.InsertStringIntoSpan("query-db", db, span)
	trace.InsertStringIntoSpan("query-measurement", measurement, span)
	trace.InsertStringIntoSpan("query-field", field, span)
	trace.InsertStringIntoSpan("query-url-path", urlPath, span)

	log.Infof(ctx,
		"influxdb query: %s", urlPath,
	)

	resp, err := i.curl.Request(
		ctx, curl.Get,
		curl.Options{
			UrlPath: urlPath,
			Headers: map[string]string{
				ContentType: i.contentType,
			},
			UserName: i.username,
			Password: i.password,
		},
	)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respContentType := resp.Header.Get("Content-type")
	dec, err := decoder.GetDecoder(respContentType)
	if err != nil {
		data, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			log.Errorf(ctx, "get decoder:%s error:%s and read error:%s", respContentType, err, readErr)
			return nil, err
		}
		log.Errorf(ctx, "get decoder:%s error:%s,data in body:%s", respContentType, err, data)
		return nil, err
	}
	res, err := dec.Decode(ctx, resp.Body)
	if err != nil {
		log.Errorf(ctx, "decoder:%s decode error:%s", respContentType, err)
		return nil, err
	}

	trace.InsertStringIntoSpan("query-cost", time.Since(startAnaylize).String(), span)

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

	trace.InsertIntIntoSpan("resp-num", len(lbs), span)

	return lbs, err
}

func (i *Instance) LabelValues(ctx context.Context, query *metadata.Query, name string, start, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
	var (
		span   oleltrace.Span
		err    error
		cancel context.CancelFunc

		lbMap = make(map[string]struct{})
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "influxdb-label-values")
	if span != nil {
		defer span.End()
	}

	var (
		db          = query.DB
		measurement = query.Measurement
		field       = query.Field
		condition   = query.Condition
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
			i.protocol, i.host, i.port, "query", values.Encode(),
		)

		trace.InsertStringIntoSpan("query-params", values.Encode(), span)
		trace.InsertStringIntoSpan("http-url", urlPath, span)

		ctx, cancel = context.WithTimeout(ctx, i.timeout)
		defer cancel()
		startAnaylize := time.Now()

		user := metadata.GetUser(ctx)

		trace.InsertStringIntoSpan("query-space-uid", user.SpaceUid, span)
		trace.InsertStringIntoSpan("query-source", user.Source, span)
		trace.InsertStringIntoSpan("query-username", user.Name, span)
		trace.InsertStringIntoSpan("query-url-path", urlPath, span)
		trace.InsertStringIntoSpan("query-q", sql, span)
		trace.InsertStringIntoSpan("query-db", db, span)
		trace.InsertStringIntoSpan("query-measurement", measurement, span)
		trace.InsertStringIntoSpan("query-field", field, span)
		trace.InsertStringIntoSpan("query-url-path", urlPath, span)
		trace.InsertStringIntoSpan("query-where", where, span)

		log.Infof(ctx,
			"influxdb query: %s, where: %s",
			urlPath, where,
		)

		resp, err := i.curl.Request(
			ctx, curl.Get,
			curl.Options{
				UrlPath: urlPath,
				Headers: map[string]string{
					ContentType: i.contentType,
				},
				UserName: i.username,
				Password: i.password,
			},
		)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		respContentType := resp.Header.Get("Content-type")
		dec, err := decoder.GetDecoder(respContentType)
		if err != nil {
			data, readErr := io.ReadAll(resp.Body)
			if readErr != nil {
				log.Errorf(ctx, "get decoder:%s error:%s and read error:%s", respContentType, err, readErr)
				return nil, err
			}
			log.Errorf(ctx, "get decoder:%s error:%s,data in body:%s", respContentType, err, data)
			return nil, err
		}
		res, err := dec.Decode(ctx, resp.Body)
		if err != nil {
			log.Errorf(ctx, "decoder:%s decode error:%s", respContentType, err)
			return nil, err
		}

		trace.InsertStringIntoSpan("query-cost", time.Since(startAnaylize).String(), span)

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

		trace.InsertIntIntoSpan("resp-num", respNum, span)
	}

	lbs := make([]string, 0, len(lbMap))
	for k := range lbMap {
		lbs = append(lbs, k)
	}

	return lbs, err
}

func (i *Instance) Series(ctx context.Context, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) storage.SeriesSet {
	return nil
}
