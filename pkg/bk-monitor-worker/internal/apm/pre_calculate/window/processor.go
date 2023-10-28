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
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/ahmetb/go-linq/v3"
	mapset "github.com/deckarep/golang-set/v2"
	jsoniter "github.com/json-iterator/go"
	log "github.com/sirupsen/logrus"
	"github.com/valyala/fastjson"
	"go.uber.org/zap"
	"golang.org/x/exp/slices"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/storage"
	monitorLogger "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type ProcessTraceMeta struct {
	Runtime Runtime
}

type ProcessResult struct {
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
	dataId string
	config ProcessorOptions

	dataIdBaseInfo core.BaseInfo
	proxy          *storage.Proxy

	logger monitorLogger.Logger
}

func (p *Processor) PreProcess(receiver chan<- storage.SaveRequest, event Event) {

	exist, err := p.proxy.Exist(storage.ExistRequest{Target: storage.BloomFilter, Key: p.getBloomKey(event.TraceId)})
	if err != nil {
		p.logger.Warnf("Attempt to retrieve traceMeta from Bloom-filter failed, this traceId: %s will be process as a new window. error: %s", event.TraceId, err)
		p.Process(receiver, event)
	} else {
		if exist {
			existSpans := p.listSpanFromStorage(event)
			p.revertToCollect(&event, existSpans)
		}
		p.Process(receiver, event)
	}
}

func (p *Processor) getBloomKey(traceId string) string {
	return fmt.Sprintf("%s%s", p.dataIdBaseInfo.AppId, traceId[:p.config.traceMetaCutLength])
}

func (p *Processor) revertToCollect(event *Event, exists []*StandardSpan) {
	event.Spans = append(event.Spans, exists...)
	for _, s := range exists {
		event.Graph.AddNode(&Node{StandardSpan: s})
	}
}

