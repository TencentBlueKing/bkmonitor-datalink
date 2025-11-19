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
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	ants "github.com/panjf2000/ants/v2"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/prometheus"
)

// HandlerFieldKeys
// @Summary  info field keys
// @ID       info_field_keys
// @Produce  json
// @Param    traceparent            header    string                        false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    Bk-Query-Source   		header    string                        false  "来源" default(username:goodman)
// @Param    X-Bk-Scope-Space-Uid   header    string                        false  "空间UID" default(bkcc__2)
// @Param	 X-Bk-Scope-Skip-Space  header	  string						false  "是否跳过空间验证" default()
// @Param    data                  	body      infos.Params 		  			true   "json data"
// @Success  200                   	{array}  []string
// @Failure  400                   	{object}  ErrResponse
// @Router   /query/ts/info/field_keys [post]
func HandlerFieldKeys(c *gin.Context) {
	var (
		ctx  = c.Request.Context()
		resp = &response{
			c: c,
		}
		err error
	)

	ctx, span := trace.NewSpan(ctx, "handler-filed-keys")
	defer span.End(&err)

	params := &Params{}
	err = json.NewDecoder(c.Request.Body).Decode(params)
	if err != nil {
		return
	}

	paramsStr, _ := json.Marshal(params)
	span.Set("request-url", c.Request.URL.String())
	span.Set("request-header", c.Request.Header)
	span.Set("request-data", paramsStr)

	metadata.Sprintf(
		metadata.MsgQueryInfo,
		"%s, header: %+v, data: %+v",
		c.Request.URL.String(), c.Request.Header, paramsStr,
	).Info(ctx)

	queryRef, err := infoParamsToQueryRef(ctx, params)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	p, _ := ants.NewPool(QueryMaxRouting)
	defer p.Release()

	var (
		wg  sync.WaitGroup
		lbl = set.New[string]()
	)

	qb := metadata.GetQueryParams(ctx)
	queryRef.Range("", func(qry *metadata.Query) {
		wg.Add(1)
		_ = p.Submit(func() {
			defer wg.Done()
			instance := prometheus.GetTsDbInstance(ctx, qry)
			if instance == nil {
				return
			}

			res, err := instance.QueryLabelValues(ctx, qry, labels.MetricName, qb.Start, qb.End)
			if err != nil {
				return
			}
			lbl.Add(res...)
		})
	})

	wg.Wait()

	data := lbl.ToArray()
	sort.Strings(data)

	resp.success(ctx, data)
}

// HandlerTagKeys
// @Summary  info tag keys
// @ID       info_tag_keys
// @Produce  json
// @Param    traceparent            header    string                        false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    Bk-Query-Source   		header    string                        false  "来源" default(username:goodman)
// @Param    X-Bk-Scope-Space-Uid   header    string                        false  "空间UID" default(bkcc__2)
// @Param	 X-Bk-Scope-Skip-Space  header	  string						false  "是否跳过空间验证" default()
// @Param    data                  	body      infos.Params 		  			true   "json data"
// @Success  200                   	{array}   []string
// @Failure  400                   	{object}  ErrResponse
// @Router   /query/ts/info/tag_keys [post]
func HandlerTagKeys(c *gin.Context) {
	var (
		ctx  = c.Request.Context()
		resp = &response{
			c: c,
		}
		err error
	)

	ctx, span := trace.NewSpan(ctx, "handler-tag-keys")
	defer span.End(&err)

	params := &Params{}
	err = json.NewDecoder(c.Request.Body).Decode(params)
	if err != nil {
		return
	}

	paramsStr, _ := json.Marshal(params)
	span.Set("request-url", c.Request.URL.String())
	span.Set("request-header", c.Request.Header)
	span.Set("request-data", paramsStr)

	metadata.Sprintf(
		metadata.MsgQueryInfo,
		"%s, header: %+v, data: %+v",
		c.Request.URL.String(), c.Request.Header, paramsStr,
	).Info(ctx)

	queryRef, err := infoParamsToQueryRef(ctx, params)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	p, _ := ants.NewPool(QueryMaxRouting)
	defer p.Release()

	var (
		wg  sync.WaitGroup
		lbl = set.New[string]()
	)

	qb := metadata.GetQueryParams(ctx)
	queryRef.Range("", func(qry *metadata.Query) {
		wg.Add(1)
		_ = p.Submit(func() {
			defer wg.Done()
			instance := prometheus.GetTsDbInstance(ctx, qry)
			if instance == nil {
				return
			}

			res, err := instance.QueryLabelNames(ctx, qry, qb.Start, qb.End)
			if err != nil {
				return
			}
			lbl.Add(res...)
		})
	})
	wg.Wait()

	data := lbl.ToArray()
	sort.Strings(data)

	resp.success(ctx, data)
}

