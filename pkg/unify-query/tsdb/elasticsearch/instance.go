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
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	elastic "github.com/olivere/elastic/v7"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/storage/remote"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/pool"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

var _ tsdb.Instance = (*Instance)(nil)

type Instance struct {
	tsdb.DefaultInstance

	ctx context.Context
	wg  sync.WaitGroup

	lock sync.Mutex

	connect Connect

	healthCheck bool

	headers map[string]string

	timeout time.Duration
	maxSize int
}

type Connect struct {
	Address  string
	UserName string
	Password string
}

func (c Connect) String() string {
	var s strings.Builder
	s.WriteString(c.Address)
	return s.String()
}

type InstanceOption struct {
	Connect Connect

	MaxSize     int
	MaxRouting  int
	Timeout     time.Duration
	Headers     map[string]string
	HealthCheck bool
}

type queryOption struct {
	indexes []string
	start   time.Time
	end     time.Time

	timeZone string

	conn Connect

	query *metadata.Query
}

var TimeSeriesResultPool = sync.Pool{
	New: func() any {
		return &TimeSeriesResult{}
	},
}

func NewInstance(ctx context.Context, opt *InstanceOption) (*Instance, error) {
	ins := &Instance{
		ctx:     ctx,
		maxSize: opt.MaxSize,
		connect: opt.Connect,

		headers:     opt.Headers,
		healthCheck: opt.HealthCheck,
		timeout:     opt.Timeout,
	}

	if opt.MaxRouting > 0 {
		err := pool.Tune(opt.MaxRouting)
		if err != nil {
			return ins, err
		}
	}

	return ins, nil
}

func (i *Instance) getClient(ctx context.Context, connect Connect) (*elastic.Client, error) {
	cliOpts := []elastic.ClientOptionFunc{
		elastic.SetURL(connect.Address),
		elastic.SetSniff(false),
		elastic.SetHealthcheck(i.healthCheck),
	}
	ctx, cancel := context.WithTimeout(ctx, i.timeout)
	defer cancel()

	headers := metadata.Headers(ctx, i.headers)
	if len(headers) > 0 {
		httpHeaders := make(http.Header, len(headers))
		for k, v := range headers {
			httpHeaders[k] = []string{v}
		}
		cliOpts = append(cliOpts, elastic.SetHeaders(httpHeaders))
	}

	if connect.UserName != "" && connect.Password != "" {
		cliOpts = append(
			cliOpts,
			elastic.SetBasicAuth(connect.UserName, connect.Password),
		)
	}

	return elastic.DialContext(ctx, cliOpts...)
}

func (i *Instance) Check(ctx context.Context, promql string, start, end time.Time, step time.Duration) string {
	return ""
}

func (i *Instance) getMappings(ctx context.Context, conn Connect, aliases []string) ([]map[string]any, error) {
	var err error

	ctx, span := trace.NewSpan(ctx, "elasticsearch-get-mapping")
	defer span.End(&err)

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("get mapping error: %s", r)
		}
		span.End(&err)
	}()

	span.Set("alias", aliases)
	client, err := i.getClient(ctx, conn)
	if err != nil {
		return nil, err
	}
	defer client.Stop()

	mappingMap, err := client.GetMapping().Index(aliases...).Type("").Do(ctx)
	if err != nil {
		log.Warnf(ctx, "get mapping error: %s", err.Error())
		return nil, err
	}

	indexes := make([]string, 0, len(mappingMap))
	for index := range mappingMap {
		indexes = append(indexes, index)
	}
	// 按照正序排列，最新的覆盖老的
	sort.Strings(indexes)
	span.Set("indexes", indexes)

	mappings := make([]map[string]any, 0, len(mappingMap))
	for _, index := range indexes {
		if mapping, ok := mappingMap[index].(map[string]any)["mappings"].(map[string]any); ok {
			mappings = append(mappings, mapping)
		}
	}

	return mappings, nil
}

