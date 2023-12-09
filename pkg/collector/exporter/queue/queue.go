// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package queue

import (
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

// Queue 是发送队列的定义
type Queue interface {
	// Put 推送数据到队列中 调用者须保证同一批次的 Event 的 RecordType/DataID 是相同的
	Put(events ...define.Event)

	// Pop 弹出转换后的 MapStr 数据
	Pop() <-chan common.MapStr

	// Close 队列清理并关闭
	Close()
}

// NewEventsMapStr 代表着事件类型数据
func NewEventsMapStr(dataId int32, data []common.MapStr) common.MapStr {
	now := time.Now()
	for i, item := range data {
		_, _ = item.Put("iterationindex", i)
	}
	return common.MapStr{
		"datetime": now.Format("2006-01-02 15:04:05"),
		"utctime":  now.UTC().Format("2006-01-02 15:04:05"),
		"time":     now.Unix(),
		"dataid":   dataId,
		"items":    data,
	}
}

// NewMetricsMapStr 代表着时序类型数据
func NewMetricsMapStr(dataId int32, data []common.MapStr) common.MapStr {
	return common.MapStr{
		"dataid":  dataId,
		"version": "1.0.0",
		"data":    data,
	}
}

// NewProfilesMapStr 代表着性能分析类型数据
func NewProfilesMapStr(dataId int32, data []common.MapStr) common.MapStr {
	return common.MapStr{
		"dataid": dataId,
		"data":   data,
	}
}
