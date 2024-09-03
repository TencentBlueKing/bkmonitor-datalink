// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package prometheus

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/panjf2000/ants/v2"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/bkapi"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	baseInfluxdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	tsDBService "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/elasticsearch"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/victoriaMetrics"
	routerInfluxdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

const (
	ReferenceName = "a"
)

type QueryRangeStorage struct {
	QueryMaxRouting int
	Timeout         time.Duration
}

func (s *QueryRangeStorage) Querier(ctx context.Context, min, max int64) (storage.Querier, error) {
	return NewQuerier(ctx, time.Unix(min, 0), time.Unix(max, 0), s.QueryMaxRouting, s.Timeout), nil
}

func NewQuerier(ctx context.Context, min, max time.Time, maxRouting int, timeout time.Duration) *Querier {
	return &Querier{
		ctx:        ctx,
		min:        min,
		max:        max,
		maxRouting: maxRouting,
		timeout:    timeout,
	}
}

type Querier struct {
	ctx        context.Context
	min        time.Time
	max        time.Time
	maxRouting int
	timeout    time.Duration
}

// checkCtxDone
func (q *Querier) checkCtxDone() bool {
	select {
	case <-q.ctx.Done():
		return true
	default:
		return false
	}
}

func (q *Querier) getQueryList(referenceName string) []*Query {
	var (
		ctx       = q.ctx
		queryList []*Query
		err       error
	)

	ctx, span := trace.NewSpan(ctx, "querier-get-query-list")
	defer span.End(&err)

	queries := metadata.GetQueryReference(ctx)
	if queryMetric, ok := queries[referenceName]; ok {
		queryList = make([]*Query, 0, len(queryMetric.QueryList))
		for _, qry := range queryMetric.QueryList {
			instance := GetTsDbInstance(ctx, qry)
			if instance != nil {
				queryList = append(queryList, &Query{
					instance: instance,
					qry:      qry,
				})
			} else {
				log.Warnf(ctx, "not instance in %s", qry.StorageID)
			}
		}
	}
	return queryList
}

// selectFn 获取原始数据
func (q *Querier) selectFn(hints *storage.SelectHints, matchers ...*labels.Matcher) storage.SeriesSet {
	var (
		ctx context.Context

		referenceName string

		set storage.SeriesSet

		setCh    = make(chan storage.SeriesSet, 1)
		recvDone = make(chan struct{})

		wg  sync.WaitGroup
		err error
	)

	ctx, span := trace.NewSpan(q.ctx, "prometheus-querier-select-fn")
	defer span.End(&err)

	go func() {
		defer func() {
			recvDone <- struct{}{}
		}()
		var sets []storage.SeriesSet
		for s := range setCh {
			if s != nil {
				sets = append(sets, s)
			}
		}
		set = storage.NewMergeSeriesSet(sets, storage.ChainedSeriesMerge)
	}()

	for _, m := range matchers {
		if m.Name == labels.MetricName {
			referenceName = m.Value
			break
		}
	}

	span.Set("max-routing", q.maxRouting)
	span.Set("reference_name", referenceName)

	queryList := q.getQueryList(referenceName)

	p, _ := ants.NewPoolWithFunc(q.maxRouting, func(i interface{}) {
		defer wg.Done()
		index, ok := i.(int)
		if ok {
			if index < len(queryList) {
				query := queryList[index]

				span.Set(fmt.Sprintf("query_%d_instance_type", i), query.instance.GetInstanceType())
				span.Set(fmt.Sprintf("query_%d_qry_source", i), query.qry.SourceType)
				span.Set(fmt.Sprintf("query_%d_qry_db", i), query.qry.DB)
				span.Set(fmt.Sprintf("query_%d_qry_vmrt", i), query.qry.VmRt)

				var (
					start int64
					end   int64
				)
				qp := metadata.GetQueryParams(ctx)
				if qp.IsReference {
					start = qp.Start * 1e3
					end = qp.End * 1e3
				} else {
					start = hints.Start
					end = hints.End

					if len(query.qry.Aggregates) == 1 {
						agg := query.qry.Aggregates[0]

						// 如果使用时间聚合计算，是否对齐开始时间
						if agg.Window.Milliseconds() > 0 {
							start = intMathFloor(start, agg.Window.Milliseconds()) * agg.Window.Milliseconds()
						}
					}
				}

				startTime := time.UnixMilli(start)
				endTime := time.UnixMilli(end)

				setCh <- query.instance.QueryRaw(ctx, query.qry, startTime, endTime)
				return

			} else {
				log.Errorf(ctx, "sql index error: %+v", index)
			}
		} else {
			log.Errorf(ctx, "sql index error: %+v", index)
		}
	})
	defer p.Release()

	for i := range queryList {
		wg.Add(1)
		p.Invoke(i)
	}
	wg.Wait()

	close(setCh)
	<-recvDone

	return set
}

