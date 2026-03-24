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
	dto "github.com/prometheus/client_model/go"
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

func upsertLabels(labels *[]*dto.LabelPair, name, value string) {
	if labels == nil {
		return
	}
	// explicit copy to avoid address reuse issues
	val := value
	nm := name
	for i := 0; i < len(*labels); i++ {
		if (*labels)[i].GetName() == name {
			(*labels)[i].Value = &val
			return
		}
	}
	*labels = append(*labels, &dto.LabelPair{
		Name: &nm, Value: &val,
	})
}

func buildPushGatewayAttrs(pd *define.PushGatewayData, metric *dto.Metric) pcommon.Map {
	attrs := pcommon.NewMap()
	for k, v := range pd.Labels {
		attrs.UpsertString(k, v)
	}
	for _, label := range metric.Label {
		attrs.UpsertString(label.GetName(), label.GetValue())
	}
	return attrs
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
			foreach.Metrics(pdMetrics, func(metric pmetric.Metric) {
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
			foreach.MetricsDataPointWithResource(pdMetrics, func(metric pmetric.Metric, rs, attrs pcommon.Map) {
				if !action.IsMetricIn(metric.Name()) || !action.MatchMap(attrs) {
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
			if !action.MatchLabels(lbs) {
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
	case define.RecordPushGateway:
		handle := func(pd *define.PushGatewayData, metric *dto.Metric, action RelabelAction) {
			attrs := buildPushGatewayAttrs(pd, metric)
			if !action.MatchMap(attrs) {
				return
			}

			target := action.Target
			switch target.Action {
			case relabelUpsert:
				upsertLabels(&metric.Label, target.Label, target.Value)
			}
		}

		for _, action := range config.Relabel {
			pd := record.Data.(*define.PushGatewayData)
			name := *pd.MetricFamilies.Name
			if *pd.MetricFamilies.Type == dto.MetricType_HISTOGRAM {
				name += "_bucket"
			}
			if !action.IsMetricIn(name) {
				continue
			}

			for _, metric := range pd.MetricFamilies.Metric {
				handle(pd, metric, action)
			}
		}
	}
}

func (p *metricsFilter) codeRelabelAction(record *define.Record, config Config) {
	switch record.RecordType {
	case define.RecordMetrics:
		for _, action := range config.CodeRelabel {
			pdMetrics := record.Data.(pmetric.Metrics)
			foreach.MetricsDataPointWithResource(pdMetrics, func(metric pmetric.Metric, rs, attrs pcommon.Map) {
				// service_name 需要从 rs 中获取
				// 其余字段从 attrs 中获取
				if !action.IsMetricIn(metric.Name()) || !action.MatchMap(rs) {
					return
				}

				for _, service := range action.Services {
					if !service.MatchMap(attrs) {
						continue
					}

					for _, code := range service.Codes {
						if !code.MatchMap(attrs) {
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
			if !action.MatchLabels(lbs) {
				return
			}

			for _, service := range action.Services {
				if !service.MatchLabels(lbs) {
					continue
				}

				for _, code := range service.Codes {
					if !code.MatchLabels(lbs) {
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
	case define.RecordPushGateway:
		handle := func(pd *define.PushGatewayData, metric *dto.Metric, action CodeRelabelAction) {
			attrs := buildPushGatewayAttrs(pd, metric)
			if !action.MatchMap(attrs) {
				return
			}

			for _, service := range action.Services {
				if !service.MatchMap(attrs) {
					continue
				}
				for _, code := range service.Codes {
					if !code.MatchMap(attrs) {
						continue
					}
					target := code.Target
					switch target.Action {
					case relabelUpsert:
						upsertLabels(&metric.Label, target.Label, target.Value)
						return // 每个指标只可能命中一次
					}
				}
			}
		}

		for _, action := range config.CodeRelabel {
			pd := record.Data.(*define.PushGatewayData)

			name := *pd.MetricFamilies.Name
			if *pd.MetricFamilies.Type == dto.MetricType_HISTOGRAM {
				// PushGateway Histogram 类型指标需补充后缀进行匹配，避免因指标名不完整导致无法命中。
				name += "_bucket"
			}
			if !action.IsMetricIn(name) {
				continue
			}

			for _, metric := range pd.MetricFamilies.Metric {
				handle(pd, metric, action)
			}
		}
	}
}
