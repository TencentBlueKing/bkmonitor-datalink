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
	"sort"
	"strconv"
	"strings"
	"time"

	elastic "github.com/olivere/elastic/v7"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/prompb"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

const (
	KeyValue   = "_key"
	FieldValue = "_value"
	FieldTime  = "_time"

	DefaultTimeFieldName = "dtEventTimeStamp"
	DefaultTimeFieldType = TimeFieldTypeTime
	DefaultTimeFieldUnit = function.Millisecond

	Type       = "type"
	Properties = "properties"

	Min         = "min"
	Max         = "max"
	Sum         = "sum"
	Count       = "count"
	Avg         = "avg"
	Cardinality = "cardinality"

	DateHistogram = "date_histogram"
	Percentiles   = "percentiles"

	Nested = "nested"
	Terms  = "terms"

	ESStep = "."
)

const (
	KeyDocID     = "__doc_id"
	KeyHighLight = "__highlight"
	KeySort      = "sort"

	KeyIndex   = "__index"
	KeyTableID = "__result_table"
	KeyAddress = "__address"

	KeyDataLabel = "__data_label"
)

const (
	KeyWord = "keyword"
	Text    = "text"
	Integer = "integer"
	Long    = "long"
	Date    = "date"
)

const (
	TimeFieldTypeTime = "date"
	TimeFieldTypeInt  = "long"
)

const (
	EpochSecond       = "epoch_second"
	EpochMillis       = "epoch_millis"
	EpochMicroseconds = "epoch_microseconds"
	EpochNanoseconds  = "epoch_nanoseconds"
)

const (
	Must      = "must"
	MustNot   = "must_not"
	Should    = "should"
	ShouldNot = "should_not"
)

type TimeSeriesResult struct {
	TimeSeriesMap map[string]*prompb.TimeSeries
	Error         error
}

func mapData(prefix string, data map[string]any, res map[string]any) {
	for k, v := range data {
		if prefix != "" {
			k = prefix + ESStep + k
		}
		switch v.(type) {
		case map[string]any:
			mapData(k, v.(map[string]any), res)
		default:
			res[k] = v
		}
	}
}

func mapProperties(prefix string, data map[string]any, res map[string]string) {
	if prefix != "" {
		if t, ok := data[Type]; ok {
			switch ts := t.(type) {
			case string:
				res[prefix] = ts
			}
		}
	}

	if properties, ok := data[Properties]; ok {
		for k, v := range properties.(map[string]any) {
			if prefix != "" {
				k = prefix + ESStep + k
			}
			switch v.(type) {
			case map[string]any:
				mapProperties(k, v.(map[string]any), res)
			}
		}
	}
}

type ValueAgg struct {
	Name     string
	FuncType string
	Field    string

	Args   []any
	KwArgs map[string]any
}

type TimeAgg struct {
	Name     string
	Window   time.Duration
	Timezone string
}

type TermAgg struct {
	Name             string
	Orders           metadata.Orders
	AttachedValueAgg *ValueAgg
}

type NestedAgg struct {
	Name string
}

type ReverseNestedAgg struct {
	Name string
}

type aggInfoList []any

type FormatFactory struct {
	ctx context.Context

	valueField string
	timeField  metadata.TimeField

	decode func(k string) string
	encode func(k string) string

	mapping map[string]string
	data    map[string]any

	aggInfoList aggInfoList
	orders      metadata.Orders

	size     int
	timezone string

	start      time.Time
	end        time.Time
	timeFormat string

	isReference bool
}

func NewFormatFactory(ctx context.Context) *FormatFactory {
	f := &FormatFactory{
		ctx:         ctx,
		mapping:     make(map[string]string),
		aggInfoList: make(aggInfoList, 0),

		encode: func(k string) string {
			return k
		},
		decode: func(k string) string {
			return k
		},
	}

	return f
}

func (f *FormatFactory) WithIsReference(isReference bool) *FormatFactory {
	f.isReference = isReference
	return f
}

func (f *FormatFactory) toFixInterval(window time.Duration) (string, error) {
	switch f.timeField.Unit {
	case function.Second:
		window /= 1e3
	case function.Microsecond:
		window *= 1e3
	case function.Nanosecond:
		window *= 1e6
	}

	if window.Milliseconds() < 1 {
		return "", fmt.Errorf("date histogram aggregation interval must be greater than 0ms")
	}
	return shortDur(window), nil
}

func (f *FormatFactory) toMillisecond(i int64) int64 {
	switch f.timeField.Unit {
	case function.Second:
		return i * 1e3
	case function.Microsecond:
		return i / 1e3
	case function.Nanosecond:
		return i / 1e6
	default:
		// 默认用毫秒
		return i
	}
}

func (f *FormatFactory) timeFormatToEpoch(unit string) string {
	switch unit {
	case function.Millisecond:
		return EpochMillis
	case function.Microsecond:
		return EpochMicroseconds
	case function.Nanosecond:
		return EpochNanoseconds
	default:
		// 默认用秒
		return EpochSecond
	}
}

