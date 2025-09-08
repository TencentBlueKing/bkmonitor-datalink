// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package window

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/ahmetb/go-linq/v3"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/elastic/go-elasticsearch/v7"
	"go.uber.org/zap"
	"golang.org/x/exp/slices"
	"golang.org/x/time/rate"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	monitorLogger "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type ProcessResult struct {
	BkTenantId            string                        `json:"bk_tenant_id"`
	BizId                 string                        `json:"biz_id"`
	BizName               string                        `json:"biz_name"`
	AppId                 string                        `json:"app_id"`
	AppName               string                        `json:"app_name"`
	TraceId               string                        `json:"trace_id"`
	HierarchyCount        int                           `json:"hierarchy_count"`
	ServiceCount          int                           `json:"service_count"`
	SpanCount             int                           `json:"span_count"`
	MinStartTime          int                           `json:"min_start_time"`
	MaxEndTime            int                           `json:"max_end_time"`
	TraceDuration         int                           `json:"trace_duration"`
	SpanMaxDuration       int                           `json:"span_max_duration"`
	SpanMinDuration       int                           `json:"span_min_duration"`
	RootService           string                        `json:"root_service"`
	RootServiceSpanId     string                        `json:"root_service_span_id"`
	RootServiceSpanName   string                        `json:"root_service_span_name"`
	RootServiceStatusCode *int                          `json:"root_service_status_code"`
	RootServiceCategory   string                        `json:"root_service_category"`
	RootServiceKind       int                           `json:"root_service_kind"`
	RootSpanId            string                        `json:"root_span_id"`
	RootSpanName          string                        `json:"root_span_name"`
	RootSpanService       string                        `json:"root_span_service"`
	RootSpanKind          int                           `json:"root_span_kind"`
	Error                 bool                          `json:"error"`
	ErrorCount            int                           `json:"error_count"`
	Time                  int64                         `json:"time"`
	CategoryStatistics    map[core.SpanCategory]int     `json:"category_statistics"`
	KindStatistics        map[core.SpanKindCategory]int `json:"kind_statistics"`
	Collections           map[string][]string           `json:"collections"`
}

type Processor struct {
	ctx context.Context

	dataId string
	config ProcessorOptions

	dataIdBaseInfo      core.BaseInfo
	proxy               storage.Backend
	traceEsQueryLimiter *rate.Limiter

	// Metric discover
	metricProcessor *MetricProcessor

	logger   monitorLogger.Logger
	baseInfo core.BaseInfo

	traceEsOriginIndexName string
	traceEsIndexName       string
	indexNameLastUpdate    time.Time
}

type IndexResponse struct {
	Index string `json:"index"`
}

func (p *Processor) PreProcess(receiver chan<- storage.SaveRequest, event Event) {
	graph := event.Graph
	if p.config.infoReportEnabled {
		exist, err := p.proxy.Exist(storage.ExistRequest{Target: storage.BloomFilter, Key: event.TraceId})
		if err != nil {
			p.logger.Warnf(
				"Attempt to retrieve traceMeta from Bloom-filter failed, "+
					"this traceId: %s will be process as a new window. error: %s",
				event.TraceId, err,
			)
			metrics.RecordApmPreCalcOperateStorageFailedTotal(p.dataId, metrics.QueryBloomFilterFailed)
		} else if exist {
			existSpans := p.listSpanFromStorage(event)
			p.revertToCollect(&event, existSpans)
		}
		graph = event.Graph
		graph.RefreshEdges()
		event.Graph = graph
		p.ToTraceInfo(receiver, event)
	}
	if p.config.metricReportEnabled {
		p.metricProcessor.ToMetrics(receiver, graph)
	}
}

func (p *Processor) revertToCollect(event *Event, exists []*StandardSpan) {
	for _, s := range exists {
		s.fromHistory = true
		event.Graph.AddNode(Node{StandardSpan: *s})
	}
}

