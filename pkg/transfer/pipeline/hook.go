// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pipeline

import (
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

const (
	ConfKeyPipelineChannelSize        = "pipeline.channel_size"
	ConfKeyPipelineFrontendWaitDelay  = "pipeline.wait_delay"
	ConfKeyPayloadBufferSize          = "pipeline.backend.buffer_size"
	ConfKeyPayloadFlushInterval       = "pipeline.backend.flush_interval"
	ConfKeyPayloadFlushReties         = "pipeline.backend.flush_reties"
	ConfKeyPayloadFlushConcurrency    = "pipeline.backend.concurrency"
	ConfKeyPayloadFlushMaxConcurrency = "pipeline.backend.max_concurrency"

	ConfKeyPipeLineDefaultNums = "pipeline.processor.default_nums"
	ConfKeyPipeLineNums        = "pipeline.processor.nums"
)

var (
	pipelineNums        define.Configuration
	defaultPipelineNums = 1
)

// initPipeLineNumMap: 初始化pipeline processor并发数
func initPipeLineNums(conf define.Configuration) {
	pipelineNums = conf
}

// GetPipeLineNum:
func GetPipeLineNum(dataId int) int {
	key := fmt.Sprintf("%s.%d", ConfKeyPipeLineNums, dataId)
	if pipelineNums == nil {
		pipelineNums = config.Configuration
	}
	v := pipelineNums.GetInt(key)
	if v == 0 {
		return defaultPipelineNums
	}
	return v
}

func init() {
	utils.CheckError(eventbus.Subscribe(eventbus.EvSysConfigPreParse, func(conf define.Configuration) {
		conf.SetDefault(ConfKeyPipelineChannelSize, 10)
		conf.SetDefault(ConfKeyPipelineFrontendWaitDelay, "3s")
		conf.SetDefault(ConfKeyPayloadBufferSize, BulkDefaultBufferSize)
		conf.SetDefault(ConfKeyPayloadFlushInterval, BulkDefaultFlushInterval)
		conf.SetDefault(ConfKeyPayloadFlushReties, BulkDefaultFlushRetries)
		conf.SetDefault(ConfKeyPayloadFlushConcurrency, BulkDefaultConcurrency)
		conf.SetDefault(ConfKeyPayloadFlushMaxConcurrency, BulkDefaultMaxConcurrency)

		conf.SetDefault(ConfKeyPipeLineDefaultNums, 1)
	}))
	utils.CheckError(eventbus.Subscribe(eventbus.EvSysConfigPostParse, func(conf define.Configuration) {
		DefaultChannelBufferSize = conf.GetInt(ConfKeyPipelineChannelSize)
		DefaultFrontendWaitDelay = conf.GetDuration(ConfKeyPipelineFrontendWaitDelay)
		BulkDefaultBufferSize = conf.GetInt(ConfKeyPayloadBufferSize)
		BulkDefaultFlushInterval = conf.GetDuration(ConfKeyPayloadFlushInterval)
		BulkDefaultFlushRetries = conf.GetInt(ConfKeyPayloadFlushReties)
		BulkDefaultConcurrency = conf.GetInt64(ConfKeyPayloadFlushConcurrency)
		BulkDefaultMaxConcurrency = conf.GetInt64(ConfKeyPayloadFlushMaxConcurrency)

		BulkGlobalConcurrencySemaphore = utils.NewWeightedSemaphore(BulkDefaultMaxConcurrency)

		defaultPipelineNums = conf.GetInt(ConfKeyPipeLineDefaultNums)
		initPipeLineNums(conf)
	}))
}
