// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

const (
	ConfSchedulerType               = "scheduler.type"
	ConfSchedulerCCBatchSize        = "scheduler.cc_batch_size"
	ConfSchedulerCCCacheExpires     = "scheduler.cc_cache_expires"
	ConfSchedulerCCCheckIntervalKey = "scheduler.cc_check_interval"
	ConfSchedulerCheckIntervalKey   = "scheduler.check_interval"
	ConfSchedulerFlowIntervalKey    = "scheduler.flow_interval"
	ConfSchedulerCleanUpDurationKey = "scheduler.clean_up_duration"
	ConfSchedulerPendingTimeoutKey  = "scheduler.pending_timeout"
	ConfSchedulerAppleLifeKey       = "scheduler.apple_life"
	ConfSchedulerPluginCCCache      = "scheduler.plugin.cc_cache"
	ConfSchedulerPluginHTTPServer   = "scheduler.plugin.http_server"
	ConfRequestTypeKey              = "scheduler.cc_cache_type"
)

func initConfiguration(c define.Configuration) {
	c.SetDefault(ConfSchedulerType, "watch")
	c.SetDefault(ConfSchedulerCCBatchSize, 100)
	c.SetDefault(ConfSchedulerCCCacheExpires, "1h")
	c.SetDefault(ConfSchedulerCCCheckIntervalKey, "10s")
	c.SetDefault(ConfSchedulerCheckIntervalKey, "1s")
	c.SetDefault(ConfSchedulerFlowIntervalKey, "60s")
	c.SetDefault(ConfSchedulerCleanUpDurationKey, "3s")
	c.SetDefault(ConfSchedulerPendingTimeoutKey, "30s")
	c.SetDefault(ConfSchedulerPluginHTTPServer, true)
	c.SetDefault(ConfSchedulerPluginCCCache, true)
	c.SetDefault(ConfRequestTypeKey, "host")
}

func init() {
	utils.CheckError(eventbus.Subscribe(eventbus.EvSysConfigPreParse, initConfiguration))
}
