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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/resourcefilter/k8scache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func init() {
	processor.Register(define.ProcessorResourceFilter, NewFactory)
}

func NewFactory(conf map[string]any, customized []processor.SubConfigProcessor) (processor.Processor, error) {
	return newFactory(conf, customized)
}

func newFactory(conf map[string]any, customized []processor.SubConfigProcessor) (*resourceFilter, error) {
	configs := confengine.NewTierConfig()
	caches := confengine.NewTierConfig()

	c := &Config{}
	if err := mapstructure.Decode(conf, c); err != nil {
		return nil, err
	}
	c.Clean()
	configs.SetGlobal(*c)

	cache := k8scache.New(&c.FromCache.Cache)
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

		customCache := k8scache.New(&cfg.FromCache.Cache)
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
	caches  *confengine.TierConfig // type k8scache.Cache
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

	equal := processor.DiffMainConfig(p.MainConfig(), config)
	if equal {
		f.caches.GetGlobal().(k8scache.Cache).Clean()
	} else {
		p.caches.GetGlobal().(k8scache.Cache).Clean()
		p.caches.SetGlobal(f.caches.GetGlobal())
	}

	diffRet := processor.DiffCustomizedConfig(p.SubConfigs(), customized)
	for _, obj := range diffRet.Keep {
		f.caches.Get(obj.Token, obj.Type, obj.ID).(k8scache.Cache).Clean()
	}

	for _, obj := range diffRet.Updated {
		p.caches.Get(obj.Token, obj.Type, obj.ID).(k8scache.Cache).Clean()
		newCache := f.caches.Get(obj.Token, obj.Type, obj.ID)
		p.caches.Set(obj.Token, obj.Type, obj.ID, newCache)
	}

	for _, obj := range diffRet.Deleted {
		p.caches.Get(obj.Token, obj.Type, obj.ID).(k8scache.Cache).Clean()
		p.caches.Del(obj.Token, obj.Type, obj.ID)
	}

	p.CommonProcessor = f.CommonProcessor
	p.configs = f.configs
}

func (p *resourceFilter) Clean() {
	for _, obj := range p.caches.All() {
		obj.(k8scache.Cache).Clean()
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
	if len(config.FromMetadata.Keys) > 0 {
		p.fromMetadataAction(record, config)
	}
	if len(config.FromToken.Keys) > 0 {
		p.fromTokenAction(record, config)
	}
	if len(config.DefaultValue) > 0 {
		p.defaultValueAction(record, config)
	}

	if config.FromCache.Cache.Validate() {
		p.fromCacheAction(record, config)
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
		rs.Attributes().UpsertString(action.Destination, strings.Join(values, action.Separator))
	}

	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.SpansSliceResource(pdTraces.ResourceSpans(), func(rs pcommon.Resource) {
			for _, action := range config.Assemble {
				handle(rs, action)
			}
		})
	}
}

// addAction 新增维度
func (p *resourceFilter) addAction(record *define.Record, config Config) {
	handle := func(rs pcommon.Resource, action AddAction) {
		rs.Attributes().UpsertString(action.Label, action.Value)
	}

	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.SpansSliceResource(pdTraces.ResourceSpans(), func(rs pcommon.Resource) {
			for _, action := range config.Add {
				handle(rs, action)
			}
		})

	case define.RecordMetrics:
		pdMetrics := record.Data.(pmetric.Metrics)
		foreach.MetricsSliceResource(pdMetrics.ResourceMetrics(), func(rs pcommon.Resource) {
			for _, action := range config.Add {
				handle(rs, action)
			}
		})

	case define.RecordLogs:
		pdLogs := record.Data.(plog.Logs)
		foreach.LogsSliceResource(pdLogs.ResourceLogs(), func(rs pcommon.Resource) {
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
		foreach.SpansSliceResource(pdTraces.ResourceSpans(), func(rs pcommon.Resource) {
			handle(rs, config.Drop)
		})

	case define.RecordMetrics:
		pdMetrics := record.Data.(pmetric.Metrics)
		foreach.MetricsSliceResource(pdMetrics.ResourceMetrics(), func(rs pcommon.Resource) {
			handle(rs, config.Drop)
		})

	case define.RecordLogs:
		pdLogs := record.Data.(plog.Logs)
		foreach.LogsSliceResource(pdLogs.ResourceLogs(), func(rs pcommon.Resource) {
			handle(rs, config.Drop)
		})
	}
}

// replaceAction 替换维度
func (p *resourceFilter) replaceAction(record *define.Record, config Config) {
	handle := func(rs pcommon.Resource, action ReplaceAction) {
		v, ok := rs.Attributes().Get(action.Source)
		if !ok {
			return
		}
		rs.Attributes().Remove(action.Source)
		rs.Attributes().Upsert(action.Destination, v)
	}

	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.SpansSliceResource(pdTraces.ResourceSpans(), func(rs pcommon.Resource) {
			for _, action := range config.Replace {
				handle(rs, action)
			}
		})

	case define.RecordMetrics:
		pdMetrics := record.Data.(pmetric.Metrics)
		foreach.MetricsSliceResource(pdMetrics.ResourceMetrics(), func(rs pcommon.Resource) {
			for _, action := range config.Replace {
				handle(rs, action)
			}
		})

	case define.RecordLogs:
		pdLogs := record.Data.(plog.Logs)
		foreach.LogsSliceResource(pdLogs.ResourceLogs(), func(rs pcommon.Resource) {
			for _, action := range config.Replace {
				handle(rs, action)
			}
		})
	}
}

