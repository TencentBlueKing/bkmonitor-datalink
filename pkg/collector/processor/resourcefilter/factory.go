// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package resourcefilter

import (
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/cache/k8scache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func init() {
	processor.Register(define.ProcessorResourceFilter, NewFactory)
}

// requestClientIPPlaceholders 列出 request.client.ip 场景下应视为占位 IP。
// 这类地址（例如 IPv4 通配地址、本机回环）不具备识别 Pod 的能力，写入后会污染下游 from_cache 的查询 key。
var requestClientIPPlaceholders = map[string]struct{}{
	"0.0.0.0":   {},
	"127.0.0.1": {},
}

func NewFactory(conf map[string]any, customized []processor.SubConfigProcessor) (processor.Processor, error) {
	return newFactory(conf, customized)
}

func newFactory(conf map[string]any, customized []processor.SubConfigProcessor) (*resourceFilter, error) {
	configs := confengine.NewTierConfig()

	c := &Config{}
	if err := mapstructure.Decode(conf, c); err != nil {
		return nil, err
	}
	c.Clean()
	configs.SetGlobal(*c)

	for _, custom := range customized {
		cfg := &Config{}
		if err := mapstructure.Decode(custom.Config.Config, cfg); err != nil {
			logger.Errorf("failed to decode config: %v", err)
			continue
		}
		cfg.Clean()
		configs.Set(custom.Token, custom.Type, custom.ID, *cfg)
	}

	return &resourceFilter{
		CommonProcessor: processor.NewCommonProcessor(conf, customized),
		configs:         configs,
	}, nil
}

type resourceFilter struct {
	processor.CommonProcessor
	configs *confengine.TierConfig // type: Config
}

func (p *resourceFilter) Name() string {
	return define.ProcessorResourceFilter
}

func (p *resourceFilter) IsDerived() bool {
	return false
}

func (p *resourceFilter) IsPreCheck() bool {
	return false
}

func (p *resourceFilter) Reload(config map[string]any, customized []processor.SubConfigProcessor) {
	f, err := newFactory(config, customized)
	if err != nil {
		logger.Errorf("failed to reload processor: %v", err)
		return
	}

	p.CommonProcessor = f.CommonProcessor
	p.configs = f.configs
}

func (p *resourceFilter) Process(record *define.Record) (*define.Record, error) {
	config := p.configs.GetByToken(record.Token.Original).(Config)
	if len(config.Replace) > 0 {
		p.replaceAction(record, config)
	}
	if len(config.Add) > 0 {
		p.addAction(record, config)
	}
	if len(config.Assemble) > 0 {
		p.assembleAction(record, config)
	}
	if len(config.Drop.Keys) > 0 {
		p.dropAction(record, config)
	}
	if len(config.FromRecord) > 0 {
		p.fromRecordAction(record, config)
	}
	if len(config.FromMetadata.Keys) > 0 {
		p.fromMetadataAction(record, config)
	}
	if len(config.FromToken.Keys) > 0 {
		p.fromTokenAction(record, config)
	}
	if len(config.DefaultValue) > 0 {
		p.defaultValueAction(record, config)
	}
	if len(config.FromCache.CacheName) > 0 {
		p.fromCacheAction(record, config)
	}
	if config.KeepOriginTraceId.Enabled {
		p.keepOriginTraceIdAction(record)
	}
	return nil, nil
}

// assembleAction 组合维度
func (p *resourceFilter) assembleAction(record *define.Record, config Config) {
	handle := func(rs pcommon.Resource, action AssembleAction) {
		var values []string
		for _, key := range action.Keys {
			v, ok := rs.Attributes().Get(key)
			if !ok {
				// 空值保留
				values = append(values, "")
				continue
			}
			values = append(values, v.AsString())
		}
		rs.Attributes().PutString(action.Destination, strings.Join(values, action.Separator))
	}

	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.SpansSliceResource(pdTraces, func(rs pcommon.Resource) {
			for _, action := range config.Assemble {
				handle(rs, action)
			}
		})

	case define.RecordMetrics:
		pdMetrics := record.Data.(pmetric.Metrics)
		foreach.MetricsSliceResource(pdMetrics, func(rs pcommon.Resource) {
			for _, action := range config.Assemble {
				handle(rs, action)
			}
		})

	case define.RecordLogs:
		pdLogs := record.Data.(plog.Logs)
		foreach.LogsSliceResource(pdLogs, func(rs pcommon.Resource) {
			for _, action := range config.Assemble {
				handle(rs, action)
			}
		})
	}
}