func (p *Processor) listSpanFromStorage(event Event) []*StandardSpan {
	var spans []*StandardSpan

	if p.config.enabledInfoCache {
		// list span data from the cache
		infoKey := storage.CacheTraceInfoKey.Format(p.dataIdBaseInfo.BkBizId, p.dataIdBaseInfo.AppName, event.TraceId)
		data, err := p.proxy.Query(storage.QueryRequest{Target: storage.Cache, Data: infoKey})
		if err == nil && data != nil {
			parseErr := jsonx.Unmarshal(data.([]byte), &spans)
			if parseErr != nil {
				p.logger.Infof(
					"Cache spans whose traceId is %s was found in traceInfo(key: %s), "+
						"but failed to be parsed to span list. error: %s",
					event.TraceId, infoKey, parseErr,
				)
				metrics.RecordApmPreCalcOperateStorageFailedTotal(p.dataId, metrics.QueryCacheResponseInvalid)
			} else {
				return spans
			}
		}
	}

	if !p.traceEsQueryLimiter.Allow() {
		logger.Debugf(
			"[NOTE] dataId: %s This es query exceeds the threshold %d. This request will be discarded.",
			p.dataId,
			p.config.traceEsQueryRate,
		)
		metrics.AddApmPreCalcRateLimitedCount(p.dataId, metrics.LimiterEs)
		return spans
	}

	spanBytes, err := p.proxy.Query(storage.QueryRequest{
		Target: storage.TraceEs,
		Data: storage.EsQueryData{
			IndexName: p.getQueryIndexName(),
			Body: map[string]any{
				"query": map[string]any{
					"bool": map[string]any{
						"must": []map[string]any{
							{
								"term": map[string]string{
									"trace_id": event.TraceId,
								},
							},
						},
					},
				},
				"size": core.SpanMaxSize,
			},
			Converter: storage.BytesConverter,
		},
	})
	if err != nil {
		p.logger.Errorf(
			"Data whose traceId: %s fails to be obtained from ES. "+
				"That data will be ignored, and result may be distorted. error: %s",
			event.TraceId, err,
		)
		metrics.RecordApmPreCalcOperateStorageFailedTotal(p.dataId, metrics.QueryEsFailed)
		return spans
	}

	if spanBytes == nil {
		// The trace does not exist in es. if it occurs frequently, the Bloom-Filter parameter may be set improperly.
		p.logger.Infof("The data with traceId: %s is empty from ES.", event.TraceId)
		metrics.RecordApmPreCalcOperateStorageFailedTotal(p.dataId, metrics.QueryEsReturnEmpty)
		return spans
	}
	originSpans, err := p.recoverSpans(spanBytes.([]map[string]any))
	if err != nil {
		p.logger.Errorf(
			"The data structure in ES is inconsistent, this data will be ignored. traceId: %s. error: %s ",
			event.TraceId, err,
		)
		metrics.RecordApmPreCalcOperateStorageFailedTotal(p.dataId, metrics.QueryESResponseInvalid)
		return spans
	}

	spans = append(spans, originSpans...)
	return spans
}

func (p *Processor) getQueryIndexName() string {
	obtainLastlyIndexName := func() (string, error) {
		client := p.proxy.GetClient(storage.TraceEs).(*elasticsearch.Client)
		response, err := client.Cat.Indices(
			client.Cat.Indices.WithIndex(fmt.Sprintf("%s_*_*", p.traceEsOriginIndexName)),
			client.Cat.Indices.WithFormat("json"),
			client.Cat.Indices.WithContext(p.ctx),
		)
		if err != nil {
			return "", err
		}
		defer response.Body.Close()
		if response.IsError() {
			return "", nil
		}
		var indices []IndexResponse
		if err := jsonx.Decode(response.Body, &indices); err != nil {
			return "", nil
		}
		indexNames := p.filterAndSortValidIndexNames(indices)
		if len(indexNames) == 0 {
			return "", nil
		}
		// 只查询最新索引的数据
		return indexNames[0], nil
	}

	if time.Since(p.indexNameLastUpdate) >= 24*time.Hour {
		// calculate lastly index name
		n, err := obtainLastlyIndexName()
		if err != nil {
			logger.Warnf("Obtain lastly index name failed, error: %s", err)
		} else {
			p.traceEsIndexName = n
		}
		p.indexNameLastUpdate = time.Now()
	}

	return p.traceEsIndexName
}