func (f *FormatFactory) queryToUnix(t time.Time, unit string) int64 {
	switch unit {
	case function.Millisecond:
		return t.UnixMilli()
	case function.Microsecond:
		return t.UnixMicro()
	case function.Nanosecond:
		return t.UnixNano()
	default:
		// 默认用秒
		return t.Unix()
	}
}

func (f *FormatFactory) WithQuery(valueKey string, timeField metadata.TimeField, start, end time.Time, timeFormat string, size int) *FormatFactory {
	if timeField.Name == "" {
		timeField.Name = DefaultTimeFieldName
	}
	if timeField.Type == "" {
		timeField.Type = DefaultTimeFieldType
	}
	if timeField.Unit == "" {
		timeField.Unit = DefaultTimeFieldUnit
	}
	if timeFormat == "" {
		timeFormat = function.Second
	}

	f.start = start
	f.end = end
	f.timeFormat = timeFormat
	f.valueField = valueKey
	f.timeField = timeField
	f.size = size

	return f
}

func (f *FormatFactory) WithTransform(encode func(string) string, decode func(string) string) *FormatFactory {
	if encode != nil {
		f.encode = encode
	}
	if decode != nil {
		f.decode = decode
		// 如果有 decode valueField 需要重新载入
		f.valueField = decode(f.valueField)
	}
	return f
}

func (f *FormatFactory) WithOrders(orders metadata.Orders) *FormatFactory {
	f.orders = make(metadata.Orders, 0, len(orders))
	for _, order := range orders {
		if f.decode != nil {
			order.Name = f.encode(order.Name)
		}
		f.orders = append(f.orders, order)
	}
	return f
}

// WithMappings 合并 mapping，后面的合并前面的
func (f *FormatFactory) WithMappings(mappings ...map[string]any) *FormatFactory {
	for _, mapping := range mappings {
		mapProperties("", mapping, f.mapping)
	}
	return f
}

func (f *FormatFactory) RangeQuery() (elastic.Query, error) {
	var (
		err error
	)

	fieldName := f.timeField.Name
	fieldType := f.timeField.Type

	var query elastic.Query
	switch fieldType {
	case TimeFieldTypeInt:
		// int 类型，直接按照 tableID 配置的单位转换
		query = elastic.NewRangeQuery(fieldName).
			Gte(f.queryToUnix(f.start, f.timeField.Unit)).
			Lte(f.queryToUnix(f.end, f.timeField.Unit))
	case TimeFieldTypeTime:
		// date 类型，使用 查询的单位转换
		query = elastic.NewRangeQuery(fieldName).
			Gte(f.queryToUnix(f.start, f.timeFormat)).
			Lte(f.queryToUnix(f.end, f.timeFormat)).
			Format(f.timeFormatToEpoch(f.timeFormat))
	default:
		err = fmt.Errorf("time field type is error %s", fieldType)
	}
	return query, err
}

func (f *FormatFactory) NestedField(field string) string {
	lbs := strings.Split(field, ESStep)
	for i := len(lbs) - 1; i >= 0; i-- {
		checkKey := strings.Join(lbs[0:i], ESStep)
		if v, ok := f.mapping[checkKey]; ok {
			if v == Nested {
				return checkKey
			}
		}
	}
	return ""
}

func buildElasticValueAgg(info ValueAgg) (elastic.Aggregation, error) {
	switch info.FuncType {
	case Min:
		return elastic.NewMinAggregation().Field(info.Field), nil
	case Max:
		return elastic.NewMaxAggregation().Field(info.Field), nil
	case Avg:
		return elastic.NewAvgAggregation().Field(info.Field), nil
	case Sum:
		return elastic.NewSumAggregation().Field(info.Field), nil
	case Count:
		return elastic.NewValueCountAggregation().Field(info.Field), nil
	case Cardinality:
		return elastic.NewCardinalityAggregation().Field(info.Field), nil
	case Percentiles:
		percents := make([]float64, 0)
		for _, arg := range info.Args {
			var percent float64
			switch v := arg.(type) {
			case float64:
				percent = v
			case int:
				percent = float64(v)
			case int32:
				percent = float64(v)
			case int64:
				percent = float64(v)
			default:
				return nil, fmt.Errorf("percent type is error: %T, %+v", v, v)
			}
			percents = append(percents, percent)
		}
		return elastic.NewPercentilesAggregation().Field(info.Field).Percentiles(percents...), nil
	default:
		return nil, fmt.Errorf("valueagg aggregation is not support this type %s, info: %+v", info.FuncType, info)
	}
}