func (i *Instance) esQuery(ctx context.Context, qo *queryOption, fact *FormatFactory) (*elastic.SearchResult, error) {
	var (
		err error
		qb  = qo.query
	)
	ctx, span := trace.NewSpan(ctx, "elasticsearch-query")
	defer span.End(&err)

	filterQueries := make([]elastic.Query, 0)

	// 过滤条件生成 elastic.query
	query, err := fact.Query(qb.AllConditions)
	if err != nil {
		return nil, err
	}
	if query != nil {
		filterQueries = append(filterQueries, query)
	}

	// 查询时间生成 elastic.query
	rangeQuery, err := fact.RangeQuery()
	if err != nil {
		return nil, err
	}
	filterQueries = append(filterQueries, rangeQuery)

	// querystring 生成 elastic.query
	if qb.QueryString != "" {
		qs := NewQueryString(qb.QueryString, qb.IsPrefix, fact.NestedField)
		q, qsErr := qs.ToDSL(ctx, qb.FieldAlias)
		if qsErr != nil {
			return nil, qsErr
		}
		if q != nil {
			filterQueries = append(filterQueries, q)
		}
	}

	source := elastic.NewSearchSource()
	for _, order := range fact.Orders() {
		source.Sort(order.Name, order.Ast)
	}

	if len(filterQueries) > 0 {
		esQuery := elastic.NewBoolQuery().Filter(filterQueries...)
		source.Query(esQuery)
	}

	sources := fact.Source(qb.Source)
	if len(sources) > 0 {
		fetchSource := elastic.NewFetchSourceContext(true)
		fetchSource.Include(sources...)
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
		source.Size(qb.Size)
		if qb.Scroll == "" {
			source.From(qb.From)
		}
	}

	collapse := fact.Collapse(qb.Collapse)
	if collapse != "" {
		source.Collapse(elastic.NewCollapseBuilder(collapse))
	}

	if source == nil {
		return nil, fmt.Errorf("empty es query source")
	}

	body, _ := source.Source()
	if body == nil {
		return nil, fmt.Errorf("empty query body")
	}

	qbString, _ := json.Marshal(qb)

	span.Set("metadata-query", qbString)
	span.Set("query-connect", qo.conn.String())
	span.Set("query-headers", i.headers)

	span.Set("query-indexes", qo.indexes)

	bodyJson, _ := json.Marshal(body)
	bodyString := string(bodyJson)
	span.Set("query-body", bodyString)

	log.Infof(ctx, "elasticsearch-query indexes: %s", qo.indexes)
	log.Infof(ctx, "elasticsearch-query body: %s", bodyString)

	startAnalyze := time.Now()
	client, err := i.getClient(ctx, qo.conn)
	if err != nil {
		return nil, err
	}
	defer client.Stop()
	opt := qb.ResultTableOption
	var res *elastic.SearchResult
	func() {
		if opt != nil {
			if opt.ScrollID != "" {
				span.Set("query-scroll-id", opt.ScrollID)
				res, err = client.Scroll(qo.indexes...).Scroll(qb.Scroll).ScrollId(opt.ScrollID).Do(ctx)
				return
			}

			if len(opt.SearchAfter) > 0 {
				span.Set("query-search-after", opt.SearchAfter)
				source.SearchAfter(opt.SearchAfter...)
				res, err = client.Search().Index(qo.indexes...).SearchSource(source).Do(ctx)
				return
			}
		}

		if qb.Scroll != "" {
			span.Set("query-scroll", qb.Scroll)
			scroll := client.Scroll(qo.indexes...).Scroll(qb.Scroll).SearchSource(source)
			option := qb.ResultTableOption
			if option != nil {
				if option.ScrollID != "" {
					span.Set("query-scroll-id", option.ScrollID)
					scroll.ScrollId(option.ScrollID)
				}
				if option.SliceMax > 1 {
					span.Set("query-scroll-slice", fmt.Sprintf("%d/%d", option.SliceIndex, option.SliceMax))
					scroll.Slice(elastic.NewSliceQuery().Id(option.SliceIndex).Max(option.SliceMax))
				}

			}
			res, err = scroll.Do(ctx)
		} else {
			span.Set("query-from", qb.From)
			res, err = client.Search().Index(qo.indexes...).SearchSource(source).Do(ctx)
		}
	}()

	if err != nil {
		var (
			e   *elastic.Error
			msg strings.Builder
		)
		if errors.As(err, &e) {
			if e.Details != nil {
				if len(e.Details.RootCause) > 0 {
					msg.WriteString("root cause: \n")
					for _, rc := range e.Details.RootCause {
						msg.WriteString(fmt.Sprintf("%s: %s \n", rc.Index, rc.Reason))
					}
				}

				if e.Details.CausedBy != nil {
					msg.WriteString("caused by: \n")
					for k, v := range e.Details.CausedBy {
						msg.WriteString(fmt.Sprintf("%s: %v \n", k, v))
					}
				}
			}

			return nil, errors.New(msg.String())
		} else if err.Error() == "EOF" {
			return nil, nil
		} else {
			return nil, err
		}
	}

	if res.Error != nil {
		err = fmt.Errorf("es query %v error: %s", qo.indexes, res.Error.Reason)
	}

	if res.Hits != nil {
		span.Set("total_hits", res.Hits.TotalHits)
		span.Set("hits_length", len(res.Hits.Hits))
	}
	if res.Aggregations != nil {
		span.Set("aggregations_length", len(res.Aggregations))
	}

	queryCost := time.Since(startAnalyze)
	span.Set("query-cost", queryCost.String())

	metric.TsDBRequestSecond(
		ctx, queryCost, consul.ElasticsearchStorageType, qo.conn.Address,
	)
	return res, err
}

