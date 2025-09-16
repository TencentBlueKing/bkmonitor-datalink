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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/maps"
)

type metricV2Event struct {
	define.CommonEvent
}

func (e metricV2Event) RecordType() define.RecordType {
	return define.RecordMetricV2
}

type metricV2Converter struct{}

func (c metricV2Converter) Clean() {}

func (c metricV2Converter) ToEvent(token define.Token, dataId int32, data common.MapStr) define.Event {
	return metricV2Event{define.NewCommonEvent(token, dataId, data)}
}

func (c metricV2Converter) ToDataID(_ *define.Record) int32 {
	return 0
}

func (c metricV2Converter) Convert(record *define.Record, f define.GatherFunc) {
	data := record.Data.(*define.MetricV2Data)
	var events []define.Event
	for _, item := range data.Data {
		target := item.Target
		if target == "" {
			var ok bool
			if target, ok = item.Dimension["target"]; !ok {
				target = define.Identity()
			}
		}
		event := c.ToEvent(record.Token, record.Token.MetricsDataId, common.MapStr{
			"metrics":   item.Metrics,
			"target":    target,
			"timestamp": item.Timestamp,
			"dimension": maps.MergeReplaceWith(item.Dimension),
		})
		events = append(events, event)
	}

	if len(events) > 0 {
		f(events...)
	}
}
