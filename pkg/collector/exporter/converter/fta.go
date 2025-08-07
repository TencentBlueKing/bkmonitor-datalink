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
)

type FtaEvent struct {
	define.CommonEvent
}

func (e FtaEvent) RecordType() define.RecordType {
	return define.RecordFta
}

var FtaConverter EventConverter = ftaConverter{}

type ftaConverter struct{}

func (c ftaConverter) Clean() {}

func (c ftaConverter) ToEvent(token define.Token, dataId int32, data common.MapStr) define.Event {
	return FtaEvent{define.NewCommonEvent(token, dataId, data)}
}

func (c ftaConverter) ToDataID(record *define.Record) int32 {
	return record.Token.MetricsDataId
}

func (c ftaConverter) Convert(record *define.Record, f define.GatherFunc) {
	dataId := c.ToDataID(record)
	data := record.Data.(*define.FtaData)
	events := []define.Event{c.ToEvent(record.Token, dataId, common.MapStr{
		"dataid":          dataId,
		"bk_data_id":      dataId,
		"bk_plugin_id":    data.PluginId,
		"bk_ingest_time":  data.IngestTime,
		"data":            data.Data,
		"__bk_event_id__": data.EventId,
	})}
	f(events...)
}