func (i *Instance) queryWithAgg(ctx context.Context, qo *queryOption, fact *FormatFactory) storage.SeriesSet {
	var err error
	ctx, span := trace.NewSpan(ctx, "query-with-aggregation")
	defer func() {
		span.End(&err)
	}()

	span.Set("query-conn", qo.conn)

	metricLabel := qo.query.MetricLabels(ctx)

	sr, err := i.esQuery(ctx, qo, fact)
	if err != nil {
		return storage.ErrSeriesSet(err)
	}

	if sr == nil || sr.Aggregations == nil {
		return storage.EmptySeriesSet()
	}

	// 如果是非时间聚合计算，则无需进行指标名的拼接作用
	qr, err := fact.AggDataFormat(sr.Aggregations, metricLabel)
	if err != nil {
		return storage.ErrSeriesSet(err)
	}

	span.Set("time-series-length", len(qr.Timeseries))

	return remote.FromQueryResult(false, qr)
}

func (i *Instance) getAlias(ctx context.Context, db string, needAddTime bool, start, end time.Time, sourceType string) ([]string, error) {
	var (
		aliases []string
		_, span = trace.NewSpan(ctx, "get-alias")
		err     error
		loc     *time.Location
	)
	defer span.End(&err)

	if db == "" {
		return nil, fmt.Errorf("alias is empty")
	}

	aliases = strings.Split(db, ",")

	span.Set("need-add-time", needAddTime)
	if !needAddTime {
		return aliases, nil
	}

	span.Set("source-type", sourceType)

	// bkdata 数据源使用东八区创建别名，而自建 es 则使用 UTC 创建别名，所以需要特殊处理该逻辑
	var timezone string
	if sourceType == structured.BkData {
		timezone = "Asia/Shanghai"
	} else {
		timezone = "UTC"
	}

	loc, err = time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	start = start.In(loc)
	end = end.In(loc)

	left := end.Unix() - start.Unix()
	// 超过 6 个月

	span.Set("timezone", loc.String())
	span.Set("start", start.String())
	span.Set("end", end.String())
	span.Set("left", left)

	var unit string

	if left > int64(time.Hour.Seconds()*24*14) {
		halfYear := time.Hour * 24 * 30 * 6
		if left > int64(halfYear.Seconds()) {
			start = end.Add(halfYear * -1)
		}

		unit = "month"
	} else {
		unit = "day"
	}

	newAliases := make([]string, 0)
	dates := function.RangeDateWithUnit(unit, start, end, 1)

	for _, d := range dates {
		for _, alias := range aliases {
			newAliases = append(newAliases, fmt.Sprintf("%s_%s*", alias, d))
		}
	}

	span.Set("new_alias_num", len(newAliases))
	return newAliases, nil
}