// AggDataFormat 解析 es 的聚合计算
func (f *FormatFactory) AggDataFormat(data elastic.Aggregations, metricLabel *prompb.Label) (*prompb.QueryResult, error) {
	if data == nil {
		return &prompb.QueryResult{
			Timeseries: []*prompb.TimeSeries{},
		}, nil
	}

	defer func() {
		if r := recover(); r != nil {
			log.Errorf(f.ctx, fmt.Sprintf("agg data format %v", r))
		}
	}()

	af := &aggFormat{
		aggInfoList:    f.aggInfoList,
		items:          make(items, 0),
		promDataFormat: f.encode,
		timeFormat:     f.toMillisecond,
	}

	af.get()
	defer af.put()

	err := af.ts(len(f.aggInfoList), data)
	if err != nil {
		return nil, err
	}

	timeSeriesMap := make(map[string]*prompb.TimeSeries)
	keySort := make([]string, 0)

	for _, im := range af.items {
		var (
			tsLabels []prompb.Label
		)
		if len(im.labels) > 0 {
			for _, dim := range af.dims {
				tsLabels = append(tsLabels, prompb.Label{
					Name:  dim,
					Value: im.labels[dim],
				})
			}
		}

		if metricLabel != nil {
			tsLabels = append(tsLabels, *metricLabel)
		}

		var seriesNameBuilder strings.Builder
		for _, l := range tsLabels {
			seriesNameBuilder.WriteString(l.String())
		}

		seriesKey := seriesNameBuilder.String()
		if _, ok := timeSeriesMap[seriesKey]; !ok {
			keySort = append(keySort, seriesKey)
			timeSeriesMap[seriesKey] = &prompb.TimeSeries{
				Samples: make([]prompb.Sample, 0),
			}
		}

		if im.timestamp == 0 {
			im.timestamp = f.start.UnixMilli()
		}

		timeSeriesMap[seriesKey].Labels = tsLabels
		timeSeriesMap[seriesKey].Samples = append(timeSeriesMap[seriesKey].Samples, prompb.Sample{
			Value:     im.value,
			Timestamp: im.timestamp,
		})
	}

	tss := make([]*prompb.TimeSeries, 0, len(timeSeriesMap))
	for _, key := range keySort {
		if ts, ok := timeSeriesMap[key]; ok {
			tss = append(tss, ts)
		}
	}

	return &prompb.QueryResult{Timeseries: tss}, nil
}

func (f *FormatFactory) SetData(data map[string]any) {
	f.data = map[string]any{}
	mapData("", data, f.data)
}

func (f *FormatFactory) Agg() (name string, agg elastic.Aggregation, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf(f.ctx, fmt.Sprintf("get mapping error: %s", r))
		}
	}()

	var currentAgg elastic.Aggregation
	var innerAggName string

	for i := len(f.aggInfoList) - 1; i >= 0; i-- {
		aggInfo := f.aggInfoList[i]
		var nextAgg elastic.Aggregation
		var nextAggName string

		switch info := aggInfo.(type) {
		case ValueAgg:
			nextAggName = info.Name
			var buildErr error
			nextAgg, buildErr = buildElasticValueAgg(info)
			if buildErr != nil {
				err = buildErr
				return
			}
		case TimeAgg:
			nextAggName = info.Name
			var interval string
			if f.timeField.Type == TimeFieldTypeInt {
				interval, err = f.toFixInterval(info.Window)
				if err != nil {
					return
				}
			} else {
				interval = shortDur(info.Window)
			}
			ta := elastic.NewDateHistogramAggregation().
				Field(f.timeField.Name).Interval(interval).MinDocCount(0).
				ExtendedBounds(f.timeFieldUnix(f.start), f.timeFieldUnix(f.end))
			if f.timeField.Type == TimeFieldTypeTime {
				ta = ta.TimeZone(info.Timezone)
			}
			if currentAgg != nil {
				ta = ta.SubAggregation(innerAggName, currentAgg)
			}
			nextAgg = ta
		case NestedAgg:
			nextAggName = info.Name
			na := elastic.NewNestedAggregation().Path(info.Name)
			if currentAgg != nil {
				na = na.SubAggregation(innerAggName, currentAgg)
			}
			nextAgg = na
		case ReverseNestedAgg:
			nextAggName = info.Name
			if info.Name == "" {
				nextAggName = "reverse_nested"
			}
			rna := elastic.NewReverseNestedAggregation()
			if currentAgg != nil {
				rna = rna.SubAggregation(innerAggName, currentAgg)
			}
			nextAgg = rna
		case TermAgg:
			nextAggName = info.Name
			ta := elastic.NewTermsAggregation().Field(info.Name)
			fieldType, ok := f.mapping[info.Name]
			if !ok || fieldType == Text || fieldType == KeyWord {
				ta = ta.Missing(" ")
			}
			if f.size > 0 {
				ta = ta.Size(f.size)
			} else {
				ta = ta.Size(0)
			}
			for _, order := range info.Orders {
				ta = ta.Order(order.Name, order.Ast)
			}

			if info.AttachedValueAgg != nil {
				metricAgg, buildErr := buildElasticValueAgg(*info.AttachedValueAgg)
				if buildErr != nil {
					err = buildErr
					return
				}
				if metricAgg != nil {
					ta = ta.SubAggregation(info.AttachedValueAgg.Name, metricAgg)
				}
			}

			if currentAgg != nil {
				ta = ta.SubAggregation(innerAggName, currentAgg)
			}
			nextAgg = ta
		default:
			err = fmt.Errorf("aggInfoList aggregation is not support this type %T, info: %+v", info, info)
			return
		}
		currentAgg = nextAgg
		innerAggName = nextAggName
	}
	agg = currentAgg
	name = innerAggName
	return
}

