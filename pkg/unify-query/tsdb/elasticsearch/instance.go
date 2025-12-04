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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	elastic "github.com/olivere/elastic/v7"
	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/storage/remote"
	"github.com/samber/lo"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
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

func NewInstance(ctx context.Context, opt *InstanceOption) (*Instance, error) {
	ins := &Instance{
		ctx:     ctx,
		maxSize: opt.MaxSize,
		connect: opt.Connect,

		headers:     opt.Headers,
		healthCheck: opt.HealthCheck,
		timeout:     opt.Timeout,
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

func (i *Instance) checkQuery(query *metadata.Query) error {
	if query == nil {
		return nil
	}

	if query.DB == "" {
		return fmt.Errorf("%s 配置的查询别名为空", query.TableID)
	}
	return nil
}

// fieldMap 获取es索引的字段映射
func (i *Instance) fieldMap(ctx context.Context, fieldAlias metadata.FieldAlias, aliases ...string) (metadata.FieldsMap, error) {
	if len(aliases) == 0 {
		return nil, fmt.Errorf("query indexes is empty")
	}

	var err error
	ctx, span := trace.NewSpan(ctx, "elasticsearch-get-mapping")
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("get mapping error: %s", r)
		}
		span.End(&err)
	}()
	span.Set("aliases", aliases)
	cli, err := i.getClient(ctx, i.connect)
	if err != nil {
		return nil, fmt.Errorf("get client error: %w", err)
	}
	defer cli.Stop()

	// 优先找 indices 接口
	settings := make(map[string]map[string]any)
	mappings := make(map[string]map[string]any)
	span.Set("get-indexes", aliases)

	cache := GetMappingCache()

	log.Infof(ctx, "[fieldMap cache] get fields map from cache: %v", aliases)

	return cache.GetFieldsMap(ctx, aliases, func(missingAlias []string) (metadata.FieldsMap, error) {
		log.Infof(ctx, "[fieldMap cache] fetch missing alias mapping: %v", missingAlias)
		span.Set("missing-alias", missingAlias)
		return fetchFieldsMap(ctx, fieldAlias, missingAlias, cli, span, mappings, settings)
	})
}

func fetchFieldsMap(ctx context.Context, fieldAlias metadata.FieldAlias, aliases []string, cli *elastic.Client, span *trace.Span, mappings map[string]map[string]any, settings map[string]map[string]any) (fieldsMap metadata.FieldsMap, err error) {
	indices, indicesErr := cli.IndexGet(aliases...).Do(ctx)
	if indicesErr != nil {
		// 兼容没有索引接口的情况，例如 bkbase
		metadata.NewMessage(
			metadata.MsgQueryES,
			"索引查询 index 接口异常: %+v",
			aliases,
		).Warn(ctx)

		span.Set("get-mapping", aliases)
		res, err := cli.GetMapping().Index(aliases...).Type("").Do(ctx)
		if err != nil {
			err = metadata.NewMessage(
				metadata.MsgQueryES,
				"索引查询异常: %+v",
				aliases,
			).Error(ctx, indicesErr)
			return nil, err
		}

		for index, r := range res {
			if nr, ok := r.(map[string]any); ok {
				mappings[index] = nr
			}
		}
	} else {
		for index, indice := range indices {
			settings[index] = indice.Settings
			mappings[index] = indice.Mappings
		}
	}

	iof := NewIndexOptionFormat(fieldAlias)

	// 忽略 mapping 为空的情况的报错
	if len(mappings) == 0 {
		fieldsMap = iof.FieldsMap()
		return fieldsMap, err
	}

	span.Set("mapping-length", len(mappings))

	indexes := make([]string, 0)
	for k := range mappings {
		indexes = append(indexes, k)
	}

	sort.Strings(indexes)

	// 按照时间倒序排列
	for idx := len(indexes) - 1; idx >= 0; idx-- {
		index := indexes[idx]
		if in, ok := mappings[index]; ok && in != nil {
			iof.Parse(settings[index], in)
		}
	}
	fieldsMap = iof.FieldsMap()
	return fieldsMap, err
}