// QueryRawData 直接查询原始返回
func (i *Instance) QueryRawData(ctx context.Context, query *metadata.Query, start, end time.Time, dataCh chan<- map[string]any) (size int64, total int64, option *metadata.ResultTableOption, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("es query error: %s", r)
		}
	}()

	ctx, span := trace.NewSpan(ctx, "elasticsearch-query-raw")
	defer span.End(&err)

	span.Set("instance-connect", i.connect.String())
	span.Set("instance-query-result-table-option", query.ResultTableOption)

	if query.DB == "" {
		err = fmt.Errorf("%s 配置的查询别名为空", query.TableID)
		return size, total, option, err
	}

	aliases, err := i.getAlias(ctx, query.DB, query.NeedAddTime, start, end, query.SourceType)
	if err != nil {
		return size, total, option, err
	}

	unit := metadata.GetQueryParams(ctx).TimeUnit
	span.Set("aliases", aliases)

	qo := &queryOption{
		indexes: aliases,
		start:   start,
		end:     end,
		query:   query,
		conn:    i.connect,
	}

	mappings, mappingErr := i.getMappings(ctx, qo.conn, aliases)
	if len(mappings) == 0 {
		log.Warnf(ctx, "index is empty with %v with %s error %s", aliases, qo.conn.String(), mappingErr)
		return size, total, option, err
	}
	span.Set("mapping-length", len(mappings))

	if i.maxSize > 0 && query.Size > i.maxSize {
		query.Size = i.maxSize
	}

	option = query.ResultTableOption
	if option != nil {
		if option.From != nil {
			query.From = *option.From
		}
	}

	queryLabelMaps, queryLabelErr := query.LabelMap()
	if queryLabelErr != nil {
		log.Warnf(ctx, "query label map error: %s", queryLabelErr)
	}

	encodeFunc := metadata.GetFieldFormat(ctx).EncodeFunc()
	decodeFunc := metadata.GetFieldFormat(ctx).DecodeFunc()
	reverseAlias := make(map[string]string, len(query.FieldAlias))
	for k, v := range query.FieldAlias {
		reverseAlias[v] = k
	}

	fact := NewFormatFactory(ctx).
		WithTransform(func(s string) string {
			// 别名替换
			ns := s
			if alias, ok := reverseAlias[ns]; ok {
				ns = alias
			}

			// 格式转换
			if encodeFunc != nil {
				ns = encodeFunc(ns)
			}
			return ns
		}, func(s string) string {
			ns := s
			// 格式转换
			if decodeFunc != nil {
				ns = decodeFunc(ns)
			}

			// 别名替换
			if alias, ok := query.FieldAlias[ns]; ok {
				ns = alias
			}
			return ns
		},
		).
		WithIsReference(metadata.GetQueryParams(ctx).IsReference).
		WithQuery(query.Field, query.TimeField, qo.start, qo.end, unit, query.Size).
		WithMappings(mappings...).
		WithOrders(query.Orders).
		WithIncludeValues(queryLabelMaps)

	sr, err := i.esQuery(ctx, qo, fact)
	if err != nil {
		log.Errorf(ctx, fmt.Sprintf("es query raw data error: %s", err.Error()))
		return size, total, option, err
	}

	option = &metadata.ResultTableOption{
		FieldType: fact.FieldType(),
		From:      &query.From,
	}

	if sr != nil {
		if sr.Hits != nil {

			span.Set("instance-out-list-size", len(sr.Hits.Hits))

			for idx, d := range sr.Hits.Hits {
				data := make(map[string]any)
				if err = json.Unmarshal(d.Source, &data); err != nil {
					return size, total, option, err
				}

				fact.SetData(data)

				// 注入别名
				for k, v := range reverseAlias {
					if _, ok := fact.data[k]; ok {
						fact.data[v] = fact.data[k]
						// TODO: 等前端适配之后，再移除
						// delete(fact.data, k)
					}
				}

				fact.data[metadata.KeyDocID] = d.Id
				fact.data[metadata.KeyIndex] = d.Index
				query.DataReload(fact.data)

				if timeValue, ok := data[fact.GetTimeField().Name]; ok {
					fact.data[FieldTime] = timeValue
				}

				if idx == len(sr.Hits.Hits)-1 && d.Sort != nil {
					option.SearchAfter = d.Sort
				}

				dataCh <- fact.data
			}

			if sr.Hits.TotalHits != nil {
				total = sr.Hits.TotalHits.Value
			}
			size = int64(len(sr.Hits.Hits))
		}

		if query.Scroll != "" {
			var originalOption *metadata.ResultTableOption
			originalOption = query.ResultTableOption

			option.ScrollID = sr.ScrollId

			if originalOption != nil {
				option.SliceIndex = originalOption.SliceIndex
				option.SliceMax = originalOption.SliceMax
			}
		}
	}

	span.Set("instance-out-total", total)
	span.Set("instance-out-result-table-option", option)

	return size, total, option, err
}

