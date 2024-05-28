// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	elastic "github.com/olivere/elastic/v7"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/prompb"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/storage/remote"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/pool"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

type Instance struct {
	ctx    context.Context
	wg     sync.WaitGroup
	client *elastic.Client

	lock sync.Mutex

	timeout time.Duration
	maxSize int

	toEs   func(string) string
	toProm func(string) string
}

// QueryRange 使用 es 直接查询引擎
func (i *Instance) QueryRange(ctx context.Context, referenceName string, start, end time.Time, step time.Duration) (promql.Matrix, error) {
	var (
		err    error
		matrix = make(promql.Matrix, 0)
	)
	ctx, span := trace.NewSpan(ctx, "elasticsearch-query-range")
	defer span.End(&err)

	references := metadata.GetQueryReference(ctx)

	if ref, ok := references[referenceName]; ok {
		for _, ql := range ref.QueryList {
			var (
				rets chan *TimeSeriesResult
				qr   *prompb.QueryResult
			)
			rets, err = i.query(ctx, ql, start.UnixMilli(), end.UnixMilli())
			if err != nil {
				return nil, err
			}

			qr, err = i.mergeTimeSeries(rets)
			if err != nil {
				return nil, err
			}

			for _, r := range qr.GetTimeseries() {
				metric := make(labels.Labels, 0, len(r.GetLabels()))
				for _, l := range r.GetLabels() {
					metric = append(metric, labels.Label{
						Name:  l.GetName(),
						Value: l.GetValue(),
					})
				}

				points := make([]promql.Point, 0, len(r.GetSamples()))
				for _, p := range r.GetSamples() {
					points = append(points, promql.Point{
						T: p.GetTimestamp(),
						V: p.GetValue(),
					})
				}

				matrix = append(matrix, promql.Series{
					Metric: metric,
					Points: points,
				})
			}
		}

		return matrix, nil
	} else {
		return nil, fmt.Errorf("reference is empty %s", referenceName)
	}
}

func (i *Instance) Query(ctx context.Context, qs string, end time.Time) (promql.Vector, error) {
	//TODO implement me
	panic("implement me")
}

func (i *Instance) QueryExemplar(ctx context.Context, fields []string, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) (*decoder.Response, error) {
	//TODO implement me
	panic("implement me")
}

func (i *Instance) LabelNames(ctx context.Context, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
	//TODO implement me
	panic("implement me")
}

func (i *Instance) LabelValues(ctx context.Context, query *metadata.Query, name string, start, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
	//TODO implement me
	panic("implement me")
}

func (i *Instance) Series(ctx context.Context, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) storage.SeriesSet {
	//TODO implement me
	panic("implement me")
}

func (i *Instance) GetInstanceType() string {
	return consul.ElasticsearchStorageType
}

type InstanceOption struct {
	Address    string
	Username   string
	Password   string
	MaxSize    int
	MaxRouting int
	Timeout    time.Duration
}

type queryOption struct {
	index    string
	start    int64
	end      int64
	timeZone string

	query *metadata.Query
}

type indexOpt struct {
	tableID string
	start   int64
	end     int64
}

var TimeSeriesResultPool = sync.Pool{
	New: func() any {
		return &TimeSeriesResult{}
	},
}

func NewInstance(ctx context.Context, opt *InstanceOption) (*Instance, error) {

	ins := &Instance{
		ctx:     ctx,
		timeout: opt.Timeout,
		maxSize: opt.MaxSize,
		toEs:    structured.QueryRawFormat(ctx),
		toProm:  structured.PromQueryFormat(ctx),
	}

	if opt.Address == "" {
		return ins, errors.New("empty es client options")
	}

	cliOpts := []elastic.ClientOptionFunc{
		elastic.SetURL(opt.Address),
		elastic.SetSniff(false),
	}
	if opt.Username != "" && opt.Password != "" {
		cliOpts = append(cliOpts, elastic.SetBasicAuth(opt.Username, opt.Password))
	}

	cli, err := elastic.NewClient(cliOpts...)
	if err != nil {
		return ins, err
	}

	if opt.MaxRouting > 0 {
		err = pool.Tune(opt.MaxRouting)
		if err != nil {
			return ins, err
		}
	}

	ins.client = cli
	return ins, nil
}

