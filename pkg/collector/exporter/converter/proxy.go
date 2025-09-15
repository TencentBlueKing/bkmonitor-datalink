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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/json"
)

type proxyEvent struct {
	define.CommonEvent
}

func (e proxyEvent) RecordType() define.RecordType {
	return define.RecordProxy
}

type proxyConverter struct{}

func (c proxyConverter) Clean() {}

func (c proxyConverter) ToEvent(token define.Token, dataId int32, data common.MapStr) define.Event {
	return proxyEvent{define.NewCommonEvent(token, dataId, data)}
}

func (c proxyConverter) ToDataID(_ *define.Record) int32 {
	return 0
}

func (c proxyConverter) Convert(record *define.Record, f define.GatherFunc) {
	pd := record.Data.(*define.ProxyData)
	var events []define.Event

	if pd.Type == define.ProxyMetricType {
		events = c.toMetrics(record.Token, pd)
	} else {
		events = c.toEvents(record.Token, pd)
	}

	if len(events) > 0 {
		f(events...)
	}
}

func (c proxyConverter) toMetrics(token define.Token, pd *define.ProxyData) []define.Event {
	var events []define.Event
	var items []define.MetricV2

	// 使用 json 序列化再反序列化目前是最快的方式 参见 benchmark
	b, err := json.Marshal(pd.Data)
	if err != nil {
		return nil
	}
	err = json.Unmarshal(b, &items)
	if err != nil {
		return nil
	}

	for _, item := range items {
		event := c.ToEvent(token, int32(pd.DataId), common.MapStr{
			"metrics":   item.Metrics,
			"target":    item.Target,
			"timestamp": item.Timestamp,
			"dimension": item.Dimension,
		})
		events = append(events, event)
	}
	return events
}

func (c proxyConverter) toEvents(token define.Token, pd *define.ProxyData) []define.Event {
	var events []define.Event
	var items []define.EventV2

	// 使用 json 序列化再反序列化目前是最快的方式 参见 benchmark
	b, err := json.Marshal(pd.Data)
	if err != nil {
		return nil
	}
	err = json.Unmarshal(b, &items)
	if err != nil {
		return nil
	}

	for _, item := range items {
		event := c.ToEvent(token, int32(pd.DataId), common.MapStr{
			"event_name": item.EventName,
			"event":      item.Event,
			"target":     item.Target,
			"dimension":  item.Dimension,
			"timestamp":  item.Timestamp,
		})
		events = append(events, event)
	}
	return events
}
