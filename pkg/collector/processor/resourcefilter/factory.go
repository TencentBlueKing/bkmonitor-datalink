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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/resourcefilter/dimscache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func init() {
	processor.Register(define.ProcessorResourceFilter, NewFactory)
}

func NewFactory(conf map[string]interface{}, customized []processor.SubConfigProcessor) (processor.Processor, error) {
	return newFactory(conf, customized)
}

func newFactory(conf map[string]interface{}, customized []processor.SubConfigProcessor) (*resourceFilter, error) {
	configs := confengine.NewTierConfig()
	caches := confengine.NewTierConfig()

	c := &Config{}
	if err := mapstructure.Decode(conf, c); err != nil {
		return nil, err
	}
	c.Clean()
	configs.SetGlobal(*c)

	cache := dimscache.New(&c.FromCache.Cache)
	cache.Sync()
	caches.SetGlobal(cache)

	for _, custom := range customized {
		cfg := &Config{}
		if err := mapstructure.Decode(custom.Config.Config, cfg); err != nil {
			logger.Errorf("failed to decode config: %v", err)
			continue
		}
		cfg.Clean()
		configs.Set(custom.Token, custom.Type, custom.ID, *cfg)

		customCache := dimscache.New(&cfg.FromCache.Cache)
		customCache.Sync()
		caches.Set(custom.Token, custom.Type, custom.ID, customCache)
	}

	return &resourceFilter{
		CommonProcessor: processor.NewCommonProcessor(conf, customized),
		configs:         configs,
		caches:          caches,
	}, nil
}

type resourceFilter struct {
	processor.CommonProcessor
	configs *confengine.TierConfig // type: Config
	caches  *confengine.TierConfig // type dimscache.Cache
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

func (p *resourceFilter) Reload(config map[string]interface{}, customized []processor.SubConfigProcessor) {
	f, err := newFactory(config, customized)
	if err != nil {
		logger.Errorf("failed to reload processor: %v", err)
		return
	}

	p.CommonProcessor = f.CommonProcessor
	p.configs = f.configs

	equal := processor.DiffMainConfig(p.MainConfig(), config)
	if equal {
		f.caches.GetGlobal().(dimscache.Cache).Clean()
	} else {
		p.caches.GetGlobal().(dimscache.Cache).Clean()
		p.caches.SetGlobal(f.caches.GetGlobal())
	}

	diffRet := processor.DiffCustomizedConfig(p.SubConfigs(), customized)
	for _, obj := range diffRet.Keep {
		f.caches.Get(obj.Token, obj.Type, obj.ID).(dimscache.Cache).Clean()
	}

	for _, obj := range diffRet.Updated {
		p.caches.Get(obj.Token, obj.Type, obj.ID).(dimscache.Cache).Clean()
		newCache := f.caches.Get(obj.Token, obj.Type, obj.ID)
		p.caches.Set(obj.Token, obj.Type, obj.ID, newCache)
	}

	for _, obj := range diffRet.Deleted {
		p.caches.Get(obj.Token, obj.Type, obj.ID).(dimscache.Cache).Clean()
		p.caches.Del(obj.Token, obj.Type, obj.ID)
	}
}

func (p *resourceFilter) Clean() {
	for _, obj := range p.caches.All() {
		obj.(dimscache.Cache).Clean()
	}
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

	if config.FromCache.Cache.Validate() {
		p.fromCacheAction(record, config)
	}
	return nil, nil
}

// assembleAction 组合维度
func (p *resourceFilter) assembleAction(record *define.Record, config Config) {
	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		resourceSpansSlice := pdTraces.ResourceSpans()
		for _, action := range config.Assemble {
			for i := 0; i < resourceSpansSlice.Len(); i++ {
				resourceSpans := resourceSpansSlice.At(i)
				attrs := resourceSpans.Resource().Attributes()
				var values []string
				for _, key := range action.Keys {
					v, ok := attrs.Get(key)
					if !ok {
						// 空值保留
						values = append(values, "")
						continue
					}
					values = append(values, v.AsString())
				}
				attrs.UpsertString(action.Destination, strings.Join(values, action.Separator))
			}
		}
	}
}

// addAction 新增维度
func (p *resourceFilter) addAction(record *define.Record, config Config) {
	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		resourceSpansSlice := pdTraces.ResourceSpans()
		for _, action := range config.Add {
			for i := 0; i < resourceSpansSlice.Len(); i++ {
				resourceSpans := resourceSpansSlice.At(i)
				resourceSpans.Resource().Attributes().UpsertString(action.Label, action.Value)
			}
		}

	case define.RecordMetrics:
		pdMetrics := record.Data.(pmetric.Metrics)
		resourceMetricsSlice := pdMetrics.ResourceMetrics()
		for _, action := range config.Add {
			for i := 0; i < resourceMetricsSlice.Len(); i++ {
				resourceMetrics := resourceMetricsSlice.At(i)
				resourceMetrics.Resource().Attributes().UpsertString(action.Label, action.Value)
			}
		}

	case define.RecordLogs:
		pdLogs := record.Data.(plog.Logs)
		resourceLogsSlice := pdLogs.ResourceLogs()
		for _, action := range config.Add {
			for i := 0; i < resourceLogsSlice.Len(); i++ {
				resourceLogs := resourceLogsSlice.At(i)
				resourceLogs.Resource().Attributes().UpsertString(action.Label, action.Value)
			}
		}
	}
}