// fromCacheAction 从缓存中补充数据
func (p *resourceFilter) fromCacheAction(record *define.Record, config Config) {
	token := record.Token.Original
	cache := p.caches.GetByToken(token).(k8scache.Cache)

	keys := config.FromCache.CombineKeys()
	handle := func(rs pcommon.Resource) {
		for _, key := range keys {
			v, ok := rs.Attributes().Get(key)
			if !ok {
				continue
			}
			dims, ok := cache.Get(v.AsString())
			if !ok {
				continue
			}

			for dk, dv := range dims {
				rs.Attributes().InsertString(dk, dv)
			}
			return // 找到一次即可
		}
	}

	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.SpansSliceResource(pdTraces.ResourceSpans(), func(rs pcommon.Resource) {
			handle(rs)
		})

	case define.RecordMetrics:
		pdMetrics := record.Data.(pmetric.Metrics)
		foreach.MetricsSliceResource(pdMetrics.ResourceMetrics(), func(rs pcommon.Resource) {
			handle(rs)
		})
	}
}

// fromRecordAction 补充 record 字段
func (p *resourceFilter) fromRecordAction(record *define.Record, config Config) {
	handle := func(rs pcommon.Resource, action FromRecordAction) {
		switch action.Source {
		case "request.client.ip":
			rs.Attributes().InsertString(action.Destination, record.RequestClient.IP)
		}
	}

	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.SpansSliceResource(pdTraces.ResourceSpans(), func(rs pcommon.Resource) {
			for _, action := range config.FromRecord {
				handle(rs, action)
			}
		})

	case define.RecordMetrics:
		pdMetrics := record.Data.(pmetric.Metrics)
		foreach.MetricsSliceResource(pdMetrics.ResourceMetrics(), func(rs pcommon.Resource) {
			for _, action := range config.FromRecord {
				handle(rs, action)
			}
		})
	}
}

// fromMetadataAction 补充 metadata 字段
func (p *resourceFilter) fromMetadataAction(record *define.Record, config Config) {
	handle := func(rs pcommon.Resource, action FromMetadataAction) {
		for _, field := range action.Keys {
			switch field {
			case "*": // 补充所有 metadata 维度
				for k, v := range record.Metadata {
					rs.Attributes().InsertString(k, v)
				}
			default:
				if v, ok := record.Metadata[field]; ok {
					rs.Attributes().InsertString(field, v)
				}
			}
		}
	}

	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.SpansSliceResource(pdTraces.ResourceSpans(), func(rs pcommon.Resource) {
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
				rs.Attributes().InsertString(field, record.Token.AppName)
			}
		}
	}

	switch record.RecordType {
	case define.RecordMetrics:
		pdMetrics := record.Data.(pmetric.Metrics)
		foreach.MetricsSliceResource(pdMetrics.ResourceMetrics(), func(rs pcommon.Resource) {
			handle(rs, config.FromToken)
		})

	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.SpansSliceResource(pdTraces.ResourceSpans(), func(rs pcommon.Resource) {
			handle(rs, config.FromToken)
		})

	case define.RecordLogs:
		pdLogs := record.Data.(plog.Logs)
		foreach.LogsSliceResource(pdLogs.ResourceLogs(), func(rs pcommon.Resource) {
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
				rs.Attributes().UpsertString(action.Key, action.StringValue())
			case "int":
				rs.Attributes().UpsertInt(action.Key, int64(action.IntValue()))
			case "bool":
				rs.Attributes().UpsertBool(action.Key, action.BoolValue())
			}
		}
	}

	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.SpansSliceResource(pdTraces.ResourceSpans(), func(rs pcommon.Resource) {
			for _, action := range config.DefaultValue {
				handle(rs, action)
			}
		})

	case define.RecordMetrics:
		pdMetrics := record.Data.(pmetric.Metrics)
		foreach.MetricsSliceResource(pdMetrics.ResourceMetrics(), func(rs pcommon.Resource) {
			for _, action := range config.DefaultValue {
				handle(rs, action)
			}
		})

	case define.RecordLogs:
		pdLogs := record.Data.(plog.Logs)
		foreach.LogsSliceResource(pdLogs.ResourceLogs(), func(rs pcommon.Resource) {
			for _, action := range config.DefaultValue {
				handle(rs, action)
			}
		})
	}
}