func (i *Instance) getMapping(ctx context.Context, alias string) (map[string]interface{}, error) {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf(ctx, fmt.Sprintf("get mapping error: %s", r))
		}
	}()

	indexs, err := i.getIndexes(ctx, alias)
	if err != nil {
		return nil, err
	}

	for _, index := range indexs {
		mappings, err := i.client.GetMapping().Index(index).Do(ctx)
		if err != nil {
			return nil, err
		}

		if mapping, ok := mappings[index].(map[string]any)["mappings"].(map[string]any); ok {
			return mapping, nil
		} else {
			return nil, fmt.Errorf("get mappings error with index: %s", index)
		}
	}

	return nil, nil
}

func (i *Instance) getAlias(opt *indexOpt) (indexes []string) {
	for ti := opt.start; ti <= opt.end; ti += int64((time.Hour * 24).Seconds()) {
		index := fmt.Sprintf("%s_%s_read", opt.tableID, time.Unix(ti, 0).Format("20060102"))
		indexes = append(indexes, index)
	}

	return
}

func (i *Instance) query(
	ctx context.Context,
	query *metadata.Query,
	start int64,
	end int64,
) (chan *TimeSeriesResult, error) {
	var (
		err error
	)

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("es query error: %s", r)
		}
	}()

	ctx, span := trace.NewSpan(ctx, "elasticsearch-query-reference")
	defer span.End(&err)

	if i.client == nil {
		return nil, fmt.Errorf("es client is nil")
	}

	// 不使用分片的模式（优化效果有限，同时会引入计算问题），直接通过 * 号，完成跨天查询
	//indexOptions, err := i.makeQueryOption(ctx, query, start, end)
	queryOptions := []*queryOption{
		{
			index: fmt.Sprintf("%s_*_read", query.DB),
			start: start,
			end:   end,
			query: query,
		},
	}
	if err != nil {
		return nil, err
	}

	if len(queryOptions) == 0 {
		return nil, nil
	}

	rets := make(chan *TimeSeriesResult, len(queryOptions))

	go func() {
		wg := &sync.WaitGroup{}
		defer func() {
			wg.Wait()
			close(rets)
		}()

		for _, qo := range queryOptions {
			wg.Add(1)
			q := qo
			err = pool.Submit(func() {
				defer func() {
					wg.Done()
				}()

				if len(query.AggregateMethodList) > 0 {
					i.queryWithAgg(ctx, q, rets)
				} else {
					i.queryWithoutAgg(ctx, q, rets)
				}
			})
		}
	}()

	user := metadata.GetUser(ctx)
	span.Set("query-space-uid", user.SpaceUid)
	span.Set("query-source", user.Source)
	span.Set("query-username", user.Name)
	span.Set("query-index-options", queryOptions)

	span.Set("query-storage-id", query.StorageID)
	span.Set("query-max-size", i.maxSize)
	span.Set("query-db", query.DB)
	span.Set("query-measurement", query.Measurement)
	span.Set("query-measurements", strings.Join(query.Measurements, ","))
	span.Set("query-field", query.Field)
	span.Set("query-fields", strings.Join(query.Fields, ","))

	return rets, err
}

func (i *Instance) esQuery(ctx context.Context, qo *queryOption, fact *FormatFactory) (*elastic.SearchResult, error) {
	var (
		err error
		qb  = qo.query
	)
	ctx, span := trace.NewSpan(ctx, "elasticsearch-query")
	defer span.End(&err)

	filterQueries := make([]elastic.Query, 0)

	query, err := fact.Query(qb.QueryString, qb.AllConditions)
	if err != nil {
		return nil, err
	}
	if query != nil {
		filterQueries = append(filterQueries, query)
	}
	filterQueries = append(filterQueries, elastic.NewRangeQuery(Timestamp).Gte(qo.start).Lt(qo.end).Format(TimeFormat))

	source := elastic.NewSearchSource()
	source.Sort(Timestamp, true)
	order := fact.Order()

	for key, asc := range order {
		source.Sort(key, asc)
	}

	if len(filterQueries) > 0 {
		esQuery := elastic.NewBoolQuery().Filter(filterQueries...)
		source.Query(esQuery)
	}

	if len(qb.Source) > 0 {
		fetchSource := elastic.NewFetchSourceContext(true)
		fetchSource.Include(qb.Source...)
		source.FetchSourceContext(fetchSource)
	}

	var (
		name string
		agg  elastic.Aggregation
	)

	// 判断是否有聚合
	if len(qb.AggregateMethodList) > 0 {
		// 如果 判断是否走 PromQL 查询
		if qb.IsNotPromQL {
			name, agg, err = fact.EsAgg(qb.AggregateMethodList)
		} else {
			name, agg, err = fact.PromAgg(qb.TimeAggregation, qb.AggregateMethodList)
		}
	}

	if err != nil {
		return nil, err
	}

	if name != "" && agg != nil {
		source.Size(0)
		source.Aggregation(name, agg)
	} else {
		// 非聚合查询需要使用 from 和 size
		fact.Size(source)
	}

	body, _ := source.Source()
	bodyJson, _ := json.Marshal(body)
	bodyString := string(bodyJson)

	span.Set("query-index", qo.index)
	span.Set("query-body", bodyString)

	log.Infof(ctx, "es query index: %s", qo.index)
	log.Infof(ctx, "es query body: %s", bodyString)

	search := i.client.Search().Index(qo.index).SearchSource(source)

	res, err := search.Do(ctx)

	if err != nil {
		var (
			e   *elastic.Error
			msg strings.Builder
		)
		if errors.As(err, &e) {
			for _, rc := range e.Details.RootCause {
				msg.WriteString(fmt.Sprintf("%s: %s, ", rc.Index, rc.Reason))
			}
			return nil, errors.New(msg.String())
		} else {
			return nil, err
		}
	}

	return res, nil
}