// dropAction 丢弃维度
func (p *resourceFilter) dropAction(record *define.Record, config Config) {
	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		resourceSpansSlice := pdTraces.ResourceSpans()
		// 只对 drop action 清洗到 span 维度
		for _, dimension := range config.Drop.Keys {
			for i := 0; i < resourceSpansSlice.Len(); i++ {
				resourceSpans := resourceSpansSlice.At(i)
				resourceSpans.Resource().Attributes().Remove(dimension)
				scopeSpansSlice := resourceSpans.ScopeSpans()
				for j := 0; j < scopeSpansSlice.Len(); j++ {
					spans := scopeSpansSlice.At(j).Spans()
					for k := 0; k < spans.Len(); k++ {
						spans.At(k).Attributes().Remove(dimension)
					}
				}
			}
		}

	case define.RecordMetrics:
		pdMetrics := record.Data.(pmetric.Metrics)
		resourceMetricsSlice := pdMetrics.ResourceMetrics()
		for _, dimension := range config.Drop.Keys {
			for i := 0; i < resourceMetricsSlice.Len(); i++ {
				resourceMetrics := resourceMetricsSlice.At(i)
				resourceMetrics.Resource().Attributes().Remove(dimension)
			}
		}

	case define.RecordLogs:
		pdLogs := record.Data.(plog.Logs)
		resourceLogsSlice := pdLogs.ResourceLogs()
		for _, dimension := range config.Drop.Keys {
			for i := 0; i < resourceLogsSlice.Len(); i++ {
				resourceLogs := resourceLogsSlice.At(i)
				resourceLogs.Resource().Attributes().Remove(dimension)
			}
		}
	}
}

// replaceAction 替换维度
func (p *resourceFilter) replaceAction(record *define.Record, config Config) {
	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		resourceSpansSlice := pdTraces.ResourceSpans()
		for _, action := range config.Replace {
			for i := 0; i < resourceSpansSlice.Len(); i++ {
				resourceSpans := resourceSpansSlice.At(i)
				v, ok := resourceSpans.Resource().Attributes().Get(action.Source)
				if !ok {
					continue
				}
				resourceSpans.Resource().Attributes().Remove(action.Source)
				resourceSpans.Resource().Attributes().Upsert(action.Destination, v)
			}
		}

	case define.RecordMetrics:
		pdMetrics := record.Data.(pmetric.Metrics)
		resourceMetricsSlice := pdMetrics.ResourceMetrics()
		for _, action := range config.Replace {
			for i := 0; i < resourceMetricsSlice.Len(); i++ {
				resourceMetrics := resourceMetricsSlice.At(i)
				v, ok := resourceMetrics.Resource().Attributes().Get(action.Source)
				if !ok {
					continue
				}
				cloned := pcommon.NewValueEmpty()
				v.CopyTo(cloned)
				resourceMetrics.Resource().Attributes().Remove(action.Source)
				resourceMetrics.Resource().Attributes().Upsert(action.Destination, cloned)
			}
		}

	case define.RecordLogs:
		pdLogs := record.Data.(plog.Logs)
		resourceLogsSlice := pdLogs.ResourceLogs()
		for _, action := range config.Replace {
			for i := 0; i < resourceLogsSlice.Len(); i++ {
				resourceLogs := resourceLogsSlice.At(i)
				v, ok := resourceLogs.Resource().Attributes().Get(action.Source)
				if !ok {
					continue
				}
				resourceLogs.Resource().Attributes().Remove(action.Source)
				resourceLogs.Resource().Attributes().Upsert(action.Destination, v)
			}
		}
	}
}

// fromCacheAction 从缓存中补充数据
func (p *resourceFilter) fromCacheAction(record *define.Record, config Config) {
	token := record.Token.Original
	cache := p.caches.GetByToken(token).(dimscache.Cache)

	keys := config.FromCache.CombineKeys()
	handleTraces := func(resourceSpans ptrace.ResourceSpans) {
		for _, key := range keys {
			v, ok := resourceSpans.Resource().Attributes().Get(key)
			if !ok {
				continue
			}
			dims, ok := cache.Get(v.AsString())
			if !ok {
				continue
			}

			for _, dim := range config.FromCache.Dimensions {
				if lb, ok := dims[dim]; ok {
					resourceSpans.Resource().Attributes().InsertString(dim, lb)
				}
			}
			return // 找到一次即可
		}
	}

	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		resourceSpansSlice := pdTraces.ResourceSpans()
		for i := 0; i < resourceSpansSlice.Len(); i++ {
			handleTraces(resourceSpansSlice.At(i))
		}
	}
}

// fromRecordAction 从 define.Record 中补充数据
func (p *resourceFilter) fromRecordAction(record *define.Record, config Config) {
	handleTraces := func(resourceSpans ptrace.ResourceSpans) {
		for _, action := range config.FromRecord {
			switch action.Source {
			case "request.client.ip":
				resourceSpans.Resource().Attributes().InsertString(action.Destination, record.RequestClient.IP)
			}
		}
	}

	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		resourceSpansSlice := pdTraces.ResourceSpans()
		for i := 0; i < resourceSpansSlice.Len(); i++ {
			handleTraces(resourceSpansSlice.At(i))
		}
	}
}
