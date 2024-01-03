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
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
	"github.com/prometheus/prometheus/storage"
	oleltrace "go.opentelemetry.io/otel/trace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/infos"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/prometheus"
)

// HandlerFieldKeys
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
func HandlerFieldKeys(c *gin.Context) {
	handlerInfo(c, infos.FieldKeys)
}

func HandlerTagKeys(c *gin.Context) {
	handlerInfo(c, infos.TagKeys)
}

func HandlerTagValues(c *gin.Context) {
	handlerInfo(c, infos.TagValues)
}

func HandlerSeries(c *gin.Context) {
	handlerInfo(c, infos.Series)
}

func HandlerLabelValues(c *gin.Context) {
	var (
		key  = infos.TagValues
		ctx  = c.Request.Context()
		resp = &response{
			c: c,
		}
		span oleltrace.Span

		err  error
		data interface{}
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "label-values-handler")
	if span != nil {
		defer span.End()
	}

	labelName := c.Param("label_name")

	params := &infos.Params{
		Keys:  []string{labelName},
		Start: c.Query("start"),
		End:   c.Query("end"),
	}

	matches := c.QueryArray("match[]")

	trace.InsertStringIntoSpan("request-url", c.Request.URL.String(), span)
	trace.InsertStringIntoSpan("request-info-type", string(key), span)
	trace.InsertStringIntoSpan("request-header", fmt.Sprintf("%+v", c.Request.Header), span)
	trace.InsertStringIntoSpan("request-label-name", labelName, span)
	trace.InsertStringIntoSpan("request-match[]", fmt.Sprintf("%+v", matches), span)

	for _, m := range matches {
		match, err := parser.ParseMetricSelector(m)
		if err != nil {
			log.Errorf(ctx, err.Error())
			resp.failed(ctx, err)
			return
		}
		metric, fields, err := structured.LabelMatcherToConditions(match)

		if metric != "" {
			route, err := structured.MakeRouteFromMetricName(metric)
			if err != nil {
				log.Errorf(ctx, err.Error())
				resp.failed(ctx, err)
				return
			}

			params.TableID = route.TableID()
			params.Metric = route.MetricName()
		}

		params.Conditions.FieldList = append(params.Conditions.FieldList, fields...)
	}

	for i := 0; i < len(params.Conditions.FieldList)-1; i++ {
		params.Conditions.ConditionList = append(params.Conditions.ConditionList, structured.ConditionAnd)
	}

	trace.InsertStringIntoSpan("params", fmt.Sprintf("%+v", params), span)

	data, err = queryInfo(ctx, key, params)
	if err != nil {
		log.Errorf(ctx, err.Error())
		resp.failed(ctx, err)
		return
	}

	resp.success(ctx, data)
}

func handlerInfo(c *gin.Context, key infos.InfoType) {
	var (
		ctx  = c.Request.Context()
		resp = &response{
			c: c,
		}
		span oleltrace.Span
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "info-handler")
	if span != nil {
		defer span.End()
	}
	params := &infos.Params{}
	json.NewDecoder(c.Request.Body).Decode(params)

	paramsStr, _ := json.Marshal(params)
	trace.InsertStringIntoSpan("request-url", c.Request.URL.String(), span)
	trace.InsertStringIntoSpan("request-info-type", string(key), span)
	trace.InsertStringIntoSpan("request-header", fmt.Sprintf("%+v", c.Request.Header), span)
	trace.InsertStringIntoSpan("request-data", string(paramsStr), span)

	data, err := queryInfo(ctx, key, params)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	resp.success(ctx, data)
}

