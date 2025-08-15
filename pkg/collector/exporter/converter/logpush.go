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

type logPushConverter struct{}

func (c logPushConverter) Clean() {}

func (c logPushConverter) ToEvent(token define.Token, dataId int32, data common.MapStr) define.Event {
	return logsEvent{define.NewCommonEvent(token, dataId, data)}
}

func (c logPushConverter) ToDataID(record *define.Record) int32 {
	return record.Token.LogsDataId
}

func (c logPushConverter) Convert(record *define.Record, f define.GatherFunc) {
	lpData := record.Data.(*define.LogPushData)
	data := lpData.Data
	if len(data) == 0 {
		return
	}

	dataId := c.ToDataID(record)
	events := make([]define.Event, 0, len(data))
	for i := 0; i < len(data); i++ {
		events = append(events, c.ToEvent(record.Token, dataId, c.Extract(data[i], lpData.Labels)))
	}
	f(events...)
}

func (c logPushConverter) Extract(data string, lbs map[string]string) common.MapStr {
	if lbs == nil {
		lbs = make(map[string]string)
	}
	return common.MapStr{
		"data": data,
		"ext":  lbs,
	}
}
