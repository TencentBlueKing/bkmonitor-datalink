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
	//TODO implement me
	panic("implement me")
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

func (i *Instance) esQuery(ctx context.Context, qo *queryOption, fact *FormatFactory) (*elastic.SearchResult, error) {
	var (
		err error
		qb  = qo.query
	)
	ctx, span := trace.NewSpan(ctx, "elasticsearch-query")
	defer span.End(&err)

	filterQueries := make([]elastic.Query, 0)

	query, err := fact.Query(qb.AllConditions)
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

	// 判断是否有聚合
	if len(qb.Aggregates) > 0 {
		name, agg, aggErr := fact.EsAgg(qb.Aggregates)
		if aggErr != nil {
			return nil, aggErr
		}
		source.Size(0)
		source.Aggregation(name, agg)
	} else {
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

func (i *Instance) queryWithAgg(ctx context.Context, qo *queryOption, fact *FormatFactory, rets chan<- *TimeSeriesResult) {
	var (
		ret = TimeSeriesResultPool.Get().(*TimeSeriesResult)
		err error
	)
	ctx, span := trace.NewSpan(ctx, "query-with-aggregation")
	defer span.End(&err)

	defer func() {
		ret.Error = err
		rets <- ret
	}()

	sr, err := i.esQuery(ctx, qo, fact)
	if err != nil {
		return
	}

	ret.TimeSeriesMap, err = fact.AggDataFormat(sr.Aggregations)
	if err != nil {
		return
	}
	return
}

func (i *Instance) queryWithoutAgg(ctx context.Context, qo *queryOption, fact *FormatFactory, rets chan<- *TimeSeriesResult) {
	var (
		ret = TimeSeriesResultPool.Get().(*TimeSeriesResult)
		err error
	)
	ctx, span := trace.NewSpan(ctx, "query-without-aggregation")
	defer span.End(&err)

	defer func() {
		ret.Error = err
		rets <- ret
	}()

	sr, err := i.esQuery(ctx, qo, fact)
	if err != nil {
		return
	}

	ret.TimeSeriesMap = make(map[string]*prompb.TimeSeries)
	for _, d := range sr.Hits.Hits {
		data := make(map[string]interface{})
		if err = json.Unmarshal(d.Source, &data); err != nil {
			return
		}
		fact.SetData(data)

		lbs, vErr := fact.Labels()
		if vErr != nil {
			err = vErr
			return
		}

		sample, vErr := fact.Sample()
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
	start time.Time,
	end time.Time,
) storage.SeriesSet {
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
		return storage.ErrSeriesSet(fmt.Errorf("es client is nil"))
	}

	rets := make(chan *TimeSeriesResult, 1)

	go func() {
		defer func() {
			close(rets)
		}()

		qo := &queryOption{
			index: query.DB,
			start: start.UnixMilli(),
			end:   end.UnixMilli(),
			query: query,
		}

		mapping, err1 := i.getMapping(ctx, qo.index)
		if err1 != nil {
			rets <- &TimeSeriesResult{
				Error: err1,
			}
			return
		}
		var size int
		if query.Size > 0 || query.Size > i.maxSize {
			size = query.Size
		} else {
			size = i.maxSize
		}

		fact := NewFormatFactory(ctx).
			WithQuery(i.toEs(query.Field), qo.start, qo.end, query.Timezone, query.From, size).
			WithMapping(mapping).
			WithOrders(query.Orders).
			WithTransform(i.toEs, i.toProm)

		if len(query.Aggregates) > 0 {
			i.queryWithAgg(ctx, qo, fact, rets)
		} else {
			i.queryWithoutAgg(ctx, qo, fact, rets)
		}

		user := metadata.GetUser(ctx)
		span.Set("query-space-uid", user.SpaceUid)
		span.Set("query-source", user.Source)
		span.Set("query-username", user.Name)
		span.Set("query-option", qo)

		span.Set("query-storage-id", query.StorageID)
		span.Set("query-max-size", i.maxSize)
		span.Set("query-db", query.DB)
		span.Set("query-measurement", query.Measurement)
		span.Set("query-measurements", strings.Join(query.Measurements, ","))
		span.Set("query-field", query.Field)
		span.Set("query-fields", strings.Join(query.Fields, ","))
	}()

	qr, err := i.mergeTimeSeries(rets)
	if err != nil {
		return storage.ErrSeriesSet(err)
	}

	return remote.FromQueryResult(false, qr)
}