func (f *FormatFactory) HighLight(queryString string, maxAnalyzedOffset int) *elastic.Highlight {
	requireFieldMatch := false
	if strings.Contains(queryString, ":") {
		requireFieldMatch = true
	}
	hl := elastic.NewHighlight().
		Field("*").NumOfFragments(0).
		RequireFieldMatch(requireFieldMatch).
		PreTags("<mark>").PostTags("</mark>")

	if maxAnalyzedOffset > 0 {
		hl = hl.MaxAnalyzedOffset(maxAnalyzedOffset)
	}

	return hl
}

type FieldPathInfo struct {
	OriginalField string
	Path          string
	FieldName     string
	IsNested      bool
}

// getFieldPathInfo 函数的核心目的是分析一个给定的字段名（例如 "events.data.value" 或 "username"），并结合 FormatFactory 中存储的字段类型映射（f.mapping），
// 来确定该字段的路径信息，特别是关于它是否属于一个 Elasticsearch 的 "nested" 类型以及其具体的嵌套路径。
func (f *FormatFactory) getFieldPathInfo(field string) FieldPathInfo {
	if field == "" {
		// Return non-nested if field is empty, though this should ideally be handled by callers.
		return FieldPathInfo{OriginalField: field, FieldName: field, Path: "", IsNested: false}
	}

	// 1. 检查字段本身是否在mapping中定义为Nested
	// 这是最高优先级，因为如果字段本身就是嵌套类型，那它就是最精确的路径。
	if typ, ok := f.mapping[field]; ok && typ == Nested {
		return FieldPathInfo{
			OriginalField: field,
			Path:          field,
			FieldName:     field,
			IsNested:      true,
		}
	}

	parts := strings.Split(field, ESStep)

	// 2. 从后往前遍历其前缀，寻找在mapping定义为Nested的字段。
	// 目的是找到构成该字段路径的最长的一个被定义为Nested的前缀。
	for i := len(parts) - 1; i >= 1; i-- {
		currentPathPrefix := strings.Join(parts[0:i], ESStep)
		if typ, ok := f.mapping[currentPathPrefix]; ok && typ == Nested {
			return FieldPathInfo{
				OriginalField: field,
				Path:          currentPathPrefix,
				FieldName:     field,
				IsNested:      true,
			}
		}
	}

	// 3. 默认：在mapping中没有找到该字段本身或其任何前缀被定义为Nested
	return FieldPathInfo{
		OriginalField: field,
		Path:          "",
		FieldName:     field,
		IsNested:      false,
	}
}

// currentLogicalPath 是当前处理的嵌套路径，用于判断是否需要添加ReverseNestedAgg和NestedAgg。
// targetPath 是当前聚合的嵌套路径。
// targetIsNested 表示targetPath是否是嵌套路径。
func (f *FormatFactory) handlePathTransition(currentLogicalPath, targetPath string, targetIsNested bool) string {
	if targetIsNested {
		if targetPath != currentLogicalPath {
			// 将路径分解为组件
			currentParts := strings.Split(currentLogicalPath, ESStep)
			if currentLogicalPath == "" {
				currentParts = []string{}
			}
			targetParts := strings.Split(targetPath, ESStep)

			// 往上走，找到currentLogicalPath和targetPath的公共祖先
			commonPrefixLen := 0
			for commonPrefixLen < len(currentParts) && commonPrefixLen < len(targetParts) && currentParts[commonPrefixLen] == targetParts[commonPrefixLen] {
				commonPrefixLen++
			}

			// 往上走，添加ReverseNestedAggs
			for i := len(currentParts) - 1; i >= commonPrefixLen; i-- {
				// 在每层添加ReverseNestedAgg，用于往上走。直至添加到公共祖先。
				f.aggInfoList = append(f.aggInfoList, ReverseNestedAgg{Name: "reverse_nested"}) // Standardized name
			}

			// 往下走，添加NestedAggs
			for i := commonPrefixLen; i < len(targetParts); i++ {
				pathToNest := strings.Join(targetParts[0:i+1], ESStep)
				// 在每层添加NestedAgg，用于往下走。直至添加到targetPath。
				f.aggInfoList = append(f.aggInfoList, NestedAgg{Name: pathToNest})
			}
			return targetPath
		}
		return currentLogicalPath // 如果targetPath和currentLogicalPath相同，则不需要添加ReverseNestedAgg和NestedAgg
	} else { // 如果当前处理的是非嵌套路径
		if currentLogicalPath != "" {
			f.aggInfoList = append(f.aggInfoList, ReverseNestedAgg{Name: "reverse_nested"}) // Standardized name
		}
		return "" // 如果targetPath和currentLogicalPath相同，则不需要添加ReverseNestedAgg和NestedAgg
	}
}