func (p *Processor) filterAndSortValidIndexNames(indices []IndexResponse) []string {
	var dateIndexPairs []struct {
		date time.Time
		name string
	}

	pattern := regexp.MustCompile(fmt.Sprintf(".*_bkapm_trace_%s_(\\d{8})_\\d+$", regexp.QuoteMeta(p.baseInfo.AppName)))

	for _, info := range indices {
		name := info.Index
		matches := pattern.FindStringSubmatch(name)
		if len(matches) > 1 {
			dateStr := matches[1]
			date, err := time.Parse("20060102", dateStr)
			if err == nil {
				dateIndexPairs = append(dateIndexPairs, struct {
					date time.Time
					name string
				}{date: date, name: name})
			}
		}
	}

	// Sort by time
	sort.Slice(dateIndexPairs, func(i, j int) bool {
		return dateIndexPairs[i].date.After(dateIndexPairs[j].date)
	})

	validIndexNames := make([]string, len(dateIndexPairs))
	for i, pair := range dateIndexPairs {
		validIndexNames[i] = pair.name
	}

	return validIndexNames
}

func (p *Processor) recoverSpans(originSpans []map[string]any) ([]*StandardSpan, error) {
	var res []*StandardSpan

	for _, s := range originSpans {
		res = append(res, ToStandardSpanFromMapping(s))
	}

	return res, nil
}

func (p *Processor) ToTraceInfo(receiver chan<- storage.SaveRequest, event Event) {
	nodeDegrees := event.Graph.NodeDepths()

	services := mapset.NewSet[string]()
	var startTimes []int
	var endTimes []int
	var duration []int
	var errorCount int
	categoryStatistics := initCategoryStatistics()
	kindCategoryStatistics := initKindCategoryStatistics()
	collections := make(map[string][]string, len(core.StandardFields))

	for _, span := range event.Graph.Nodes {
		if svrName := span.GetFieldValue(core.ServiceNameField); svrName != "" {
			services.Add(svrName)
		}

		startTimes = append(startTimes, span.StartTime)
		endTimes = append(endTimes, span.EndTime)
		duration = append(duration, span.EndTime-span.StartTime)
		if span.StatusCode == core.StatusCodeError {
			errorCount++
		}
		processCategoryStatistics(span.Collections, categoryStatistics)
		processKindCategoryStatistics(span.Kind, kindCategoryStatistics)
		collectCollections(collections, span.Collections)
		metrics.RecordHandleTraceDelta(p.dataId, span.StartTime)
	}

	sort.Ints(startTimes)
	sort.Ints(endTimes)
	sort.Ints(duration)

	// Root Span
	var rootSpan Node
	if len(nodeDegrees) != 0 {
		sort.Slice(nodeDegrees, sortNode(nodeDegrees))
		rootSpan = nodeDegrees[0].Node
	} else {
		rootSpan = Node{StandardSpan: StandardSpan{}}
	}

	// Root Service Span
	var calledKindSpans []NodeDegree
	linq.From(nodeDegrees).Where(func(i any) bool {
		item := i.(NodeDegree)
		return core.SpanKind(item.Node.Kind).IsCalledKind()
	}).ToSlice(&calledKindSpans)
	sort.Slice(calledKindSpans, sortNode(calledKindSpans))
	var rootServiceSpan Node
	var rSc string
	var rootServiceName string
	if len(calledKindSpans) != 0 {
		rootServiceSpan = calledKindSpans[0].Node
		rootServiceCategory, _ := inferCategory(rootServiceSpan.Collections)
		rSc = string(rootServiceCategory)
		rootServiceName = rootServiceSpan.GetFieldValue(core.ServiceNameField)
	} else {
		rootServiceSpan = Node{StandardSpan: StandardSpan{}}
	}
	// status code of trace originates from http/rpc
	var statusCodeOptional int
	foundStatusCode := false
	if v := rootServiceSpan.GetFieldValue(core.HttpStatusCodeField); v != "" {
		vI, _ := strconv.Atoi(v)
		statusCodeOptional = vI
		foundStatusCode = true
	} else if v = rootServiceSpan.GetFieldValue(core.RpcGrpcStatusCode); v != "" {
		vI, _ := strconv.Atoi(v)
		statusCodeOptional = vI
		foundStatusCode = true
	}

	res := ProcessResult{
		BkTenantId:          p.baseInfo.BkTenantId,
		BizId:               p.baseInfo.BkBizId,
		BizName:             p.baseInfo.BkBizName,
		AppId:               p.baseInfo.AppId,
		AppName:             p.baseInfo.AppName,
		TraceId:             event.TraceId,
		HierarchyCount:      event.Graph.LongestPath() + 1,
		ServiceCount:        len(services.ToSlice()),
		SpanCount:           event.Graph.Length(),
		MinStartTime:        startTimes[0],
		MaxEndTime:          endTimes[len(endTimes)-1],
		TraceDuration:       endTimes[len(endTimes)-1] - startTimes[0],
		SpanMaxDuration:     duration[len(duration)-1],
		SpanMinDuration:     duration[0],
		RootService:         rootServiceName,
		RootServiceSpanId:   rootServiceSpan.SpanId,
		RootServiceSpanName: rootServiceSpan.SpanName,
		RootServiceCategory: rSc,
		RootServiceKind:     rootServiceSpan.Kind,
		RootSpanId:          rootSpan.SpanId,
		RootSpanName:        rootSpan.SpanName,
		RootSpanService:     rootSpan.GetFieldValue(core.ServiceNameField),
		RootSpanKind:        rootSpan.Kind,
		Error:               errorCount != 0,
		ErrorCount:          errorCount,
		Time:                time.Now().UnixNano() / 1e3,
		CategoryStatistics:  categoryStatistics,
		KindStatistics:      kindCategoryStatistics,
		Collections:         collections,
	}
	if foundStatusCode {
		// determine statusCode additionally so that this field can support <null> value in json
		res.RootServiceStatusCode = &statusCodeOptional
	}

	p.sendStorageRequests(receiver, res, event)
}