// QuerySeriesSet 给 PromEngine 提供查询接口
func (i *Instance) QuerySeriesSet(
	ctx context.Context,
	query *metadata.Query,
	start time.Time,
	end time.Time,
) storage.SeriesSet {
	var err error

	ctx, span := trace.NewSpan(ctx, "elasticsearch-query-series-set")
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("es query panic error: %s", r)
		}
		span.End(&err)
	}()

	if len(query.Aggregates) == 0 {
		err = fmt.Errorf("聚合函数不能为空以及聚合周期跟 Step 必须一样")
		return storage.ErrSeriesSet(err)
	}

	if query.DB == "" {
		err = fmt.Errorf("%s 配置的查询别名为空", query.TableID)
		return storage.ErrSeriesSet(err)
	}

	unit := metadata.GetQueryParams(ctx).TimeUnit

	rangeLeftTime := end.Sub(start)
	metric.TsDBRequestRangeMinute(ctx, rangeLeftTime, i.InstanceType())

	user := metadata.GetUser(ctx)
	span.Set("query-space-uid", user.SpaceUID)
	span.Set("query-source", user.Source)
	span.Set("query-username", user.Name)
	span.Set("query-connects", i.connect.String())

	span.Set("query-storage-id", query.StorageID)

	span.Set("query-max-size", i.maxSize)
	span.Set("query-db", query.DB)
	span.Set("query-measurement", query.Measurement)
	span.Set("query-measurements", query.Measurements)
	span.Set("query-field", query.Field)
	span.Set("query-fields", query.Fields)

	aliases, err := i.getAlias(ctx, query.DB, query.NeedAddTime, start, end, query.SourceType)
	if err != nil {
		return storage.ErrSeriesSet(err)
	}

	span.Set("query-aliases", aliases)

	qo := &queryOption{
		indexes: aliases,
		start:   start,
		end:     end,
		query:   query,
		conn:    i.connect,
	}
	mappings, errMapping := i.getMappings(ctx, qo.conn, qo.indexes)
	// index 不存在，mappings 获取异常直接返回空
	if len(mappings) == 0 {
		log.Warnf(ctx, "index is empty with %v with %s error %v", qo.indexes, qo.conn.String(), errMapping)
		return storage.EmptySeriesSet()
	}
	span.Set("mapping-length", len(mappings))

	var size int
	if query.Size > 0 && query.Size < i.maxSize {
		size = query.Size
	} else {
		size = i.maxSize
	}

	queryLabelMap, queryLabelErr := query.LabelMap()
	if queryLabelErr != nil {
		log.Warnf(ctx, "query label map error: %s", queryLabelErr)
	}

	encodeFunc := metadata.GetFieldFormat(ctx).EncodeFunc()
	decodeFunc := metadata.GetFieldFormat(ctx).DecodeFunc()

	reverseAlias := make(map[string]string, len(query.FieldAlias))
	for k, v := range query.FieldAlias {
		reverseAlias[v] = k
	}

	fact := NewFormatFactory(ctx).
		WithTransform(func(s string) string {
			// 别名替换
			ns := s
			if alias, ok := reverseAlias[ns]; ok {
				ns = alias
			}

			// 格式转换
			if encodeFunc != nil {
				ns = encodeFunc(ns)
			}
			return ns
		}, func(s string) string {
			ns := s
			// 格式转换
			if decodeFunc != nil {
				ns = decodeFunc(ns)
			}

			// 别名替换
			if alias, ok := query.FieldAlias[ns]; ok {
				ns = alias
			}
			return ns
		},
		).
		WithIncludeValues(queryLabelMap).
		WithIsReference(metadata.GetQueryParams(ctx).IsReference).
		WithQuery(query.Field, query.TimeField, qo.start, qo.end, unit, size).
		WithMappings(mappings...).
		WithOrders(query.Orders)

	if len(query.Aggregates) == 0 {
		return storage.ErrSeriesSet(fmt.Errorf("aggregates is empty"))
	}

	return i.queryWithAgg(ctx, qo, fact)
}

func (i *Instance) InstanceType() string {
	return consul.ElasticsearchStorageType
}