// EsAgg generates the Elasticsearch aggregation structure.
func (f *FormatFactory) EsAgg(aggregates metadata.Aggregates) (string, elastic.Aggregation, error) {
	if len(aggregates) == 0 {
		return "", nil, errors.New("aggregate_method_list is empty")
	}
	aggDef := aggregates[0]

	f.aggInfoList = make(aggInfoList, 0)

	// 追踪当前处理的嵌套路径，用于判断是否需要添加ReverseNestedAgg和NestedAgg。
	currentLogicalPath := ""

	metricFieldForOp := aggDef.Field
	if metricFieldForOp == "" && aggDef.Name == Count {
		metricFieldForOp = f.valueField
	}

	metricFieldInfo := f.getFieldPathInfo(metricFieldForOp)

	// 处理的顺序：维度先处理，然后是时间聚合，最后是指标聚合。
	// 如果维度存在，则维度在最外层。
	var metricAggAttached bool // 标记是否已经将主指标附加到TermAgg

	// 1. 维度聚合
	for i, dim := range aggDef.Dimensions {
		if dim == labels.MetricName {
			continue
		}
		decodedDim := dim
		if f.decode != nil {
			decodedDim = f.decode(dim)
		}
		dimInfo := f.getFieldPathInfo(decodedDim)

		currentLogicalPath = f.handlePathTransition(currentLogicalPath, dimInfo.Path, dimInfo.IsNested)

		termAgg := TermAgg{Name: dimInfo.FieldName}
		// 应用orders：
		// 如果这是第一个维度（i=0），它是外层的维度组。
		// 按这个维度的key（KeyValue）或指标（FieldValue）排序。
		if i == 0 {
			for _, order := range f.orders {
				if dimInfo.OriginalField == order.Name {
					termAgg.Orders = append(termAgg.Orders, metadata.Order{Name: KeyValue, Ast: order.Ast})
				} else if order.Name == FieldValue {
					termAgg.Orders = append(termAgg.Orders, order)
				}
			}
		} else {
			// 对于内层的维度，只有当它是这个维度的key时才应用order。
			for _, order := range f.orders {
				if dimInfo.OriginalField == order.Name {
					termAgg.Orders = append(termAgg.Orders, metadata.Order{Name: KeyValue, Ast: order.Ast})
				}
			}
		}

		// 检查这个维度是否也是指标字段，并且指标字段本身是嵌套的。
		// 这是为了在嵌套上下文中，指标字段应该是一个兄弟节点，用于进一步的reverse_nested操作。
		if !metricAggAttached && aggDef.Name != DateHistogram &&
			dimInfo.OriginalField == metricFieldInfo.OriginalField && // 当前维度是指标字段
			metricFieldInfo.IsNested && // 并且指标字段本身是嵌套的
			aggDef.Window == 0 { // 并且没有时间窗口

			termAgg.AttachedValueAgg = &ValueAgg{
				Name:     FieldValue, // 标准名称，用于指标值聚合
				FuncType: aggDef.Name,
				Field:    metricFieldInfo.OriginalField, // 使用指标字段的原始字段
				Args:     aggDef.Args,
			}
			metricAggAttached = true
			log.Debugf(f.ctx, "EsAgg: Attached ValueAgg for NESTED field '%s' (NO window) to TermAgg for dimension '%s'", metricFieldInfo.OriginalField, dimInfo.OriginalField)
		}

		f.aggInfoList = append(f.aggInfoList, termAgg)
	}

	// 2. 时间聚合
	if aggDef.Window > 0 && !aggDef.Without {
		f.aggInfoList = append(f.aggInfoList, TimeAgg{
			Name:     f.timeField.Name, // This is the key for the time agg block
			Window:   aggDef.Window,
			Timezone: aggDef.TimeZone,
		})
	}

	// 3. 指标聚合（仅在未附加时处理）
	// 这处理了指标字段不是维度的情况，或者非TermAgg的情况。
	if !metricAggAttached && aggDef.Name != DateHistogram { // DateHistogram is handled by TimeAgg
		fieldForMetricValueAgg := metricFieldForOp
		if aggDef.Field == "" && aggDef.Name == Count {
			// Uses f.valueField (via metricFieldForOp) or explicitly set aggDef.Field.
		} else if aggDef.Field != "" {
			fieldForMetricValueAgg = aggDef.Field // Prioritize explicitly set aggDef.Field
		}

		// The currentLogicalPath is the state after processing all dimensions and time aggs.
		// The metric (ValueAgg) needs to be placed in the context of its own field (metricFieldInfo).
		// Call handlePathTransition to add any necessary NestedAgg or ReverseNestedAgg
		// to transition from currentLogicalPath to metricFieldInfo.Path.
		currentLogicalPath = f.handlePathTransition(currentLogicalPath, metricFieldInfo.Path, metricFieldInfo.IsNested)

		f.aggInfoList = append(f.aggInfoList, ValueAgg{
			Name:     FieldValue, // Standard name for the metric value aggregation
			FuncType: aggDef.Name,
			Field:    fieldForMetricValueAgg,
			Args:     aggDef.Args,
		})
		log.Debugf(f.ctx, "EsAgg: Added standalone ValueAgg for field '%s' with path '%s'", fieldForMetricValueAgg, currentLogicalPath)
	}

	log.Debugf(f.ctx, "EsAgg: Constructed aggInfoList: %+v", f.aggInfoList)

	_, mainAgg, err := f.Agg()
	if err != nil {
		return "", nil, err
	}

	// Determine outermostAggName:
	// Priority: First Dimension > Time Window > Metric Field Path/Name
	outermostAggName := ""
	if len(aggDef.Dimensions) > 0 {
		firstDimDecoded := aggDef.Dimensions[0]
		if f.decode != nil {
			firstDimDecoded = f.decode(firstDimDecoded)
		}
		firstDimInfo := f.getFieldPathInfo(firstDimDecoded)
		if firstDimInfo.IsNested { // If first dim is like "events.name", path "events" is outer.
			outermostAggName = firstDimInfo.Path
		} else { // If first dim is like "name", field "name" is outer.
			outermostAggName = firstDimInfo.FieldName
		}
	} else if aggDef.Window > 0 && !aggDef.Without {
		outermostAggName = f.timeField.Name // Key used for the TimeAgg block
	} else if aggDef.Name != DateHistogram { // Metric is the only component
		if metricFieldInfo.IsNested {
			outermostAggName = metricFieldInfo.Path
		} else {
			if metricFieldForOp != "" {
				outermostAggName = metricFieldForOp
			} else {
				outermostAggName = FieldValue // Fallback for global metric
			}
		}
	} else {
		return "", nil, errors.New("unable to determine outermost aggregation name")
	}

	log.Debugf(f.ctx, "EsAgg: Determined outermost name: %s", outermostAggName)
	return outermostAggName, mainAgg, err
}