func (q *Querier) Select(_ bool, hints *storage.SelectHints, matchers ...*labels.Matcher) storage.SeriesSet {
	promise := make(chan storage.SeriesSet, 1)
	go func() {
		defer close(promise)
		if ok := q.checkCtxDone(); ok {
			promise <- storage.ErrSeriesSet(ErrContextDone)
			return
		}

		promise <- q.selectFn(hints, matchers...)
	}()

	return &lazySeriesSet{
		create: func() (s storage.SeriesSet, ok bool) {
			set, ok := <-promise
			if set.Err() != nil {
				log.Errorf(q.ctx, set.Err().Error())
				return storage.ErrSeriesSet(set.Err()), false
			}
			if !ok {
				return storage.ErrSeriesSet(ErrChannelReceived), false
			}
			return set, set.Next()
		},
		set: nil,
	}
}

// LabelValues 返回可能的标签(维度)值。
// 在查询器的生命周期以外使用这些字符串是不安全的
func (q *Querier) LabelValues(name string, matchers ...*labels.Matcher) ([]string, storage.Warnings, error) {
	var (
		ctx context.Context
		err error

		labelMap = make(map[string]struct{}, 0)
	)

	ctx, span := trace.NewSpan(q.ctx, "prometheus-querier-label-values")
	defer span.End(&err)

	referenceName := ""
	for _, m := range matchers {
		if m.Name == labels.MetricName {
			referenceName = m.Value
		}
	}

	queryReference := metadata.GetQueryReference(q.ctx)
	ok, vmExpand, err := queryReference.CheckVmQuery(ctx)

	if ok {
		if err != nil {
			return nil, nil, err
		}

		metadata.SetExpand(ctx, vmExpand)
		instance := GetTsDbInstance(ctx, &metadata.Query{
			StorageType: consul.VictoriaMetricsStorageType,
		})
		if instance == nil {
			err = fmt.Errorf("%s storage get error", consul.VictoriaMetricsStorageType)
			log.Errorf(ctx, err.Error())
			return nil, nil, err
		}

		lbl, err := instance.LabelValues(ctx, nil, name, q.min, q.max, matchers...)
		if err != nil {
			return nil, nil, err
		}
		for _, lb := range lbl {
			labelMap[lb] = struct{}{}
		}
	} else {
		queryList := q.getQueryList(referenceName)
		for _, query := range queryList {
			lbl, err := query.instance.LabelValues(ctx, query.qry, name, q.min, q.max, matchers...)
			if err != nil {
				log.Errorf(ctx, err.Error())
				continue
			}
			for _, l := range lbl {
				labelMap[l] = struct{}{}
			}
		}
	}

	lbn := make([]string, 0, len(labelMap))
	for k := range labelMap {
		lbn = append(lbn, k)
	}

	sort.Strings(lbn)
	return lbn, nil, nil
}

// LabelNames 以块中的排序顺序返回所有的唯一的标签
func (q *Querier) LabelNames(matchers ...*labels.Matcher) ([]string, storage.Warnings, error) {
	var (
		ctx context.Context
		err error

		labelMap = make(map[string]struct{}, 0)
	)

	ctx, span := trace.NewSpan(q.ctx, "prometheus-querier-label-names")
	defer span.End(&err)

	referenceName := ""
	for _, m := range matchers {
		if m.Name == labels.MetricName {
			referenceName = m.Value
		}
	}

	queryReference := metadata.GetQueryReference(q.ctx)
	ok, vmExpand, err := queryReference.CheckVmQuery(ctx)

	if ok {
		if err != nil {
			return nil, nil, err
		}

		metadata.SetExpand(ctx, vmExpand)
		instance := GetTsDbInstance(ctx, &metadata.Query{
			StorageType: consul.VictoriaMetricsStorageType,
		})
		if instance == nil {
			err = fmt.Errorf("%s storage get error", consul.VictoriaMetricsStorageType)
			log.Errorf(ctx, err.Error())
			return nil, nil, err
		}

		lbl, err := instance.LabelNames(ctx, nil, q.min, q.max, matchers...)
		if err != nil {
			return nil, nil, err
		}
		for _, lb := range lbl {
			labelMap[lb] = struct{}{}
		}
	} else {
		queryList := q.getQueryList(referenceName)
		for _, query := range queryList {
			lbl, err := query.instance.LabelNames(ctx, query.qry, q.min, q.max, matchers...)
			if err != nil {
				return nil, nil, err
			}
			for _, lb := range lbl {
				labelMap[lb] = struct{}{}
			}
		}
	}

	lbn := make([]string, 0, len(labelMap))
	for k := range labelMap {
		lbn = append(lbn, k)
	}

	sort.Strings(lbn)
	return lbn, nil, nil
}

