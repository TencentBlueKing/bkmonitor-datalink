// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package converter

import (
	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define/prompb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
)

type remoteWriteEvent struct {
	define.CommonEvent
}

func (e remoteWriteEvent) RecordType() define.RecordType {
	return define.RecordRemoteWrite
}

var RemoteWriteConverter EventConverter = remoteWriteConverter{}

type remoteWriteConverter struct{}

func (c remoteWriteConverter) ToEvent(dataId int32, data common.MapStr) define.Event {
	return remoteWriteEvent{define.NewCommonEvent(dataId, data)}
}

func (c remoteWriteConverter) ToDataID(record *define.Record) int32 {
	return record.Token.MetricsDataId
}

func (c remoteWriteConverter) Convert(record *define.Record, f define.GatherFunc) {
	rwData, ok := record.Data.(*define.RemoteWriteData)
	if !ok {
		return
	}

	dataId := c.ToDataID(record)
	events := make([]define.Event, 0)
	for i := 0; i < len(rwData.Timeseries); i++ {
		ts := rwData.Timeseries[i]
		name, dims := c.extractNameDimensions(ts.GetLabels())
		samples := ts.GetSamples()
		for j := 0; j < len(samples); j++ {
			sample := samples[j]
			if !utils.IsValidFloat64(sample.GetValue()) {
				DefaultMetricMonitor.IncConverterFailedCounter(define.RecordRemoteWrite, dataId)
				continue
			}

			pm := promMapper{
				Metrics:    common.MapStr{name: sample.GetValue()},
				Target:     "remote_write",
				Timestamp:  sample.GetTimestampMs(),
				Dimensions: utils.CloneMap(dims),
			}
			events = append(events, c.ToEvent(dataId, pm.AsMapStr()))
		}
	}
	if len(events) > 0 {
		f(events...)
	}
}

func (c remoteWriteConverter) extractNameDimensions(labels []*prompb.LabelPair) (string, map[string]string) {
	dims := make(map[string]string)
	var name string
	for _, label := range labels {
		if label.GetName() == "__name__" {
			name = label.GetValue()
			continue
		}
		dims[label.GetName()] = label.GetValue()
	}
	return name, dims
}
