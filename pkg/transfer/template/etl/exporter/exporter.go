// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package exporter

import (
	"context"

	"github.com/cstockton/go-conv"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/types"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// PrometheusCollectorMetric :
type PrometheusCollectorMetric struct {
	Exemplar  map[string]interface{} `json:"exemplar"`
	Key       string                 `json:"key"`
	Labels    map[string]interface{} `json:"labels"`
	Value     interface{}            `json:"value"`
	Timestamp int64                  `json:"timestamp"`
}

type prometheusCollectorData struct {
	Timestamp  types.TimeStamp          `json:"@timestamp"`
	SupplierID uint32                   `json:"bizid"`
	CloudID    uint32                   `json:"cloudid"`
	IP         string                   `json:"ip"`
	Group      []map[string]interface{} `json:"group_info"`
	CMDBLevel  []map[string]interface{} `json:"bK_cmdb_level"`
	Prometheus struct {
		Collector struct {
			Metrics []PrometheusCollectorMetric `json:"metrics"`
		} `json:"collector"`
	} `json:"prometheus"`
}

type filterRecord struct {
	*PrometheusCollectorMetric
	Timestamp int64                    `json:"time"`
	GroupInfo []map[string]interface{} `json:"group_info"`
	CMDBLevel []map[string]interface{} `json:"bk_cmdb_level"`
}

// FilterProcessor :
type FilterProcessor struct {
	*define.BaseDataProcessor
	*define.ProcessorMonitor
	store           define.Store
	metrics         map[string]*config.MetaFieldConfig
	enableBlackList bool
}

// NewFilterProcessor :
func NewFilterProcessor(ctx context.Context, name string) *FilterProcessor {
	pipe := config.PipelineConfigFromContext(ctx)
	p := &FilterProcessor{
		BaseDataProcessor: define.NewBaseDataProcessor(name),
		ProcessorMonitor:  pipeline.NewDataProcessorMonitor(name, pipe),
		metrics:           make(map[string]*config.MetaFieldConfig, len(pipe.ResultTableList)),
		store:             define.StoreFromContext(ctx),
	}

	rtConfig := config.ResultTableConfigFromContext(ctx)
	rtOpt := utils.NewMapHelper(rtConfig.Option)
	p.enableBlackList, _ = rtOpt.GetBool(config.ResultTableOptEnableBlackList)
	for _, rt := range pipe.ResultTableList {
		logging.PanicIf(rt.VisitFieldByTag(func(field *config.MetaFieldConfig) error {
			p.metrics[field.FieldName] = field
			return nil
		}, nil))
	}

	return p
}

func (p *FilterProcessor) rejectField(s string) bool {
	field := p.metrics[s]

	// field 存在分两种情况
	if field != nil {
		// 1) 确定 disabled 的 field 直接丢弃
		if field.Disabled {
			return true
		}

		// 2) field 存在且非 disabled 需要保留
		return false
	}

	// field 不存在也分两种情况
	// 1) field 不存在 没开启黑名单模式（丢弃）
	// 2) field 不存在 开启黑名单模式（放行）
	return !p.enableBlackList
}

// Process : process json data
func (p *FilterProcessor) Process(d define.Payload, outputChan chan<- define.Payload, killChan chan<- error) {
	var (
		err  error
		data prometheusCollectorData
	)

	err = d.To(&data)
	if err != nil {
		logging.Warnf("%v load exporter payload %v error %v", p, d, err)
		return
	}

	n := 0
	for _, metric := range data.Prometheus.Collector.Metrics {
		key := metric.Key
		if p.rejectField(key) {
			continue
		}

		// 针对 ip 字段进行特殊处理
		// 如果用户上报的数据中存在 ip 维度，先存储在 `define.RecordTmpUserIPFieldName` label 中
		if v, ok := metric.Labels[define.RecordIPFieldName]; ok {
			metric.Labels[define.RecordTmpUserIPFieldName] = v
		}

		metric.Labels[define.RecordIPFieldName] = data.IP
		metric.Labels[define.RecordSupplierIDFieldName] = conv.String(data.SupplierID)
		metric.Labels[define.RecordCloudIDFieldName] = conv.String(data.CloudID)

		var ts int64
		if metric.Timestamp > 0 {
			ts = metric.Timestamp
		} else {
			ts = data.Timestamp.Int64()
		}

		output, err := define.DerivePayload(d, filterRecord{
			PrometheusCollectorMetric: &metric,
			Timestamp:                 ts,
			GroupInfo:                 data.Group,
			CMDBLevel:                 data.CMDBLevel,
		})
		if err != nil {
			logging.Warnf("create output payload %v", output)
			continue
		}

		outputChan <- output
		n++
	}
	logging.Debugf("%v filtered %d metrics", p, n)
	p.CounterSuccesses.Inc()
}

// Processor :
type Processor struct {
	*define.BaseDataProcessor
	*define.ProcessorMonitor
	metrics         map[string]*config.MetaFieldConfig
	enableBlackList bool
}

// NewProcessor :
func NewProcessor(ctx context.Context, name string) (*Processor, error) {
	pipe := config.PipelineConfigFromContext(ctx)
	rt := config.ResultTableConfigFromContext(ctx)
	p := &Processor{
		BaseDataProcessor: define.NewBaseDataProcessor(name),
		ProcessorMonitor:  pipeline.NewDataProcessorMonitor(name, pipe),
		metrics:           map[string]*config.MetaFieldConfig{},
	}

	rtConfig := config.ResultTableConfigFromContext(ctx)
	rtOpt := utils.NewMapHelper(rtConfig.Option)
	p.enableBlackList, _ = rtOpt.GetBool(config.ResultTableOptEnableBlackList)
	err := rt.VisitFieldByTag(func(f *config.MetaFieldConfig) error {
		p.metrics[f.FieldName] = f
		return nil
	}, nil)
	if err != nil {
		return nil, err
	}

	return p, nil
}

func (p *Processor) rejectField(s string) bool {
	field := p.metrics[s]

	// field 存在分两种情况
	if field != nil {
		// 1) 确定 disabled 的 field 直接丢弃
		if field.Disabled {
			return true
		}

		// 2) field 存在且非 disabled 需要保留
		return false
	}

	// field 不存在也分两种情况
	// 1) field 不存在 没开启黑名单模式（丢弃）
	// 2) field 不存在 开启黑名单模式（放行）
	return !p.enableBlackList
}

// Process : process json data
func (p *Processor) Process(d define.Payload, outputChan chan<- define.Payload, killChan chan<- error) {
	var record filterRecord
	err := d.To(&record)
	if err != nil {
		logging.Warnf("%v load payload error %v", p, err)
		p.CounterFails.Inc()
		return
	} else if record.PrometheusCollectorMetric == nil || record.Labels == nil {
		logging.Warnf("%v load payload failed", p)
		p.CounterFails.Inc()
		return
	}

	if p.rejectField(record.Key) {
		return
	}

	data := &define.GroupETLRecord{
		ETLRecord: &define.ETLRecord{
			Time:       &record.Timestamp,
			Dimensions: record.Labels,
			Metrics: map[string]interface{}{
				record.Key: record.Value,
			},
			Exemplar: record.Exemplar,
		},
		GroupInfo: record.GroupInfo,
		CMDBInfo:  record.CMDBLevel,
	}

	output, err := define.DerivePayload(d, data)
	if err != nil {
		logging.Warnf("%v metric dump error %v", p, err)
		return
	}

	outputChan <- output
}

func init() {
	define.RegisterDataProcessor("exporter-filter", func(ctx context.Context, name string) (processor define.DataProcessor, e error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewFilterProcessor(ctx, pipeConfig.FormatName(name)), nil
	})
	define.RegisterDataProcessor("exporter", func(ctx context.Context, name string) (processor define.DataProcessor, e error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewProcessor(ctx, pipeConfig.FormatName(name))
	})
}
