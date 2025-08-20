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
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

type pingserverEvent struct {
	define.CommonEvent
}

func (e pingserverEvent) RecordType() define.RecordType {
	return define.RecordPingserver
}

type pingserverMapper struct {
	pd *define.PingserverData
}

// AsMapStr 转换为 beat 框架要求的 MapStr 对象
func (p pingserverMapper) AsMapStr() common.MapStr {
	now := time.Now().Unix()
	return common.MapStr{
		"dataid":    p.pd.DataId,
		"version":   p.pd.Version,
		"data":      []map[string]interface{}{p.pd.Data},
		"time":      now,
		"timestamp": now,
	}
}

type pingserverConverter struct{}

func (c pingserverConverter) Clean() {}

func (c pingserverConverter) ToEvent(token define.Token, dataId int32, data common.MapStr) define.Event {
	return pingserverEvent{define.NewCommonEvent(token, dataId, data)}
}

func (c pingserverConverter) ToDataID(_ *define.Record) int32 {
	return 0
}

func (c pingserverConverter) Convert(record *define.Record, f define.GatherFunc) {
	pd := record.Data.(*define.PingserverData)
	pm := pingserverMapper{pd: pd}
	f(c.ToEvent(record.Token, int32(pd.DataId), pm.AsMapStr()))
}