func (i *Instance) queryWithAgg(ctx context.Context, qo *queryOption, rets chan<- *TimeSeriesResult) {
	var (
		ret = TimeSeriesResultPool.Get().(*TimeSeriesResult)
		qb  = qo.query
		err error
	)
	ctx, span := trace.NewSpan(ctx, "without-aggregation")
	defer span.End(&err)

	defer func() {
		ret.Error = err
		rets <- ret
	}()

	mapping, err := i.getMapping(ctx, qo.index)
	if err != nil {
		return
	}

	// size 如果为 0，则去 maxSize
	var size int
	if qb.Size > 0 {
		size = qb.Size
	} else {
		size = i.maxSize
	}

	formatFactory := NewFormatFactory(ctx, qb.Field, mapping, qb.Orders, qb.From, size, qb.Timezone, i.toEs, i.toProm)

	sr, err := i.esQuery(ctx, qo, formatFactory)
	if err != nil {
		return
	}

	ret.TimeSeriesMap, err = formatFactory.AggDataFormat(sr.Aggregations, qb.IsNotPromQL, qo.end)
	if err != nil {
		return
	}
	return
}

func (i *Instance) queryWithoutAgg(ctx context.Context, qo *queryOption, rets chan<- *TimeSeriesResult) {
	var (
		ret = TimeSeriesResultPool.Get().(*TimeSeriesResult)
		qb  = qo.query
		err error
	)
	ctx, span := trace.NewSpan(ctx, "without-aggregation")
	defer span.End(&err)

	defer func() {
		ret.Error = err
		rets <- ret
	}()

	mapping, err := i.getMapping(ctx, qo.index)
	if err != nil {
		return
	}

	var size int
	if qb.Size > 0 {
		size = qb.Size
	} else {
		size = i.maxSize
	}

	formatFactory := NewFormatFactory(ctx, qb.Field, mapping, qb.Orders, qb.From, size, qb.Timezone, i.toEs, i.toProm)

	sr, err := i.esQuery(ctx, qo, formatFactory)
	if err != nil {
		return
	}

	ret.TimeSeriesMap = make(map[string]*prompb.TimeSeries)
	for _, d := range sr.Hits.Hits {
		data := make(map[string]interface{})
		if err = json.Unmarshal(d.Source, &data); err != nil {
			return
		}
		formatFactory.SetData(data)

		lbs, vErr := formatFactory.Labels()
		if vErr != nil {
			err = vErr
			return
		}

		sample, vErr := formatFactory.Sample()
		if vErr != nil {
			err = vErr
			return
		}

		if _, ok := ret.TimeSeriesMap[lbs.String()]; !ok {
			ret.TimeSeriesMap[lbs.String()] = &prompb.TimeSeries{
				Labels:  lbs.GetLabels(),
				Samples: make([]prompb.Sample, 0),
			}
		}
		ret.TimeSeriesMap[lbs.String()].Samples = append(ret.TimeSeriesMap[lbs.String()].Samples, sample)
	}

	return
}

func (i *Instance) getIndexes(ctx context.Context, aliases ...string) ([]string, error) {
	catAlias, err := i.client.CatAliases().Alias(aliases...).Do(ctx)
	if err != nil {
		return nil, err
	}

	indexMap := make(map[string]struct{}, 0)
	for _, a := range catAlias {
		indexMap[a.Index] = struct{}{}
	}
	indexes := make([]string, 0, len(indexMap))
	for idx := range indexMap {
		indexes = append(indexes, idx)
	}

	return indexes, nil
}

