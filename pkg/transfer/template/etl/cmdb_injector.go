// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package etl

import (
	"context"

	"github.com/cstockton/go-conv"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// CMDBLevelRecord
type CMDBLevelRecord struct {
	define.ETLRecord
	CMDBLevel interface{}         `json:"bk_cmdb_level"`
	Groups    []map[string]string `json:"group_info"`
}

// GroupInjector
type CMDBLevelInjector struct {
	*define.BaseDataProcessor
	*define.ProcessorMonitor
}

// Process
func (p *CMDBLevelInjector) Process(d define.Payload, outputChan chan<- define.Payload, killChan chan<- error) {
	if d.Flag()&define.PayloadFlagNoCmdbLevels == define.PayloadFlagNoCmdbLevels {
		logging.Debugf("%v no cmdblevels found, playload %#v", p, d)
		outputChan <- d
		p.CounterSuccesses.Inc()
		return
	}

	var err error
	raw := new(CMDBLevelRecord)
	err = d.To(raw)

	if err != nil {
		p.CounterFails.Inc()
		logging.Warnf("%v convert payload %#v error %v", p, d, err)
		return
	}
	record := GroupedRecord{
		ETLRecord: define.ETLRecord{
			Time:       raw.Time,
			Metrics:    raw.Metrics,
			Dimensions: raw.Dimensions,
			Exemplar:   raw.Exemplar,
		},
		Groups: raw.Groups,
	}
	if info, ok := raw.CMDBLevel.([]interface{}); ok {
		for _, value := range info {
			if level, ok := utils.NewFormatMapHelper(value); ok {
				bizID, _ := level.Get(define.RecordBizIDFieldName)
				record.Dimensions[define.RecordBizIDFieldName] = conv.String(bizID)
			}
		}
	}

	if raw.CMDBLevel != nil {
		level, err := etl.TransformJSON(raw.CMDBLevel)
		if err == nil {
			record.Dimensions[define.RecordCMDBLevelFieldName] = level
		} else {
			logging.Warnf("%v transform cmdb level %v error %v", p, level, err)
		}
	}

	payload, err := define.DerivePayload(d, record)
	if err == nil {
		outputChan <- payload
	} else {
		logging.Warnf("%v derive payload %#v error %v", p, d, err)
	}
	p.CounterSuccesses.Inc()
}

// NewGroupInjector :
func NewCMDBInjector(ctx context.Context, name string) (*CMDBLevelInjector, error) {
	return &CMDBLevelInjector{
		BaseDataProcessor: define.NewBaseDataProcessor(name),
		ProcessorMonitor:  pipeline.NewDataProcessorMonitor(name, config.PipelineConfigFromContext(ctx)),
	}, nil
}

func init() {
	define.RegisterDataProcessor("cmdb_injector", func(ctx context.Context, name string) (processor define.DataProcessor, e error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewCMDBInjector(ctx, pipeConfig.FormatName(name))
	})
}
