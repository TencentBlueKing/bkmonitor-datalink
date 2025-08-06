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

type beatEvent struct {
	define.CommonEvent
}

func (e beatEvent) RecordType() define.RecordType {
	return define.RecordBeat
}

var BeatConverter EventConverter = beatConverter{}

type beatConverter struct{}

func (c beatConverter) Clean() {}

func (c beatConverter) ToEvent(token define.Token, dataId int32, data common.MapStr) define.Event {
	return beatEvent{define.NewCommonEvent(token, dataId, data)}
}

func (c beatConverter) ToDataID(record *define.Record) int32 {
	return record.Token.BeatDataId
}

func (c beatConverter) Convert(record *define.Record, f define.GatherFunc) {
	dataID := c.ToDataID(record)
	data := record.Data.(*define.BeatData)
	dst := make(map[string]interface{})
	if err := json.Unmarshal(data.Data, &dst); err != nil {
		return
	}

	f(c.ToEvent(record.Token, dataID, dst))
}