// HandlerTagValues
// @Summary  info tag values
// @ID       info_tag_values
// @Produce  json
// @Param    traceparent            header    string                        false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    Bk-Query-Source   		header    string                        false  "来源" default(username:goodman)
// @Param    X-Bk-Scope-Space-Uid   header    string                        false  "空间UID" default(bkcc__2)
// @Param	 X-Bk-Scope-Skip-Space  header	  string						false  "是否跳过空间验证" default()
// @Param    data                  	body      infos.Params 		  			true   "json data"
// @Success  200                   	{object}  TagValuesData
// @Failure  400                   	{object}  ErrResponse
// @Router   /query/ts/info/tag_values [post]
func HandlerTagValues(c *gin.Context) {
	var (
		ctx  = c.Request.Context()
		resp = &response{
			c: c,
		}
		err error
	)

	ctx, span := trace.NewSpan(ctx, "handler-tag-values")
	defer span.End(&err)

	params := &Params{}
	err = json.NewDecoder(c.Request.Body).Decode(params)
	if err != nil {
		return
	}

	paramsStr, _ := json.Marshal(params)
	span.Set("request-url", c.Request.URL.String())
	span.Set("request-header", c.Request.Header)
	span.Set("request-data", paramsStr)

	metadata.Sprintf(
		metadata.MsgQueryInfo,
		"%s, header: %+v, data: %+v",
		c.Request.URL.String(), c.Request.Header, string(paramsStr),
	).Info(ctx)

	queryRef, err := infoParamsToQueryRef(ctx, params)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	p, _ := ants.NewPool(QueryMaxRouting)
	defer p.Release()

	var (
		wg   sync.WaitGroup
		data = TagValuesData{
			Values: make(map[string][]string),
		}

		lblMap sync.Map
	)

	qb := metadata.GetQueryParams(ctx)
	left := qb.End.Sub(qb.Start)
	span.Set("left", left)

	for _, name := range params.Keys {
		lbl, _ := lblMap.LoadOrStore(name, set.New[string]())
		queryRef.Range("", func(qry *metadata.Query) {
			wg.Add(1)
			name := name
			lbl := lbl

			_ = p.Submit(func() {
				defer wg.Done()

				instance := prometheus.GetTsDbInstance(ctx, qry)
				if instance == nil {
					return
				}

				var res []string

				res, err = instance.QueryLabelValues(ctx, qry, name, qb.Start, qb.End)
				if err != nil {
					return
				}

				span.Set("result-size", len(res))
				lbl.(*set.Set[string]).Add(res...)
			})
		})
	}
	wg.Wait()

	lblMap.Range(func(key, value any) bool {
		name := key.(string)
		lb := value.(*set.Set[string])

		res := lb.ToArray()
		sort.Strings(res)
		data.Values[name] = res
		return true
	})

	data.TraceID = span.TraceID()
	resp.success(ctx, data)
}

