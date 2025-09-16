// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metricsfilter

import (
	"github.com/prometheus/prometheus/prompb"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/promlabels"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func init() {
	processor.Register(define.ProcessorMetricsFilter, NewFactory)
}

func NewFactory(conf map[string]any, customized []processor.SubConfigProcessor) (processor.Processor, error) {
	return newFactory(conf, customized)
}

func newFactory(conf map[string]any, customized []processor.SubConfigProcessor) (*metricsFilter, error) {
	configs := confengine.NewTierConfig()

	c := &Config{}
	if err := mapstructure.Decode(conf, c); err != nil {
		return nil, err
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	configs.SetGlobal(*c)

	for _, custom := range customized {
		cfg := &Config{}
		if err := mapstructure.Decode(custom.Config.Config, cfg); err != nil {
			logger.Errorf("failed to decode config: %v", err)
			continue
		}
		if err := cfg.Validate(); err != nil {
			logger.Errorf("invalid config: %v", err)
			continue
		}
		configs.Set(custom.Token, custom.Type, custom.ID, *cfg)
	}

	return &metricsFilter{
		CommonProcessor: processor.NewCommonProcessor(conf, customized),
		configs:         configs,
	}, nil
}

type metricsFilter struct {
	processor.CommonProcessor
	configs *confengine.TierConfig // type: Config
}

func (p *metricsFilter) Name() string {
	return define.ProcessorMetricsFilter
}

func (p *metricsFilter) IsDerived() bool {
	return false
}

func (p *metricsFilter) IsPreCheck() bool {
	return false
}

func (p *metricsFilter) Reload(config map[string]any, customized []processor.SubConfigProcessor) {
	f, err := newFactory(config, customized)
	if err != nil {
		logger.Errorf("failed to reload processor: %v", err)
		return
	}

	p.CommonProcessor = f.CommonProcessor
	p.configs = f.configs
}

func (p *metricsFilter) Process(record *define.Record) (*define.Record, error) {
	config := p.configs.GetByToken(record.Token.Original).(Config)
	if len(config.Drop.Metrics) > 0 {
		p.dropAction(record, config)
	}
	if len(config.Replace) > 0 {
		p.replaceAction(record, config)
	}
	if len(config.Relabel) > 0 {
		p.relabelAction(record, config)
	}
	if len(config.CodeRelabel) > 0 {
		p.codeRelabelAction(record, config)
	}
	return nil, nil
}

func (p *metricsFilter) dropAction(record *define.Record, config Config) {
	switch record.RecordType {
	case define.RecordMetrics:
		for _, name := range config.Drop.Metrics {
			pdMetrics := record.Data.(pmetric.Metrics)
			pdMetrics.ResourceMetrics().RemoveIf(func(resourceMetrics pmetric.ResourceMetrics) bool {
				resourceMetrics.ScopeMetrics().RemoveIf(func(scopeMetrics pmetric.ScopeMetrics) bool {
					scopeMetrics.Metrics().RemoveIf(func(metric pmetric.Metric) bool {
						return metric.Name() == name
					})
					return scopeMetrics.Metrics().Len() == 0
				})
				return resourceMetrics.ScopeMetrics().Len() == 0
			})
		}
	}
}

func (p *metricsFilter) replaceAction(record *define.Record, config Config) {
	switch record.RecordType {
	case define.RecordMetrics:
		for _, action := range config.Replace {
			pdMetrics := record.Data.(pmetric.Metrics)
			foreach.Metrics(pdMetrics.ResourceMetrics(), func(metric pmetric.Metric) {
				if metric.Name() == action.Source {
					metric.SetName(action.Destination)
				}
			})
		}
	}
}

func (p *metricsFilter) relabelAction(record *define.Record, config Config) {
	switch record.RecordType {
	case define.RecordMetrics:
		for _, action := range config.Relabel {
			pdMetrics := record.Data.(pmetric.Metrics)
			foreach.MetricsSliceDataPointsAttrs(pdMetrics.ResourceMetrics(), func(name string, attrs pcommon.Map) {
				if !action.IsMetricIn(name) || !action.MatchOTAttrs(attrs) {
					return
				}

				target := action.Target
				switch action.Target.Action {
				case relabelUpsert:
					attrs.UpsertString(target.Label, target.Value)
				}
			})
		}

	case define.RecordRemoteWrite:
		handle := func(ts *prompb.TimeSeries, action RelabelAction) {
			lbs := promlabels.Labels(ts.GetLabels())
			nameLabel, ok := lbs.Get("__name__")
			if !ok || !action.IsMetricIn(nameLabel.GetValue()) {
				return
			}
			if !action.MatchRWLabels(lbs) {
				return
			}

			target := action.Target
			switch target.Action {
			case relabelUpsert:
				lbs.Upsert(target.Label, target.Value)
			}
			ts.Labels = lbs
		}
		for _, action := range config.Relabel {
			rwData := record.Data.(*define.RemoteWriteData)
			for i := 0; i < len(rwData.Timeseries); i++ {
				handle(&rwData.Timeseries[i], action)
			}
		}
	}
}

func (p *metricsFilter) codeRelabelAction(record *define.Record, config Config) {
	switch record.RecordType {
	case define.RecordMetrics:
		for _, action := range config.CodeRelabel {
			pdMetrics := record.Data.(pmetric.Metrics)
			foreach.MetricsSliceDataPointsAttrs(pdMetrics.ResourceMetrics(), func(name string, attrs pcommon.Map) {
				if !action.IsMetricIn(name) || !action.MatchOTAttrs(attrs) {
					return
				}

				for _, service := range action.Services {
					if !service.MatchOTAttrs(attrs) {
						continue
					}

					for _, code := range service.Codes {
						if !code.MatchOTAttrs(attrs) {
							continue
						}

						target := code.Target
						switch target.Action {
						case relabelUpsert:
							attrs.UpsertString(target.Label, target.Value)
							return // 每个指标只可能命中一次
						}
					}
				}
			})
		}

	case define.RecordRemoteWrite:
		handle := func(ts *prompb.TimeSeries, action CodeRelabelAction) {
			lbs := promlabels.Labels(ts.GetLabels())
			nameLabel, ok := lbs.Get("__name__")
			if !ok || !action.IsMetricIn(nameLabel.GetValue()) {
				return
			}
			if !action.MatchRWLabels(lbs) {
				return
			}

			for _, service := range action.Services {
				if !service.MatchRWLabels(lbs) {
					continue
				}

				for _, code := range service.Codes {
					if !code.MatchRWLabels(lbs) {
						continue
					}

					target := code.Target
					switch target.Action {
					case relabelUpsert:
						lbs.Upsert(target.Label, target.Value)
						ts.Labels = lbs
						return // 每个指标只可能命中一次
					}
				}
			}
		}
		for _, action := range config.CodeRelabel {
			rwData := record.Data.(*define.RemoteWriteData)
			for i := 0; i < len(rwData.Timeseries); i++ {
				handle(&rwData.Timeseries[i], action)
			}
		}
	}
}