// Close 释放查询器的所有资源
func (q *Querier) Close() error {
	return nil
}

func GetTsDbInstance(ctx context.Context, qry *metadata.Query) tsdb.Instance {
	var (
		instance tsdb.Instance
		err      error
	)

	ctx, span := trace.NewSpan(ctx, "get-ts-db-instance")
	defer func() {
		if err != nil {
			log.Errorf(ctx, err.Error())
		}
		span.End(&err)
	}()

	span.Set("storage-id", qry.StorageID)

	// 兼容原逻辑，storageType 通过 storageMap 获取
	stg, err := tsdb.GetStorage(qry.StorageID)
	if stg != nil {
		qry.StorageType = stg.Type
	}

	span.Set("storage-type", qry.StorageType)
	curlGet := &curl.HttpCurl{Log: log.DefaultLogger}

	switch qry.StorageType {
	case consul.InfluxDBStorageType:
		opt := influxdb.Options{
			Timeout:        tsDBService.InfluxDBTimeout,
			ContentType:    tsDBService.InfluxDBContentType,
			ChunkSize:      tsDBService.InfluxDBChunkSize,
			RawUriPath:     tsDBService.InfluxDBQueryRawUriPath,
			Accept:         tsDBService.InfluxDBQueryRawAccept,
			AcceptEncoding: tsDBService.InfluxDBQueryRawAcceptEncoding,
			MaxLimit:       tsDBService.InfluxDBMaxLimit,
			MaxSlimit:      tsDBService.InfluxDBMaxSLimit,
			Tolerance:      tsDBService.InfluxDBTolerance,
			ReadRateLimit:  tsDBService.InfluxDBQueryReadRateLimit,
			Curl:           curlGet,
		}
		var host *routerInfluxdb.Host
		host, err = baseInfluxdb.GetInfluxDBRouter().GetInfluxDBHost(
			ctx, qry.TagsKey, qry.ClusterName, qry.DB, qry.Measurement, qry.Condition,
		)
		if err != nil {
			return nil
		}
		opt.Host = host.DomainName
		opt.Port = host.Port
		opt.GrpcPort = host.GrpcPort
		opt.Protocol = host.Protocol
		opt.Username = host.Username
		opt.Password = host.Password
		// 如果 host 有单独配置，则替换默认限速配置
		if host.ReadRateLimit > 0 {
			opt.ReadRateLimit = host.ReadRateLimit
		}

		span.Set("cluster-name", qry.ClusterName)
		span.Set("tag-keys", qry.TagsKey)
		span.Set("ins-option", opt)

		instance, err = influxdb.NewInstance(ctx, opt)
	case consul.ElasticsearchStorageType:
		opt := &elasticsearch.InstanceOption{
			MaxSize:    tsDBService.EsMaxSize,
			Timeout:    tsDBService.EsTimeout,
			MaxRouting: tsDBService.EsMaxRouting,
		}
		if qry.SourceType == structured.BkData {
			opt.Address = bkapi.GetBkDataAPI().QueryEsUrl()
			opt.Headers = bkapi.GetBkDataAPI().Headers(nil)
			opt.HealthCheck = false
		} else {
			if stg == nil {
				err = fmt.Errorf("%s storage is nil in %s", consul.ElasticsearchStorageType, qry.StorageID)
				return nil
			}
			opt.Address = stg.Address
			opt.Username = stg.Username
			opt.Password = stg.Password
			opt.HealthCheck = true
		}
		instance, err = elasticsearch.NewInstance(ctx, opt)
	case consul.BkSqlStorageType:
		instance, err = bksql.NewInstance(ctx, bksql.Options{
			Address: bkapi.GetBkDataAPI().QuerySyncUrl(),
			Headers: bkapi.GetBkDataAPI().Headers(map[string]string{
				bksql.ContentType: tsDBService.BkSqlContentType,
			}),
			Timeout:   tsDBService.BkSqlTimeout,
			MaxLimit:  tsDBService.BkSqlLimit,
			Tolerance: tsDBService.BkSqlTolerance,
			Curl:      curlGet,
		})
	case consul.VictoriaMetricsStorageType:
		instance, err = victoriaMetrics.NewInstance(ctx, victoriaMetrics.Options{
			Address: bkapi.GetBkDataAPI().QuerySyncUrl(),
			Headers: bkapi.GetBkDataAPI().Headers(map[string]string{
				victoriaMetrics.ContentType: tsDBService.VmContentType,
			}),
			MaxConditionNum:  tsDBService.VmMaxConditionNum,
			Timeout:          tsDBService.VmTimeout,
			InfluxCompatible: tsDBService.VmInfluxCompatible,
			UseNativeOr:      tsDBService.VmUseNativeOr,
			Curl:             curlGet,
		})
	default:
		err = fmt.Errorf("sotrage type is error %+v", qry)
		return nil
	}

	return instance
}