func (p *Processor) sendStorageRequests(receiver chan<- storage.SaveRequest, result ProcessResult, event Event) {
	if p.config.enabledInfoCache {
		spanBytes, _ := jsonx.Marshal(event.Graph.StandardSpans())
		receiver <- storage.SaveRequest{
			Target: storage.Cache,
			Data: storage.CacheStorageData{
				DataId: p.dataId,
				Key:    storage.CacheTraceInfoKey.Format(p.dataIdBaseInfo.BkBizId, p.dataIdBaseInfo.AppName, event.TraceId),
				Value:  spanBytes,
				Ttl:    storage.CacheTraceInfoKey.Ttl,
			},
		}
	}

	receiver <- storage.SaveRequest{
		Target: storage.BloomFilter,
		Data: storage.BloomStorageData{
			DataId: p.dataId,
			Key:    event.TraceId,
		},
	}

	resultBytes, _ := jsonx.Marshal(result)
	receiver <- storage.SaveRequest{
		Target: storage.SaveEs,
		Data: storage.EsStorageData{
			DataId:     p.dataId,
			DocumentId: result.TraceId,
			Value:      resultBytes,
		},
	}
}

func sortNode(nodeDegrees []NodeDegree) func(a, b int) bool {
	return func(a, b int) bool {
		aItem := nodeDegrees[a]
		bItem := nodeDegrees[b]

		if aItem.Degree != bItem.Degree {
			return aItem.Degree < bItem.Degree
		}
		return aItem.Node.StartTime < bItem.Node.StartTime
	}
}

func initCategoryStatistics() map[core.SpanCategory]int {
	return map[core.SpanCategory]int{
		core.CategoryHttp:         0,
		core.CategoryRpc:          0,
		core.CategoryDb:           0,
		core.CategoryMessaging:    0,
		core.CategoryAsyncBackend: 0,
		core.CategoryOther:        0,
	}
}

func inferCategory(collections map[string]string) (core.SpanCategory, bool) {
	var matchCategory core.SpanCategory
	var isMatch bool
	for _, predicate := range core.CategoryPredicateFields {
		match := true

		if len(predicate.OptionFields) != 0 {
			match = linq.From(predicate.OptionFields).Where(func(i any) bool {
				v := i.(core.CommonField)
				_, exist := collections[v.DisplayKey()]
				return exist
			}).Any()
			if !match {
				continue
			}
		}

		match = linq.From(predicate.AnyFields).Where(func(i any) bool {
			v := i.(core.CommonField)
			_, exist := collections[v.DisplayKey()]
			return exist
		}).Any()
		if match {
			matchCategory = predicate.Category
			isMatch = true
			// if span contains multiple category fields, the count is not repeated
			break
		}
	}

	return matchCategory, isMatch
}