// HandlerTimeSeries
// @Summary  info time series
// @ID       info_time_series
// @Produce  json
// @Param    traceparent            header    string                        false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    Bk-Query-Source   		header    string                        false  "来源" default(username:goodman)
// @Param    X-Bk-Scope-Space-Uid   header    string                        false  "空间UID" default(bkcc__2)
// @Param	 X-Bk-Scope-Skip-Space  header	  string						false  "是否跳过空间验证" default()
// @Param    data                  	body      infos.Params 		  			true   "json data"
// @Success  200                   	{object}  SeriesDataList
// @Failure  400                   	{object}  ErrResponse
// @Router   /query/ts/info/time_series [post]
func HandlerTimeSeries(c *gin.Context) {
	var (
		ctx  = c.Request.Context()
		resp = &response{
			c: c,
		}
		err error
	)

	ctx, span := trace.NewSpan(ctx, "handler-time-series")
	defer span.End(&err)

	params := &Params{}
	err = json.NewDecoder(c.Request.Body).Decode(params)
	if err != nil {
		return
	}

	paramsStr, _ := json.Marshal(params)
	span.Set("request-url", c.Request.URL.String())
	span.Set("request-header", c.Request.Header)
	span.Set("request-data", paramsStr)

	metadata.Sprintf(
		metadata.MsgQueryInfo,
		"%s, header: %+v, data: %+v",
		c.Request.URL.String(), c.Request.Header, paramsStr,
	).Info(ctx)

	data := &InfoData{}
	data.Tables = make([]*TablesItem, 0)

	resp.success(ctx, data)
}

// HandlerSeries
// @Summary  info series
// @ID       info_series
// @Produce  json
// @Param    traceparent            header    string                        false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    Bk-Query-Source   		header    string                        false  "来源" default(username:goodman)
// @Param    X-Bk-Scope-Space-Uid   header    string                        false  "空间UID" default(bkcc__2)
// @Param	 X-Bk-Scope-Skip-Space  header	  string						false  "是否跳过空间验证" default()
// @Param    data                  	body      infos.Params 		  			true   "json data"
// @Success  200                   	{object}  SeriesDataList
// @Failure  400                   	{object}  ErrResponse
// @Router   /query/ts/info/series [post]
func HandlerSeries(c *gin.Context) {
	var (
		ctx  = c.Request.Context()
		resp = &response{
			c: c,
		}
		err error
	)

	ctx, span := trace.NewSpan(ctx, "handler-series")
	defer span.End(&err)

	params := &Params{}
	err = json.NewDecoder(c.Request.Body).Decode(params)
	if err != nil {
		return
	}

	paramsStr, _ := json.Marshal(params)
	span.Set("request-url", c.Request.URL.String())
	span.Set("request-header", c.Request.Header)
	span.Set("request-data", paramsStr)

	metadata.Sprintf(
		metadata.MsgQueryInfo,
		"%s, header: %+v, data: %+v",
		c.Request.URL.String(), c.Request.Header, paramsStr,
	).Info(ctx)

	queryRef, err := infoParamsToQueryRef(ctx, params)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	p, _ := ants.NewPool(QueryMaxRouting)
	defer p.Release()

	var (
		wg   sync.WaitGroup
		data = &SeriesData{
			Measurement: "",
			Keys:        make([]string, 0),
			Series:      make([][]string, 0),
		}

		keySet    = set.New[string]()
		seriesSet = set.New[string]()

		paramsSet = set.New[string]()
	)

	for _, k := range params.Keys {
		paramsSet.Add(k)
	}

	qb := metadata.GetQueryParams(ctx)

	queryRef.Range("", func(qry *metadata.Query) {
		wg.Add(1)
		_ = p.Submit(func() {
			defer wg.Done()

			if params.Limit > 0 && len(data.Series) > params.Limit {
				return
			}

			instance := prometheus.GetTsDbInstance(ctx, qry)
			if instance == nil {
				return
			}

			res, err := instance.QuerySeries(ctx, qry, qb.Start, qb.End)
			if err != nil {
				return
			}

			for _, r := range res {
				// 首先获取 series key，为了避免数据冲突，只获取一次
				if keySet.Size() == 0 {
					for k := range r {
						if k == labels.MetricName {
							data.Measurement = r[k]
						}

						if paramsSet.Size() == 0 || paramsSet.Existed(k) {
							keySet.Add(k)
						}
					}

					data.Keys = keySet.ToArray()
					sort.Strings(data.Keys)
				}

				var (
					series = make([]string, 0, len(data.Keys))
					buf    = strings.Builder{}
				)
				for _, k := range data.Keys {
					v, ok := r[k]
					if !ok {
						v = ""
					}
					series = append(series, v)
					buf.WriteString(v)
				}

				if !seriesSet.Existed(buf.String()) {
					seriesSet.Add(buf.String())
					data.Series = append(data.Series, series)
				}

			}
		})
	})

	wg.Wait()
	data.TraceID = span.TraceID()

	resp.success(ctx, data)
}

