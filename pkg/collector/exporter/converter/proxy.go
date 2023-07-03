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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type proxyEvent struct {
	define.CommonEvent
}

func (e proxyEvent) RecordType() define.RecordType {
	return define.RecordProxy
}

type proxyMapper struct {
	pd *define.ProxyData
}

// AsMapStr 转换为 beat 框架要求的 MapStr 对象
func (p proxyMapper) AsMapStr() common.MapStr {
	now := time.Now().Unix()
	ms := common.MapStr{
		"dataid":    p.pd.DataId,
		"version":   p.pd.Version,
		"data":      p.pd.Data,
		"bk_info":   p.pd.Extra,
		"time":      now,
		"timestamp": now,
	}

	logger.Debugf("convert proxy data: %+v", ms)
	return ms
}

var ProxyConverter EventConverter = proxyConverter{}

type proxyConverter struct{}

func (c proxyConverter) ToEvent(dataId int32, data common.MapStr) define.Event {
	return proxyEvent{define.NewCommonEvent(dataId, data)}
}

func (c proxyConverter) ToDataID(_ *define.Record) int32 {
	return 0
}

func (c proxyConverter) Convert(record *define.Record, f define.GatherFunc) {
	pd, ok := record.Data.(*define.ProxyData)
	if !ok {
		return
	}

	pm := proxyMapper{pd: pd}
	f(c.ToEvent(int32(pd.DataId), pm.AsMapStr()))
}