func processCategoryStatistics(collections map[string]string, s map[core.SpanCategory]int) {
	category, match := inferCategory(collections)
	if match {
		s[category]++
	}
}

func initKindCategoryStatistics() map[core.SpanKindCategory]int {
	return map[core.SpanKindCategory]int{
		core.KindCategoryUnspecified: 0,
		core.KindCategoryInterval:    0,
		core.KindCategorySync:        0,
		core.KindCategoryAsync:       0,
	}
}

func processKindCategoryStatistics(kind int, s map[core.SpanKindCategory]int) {
	k := core.SpanKind(kind).ToKindCategory()
	if k != "" {
		s[k]++
	}
}

func collectCollections(collections map[string][]string, spanCollections map[string]string) {
	for k, v := range spanCollections {
		items, exist := collections[k]
		if exist {
			if !slices.Contains(items, v) {
				items = append(items, v)
			}
		} else {
			items = []string{v}
		}
		collections[k] = items
	}
}

type ProcessorOptions struct {
	enabledInfoCache          bool
	traceEsQueryRate          int
	metricReportEnabled       bool
	infoReportEnabled         bool
	metricLayer4ReportEnabled bool
}

type ProcessorOption func(*ProcessorOptions)

// EnabledTraceInfoCache Whether to enable Storing the latest trace data into cache.
// If this is enabled, the query frequency of elasticsearch is reduced.
func EnabledTraceInfoCache(b bool) ProcessorOption {
	return func(options *ProcessorOptions) {
		options.enabledInfoCache = b
	}
}

// TraceEsQueryRate To prevent too many es queries caused by bloom-filter,
// each dataId needs to set a threshold for the maximum number of requests in a minute. default is 20
func TraceEsQueryRate(r int) ProcessorOption {
	return func(options *ProcessorOptions) {
		options.traceEsQueryRate = r
	}
}

// TraceMetricsReportEnabled enable the metrics report
func TraceMetricsReportEnabled(e bool) ProcessorOption {
	return func(options *ProcessorOptions) {
		options.metricReportEnabled = e
	}
}

// TraceInfoReportEnabled enable the trace info report
func TraceInfoReportEnabled(e bool) ProcessorOption {
	return func(options *ProcessorOptions) {
		options.infoReportEnabled = e
	}
}

func TraceMetricsLayer4ReportEnabled(e bool) ProcessorOption {
	return func(options *ProcessorOptions) {
		options.metricLayer4ReportEnabled = e
	}
}

func NewProcessor(ctx context.Context, dataId string, storageProxy *storage.Proxy, options ...ProcessorOption) Processor {
	opts := ProcessorOptions{}
	for _, setter := range options {
		setter(&opts)
	}

	limiter := rate.NewLimiter(rate.Every(time.Minute/time.Duration(opts.traceEsQueryRate)), opts.traceEsQueryRate)
	logger.Infof(
		"[NewProcessor] es query limiter, dataId: %s rate: %d metricReport: %t",
		dataId, opts.traceEsQueryRate, opts.metricReportEnabled,
	)

	return Processor{
		ctx:                 ctx,
		dataId:              dataId,
		config:              opts,
		dataIdBaseInfo:      core.GetMetadataCenter().GetBaseInfo(dataId),
		proxy:               storageProxy,
		traceEsQueryLimiter: limiter,
		logger: monitorLogger.With(
			zap.String("location", "processor"),
			zap.String("dataId", dataId),
		),
		metricProcessor:        newMetricProcessor(ctx, dataId, opts.metricLayer4ReportEnabled),
		baseInfo:               core.GetMetadataCenter().GetBaseInfo(dataId),
		indexNameLastUpdate:    time.Now().Add(-24 * time.Hour),
		traceEsOriginIndexName: core.GetMetadataCenter().GetTraceEsConfig(dataId).IndexName,
		traceEsIndexName:       fmt.Sprintf("%s*", core.GetMetadataCenter().GetTraceEsConfig(dataId).IndexName),
	}
}