// addAction 新增维度
func (p *resourceFilter) addAction(record *define.Record, config Config) {
	handle := func(rs pcommon.Resource, action AddAction) {
		rs.Attributes().PutString(action.Label, action.Value)
	}

	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.SpansSliceResource(pdTraces, func(rs pcommon.Resource) {
			for _, action := range config.Add {
				handle(rs, action)
			}
		})

	case define.RecordMetrics:
		pdMetrics := record.Data.(pmetric.Metrics)
		foreach.MetricsSliceResource(pdMetrics, func(rs pcommon.Resource) {
			for _, action := range config.Add {
				handle(rs, action)
			}
		})

	case define.RecordLogs:
		pdLogs := record.Data.(plog.Logs)
		foreach.LogsSliceResource(pdLogs, func(rs pcommon.Resource) {
			for _, action := range config.Add {
				handle(rs, action)
			}
		})
	}
}

// dropAction 丢弃维度
func (p *resourceFilter) dropAction(record *define.Record, config Config) {
	handle := func(rs pcommon.Resource, action DropAction) {
		for _, key := range action.Keys {
			rs.Attributes().Remove(key)
		}
	}

	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.SpansSliceResource(pdTraces, func(rs pcommon.Resource) {
			handle(rs, config.Drop)
		})

	case define.RecordMetrics:
		pdMetrics := record.Data.(pmetric.Metrics)
		foreach.MetricsSliceResource(pdMetrics, func(rs pcommon.Resource) {
			handle(rs, config.Drop)
		})

	case define.RecordLogs:
		pdLogs := record.Data.(plog.Logs)
		foreach.LogsSliceResource(pdLogs, func(rs pcommon.Resource) {
			handle(rs, config.Drop)
		})
	}
}

// extractByRegex 正则表达式提取（使用action身上的预编译对象）
func (p *resourceFilter) extractByRegex(value string, action ReplaceAction) string {
	if action.compiledRegex != nil {
		matches := action.compiledRegex.FindStringSubmatch(value)
		if len(matches) > 1 {
			// 返回第一个捕获组的内容
			return matches[1]
		} else if len(matches) == 1 {
			// 返回整个匹配的内容
			return matches[0]
		}
	}

	// 没有匹配到则使用原始值
	return value
}

// replaceAction 替换维度
func (p *resourceFilter) replaceAction(record *define.Record, config Config) {
	handle := func(rs pcommon.Resource, action ReplaceAction) {
		v, ok := rs.Attributes().Get(action.Source)
		if !ok {
			return
		}

		// 复制原始值，防止下面的 Remove 操作导致原始值被删除
		copyValue := pcommon.NewValueEmpty()
		v.CopyTo(copyValue)

		rs.Attributes().Remove(action.Source)
		if action.ExtractPattern != "" {
			extractedValue := p.extractByRegex(copyValue.AsString(), action)
			rs.Attributes().PutString(action.Destination, extractedValue)
		} else {
			copyValue.CopyTo(rs.Attributes().PutEmpty(action.Destination))
		}
	}

	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.SpansSliceResource(pdTraces, func(rs pcommon.Resource) {
			for _, action := range config.Replace {
				handle(rs, action)
			}
		})

	case define.RecordMetrics:
		pdMetrics := record.Data.(pmetric.Metrics)
		foreach.MetricsSliceResource(pdMetrics, func(rs pcommon.Resource) {
			for _, action := range config.Replace {
				handle(rs, action)
			}
		})

	case define.RecordLogs:
		pdLogs := record.Data.(plog.Logs)
		foreach.LogsSliceResource(pdLogs, func(rs pcommon.Resource) {
			for _, action := range config.Replace {
				handle(rs, action)
			}
		})
	}
}

