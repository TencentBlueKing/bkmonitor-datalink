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
	oleltrace "go.opentelemetry.io/otel/trace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	tsDBInfluxdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/influxdb"
)

const (
	ReferenceName = "a"
)

type QueryRangeStorage struct {
	QueryMaxRouting int
	Timeout         time.Duration
}

func (s *QueryRangeStorage) Querier(ctx context.Context, min, max int64) (storage.Querier, error) {
	return &Querier{
		ctx:        ctx,
		min:        time.Unix(min, 0),
		max:        time.Unix(max, 0),
		maxRouting: s.QueryMaxRouting,
		timeout:    s.Timeout,
	}, nil
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
		span      oleltrace.Span
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "querier-get-query-list")
	if span != nil {
		defer span.End()
	}

	queries := metadata.GetQueryReference(ctx)
	if queryMetric, ok := queries[referenceName]; ok {
		queryList = make([]*Query, 0, len(queryMetric.QueryList))
		for _, qry := range queryMetric.QueryList {
			instance := GetInstance(ctx, qry)
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
		ctx  context.Context
		span oleltrace.Span

		referenceName string

		set storage.SeriesSet

		setCh    = make(chan storage.SeriesSet, 1)
		recvDone = make(chan struct{})

		wg sync.WaitGroup
	)

	ctx, span = trace.IntoContext(q.ctx, trace.TracerName, "prometheus-querier-select-fn")
	if span != nil {
		defer span.End()
	}

	go func() {
		defer func() {
			recvDone <- struct{}{}
		}()
		var sets []storage.SeriesSet
		for s := range setCh {
			if s != nil && s.Err() == nil {
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

	trace.InsertIntIntoSpan("max-routing", q.maxRouting, span)
	trace.InsertStringIntoSpan("reference_name", referenceName, span)

	queryList := q.getQueryList(referenceName)

	p, _ := ants.NewPoolWithFunc(q.maxRouting, func(i interface{}) {
		defer wg.Done()
		index, ok := i.(int)
		if ok {
			if index < len(queryList) {
				query := queryList[index]

				trace.InsertStringIntoSpan(fmt.Sprintf("query_%d_instance_type", i), query.instance.GetInstanceType(), span)
				trace.InsertStringIntoSpan(fmt.Sprintf("query_%d_qry_source", i), query.qry.SourceType, span)
				trace.InsertStringIntoSpan(fmt.Sprintf("query_%d_qry_db", i), query.qry.DB, span)
				trace.InsertStringIntoSpan(fmt.Sprintf("query_%d_qry_vmrt", i), query.qry.VmRt, span)

				setCh <- query.instance.QueryRaw(ctx, query.qry, hints, matchers...)
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
		ctx  context.Context
		span oleltrace.Span

		labelMap = make(map[string]struct{}, 0)
	)

	ctx, span = trace.IntoContext(q.ctx, trace.TracerName, "prometheus-querier-label-values")
	if span != nil {
		defer span.End()
	}

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
		instance := GetInstance(ctx, &metadata.Query{
			StorageID: consul.VictoriaMetricsStorageType,
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
		ctx  context.Context
		span oleltrace.Span

		labelMap = make(map[string]struct{}, 0)
	)

	ctx, span = trace.IntoContext(q.ctx, trace.TracerName, "prometheus-querier-label-names")
	if span != nil {
		defer span.End()
	}

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
		instance := GetInstance(ctx, &metadata.Query{
			StorageID: consul.VictoriaMetricsStorageType,
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

// GetInstance 通过 qry 获取实例
func GetInstance(ctx context.Context, qry *metadata.Query) tsdb.Instance {
	var (
		span     oleltrace.Span
		instance tsdb.Instance
	)
	ctx, span = trace.IntoContext(ctx, trace.TracerName, "storage-get-instance")
	if span != nil {
		defer span.End()
	}
	storage, err := tsdb.GetStorage(qry.StorageID)
	if err != nil {
		log.Errorf(
			ctx, "get storage error: %s.%s: %s", qry.DB, qry.Measurement, err.Error(),
		)
		return nil
	}
	if storage.Instance != nil {
		return storage.Instance
	}

	trace.InsertStringIntoSpan("stroage-type", storage.Type, span)
	trace.InsertStringIntoSpan("storage-id", qry.StorageID, span)
	trace.InsertStringIntoSpan("storage-address", storage.Address, span)
	trace.InsertStringIntoSpan("storage-uri-path", storage.UriPath, span)
	trace.InsertStringIntoSpan("storage-password", storage.Password, span)

	curl := &curl.HttpCurl{Log: log.OtLogger}
	switch storage.Type {
	// vm 实例直接在 storage.instance 就有了，无需进到这个逻辑
	case consul.VictoriaMetricsStorageType:
		return nil
	case consul.InfluxDBStorageType:
		insOption := tsDBInfluxdb.Options{
			ReadRateLimit:  storage.ReadRateLimit,
			Timeout:        storage.Timeout,
			ContentType:    storage.ContentType,
			ChunkSize:      storage.ChunkSize,
			RawUriPath:     storage.UriPath,
			Accept:         storage.Accept,
			AcceptEncoding: storage.AcceptEncoding,
			MaxLimit:       storage.MaxLimit,
			MaxSlimit:      storage.MaxSLimit,
			Tolerance:      storage.Toleration,
			Curl:           curl,
		}

		host, err := influxdb.GetInfluxDBRouter().GetInfluxDBHost(
			ctx, qry.TagsKey, qry.ClusterName, qry.DB, qry.Measurement, qry.Condition,
		)
		if err != nil {
			log.Errorf(ctx, err.Error())
			return nil
		}
		insOption.Host = host.DomainName
		insOption.Port = host.Port
		insOption.GrpcPort = host.GrpcPort
		insOption.Protocol = host.Protocol
		insOption.Username = host.Username
		insOption.Password = host.Password

		// 如果 host 有单独配置，则替换默认限速配置
		if host.ReadRateLimit > 0 {
			insOption.ReadRateLimit = host.ReadRateLimit
		}
		instance = tsDBInfluxdb.NewInstance(ctx, insOption)

		trace.InsertStringIntoSpan("cluster-name", qry.ClusterName, span)
		trace.InsertStringIntoSpan("tag-keys", fmt.Sprintf("%+v", qry.TagsKey), span)
		trace.InsertStringIntoSpan("ins-option", fmt.Sprintf("%+v", insOption), span)
	default:
		return nil
	}
	return instance
}