// HandlerLabelValues
// @Summary  info label values
// @ID       info_label_values
// @Produce  json
// @Param    traceparent            header    string                        false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    Bk-Query-Source   		header    string                        false  "来源" default(username:goodman)
// @Param    X-Bk-Scope-Space-Uid   header    string                        false  "空间UID" default(bkcc__2)
// @Param	 X-Bk-Scope-Skip-Space  header	  string						false  "是否跳过空间验证" default()
// @Param    data                  	body      infos.Params 		  			true   "json data"
// @Success  200                   	{array}   []string
// @Failure  400                   	{object}  ErrResponse
// @Router   /query/ts/label/{label_name}/values [get]
func HandlerLabelValues(c *gin.Context) {
	var (
		ctx  = c.Request.Context()
		resp = &response{
			c: c,
		}

		data = TagValuesData{
			Values: make(map[string][]string),
		}

		err error
	)

	ctx, span := trace.NewSpan(ctx, "label-values-handler")
	defer func() {
		if err != nil {
			resp.failed(ctx, err)
			return
		}

		span.End(&err)
	}()

	labelName := c.Param("label_name")
	start := c.Query("start")
	end := c.Query("end")
	matches := c.QueryArray("match[]")
	limit := c.Query("limit")

	span.Set("request-start", start)
	span.Set("request-end", end)
	span.Set("request-label-name", labelName)
	span.Set("request-matches", matches)

	span.Set("request-url", c.Request.URL.String())
	span.Set("request-header", c.Request.Header)

	metadata.Sprintf(
		metadata.MsgQueryInfo,
		"%s, header: %+v",
		c.Request.URL.String(), c.Request.Header,
	).Info(ctx)

	if len(matches) != 1 {
		err = fmt.Errorf("match[] 参数只支持 1 个, %+v", matches)
		return
	}

	query, err := promQLToStruct(ctx, &structured.QueryPromQL{
		PromQL: matches[0],
		Start:  start,
		End:    end,
		TsDBs:  nil,
	})
	if err != nil {
		return
	}

	instance, stmt, err := queryTsToInstanceAndStmt(ctx, query)
	if err != nil {
		return
	}

	matcher, err := parser.ParseMetricSelector(stmt)
	if err != nil {
		return
	}

	limitNum, _ := strconv.Atoi(limit)
	qb := metadata.GetQueryParams(ctx)

	result, err := instance.DirectLabelValues(ctx, labelName, qb.Start, qb.End, limitNum, matcher...)
	if err != nil {
		return
	}

	span.Set("result-num", len(result))
	data.Values[labelName] = result
	data.TraceID = span.TraceID()

	resp.success(ctx, data)
	return
}

