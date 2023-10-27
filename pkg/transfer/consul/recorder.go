// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
)

// RecordProcessor :
type RecordProcessor struct {
	*define.BaseDataProcessor
	client        SourceClient
	ctx           context.Context
	isFirstCalled bool
	samplingPath  string
	samplingTime  int64
	lastTime      int64
}

func (p *RecordProcessor) isRightTime() bool {
	if time.Now().Unix()-p.lastTime > p.samplingTime {
		p.lastTime = time.Now().Unix()
		return true
	}
	return false
}

func (p *RecordProcessor) isFitSampling() bool {
	if !p.isFirstCalled && !p.isRightTime() {
		return false
	}
	p.isFirstCalled = false
	return true
}

// Process : process json data
func (p *RecordProcessor) Process(d define.Payload, outputChan chan<- define.Payload, killChan chan<- error) {
	// whatever happened, must push payload to next output
	defer func() {
		outputChan <- d
	}()
	if !p.isFitSampling() {
		return
	}
	logging.Debugf("%v fit sampling condition", p)
	var item define.ETLRecord
	if err := d.To(&item); err != nil {
		logging.Errorf("payload %v to recorder failed: %v", d, err)
		MonitorWriteFailed.Inc()
		return
	}
	samplingRst := make([]*SamplingItem, 0)
	samplingRst = append(samplingRst, NewSamplingItem(define.MetaFieldTypeTimestamp, define.MetaFieldTagTime, "time", *item.Time))
	for k, v := range item.Dimensions {
		t := getType(v)
		if t != "" {
			samplingRst = append(samplingRst, NewSamplingItem(t, define.MetaFieldTagDimension, k, v))
		}
	}
	for k, v := range item.Metrics {
		t := getType(v)
		if t != "" {
			samplingRst = append(samplingRst, NewSamplingItem(t, define.MetaFieldTagMetric, k, v))
		}
	}
	logging.Debugf("sampling %v push %v to %v", p, d, p.samplingPath)
	samplVal, err := json.Marshal(samplingRst)
	if err != nil {
		logging.Errorf("%v marshal %v failed:%s", p, samplingRst, err)
		MonitorWriteFailed.Inc()
		return
	}
	if err := p.client.Put(p.samplingPath, samplVal); err != nil {
		logging.Errorf("%v put data to consul Root %s failed:%s", p, p.samplingPath, err)
		MonitorWriteFailed.Inc()
		return
	}
	MonitorWriteSuccess.Inc()
	logging.Debugf("%v sampling report %v success", p, d)
}

func getType(v interface{}) define.MetaFieldType {
	switch v.(type) {
	case bool:
		return define.MetaFieldTypeBool
	case float32, float64:
		return define.MetaFieldTypeFloat
	case int, int32, int64:
		return define.MetaFieldTypeInt
	case uint, uint32, uint64:
		return define.MetaFieldTypeUint
	case string, []byte:
		return define.MetaFieldTypeString
	case time.Time:
		return define.MetaFieldTypeTimestamp
	default:
		return define.MetaFieldTypeObject
	}
}

// NewConsulProcessor :
func NewConsulProcessor(ctx context.Context, name string) (*RecordProcessor, error) {
	client, err := NewConsulClient(ctx)
	if err != nil {
		return nil, err
	}
	conf := config.FromContext(ctx)
	rt := config.ResultTableConfigFromContext(ctx)
	subPath := GetPathByKey(conf, ConfKeySamplingDataSubPath)
	return &RecordProcessor{
		ctx:               ctx,
		BaseDataProcessor: define.NewBaseDataProcessor(name),
		lastTime:          time.Now().Unix(),
		isFirstCalled:     true,
		client:            client,
		samplingPath:      fmt.Sprintf("%s/%s/fields", subPath, rt.ResultTable),
		samplingTime:      int64(conf.GetDuration(ConfKeySamplingInterval).Seconds()),
	}, nil
}

func init() {
	define.RegisterDataProcessor("sampling_reporter", func(ctx context.Context, name string) (define.DataProcessor, error) {
		pipe := config.PipelineConfigFromContext(ctx)
		if pipe == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		rt := config.ResultTableConfigFromContext(ctx)
		if rt == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "result table is empty")
		}
		if config.FromContext(ctx) == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "config is empty")
		}
		return NewConsulProcessor(ctx, pipe.FormatName(rt.FormatName(name)))
	})
}