func (f *FormatFactory) Orders() metadata.Orders {
	orders := make(metadata.Orders, 0, len(f.orders))
	for _, order := range f.orders {
		if order.Name == FieldValue {
			order.Name = f.valueField
		} else if order.Name == FieldTime {
			order.Name = f.timeField.Name
		}

		if _, ok := f.mapping[order.Name]; ok {
			orders = append(orders, order)
		}
	}
	return orders
}

func (f *FormatFactory) timeFieldUnix(t time.Time) (u int64) {
	switch f.timeField.Unit {
	case function.Millisecond:
		u = t.UnixMilli()
	case function.Microsecond:
		u = t.UnixMicro()
	case function.Nanosecond:
		u = t.UnixNano()
	default:
		u = t.Unix()
	}

	return
}

func (f *FormatFactory) getQuery(key string, qs ...elastic.Query) (q elastic.Query) {
	if len(qs) == 0 {
		return q
	}

	switch key {
	case Must:
		if len(qs) == 1 {
			q = qs[0]
		} else {
			q = elastic.NewBoolQuery().Must(qs...)
		}
	case Should:
		if len(qs) == 1 {
			q = qs[0]
		} else {
			q = elastic.NewBoolQuery().Should(qs...)
		}
	case MustNot:
		q = elastic.NewBoolQuery().MustNot(qs...)
	}
	return q
}