// fromCacheAction 从缓存中补充数据
func (p *resourceFilter) fromCacheAction(record *define.Record, config Config) {
	// 目前仅支持 k8scache
	if config.FromCache.CacheName != k8scache.Name {
		return
	}

	cache := k8scache.Default()
	if cache == nil {
		return // 缓存未初始化
	}

	keys := config.FromCache.CombineKeys()
	handle := func(rs pcommon.Resource) {
		for _, key := range keys {
			v, ok := rs.Attributes().Get(key)
			if !ok {
				continue
			}
			cacheKey := v.AsString()
			if cacheKey == "" {
				continue
			}
			dims, ok := cache.Get(cacheKey)
			if !ok {
				continue
			}

			for dk, dv := range dims {
				upsertStringIfMissingOrEmpty(rs.Attributes(), dk, dv, nil)
			}
			return // 找到一次即可
		}
	}

	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.SpansSliceResource(pdTraces, func(rs pcommon.Resource) {
			handle(rs)
		})

	case define.RecordMetrics:
		pdMetrics := record.Data.(pmetric.Metrics)
		foreach.MetricsSliceResource(pdMetrics, func(rs pcommon.Resource) {
			handle(rs)
		})

	case define.RecordLogs:
		pdLogs := record.Data.(plog.Logs)
		foreach.LogsSliceResource(pdLogs, func(rs pcommon.Resource) {
			handle(rs)
		})
	}
}

// fromRecordAction 补充 record 字段
func (p *resourceFilter) fromRecordAction(record *define.Record, config Config) {
	handle := func(rs pcommon.Resource, action FromRecordAction) {
		switch action.Source {
		case "request.client.ip":
			upsertStringIfMissingOrEmpty(rs.Attributes(), action.Destination, record.RequestClient.IP, requestClientIPPlaceholders)
		}
	}

	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.SpansSliceResource(pdTraces, func(rs pcommon.Resource) {
			for _, action := range config.FromRecord {
				handle(rs, action)
			}
		})

	case define.RecordMetrics:
		pdMetrics := record.Data.(pmetric.Metrics)
		foreach.MetricsSliceResource(pdMetrics, func(rs pcommon.Resource) {
			for _, action := range config.FromRecord {
				handle(rs, action)
			}
		})

	case define.RecordLogs:
		pdLogs := record.Data.(plog.Logs)
		foreach.LogsSliceResource(pdLogs, func(rs pcommon.Resource) {
			for _, action := range config.FromRecord {
				handle(rs, action)
			}
		})
	}
}

// upsertStringIfMissingOrEmpty 在目标字段缺失或视为空时写入 value。
func upsertStringIfMissingOrEmpty(attrs pcommon.Map, key, value string, placeholders map[string]struct{}) {
	if value == "" {
		return
	}

	// 来源命中占位符（如 0.0.0.0），不进行替换。
	// placeholders 为 nil 在这里也是安全的写法。
	if _, ok := placeholders[value]; ok {
		return
	}
	if current, ok := attrs.Get(key); ok {
		cur := current.AsString()
		if cur != "" {
			// 目标已有值，且不是占位符。
			if _, isPlaceholder := placeholders[cur]; !isPlaceholder {
				return
			}
		}
	}
	attrs.PutString(key, value)
}

// fromMetadataAction 补充 metadata 字段
func (p *resourceFilter) fromMetadataAction(record *define.Record, config Config) {
	handle := func(rs pcommon.Resource, action FromMetadataAction) {
		for _, field := range action.Keys {
			switch field {
			case "*": // 补充所有 metadata 维度
				for k, v := range record.Metadata {
					utils.InsertString(rs.Attributes(), k, v)
				}
			default:
				if v, ok := record.Metadata[field]; ok {
					utils.InsertString(rs.Attributes(), field, v)
				}
			}
		}
	}

	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.SpansSliceResource(pdTraces, func(rs pcommon.Resource) {
			handle(rs, config.FromMetadata)
		})
	}
}

// fromTokenAction 补充 token 信息, 目前仅支持 bk_app_name
func (p *resourceFilter) fromTokenAction(record *define.Record, config Config) {
	handle := func(rs pcommon.Resource, action FromTokenAction) {
		for _, field := range action.Keys {
			switch field {
			case define.TokenAppName:
				utils.InsertString(rs.Attributes(), field, record.Token.AppName)
			}
		}
	}

	switch record.RecordType {
	case define.RecordMetrics, define.RecordMetricsDerived:
		pdMetrics := record.Data.(pmetric.Metrics)
		foreach.MetricsSliceResource(pdMetrics, func(rs pcommon.Resource) {
			handle(rs, config.FromToken)
		})

	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.SpansSliceResource(pdTraces, func(rs pcommon.Resource) {
			handle(rs, config.FromToken)
		})

	case define.RecordLogs:
		pdLogs := record.Data.(plog.Logs)
		foreach.LogsSliceResource(pdLogs, func(rs pcommon.Resource) {
			handle(rs, config.FromToken)
		})
	}
}