func (i *Instance) indexOption(ctx context.Context, index string) (docCount int64, storeSize int64, err error) {
	cats, err := i.client.CatIndices().Index(index).Do(ctx)
	if err != nil {
		return
	}
	for _, c := range cats {
		docCount = int64(c.DocsCount)
		storeSize, err = parseSizeString(c.StoreSize)
		if err != nil {
			return
		}
		break
	}

	return
}

func (i *Instance) makeQueryOption(ctx context.Context, query *metadata.Query, start, end int64) (indexQueryOpts []*queryOption, err error) {
	aliases := make([]string, 0)
	for ti := start; ti <= end; ti += (time.Hour * 24).Milliseconds() {
		alias := fmt.Sprintf("%s_%s_read", query.DB, time.UnixMilli(ti).Format("20060102"))
		aliases = append(aliases, alias)
	}
	indexes, err := i.getIndexes(ctx, aliases...)
	if err != nil {
		return
	}
	if len(indexes) == 0 {
		err = fmt.Errorf("empty index with tableID %+v", query.TableID)
		return
	}

	indexQueryOpts = make([]*queryOption, 0)
	for _, index := range indexes {
		var (
			list      [][2]int64
			docCount  int64
			storeSize int64
		)

		if query.TimeAggregation != nil && query.TimeAggregation.WindowDuration.Milliseconds() > 0 {
			docCount, storeSize, err = i.indexOption(ctx, index)
			if err != nil {
				return
			}
			list, err = newRangeSegment(&querySegmentOption{
				start:     start,
				end:       end,
				interval:  query.TimeAggregation.WindowDuration.Milliseconds(),
				docCount:  docCount,
				storeSize: storeSize,
			})
			if err != nil {
				err = err
				return
			}
		} else {
			list = [][2]int64{{start, end}}
		}

		for _, l := range list {
			indexQueryOpts = append(indexQueryOpts, &queryOption{
				index:    index,
				start:    l[0],
				end:      l[1],
				query:    query,
				timeZone: query.Timezone,
			})
		}
	}

	return
}

func (i *Instance) mergeTimeSeries(rets chan *TimeSeriesResult) (*prompb.QueryResult, error) {
	seriesMap := make(map[string]*prompb.TimeSeries)

	for ret := range rets {
		if ret.Error != nil {
			return nil, ret.Error
		}

		if len(ret.TimeSeriesMap) == 0 {
			continue
		}

		for key, ts := range ret.TimeSeriesMap {
			if _, ok := seriesMap[key]; !ok {
				seriesMap[key] = &prompb.TimeSeries{
					Labels:  ts.GetLabels(),
					Samples: make([]prompb.Sample, 0),
				}
			}

			seriesMap[key].Samples = append(seriesMap[key].Samples, ts.Samples...)
		}

		ret.TimeSeriesMap = nil
		ret.Error = nil
		TimeSeriesResultPool.Put(ret)
	}

	qr := &prompb.QueryResult{
		Timeseries: make([]*prompb.TimeSeries, 0, len(seriesMap)),
	}
	for _, ts := range seriesMap {
		sort.Slice(ts.Samples, func(i, j int) bool {
			return ts.Samples[i].GetTimestamp() < ts.Samples[j].GetTimestamp()
		})

		qr.Timeseries = append(qr.Timeseries, ts)
	}

	return qr, nil
}

// QueryRaw 给 PromEngine 提供查询接口
func (i *Instance) QueryRaw(
	ctx context.Context,
	query *metadata.Query,
	hints *storage.SelectHints,
	matchers ...*labels.Matcher,
) storage.SeriesSet {
	var (
		err error
	)

	qp := metadata.GetQueryParams(ctx)

	start := qp.Start
	end := qp.End

	if query.TimeAggregation != nil {
		window := query.TimeAggregation.WindowDuration

		// 是否对齐开始时间
		if window.Milliseconds() > 0 {
			start = intMathFloor(start, window.Milliseconds()) * window.Milliseconds()
		}
	}

	rets, err := i.query(ctx, query, start, end)
	if err != nil {
		return storage.ErrSeriesSet(err)
	}

	qr, err := i.mergeTimeSeries(rets)
	if err != nil {
		return storage.ErrSeriesSet(err)
	}

	return remote.FromQueryResult(false, qr)
}
