// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package probefilter

import (
	"regexp"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	attributeSpanLayer   = "sw8.span_layer"
	attributeHttpHeaders = "http.headers"
	attributeHttpCookie  = "Cookie"
	attributeHttpParams  = "http.params"
)

func init() {
	processor.Register(define.ProcessorProbeFilter, NewFactory)
}

func NewFactory(conf map[string]any, customized []processor.SubConfigProcessor) (processor.Processor, error) {
	return newFactory(conf, customized)
}

func newFactory(conf map[string]any, customized []processor.SubConfigProcessor) (*probeFilter, error) {
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

	return &probeFilter{
		CommonProcessor: processor.NewCommonProcessor(conf, customized),
		configs:         configs,
	}, nil
}

type probeFilter struct {
	processor.CommonProcessor
	configs *confengine.TierConfig // type: Config
}

func (p *probeFilter) Name() string {
	return define.ProcessorProbeFilter
}

func (p *probeFilter) IsDerived() bool {
	return false
}

func (p *probeFilter) IsPreCheck() bool {
	return false
}

func (p *probeFilter) Reload(config map[string]any, customized []processor.SubConfigProcessor) {
	f, err := newFactory(config, customized)
	if err != nil {
		logger.Errorf("failed to reload processor: %v", err)
		return
	}

	p.CommonProcessor = f.CommonProcessor
	p.configs = f.configs
}

func (p *probeFilter) Process(record *define.Record) (*define.Record, error) {
	config := p.configs.GetByToken(record.Token.Original).(Config)
	if len(config.AddAttrs) > 0 {
		p.processAddAttrsAction(record, config)
	}
	return nil, nil
}

// Add Attributes Action

func (p *probeFilter) processAddAttrsAction(record *define.Record, config Config) {
	switch record.RecordType {
	case define.RecordTraces:
		pdTraces := record.Data.(ptrace.Traces)
		foreach.SpansWithResourceAttrs(pdTraces.ResourceSpans(), func(rsAttrs pcommon.Map, span ptrace.Span) {
			for _, action := range config.AddAttrs {
				for _, rule := range action.Rules {
					if !rule.Enabled {
						continue
					}
					if v, ok := span.Attributes().Get(attributeSpanLayer); ok && v.StringVal() == rule.Type {
						processAddAttrs(span, rule, rsAttrs)
					}
				}
			}
		})
	}
}

var (
	commonRegexp = regexp.MustCompile(`(.*?)=\[(.*?)\]`)
	cookieRegexp = regexp.MustCompile(`([^ ;]+)=([^;]+)`)
)

const (
	typeService   = "service"
	typeInterface = "interface"

	targetCookie = "cookie"
	targetHeader = "header"
	targetParams = "query_parameter"
)

// matchAddAttrsRules 匹配 add_attributes 规则 满足任意一个 filter 类型即可
func matchAddAttrsRules(span ptrace.Span, rule Rule, attrs pcommon.Map) bool {
	for _, filter := range rule.Filters {
		switch filter.Type {
		case typeService:
			v, ok := attrs.Get(filter.Field)
			if ok && filter.Value == v.StringVal() {
				return true
			}
		case typeInterface:
			v, ok := span.Attributes().Get(filter.Field)
			if ok && filter.Value == v.StringVal() {
				return true
			}
		}
	}

	return false
}

// processAddAttrs 处理并新增 attributes
func processAddAttrs(span ptrace.Span, rule Rule, attrs pcommon.Map) {
	if !matchAddAttrsRules(span, rule, attrs) {
		return
	}

	// 提取 attributeHttpHeaders
	headers := make(map[string]string)
	v, ok := span.Attributes().Get(attributeHttpHeaders)
	if ok {
		matched := commonRegexp.FindAllStringSubmatch(v.StringVal(), -1)
		for _, item := range matched {
			headers[item[1]] = item[2]
		}
	}

	// 提取 attributeHttpCookie
	cookies := make(map[string]string)
	s, ok := headers[attributeHttpCookie]
	if ok {
		matched := cookieRegexp.FindAllStringSubmatch(s, -1)
		for _, item := range matched {
			cookies[item[1]] = item[2]
		}
	}

	// 提取 attributeHttpParams
	params := make(map[string]string)
	v, ok = span.Attributes().Get(attributeHttpParams)
	if ok {
		matched := commonRegexp.FindAllStringSubmatch(v.StringVal(), -1)
		for _, item := range matched {
			params[item[1]] = item[2]
		}
	}

	key := rule.Prefix + "." + rule.Field
	switch rule.Target {
	case targetCookie:
		if val, ok := cookies[rule.Field]; ok {
			span.Attributes().InsertString(key, val)
		}
	case targetHeader:
		if val, ok := headers[rule.Field]; ok {
			span.Attributes().InsertString(key, val)
		}
	case targetParams:
		if val, ok := params[rule.Field]; ok {
			span.Attributes().InsertString(key, val)
		}
	}
}