// defaultValueAction 补充默认值
func (p *resourceFilter) defaultValueAction(record *define.Record, config Config) {
	handle := func(rs pcommon.Resource, action DefaultValueAction) {
		v, ok := rs.Attributes().Get(action.Key)
		if !ok || v.AsString() == "" {
			switch action.Type {
			case "string":
				rs.Attributes().PutString(action.Key, action.StringValue())
			case "int":
				rs.Attributes().PutInt(action.Key, int64(action.IntValue()))
			case "bool":
				rs.Attributes().PutBool(action.Key, action.BoolValue())
			}
		}
	}

	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.SpansSliceResource(pdTraces, func(rs pcommon.Resource) {
			for _, action := range config.DefaultValue {
				handle(rs, action)
			}
		})

	case define.RecordMetrics:
		pdMetrics := record.Data.(pmetric.Metrics)
		foreach.MetricsSliceResource(pdMetrics, func(rs pcommon.Resource) {
			for _, action := range config.DefaultValue {
				handle(rs, action)
			}
		})

	case define.RecordLogs:
		pdLogs := record.Data.(plog.Logs)
		foreach.LogsSliceResource(pdLogs, func(rs pcommon.Resource) {
			for _, action := range config.DefaultValue {
				handle(rs, action)
			}
		})
	}
}

const (
	keySdkName       = "telemetry.sdk.name"
	keyOriginTraceID = "origin.trace_id"
	keySw8TraceID    = "sw8.trace_id"

	sdkSkyWalking    = "skywalking"
	sdkOpenTelemetry = "opentelemetry"
)

// keepOriginTraceIdAction 保留原始 traceID
func (p *resourceFilter) keepOriginTraceIdAction(record *define.Record) {
	switch record.RecordType {
	case define.RecordTraces:
		// 根据 traceID 进行重分组，保证同 traceID 下的 span 在同一 resourceSpan 下，方便处理
		pdTraces := regroupResourceSpansByTraceID(record.Data.(ptrace.Traces))
		foreach.SpansWithResource(pdTraces, func(rs pcommon.Map, span ptrace.Span) {
			v, ok := rs.Get(keySdkName)
			if !ok {
				return
			}

			switch strings.ToLower(v.AsString()) {
			case sdkSkyWalking:
				if src, ok := rs.Get(keySw8TraceID); ok {
					utils.InsertString(rs, keyOriginTraceID, src.AsString())
					// 删除 sw8.trace_id 冗余字段
					rs.Remove(keySw8TraceID)
				}
			case sdkOpenTelemetry:
				utils.InsertString(rs, keyOriginTraceID, span.TraceID().HexString())
			}
		})
		record.Data = pdTraces
	}
}

func regroupResourceSpansByTraceID(traces ptrace.Traces) ptrace.Traces {
	newTraces := ptrace.NewTraces()
	traceIDToResourceSpans := make(map[string]ptrace.ResourceSpans)

	originalResourceSpans := traces.ResourceSpans()
	for i := 0; i < originalResourceSpans.Len(); i++ {
		resourceSpans := originalResourceSpans.At(i)
		scopeSpansSlice := resourceSpans.ScopeSpans()
		for j := 0; j < scopeSpansSlice.Len(); j++ {
			scopeSpans := scopeSpansSlice.At(j)
			spans := scopeSpans.Spans()
			for k := 0; k < spans.Len(); k++ {
				span := spans.At(k)
				traceID := span.TraceID().HexString()

				rs, exists := traceIDToResourceSpans[traceID]
				if !exists {
					rs = newTraces.ResourceSpans().AppendEmpty()
					resourceSpans.Resource().CopyTo(rs.Resource())
					traceIDToResourceSpans[traceID] = rs
				}

				ss := rs.ScopeSpans().AppendEmpty()
				scopeSpans.Scope().CopyTo(ss.Scope())
				newSpan := ss.Spans().AppendEmpty()
				span.CopyTo(newSpan)
			}
		}
	}
	return newTraces
}