func (p *Processor) listSpanFromStorage(event Event) []*StandardSpan {
	var spans []*StandardSpan

	if p.config.enabledInfoCache {
		// list span data from the cache
		infoKey := storage.CacheTraceInfoKey.Format(p.dataIdBaseInfo.BkBizId, p.dataIdBaseInfo.AppName, event.TraceId)
		data, err := p.proxy.Query(storage.QueryRequest{Target: storage.Cache, Data: infoKey})
		if err == nil && data != nil {
			parseErr := jsoniter.Unmarshal(data.([]byte), &spans)
			if parseErr != nil {
				p.logger.Infof("Cache spans whose traceId is %s was found in traceInfo(key: %s), but failed to be parsed to span list. error: %s", event.TraceId, infoKey, parseErr)
			} else {
				return spans
			}
		}
	}

	spanBytes, err := p.proxy.Query(storage.QueryRequest{
		Target: storage.TraceEs,
		Data: storage.EsQueryData{
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
		log.Errorf("Data whose traceId: %s fails to be obtained from ES. That data will be ignored, and result may be distorted. error: %s", event.TraceId, err)
		return spans
	}
	if spanBytes == nil {
		// The trace does not exist in es. if it occurs frequently, the Bloom-Filter parameter may be set improperly.
		p.logger.Debug("The data with traceId: %s is empty from ES.", event.TraceId)
		return spans
	}
	originSpans, err := p.recoverSpans(spanBytes.([]byte))
	if err != nil {
		p.logger.Errorf("The data structure in ES is inconsistent, this data will be ignored. traceId: %s. error: %s ", event.TraceId, err)
		return spans
	}

	spans = append(spans, originSpans...)
	return spans
}

func (p *Processor) recoverSpans(originSpans []byte) ([]*StandardSpan, error) {
	var res []*StandardSpan
	v, _ := fastjson.ParseBytes(originSpans)
	spans, err := v.Array()
	if err != nil {
		return nil, err
	}

	for _, s := range spans {
		res = append(res, ToStandardSpan(s))
	}

	return res, nil
}

func (p *Processor) Process(receiver chan<- storage.SaveRequest, event Event) {

	graph := event.Graph
	graph.RefreshEdges()
	nodeDegrees := graph.NodeDepths()

	services := mapset.NewSet[string]()
	var startTimes []int
	var endTimes []int
	var duration []int
	var errorCount int
	categoryStatistics := initCategoryStatistics()
	kindCategoryStatistics := initKindCategoryStatistics()
	collections := make(map[string][]string, len(core.StandardFields))

	for _, span := range event.Spans {
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
	}

	sort.Ints(startTimes)
	sort.Ints(endTimes)
	sort.Ints(duration)

	// Root Span
	var rootSpan Node
	if len(nodeDegrees) != 0 {
		sort.Slice(nodeDegrees, sortNode(nodeDegrees))
		rootSpan = *nodeDegrees[0].Node
	} else {
		rootSpan = Node{StandardSpan: &StandardSpan{}}
	}

	// Root Service Span
	var calledKindSpans []NodeDegree
	linq.From(nodeDegrees).Where(func(i interface{}) bool {
		item := i.(NodeDegree)
		return core.SpanKind(item.Node.Kind).IsCalledKind()
	}).ToSlice(&calledKindSpans)
	sort.Slice(calledKindSpans, sortNode(calledKindSpans))
	var rootServiceSpan Node
	var rSc string
	var rootServiceName string
	if len(calledKindSpans) != 0 {
		rootServiceSpan = *calledKindSpans[0].Node
		rootServiceCategory, _ := inferCategory(rootServiceSpan.Collections)
		rSc = string(rootServiceCategory)
		rootServiceName = rootServiceSpan.GetFieldValue(core.ServiceNameField)
	} else {
		rootServiceSpan = Node{StandardSpan: &StandardSpan{}}
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

	baseInfo := core.GetMetadataCenter().GetBaseInfo(p.dataId)
	res := ProcessResult{
		BizId:               baseInfo.BkBizId,
		BizName:             baseInfo.BkBizName,
		AppId:               baseInfo.AppId,
		AppName:             baseInfo.AppName,
		TraceId:             event.TraceId,
		HierarchyCount:      graph.LongestPath(),
		ServiceCount:        len(services.ToSlice()),
		SpanCount:           len(event.Spans),
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
		spanBytes, _ := jsoniter.Marshal(event.Spans)
		receiver <- storage.SaveRequest{
			Target: storage.Cache,
			Action: storage.SaveTraceCache,
			Data: storage.CacheStorageData{
				Key:   storage.CacheTraceInfoKey.Format(p.dataIdBaseInfo.BkBizId, p.dataIdBaseInfo.AppName, event.TraceId),
				Value: spanBytes,
				Ttl:   storage.CacheTraceInfoKey.Ttl,
			},
		}
	}

	receiver <- storage.SaveRequest{
		Target: storage.BloomFilter,
		Data: storage.BloomStorageData{
			Key: p.getBloomKey(event.TraceId),
		},
	}

	resultBytes, _ := jsoniter.Marshal(result)
	receiver <- storage.SaveRequest{
		Target: storage.SaveEs,
		Action: storage.SavePrecalculateResult,
		Data: storage.EsStorageData{
			DocumentId: result.TraceId,
			Value:      resultBytes,
		},
	}
}

func sortNode(nodeDegrees []NodeDegree) func(a, b int) bool {
	return func(a, b int) bool {
		aItem := nodeDegrees[a]
		bItem := nodeDegrees[b]

		if aItem.Degree != aItem.Degree {
			return aItem.Degree > bItem.Degree
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
	for category, predicate := range core.CategoryPredicateFieldMapping {
		match := true

		if len(predicate.OptionFields) != 0 {
			match = linq.From(predicate.OptionFields).Where(func(i interface{}) bool {
				v := i.(core.CommonField)
				_, exist := collections[v.DisplayKey()]
				return exist
			}).Any()
			if !match {
				continue
			}
		}

		match = linq.From(predicate.AnyFields).Where(func(i interface{}) bool {
			v := i.(core.CommonField)
			_, exist := collections[v.DisplayKey()]
			return exist
		}).Any()
		if match {
			matchCategory = category
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
	enabledInfoCache   bool
	traceMetaCutLength int
}

type ProcessorOption func(*ProcessorOptions)

// EnabledTraceInfoCache Whether to enable Storing the latest trace data into cache.
// If this is enabled, the query frequency of elasticsearch is reduced.
func EnabledTraceInfoCache(b bool) ProcessorOption {
	return func(options *ProcessorOptions) {
		options.enabledInfoCache = b
	}
}

// TraceMetaCutLength When the processor determines whether this trace has survived in the window,
// the Bloom-filter mechanism is used.
// In order to keep the key length as short as possible,
// the TraceId needs to be cut short.
// The accuracy of the determination varies with the length after cutting.
// -------- PROBABILITY FORMULA --------
// Assuming that R traceIds can be generated per minute,
// we only take the first N of the traceIds.
// Formula:
// Time (minutes) T = (log(0.5)/log(1-1/36 ^N))/R,
// resulting in a 50% probability of repetition every T minutes.
func TraceMetaCutLength(b int) ProcessorOption {
	return func(options *ProcessorOptions) {
		options.traceMetaCutLength = b
	}
}

func NewProcessor(dataId string, storageProxy *storage.Proxy, options ...ProcessorOption) Processor {
	opts := ProcessorOptions{}
	for _, setter := range options {
		setter(&opts)
	}

	return Processor{
		dataId:         dataId,
		config:         opts,
		dataIdBaseInfo: core.GetMetadataCenter().GetBaseInfo(dataId),
		proxy:          storageProxy,
		logger: monitorLogger.With(
			zap.String("location", "processor"),
			zap.String("dataId", dataId),
		),
	}
}
