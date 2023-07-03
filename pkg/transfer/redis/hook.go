// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redis

import (
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

const (
	// PayloadBufferSize : 允许写入的最大队列长度
	PayloadRedisBufferSize = "redis.backend.buffer_size"
	// PayloadRedisFlushInterval : 触发刷写入间隔
	PayloadRedisFlushInterval = "redis.backend.flush_interval"
	// PayloadRedisFlushRetries : 重试次数
	PayloadRedisFlushRetries = "redis.backend.flush_retries"
	// BatchSize  : 每次批次最大值
	PayloadRedisBatchSize = "redis.backend.batch.size"
)

// InitConfiguration :
func InitConfiguration(c define.Configuration) {
	c.SetDefault(PayloadRedisBufferSize, 1000)
	c.SetDefault(PayloadRedisFlushInterval, 100*time.Millisecond)
	c.SetDefault(PayloadRedisFlushRetries, 10)
	c.SetDefault(PayloadRedisBatchSize, 10)
}

func init() {
	utils.CheckError(eventbus.Subscribe(eventbus.EvSysConfigPreParse, InitConfiguration))
}