func queryInfo(ctx context.Context, key infos.InfoType, params *infos.Params) (interface{}, error) {
	var (
		span oleltrace.Span

		warns []error
		data  interface{}
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "query-info")
	if span != nil {
		defer span.End()
	}

	trace.InsertStringIntoSpan("request-info-type", string(key), span)

	q, err := newInfoQuerier(ctx, params)
	if err != nil {
		return nil, err
	}

	labelMatchers := make([]*labels.Matcher, 0, 1)
	match, err := labels.NewMatcher(
		labels.MatchEqual, labels.MetricName, prometheus.ReferenceName,
	)
	if err != nil {
		return nil, err
	}
	labelMatchers = append(labelMatchers, match)

	switch key {
	case infos.FieldKeys:
		data, warns, err = q.LabelValues(labels.MetricName, labelMatchers...)
	case infos.TagKeys:
		data, warns, err = q.LabelNames(labelMatchers...)
	case infos.TagValues:
		var (
			lvs    []string
			lvsMap = make(map[string][]string, len(params.Keys))
		)
		sort.Strings(params.Keys)
		for _, k := range params.Keys {
			if k != "" {
				lvs, warns, err = q.LabelValues(k, labelMatchers...)
				if err != nil || len(warns) > 0 {
					continue
				}
				lvsMap[k] = lvs
			}
		}
		data = TagValuesData{
			Values: lvsMap,
		}
	case infos.Series:
		start, err := params.StartTimeUnix()
		if err != nil {
			return nil, fmt.Errorf("start time error: %s", err.Error())
		}
		end, err := params.EndTimeUnix()
		if err != nil {
			return nil, fmt.Errorf("end time error: %s", err.Error())
		}

		hints := &storage.SelectHints{
			Start: start * 1e3,
			End:   end * 1e3,
			Func:  "series", // There is no series function, this token is used for lookups that don't need samples.
		}

		set := q.Select(true, hints, labelMatchers...)
		if set.Err() != nil {
			return nil, set.Err()
		}

		keyExists := make(map[string]struct{}, 0)
		dataExists := make(map[string]struct{}, 0)

		paramsKeys := make(map[string]struct{}, len(params.Keys))
		for _, k := range params.Keys {
			paramsKeys[k] = struct{}{}
		}

		keys := make([]string, 0)
		series := make([][]string, 0)

		for set.Next() {
			if len(keys) == 0 {
				for _, lb := range set.At().Labels() {
					if len(paramsKeys) > 0 {
						if _, ok := paramsKeys[lb.Name]; ok {
							keyExists[lb.Name] = struct{}{}
							keys = append(keys, lb.Name)
						}
					} else if lb.Name != influxdb.BKTaskIndex {
						keyExists[lb.Name] = struct{}{}
						keys = append(keys, lb.Name)
					}
				}
			}

			values := make([]string, 0, len(keyExists))
			buf := ""
			for _, lb := range set.At().Labels() {
				if _, ok := keyExists[lb.Name]; ok {
					values = append(values, lb.Value)
					buf = fmt.Sprintf("%s%s", buf, lb.Value)
				}
			}
			if _, ok := dataExists[buf]; !ok {
				series = append(series, values)
				dataExists[buf] = struct{}{}
			}
		}

		data = []*SeriesData{
			{
				Keys:   keys,
				Series: series,
			},
		}
	default:
		err = fmt.Errorf("error info type %s", key)
	}

	if warns != nil {
		err = fmt.Errorf("warns: %v", warns)
	}
	return data, err
}

func newInfoQuerier(ctx context.Context, params *infos.Params) (storage.Querier, error) {
	var (
		span oleltrace.Span
		user = metadata.GetUser(ctx)

		err error

		start int64
		end   int64
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "new-info-querier")
	if span != nil {
		defer span.End()
	}

	paramsStr, _ := json.Marshal(params)
	trace.InsertStringIntoSpan("query-body", string(paramsStr), span)

	query := &structured.Query{
		DataSource:    params.DataSource,
		TableID:       params.TableID,
		FieldName:     params.Metric,
		Conditions:    params.Conditions,
		ReferenceName: prometheus.ReferenceName,
	}

	queryMetric, err := query.ToQueryMetric(ctx, user.SpaceUid)
	if err != nil {
		log.Errorf(ctx, err.Error())
		return nil, err
	}

	if params.Start != "" {
		start, err = params.StartTimeUnix()
		if err != nil {
			err = fmt.Errorf("start time is error: %s", err.Error())
			return nil, err
		}
	} else {
		start = time.Now().Add(time.Hour * -1).Unix()
	}

	if params.End != "" {
		end, err = params.EndTimeUnix()
		if err != nil {
			err = fmt.Errorf("end time is error: %s", err.Error())
			return nil, err
		}
	} else {
		end = time.Now().Unix()
	}

	if user.SpaceUid == "" {
		err = fmt.Errorf("space uid is empty")
		log.Errorf(ctx, err.Error())
		return nil, err
	}
	metadata.SetQueryReference(ctx, map[string]*metadata.QueryMetric{
		prometheus.ReferenceName: queryMetric,
	})

	storage := &prometheus.QueryRangeStorage{
		QueryMaxRouting: QueryMaxRouting,
		Timeout:         SingleflightTimeout,
	}
	q, err := storage.Querier(ctx, start, end)
	return q, err
}
