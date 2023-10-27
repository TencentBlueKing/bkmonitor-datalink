// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// InitConfiguration :
func InitConfiguration(c define.Configuration) {
	c.RegisterAlias("influxdb.backend.channel_size", pipeline.ConfKeyPipelineChannelSize)
	c.RegisterAlias("influxdb.backend.wait_delay", pipeline.ConfKeyPipelineFrontendWaitDelay)
	c.RegisterAlias("influxdb.backend.buffer_size", pipeline.ConfKeyPayloadBufferSize)
	c.RegisterAlias("influxdb.backend.flush_interval", pipeline.ConfKeyPayloadFlushInterval)
	c.RegisterAlias("influxdb.backend.flush_reties", pipeline.ConfKeyPayloadFlushReties)
	c.RegisterAlias("influxdb.backend.max_concurrency", pipeline.ConfKeyPayloadFlushConcurrency)
}

func init() {
	utils.CheckError(eventbus.Subscribe(eventbus.EvSysConfigPreParse, InitConfiguration))
}