// HandlerFieldMap
// @Summary  info field map
// @ID       info_field_map
// @Produce  json
// @Param    traceparent            header    string                        false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    Bk-Query-Source   		header    string                        false  "来源" default(username:goodman)
// @Param    X-Bk-Scope-Space-Uid   header    string                        false  "空间UID" default(bkcc__2)
// @Param	 X-Bk-Scope-Skip-Space  header	  string						false  "是否跳过空间验证" default()
// @Param    data                  	body      infos.Params 		  			true   "json data"
// @Success  200                   	{object}  SeriesDataList
// @Failure  400                   	{object}  ErrResponse
// @Router   /query/ts/info/field_map [post]
func HandlerFieldMap(c *gin.Context) {
	var (
		ctx  = c.Request.Context()
		resp = &response{
			c: c,
		}
		err error
	)

	ctx, span := trace.NewSpan(ctx, "handler-field-map")
	defer func() {
		span.End(&err)
		if err != nil {
			resp.failed(ctx, err)
		}
	}()

	params := &Params{}
	err = json.NewDecoder(c.Request.Body).Decode(params)
	if err != nil {
		return
	}

	paramsStr, _ := json.Marshal(params)
	span.Set("request-url", c.Request.URL.String())
	span.Set("request-header", c.Request.Header)
	span.Set("request-data", paramsStr)

	metadata.Sprintf(
		metadata.MsgQueryInfo,
		"%s, header: %+v, data: %+v",
		c.Request.URL.String(), c.Request.Header, string(paramsStr),
	).Info(ctx)

	queryRef, err := infoParamsToQueryRef(ctx, params)
	if err != nil {
		return
	}

	p, _ := ants.NewPool(QueryMaxRouting)
	defer p.Release()

	var (
		wg      sync.WaitGroup
		lock    sync.Mutex
		dataMap = make(metadata.FieldsMap)
		keys    []string
	)

	qb := metadata.GetQueryParams(ctx)
	queryRef.Range("", func(qry *metadata.Query) {
		wg.Add(1)
		err = p.Submit(func() {
			defer wg.Done()

			instance := prometheus.GetTsDbInstance(ctx, qry)
			if instance == nil {
				return
			}

			res, qErr := instance.QueryFieldMap(ctx, qry, qb.Start, qb.End)
			if qErr != nil {
				_ = metadata.Sprintf(
					metadata.MsgQueryInfo,
					"查询字段列表接口报错",
				).Error(ctx, qErr)
				return
			}

			span.Set(fmt.Sprintf("field-map-length-%s", qry.TableUUID()), len(res))

			for k, v := range res {
				lock.Lock()
				if _, ok := dataMap[k]; !ok {
					keys = append(keys, k)
					dataMap[k] = v
				}
				lock.Unlock()
			}
		})
		if err != nil {
			wg.Done()
		}
	})
	wg.Wait()

	sort.Strings(keys)

	span.Set("keys", keys)

	data := make([]metadata.FieldOption, 0, len(dataMap))
	for _, k := range keys {
		if v, ok := dataMap[k]; ok && v.FieldType != "" {
			data = append(data, dataMap[k])
		}
	}

	resp.success(ctx, &DataResponse{
		Data:    data,
		TraceID: span.TraceID(),
	})
}

func infoParamsToQueryRef(ctx context.Context, params *Params) (queryRef metadata.QueryReference, err error) {
	user := metadata.GetUser(ctx)

	queryTs := &structured.QueryTs{
		TsDBs:    params.TsDBs,
		SpaceUid: user.SpaceUID,
		QueryList: []*structured.Query{
			{
				DataSource:    params.DataSource,
				TableID:       params.TableID,
				FieldName:     params.Metric,
				IsRegexp:      params.IsRegexp,
				Conditions:    params.Conditions,
				Limit:         params.Limit,
				ReferenceName: metadata.DefaultReferenceName,
			},
		},
		MetricMerge: metadata.DefaultReferenceName,
		Start:       params.Start,
		End:         params.End,
		Timezone:    params.Timezone,
	}

	return queryTs.ToQueryReference(ctx)
}