func (i *Instance) esQuery(ctx context.Context, qo *queryOption, fact *FormatFactory) (*elastic.SearchResult, error) {
	var (
		err error
		qb  = qo.query
	)
	ctx, span := trace.NewSpan(ctx, "elasticsearch-query")
	defer func() {
		// 忽略 elastic返回的io.EOF报错
		if errors.Is(err, io.EOF) {
			err = nil
		}
		span.End(&err)
	}()

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
		q := fact.ParserQueryString(ctx, qb.QueryString, qb.IsPrefix)
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

	metadata.NewMessage(
		metadata.MsgQueryES,
		"es 查询 index: %+v, body: %s",
		qo.indexes, bodyString,
	).Info(ctx)

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
		return nil, processOnESErr(ctx, qo.conn.Address, err)
	}
	if res.Error != nil {
		err = metadata.NewMessage(
			metadata.MsgQueryES,
			"es 查询失败 index: %+v",
			qo.indexes,
		).Error(ctx, errors.New(res.Error.Reason))
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
		ctx, queryCost, metadata.ElasticsearchStorageType, qo.conn.Address,
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

	return remote.FromQueryResult(true, qr)
}

func (i *Instance) getAlias(ctx context.Context, query *metadata.Query, start, end time.Time) ([]string, error) {
	_, span := trace.NewSpan(ctx, "get-alias")

	allAlias := make([]string, 0)
	dbs := query.DBs
	if len(dbs) == 0 {
		dbs = []string{query.DB}
	}

	span.Set("dbs", dbs)

	// 多表的字段进行合并查询，进行倒序遍历
	for idx := len(dbs) - 1; idx >= 0; idx-- {
		db := dbs[idx]
		if db == "" {
			continue
		}

		alias := i.explainDB(ctx, db, query.NeedAddTime, start, end, query.SourceType)
		allAlias = append(allAlias, alias...)
	}

	span.Set("alias", allAlias)

	if len(allAlias) == 0 {
		return nil, metadata.NewMessage(
			metadata.MsgQueryES,
			"%s 构建索引异常",
			query.TableID,
		).Error(ctx, fmt.Errorf("es 查询没有匹配到索引"))
	}
	return allAlias, nil
}