// Query 把 ts 的 conditions 转换成 es 查询
func (f *FormatFactory) Query(allConditions metadata.AllConditions) (elastic.Query, error) {
	orQuery := make([]elastic.Query, 0, len(allConditions))

	for _, conditions := range allConditions {
		nestedPathQueries := make(map[string][]elastic.Query) // Stores queries that should be ANDed under a specific nested path
		rootLevelQueries := make([]elastic.Query, 0)          // Stores non-nested queries and fully formed must_not(nested) queries

		for _, con := range conditions {
			key := con.DimensionName
			if f.decode != nil {
				key = f.decode(key)
			}

			nestedPath := f.NestedField(con.DimensionName)

			var q elastic.Query
			isNegativeOperator := false
			positiveOperator := con.Operator // Used if we handle negativity at a higher level for nested queries

			switch con.Operator {
			case structured.ConditionNotEqual,
				structured.ConditionNotContains,
				structured.ConditionNotRegEqual,
				structured.ConditionNotExisted:
				isNegativeOperator = true
				// Determine the positive equivalent operator for nested must_not cases
				switch con.Operator {
				case structured.ConditionNotEqual: // match_phrase
					positiveOperator = structured.ConditionEqual
				case structured.ConditionNotContains: // wildcard or match_phrase
					positiveOperator = structured.ConditionContains
				case structured.ConditionNotRegEqual: // regexp
					positiveOperator = structured.ConditionRegEqual
				case structured.ConditionNotExisted: // exists
					positiveOperator = structured.ConditionExisted
				}
			}

			if nestedPath != "" && isNegativeOperator && con.Operator != structured.ConditionNotExisted {
				// Handle must_not(nested(positive_condition)) scenario
				// Construct the positive query part first
				positiveQueries := make([]elastic.Query, 0)
				fieldType, _ := f.mapping[key]
				for _, value := range con.Value {
					var positiveQueryPart elastic.Query
					switch positiveOperator {
					case structured.ConditionEqual: // from NotEqual
						if con.IsPrefix {
							positiveQueryPart = elastic.NewMatchPhrasePrefixQuery(key, value)
						} else {
							positiveQueryPart = elastic.NewMatchPhraseQuery(key, value)
						}
					case structured.ConditionContains: // from NotContains
						if fieldType == KeyWord {
							value = fmt.Sprintf("*%s*", value)
						}
						if !con.IsWildcard && fieldType == Text {
							if con.IsPrefix {
								positiveQueryPart = elastic.NewMatchPhrasePrefixQuery(key, value)
							} else {
								positiveQueryPart = elastic.NewMatchPhraseQuery(key, value)
							}
						} else {
							positiveQueryPart = elastic.NewWildcardQuery(key, value)
						}
					case structured.ConditionRegEqual: // from NotRegEqual
						positiveQueryPart = elastic.NewRegexpQuery(key, value)
					// Add other positive operators if needed for other negative ones
					default:
						return nil, fmt.Errorf("unhandled positive operator mapping for %s -> %s", con.Operator, positiveOperator)
					}
					if positiveQueryPart != nil {
						positiveQueries = append(positiveQueries, positiveQueryPart)
					}
				}
				if len(positiveQueries) > 0 {
					positiveBoolQuery := f.getQuery(Should, positiveQueries...) // If multiple values for NotEqual, they are ORed for the positive match inside nested
					nestedQ := elastic.NewNestedQuery(nestedPath, positiveBoolQuery)
					q = f.getQuery(MustNot, nestedQ)
					rootLevelQueries = append(rootLevelQueries, q)
				}
				continue // Handled this condition, move to next
			}

			// Original logic for non-special-nested-must_not or all positive queries
			switch con.Operator {
			case structured.ConditionExisted:
				q = elastic.NewExistsQuery(key)
			case structured.ConditionNotExisted:
				// For ConditionNotExisted, if it's nested, it should be must_not(nested(exists(...)))
				if nestedPath != "" {
					existsQuery := elastic.NewExistsQuery(key)
					nestedExistsQuery := elastic.NewNestedQuery(nestedPath, existsQuery)
					q = f.getQuery(MustNot, nestedExistsQuery)
				} else {
					q = f.getQuery(MustNot, elastic.NewExistsQuery(key))
				}
			default:
				fieldType, ok := f.mapping[key]
				isExistsQuery := true // Should this be used for empty value check?
				if ok && (fieldType == Text || fieldType == KeyWord) {
					isExistsQuery = false
				}

				queries := make([]elastic.Query, 0)
				for _, value := range con.Value {
					var queryPart elastic.Query
					if con.DimensionName != "" {
						if value == "" && isExistsQuery && (con.Operator == structured.ConditionEqual || con.Operator == structured.ConditionContains || con.Operator == structured.ConditionNotEqual || con.Operator == structured.ConditionNotContains) {
							existsQ := elastic.NewExistsQuery(key)
							switch con.Operator {
							case structured.ConditionEqual, structured.ConditionContains:
								queryPart = f.getQuery(MustNot, existsQ)
							case structured.ConditionNotEqual, structured.ConditionNotContains:
								queryPart = f.getQuery(Must, existsQ)
							default:
								// This case should ideally not be reached if handled by prior empty string checks.
							}
						} else {
							switch con.Operator {
							case structured.ConditionEqual, structured.ConditionNotEqual:
								if con.IsPrefix {
									queryPart = elastic.NewMatchPhrasePrefixQuery(key, value)
								} else {
									queryPart = elastic.NewMatchPhraseQuery(key, value)
								}
							case structured.ConditionContains, structured.ConditionNotContains:
								if fieldType == KeyWord {
									value = fmt.Sprintf("*%s*", value)
								}
								if !con.IsWildcard && fieldType == Text {
									if con.IsPrefix {
										queryPart = elastic.NewMatchPhrasePrefixQuery(key, value)
									} else {
										queryPart = elastic.NewMatchPhraseQuery(key, value)
									}
								} else {
									queryPart = elastic.NewWildcardQuery(key, value)
								}
							case structured.ConditionRegEqual, structured.ConditionNotRegEqual:
								queryPart = elastic.NewRegexpQuery(key, value)
							case structured.ConditionGt:
								queryPart = elastic.NewRangeQuery(key).Gt(value)
							case structured.ConditionGte:
								queryPart = elastic.NewRangeQuery(key).Gte(value)
							case structured.ConditionLt:
								queryPart = elastic.NewRangeQuery(key).Lt(value)
							case structured.ConditionLte:
								queryPart = elastic.NewRangeQuery(key).Lte(value)
							default:
								return nil, fmt.Errorf("operator is not supported: %+v", con)
							}
						}
					} else {
						queryPart = elastic.NewQueryStringQuery(value)
					}
					if queryPart != nil {
						queries = append(queries, queryPart)
					}
				}

				switch con.Operator {
				case structured.ConditionEqual, structured.ConditionContains, structured.ConditionRegEqual:
					q = f.getQuery(Should, queries...)
				case structured.ConditionNotEqual, structured.ConditionNotContains, structured.ConditionNotRegEqual:
					q = f.getQuery(MustNot, queries...)
				case structured.ConditionGt, structured.ConditionGte, structured.ConditionLt, structured.ConditionLte:
					q = f.getQuery(Must, queries...)
					// ConditionExisted and ConditionNotExisted are handled above or by special nested logic.
				default:
					// This path might be taken by ConditionExisted/NotExisted if not handled by special nested logic.
					// Ensure q is not nil if those were processed to avoid erroring out.
					if q == nil { // if q was set by ConditionExisted/NotExisted, this won't be true
						return nil, fmt.Errorf("operator is not supported or q remained nil: %+v", con)
					}
				}
			}

			if q == nil {
				continue // Skip if no query was generated for this condition
			}

			if nestedPath != "" && !(isNegativeOperator && con.Operator != structured.ConditionNotExisted) && con.Operator != structured.ConditionNotExisted {
				nestedPathQueries[nestedPath] = append(nestedPathQueries[nestedPath], q)
			} else {
				rootLevelQueries = append(rootLevelQueries, q)
			}
		}

		// Combine queries for each nested path
		// To ensure consistent order for tests, sort the paths
		paths := make([]string, 0, len(nestedPathQueries))
		for path := range nestedPathQueries {
			paths = append(paths, path)
		}
		sort.Strings(paths)

		for _, path := range paths { // Iterate over sorted paths
			nqs := nestedPathQueries[path]
			if len(nqs) > 0 {
				pathQuery := f.getQuery(Must, nqs...)
				nestedQuery := elastic.NewNestedQuery(path, pathQuery)
				rootLevelQueries = append(rootLevelQueries, nestedQuery)
			}
		}

		if len(rootLevelQueries) > 0 {
			andQuery := f.getQuery(Must, rootLevelQueries...)
			if andQuery != nil {
				orQuery = append(orQuery, andQuery)
			}
		}
	}

	finalQuery := f.getQuery(Should, orQuery...)
	return finalQuery, nil
}