func (i *Instance) explainDB(ctx context.Context, db string, needAddTime bool, start, end time.Time, sourceType string) []string {
	var (
		aliases []string
		_, span = trace.NewSpan(ctx, "explain-db")
		err     error
		loc     *time.Location
	)
	defer span.End(&err)

	if db == "" {
		return nil
	}

	aliases = strings.Split(db, ",")

	span.Set("need-add-time", needAddTime)
	if !needAddTime {
		return aliases
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
	return newAliases
}

// QueryFieldMap 查询字段映射
func (i *Instance) QueryFieldMap(ctx context.Context, query *metadata.Query, start, end time.Time) (metadata.FieldsMap, error) {
	var err error
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("es query error: %s", r)
		}
	}()

	ctx, span := trace.NewSpan(ctx, "elasticsearch-query-field-map")
	defer span.End(&err)

	err = i.checkQuery(query)
	if err != nil {
		return nil, err
	}

	aliases, err := i.getAlias(ctx, query, start, end)
	if err != nil {
		return nil, err
	}
	span.Set("query-db", query.DB)
	span.Set("query-dbs", query.DBs)
	span.Set("aliases", aliases)

	fieldMap, err := i.fieldMap(ctx, query.FieldAlias, aliases...)
	if err != nil {
		return nil, err
	}

	return fieldMap, nil
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

	err = i.checkQuery(query)
	if err != nil {
		return size, total, option, err
	}

	aliases, err := i.getAlias(ctx, query, start, end)
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

	fieldMap, err := i.fieldMap(ctx, query.FieldAlias, aliases...)
	if err != nil {
		return size, total, option, metadata.NewMessage(
			metadata.MsgQueryES,
			"字段查询异常: %+v",
			aliases,
		).Error(ctx, err)
	}
	span.Set("field-map-length", len(fieldMap))

	if i.maxSize > 0 && query.Size > i.maxSize {
		query.Size = i.maxSize
	}

	option = query.ResultTableOption
	if option != nil {
		if option.From != nil {
			query.From = *option.From
		}
	}

	labelMap := function.LabelMap(ctx, query)
	reverseAlias := make(map[string]string, len(query.FieldAlias))
	for k, v := range query.FieldAlias {
		reverseAlias[v] = k
	}

	fact := NewFormatFactory(ctx).
		WithTransform(func(s string) string {
			if s == "" {
				return ""
			}
			// 别名替换
			ns := s
			if alias, ok := reverseAlias[s]; ok {
				ns = alias
			}

			return ns
		}, func(s string) string {
			if s == "" {
				return ""
			}
			ns := s

			// 别名替换
			if alias, ok := query.FieldAlias[s]; ok {
				ns = alias
			}
			return ns
		},
		).
		WithIsReference(metadata.GetQueryParams(ctx).IsReference).
		WithQuery(query.Field, query.TimeField, qo.start, qo.end, unit, query.Size).
		WithFieldMap(fieldMap).
		WithOrders(query.Orders).
		WithIncludeValues(labelMap)

	sr, err := i.esQuery(ctx, qo, fact)
	if err != nil {
		return size, total, option, metadata.NewMessage(
			metadata.MsgQueryES,
			"原始数据查询异常",
		).Error(ctx, err)
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
				decoder := json.NewDecoder(bytes.NewReader(d.Source))
				decoder.UseNumber()
				if err = decoder.Decode(&data); err != nil {
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

	err = i.checkQuery(query)
	if err != nil {
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

	aliases, err := i.getAlias(ctx, query, start, end)
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
	fieldMap, err := i.fieldMap(ctx, query.FieldAlias, aliases...)
	if err != nil {
		metadata.NewMessage(
			metadata.MsgQueryES,
			"字段查询异常: %v",
			err,
		).Warn(ctx)
		return storage.EmptySeriesSet()
	}
	span.Set("field-map-length", len(fieldMap))

	var size int
	if query.Size > 0 && query.Size < i.maxSize {
		size = query.Size
	} else {
		size = i.maxSize
	}

	labelMap := function.LabelMap(ctx, query)

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
		WithIncludeValues(labelMap).
		WithIsReference(metadata.GetQueryParams(ctx).IsReference).
		WithQuery(query.Field, query.TimeField, qo.start, qo.end, unit, size).
		WithFieldMap(fieldMap).
		WithOrders(query.Orders)

	if len(query.Aggregates) == 0 {
		return storage.ErrSeriesSet(fmt.Errorf("aggregates is empty"))
	}

	return i.queryWithAgg(ctx, qo, fact)
}

func (i *Instance) QueryLabelNames(ctx context.Context, query *metadata.Query, start, end time.Time) ([]string, error) {
	var err error
	ctx, span := trace.NewSpan(ctx, "elasticsearch-query-label-names")
	defer span.End(&err)

	fieldMap, err := i.QueryFieldMap(ctx, query, start, end)
	if err != nil {
		return nil, err
	}

	ignoreLabelNames := []string{query.TimeField.Name, FieldTime, metadata.KeyDocID, metadata.KeyIndex}
	allFieldNames := lo.Keys(fieldMap)

	filteredLabelNames := lo.Filter(allFieldNames, func(fieldName string, _ int) bool {
		return !lo.Contains(ignoreLabelNames, fieldName)
	})

	sort.Strings(filteredLabelNames)

	span.Set("label-names-count", len(filteredLabelNames))
	return filteredLabelNames, nil
}

func (i *Instance) QueryLabelValues(ctx context.Context, query *metadata.Query, name string, start, end time.Time) ([]string, error) {
	var err error
	ctx, span := trace.NewSpan(ctx, "elasticsearch-query-label-values")
	defer span.End(&err)

	aliases, err := i.getAlias(ctx, query, start, end)
	if err != nil {
		return nil, err
	}

	qo := &queryOption{
		indexes: aliases,
		start:   start,
		end:     end,
		query:   query,
		conn:    i.connect,
	}

	fieldMap, err := i.QueryFieldMap(ctx, query, start, end)
	if err != nil {
		return nil, err
	}

	unit := metadata.GetQueryParams(ctx).TimeUnit
	fact := NewFormatFactory(ctx).
		WithQuery(name, query.TimeField, start, end, unit, 0).
		WithFieldMap(fieldMap)

	// 添加 exists 条件确保字段存在
	query.AllConditions = append(query.AllConditions, []metadata.ConditionField{
		{
			DimensionName: name,
			Value:         []string{},
			Operator:      metadata.ConditionExisted,
		},
	})

	query.Aggregates = append(query.Aggregates, metadata.Aggregate{
		Name:       Cardinality,
		Field:      name,
		Dimensions: []string{name},
		Without:    true,
	})

	searchResult, err := i.esQuery(ctx, qo, fact)
	if err != nil {
		return nil, err
	}

	var labelValues []string
	if aggs := searchResult.Aggregations; aggs != nil {
		if terms, found := aggs.Terms(name); found {
			labelValues = lo.FilterMap(terms.Buckets, func(bucket *elastic.AggregationBucketKeyItem, _ int) (string, bool) {
				if bucket.Key == nil {
					return "", false
				}
				if keyStr, ok := bucket.Key.(string); ok && keyStr != "" {
					return keyStr, true
				}
				return "", false
			})
		}
	}

	sort.Strings(labelValues)

	span.Set("label-values-count", len(labelValues))
	return labelValues, nil
}

func (i *Instance) InstanceType() string {
	return metadata.ElasticsearchStorageType
}