func (f *FormatFactory) Sample() (prompb.Sample, error) {
	var (
		err error
		ok  bool

		timestamp interface{}
		value     interface{}

		sample = prompb.Sample{}
	)

	// 如果是非 prom 计算场景，则提前退出
	if f.isReference {
		return sample, nil
	}

	if value, ok = f.data[f.valueField]; ok {
		switch value.(type) {
		case float64:
			sample.Value = value.(float64)
		case int64:
			sample.Value = float64(value.(int64))
		case int:
			sample.Value = float64(value.(int))
		case string:
			sample.Value, err = strconv.ParseFloat(value.(string), 64)
			if err != nil {
				return sample, err
			}
		default:
			return sample, fmt.Errorf("value key %s type is error: %T, %v", f.valueField, value, value)
		}
	} else {
		sample.Value = 0
	}

	if timestamp, ok = f.data[f.timeField.Name]; ok {
		switch timestamp.(type) {
		case int64:
			sample.Timestamp = timestamp.(int64)
		case int:
			sample.Timestamp = int64(timestamp.(int))
		case float64:
			sample.Timestamp = int64(timestamp.(float64))
		case string:
			v, parseErr := strconv.ParseInt(timestamp.(string), 10, 64)
			if parseErr != nil {
				return sample, parseErr
			}
			sample.Timestamp = v
		default:
			return sample, fmt.Errorf("timestamp key type is error: %T, %v", timestamp, timestamp)
		}
		sample.Timestamp = f.toMillisecond(sample.Timestamp)
	} else {
		return sample, fmt.Errorf("timestamp is empty %s", f.timeField.Name)
	}

	return sample, nil
}

func (f *FormatFactory) Labels() (lbs *prompb.Labels, err error) {
	lbl := make([]string, 0)
	for k := range f.data {
		// 只有 promEngine 查询的场景需要跳过该字段
		if !f.isReference {
			if k == f.valueField {
				continue
			}
			if k == f.timeField.Name {
				continue
			}
		}

		if f.encode != nil {
			k = f.encode(k)
		}

		lbl = append(lbl, k)
	}

	sort.Strings(lbl)

	lbs = &prompb.Labels{
		Labels: make([]prompb.Label, 0, len(lbl)),
	}

	for _, k := range lbl {
		var value string
		d := f.data[k]

		if d == nil {
			continue
		}

		switch d.(type) {
		case string:
			value = fmt.Sprintf("%s", d)
		case float64, float32:
			value = fmt.Sprintf("%.f", d)
		case int64, int32, int:
			value = fmt.Sprintf("%d", d)
		case []interface{}:
			o, _ := json.Marshal(d)
			value = fmt.Sprintf("%s", o)
		default:
			err = fmt.Errorf("dimensions key type is error: %T, %v", d, d)
			return
		}

		lbs.Labels = append(lbs.Labels, prompb.Label{
			Name:  k,
			Value: value,
		})
	}

	return
}

func (f *FormatFactory) GetTimeField() metadata.TimeField {
	return f.timeField
}
