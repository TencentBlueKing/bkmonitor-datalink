// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

var (
	NotifierChanBufferSize                int
	WindowMaxSize                         int
	WindowExpireInterval                  int
	WindowMaxDuration                     int
	WindowExpireIntervalIncrement         int
	WindowNoDataMaxDuration               int
	DistributiveWindowSubSize             int
	DistributiveWindowWatchExpireInterval int

	DistributiveWindowConcurrentCount             int
	DistributiveWindowConcurrentExpirationMaximum int
	EnabledTraceInfoCache                         int
	TraceMetaBloomCutLength                       int
	StorageSaveRequestBufferSize                  int
	StorageWorkerCount                            int
	StorageSaveHoldMaxCount                       int
	StorageSaveHoldMaxDuration                    int
	StorageBloomFpRate                            float64
	StorageBloomAutoClean                         int

	MetricEnabled                       bool
	MetricReportInterval                int
	ProfileEnabled                      bool
	ProfileHost                         string
	MetricReportHost                    string
	SaveRequestCountMetricDataId        int
	SaveRequestCountMetricAccessToken   string
	MessageCountMetricDataId            int
	MessageCountMetricAccessToken       string
	WindowTraceCountMetricDataId        int
	WindowTraceCountMetricAccessToken   string
	WindowSpanCountMetricDataId         int
	WindowSpanCountMetricAccessToken    string
	EsOriginTraceCountMetricDataId      int
	EsOriginTraceCountMetricAccessToken string
	EsPreCalTraceCountMetricDataId      int
	EsPreCalTraceCountMetricAccessToken string
)

func initApmVariables() {
	NotifierChanBufferSize = GetValue("taskConfig.apmPreCalculate.notifier.chanBufferSize", 100000)

	WindowMaxSize = GetValue("taskConfig.apmPreCalculate.window.maxSize", 100*100)
	WindowExpireInterval = GetValue("taskConfig.apmPreCalculate.window.expireInterval", 60)
	WindowMaxDuration = GetValue("taskConfig.apmPreCalculate.window.maxDuration", 60*5)
	WindowExpireIntervalIncrement = GetValue("taskConfig.apmPreCalculate.window.expireIntervalIncrement", 60)
	WindowNoDataMaxDuration = GetValue("taskConfig.apmPreCalculate.window.noDataMaxDuration", 120)

	DistributiveWindowSubSize = GetValue("taskConfig.apmPreCalculate.window.distributive.subSize", 10)
	DistributiveWindowWatchExpireInterval = GetValue("taskConfig.apmPreCalculate.window.distributive.watchExpireInterval", 100)
	DistributiveWindowConcurrentCount = GetValue("taskConfig.apmPreCalculate.window.distributive.concurrentCount", 1000)
	DistributiveWindowConcurrentExpirationMaximum = GetValue("taskConfig.apmPreCalculate.window.distributive.concurrentExpirationMaximum", 100000)

	EnabledTraceInfoCache = GetValue("taskConfig.apmPreCalculate.processor.enabledTraceInfoCache", 0)
	TraceMetaBloomCutLength = GetValue("taskConfig.apmPreCalculate.processor.traceMetaBloomCutLength", 16)

	StorageSaveRequestBufferSize = GetValue("taskConfig.apmPreCalculate.storage.saveRequestBufferSize", 100000)
	StorageWorkerCount = GetValue("taskConfig.apmPreCalculate.storage.workerCount", 10)
	StorageSaveHoldMaxCount = GetValue("taskConfig.apmPreCalculate.storage.saveHoldMaxCount", 1000)
	StorageSaveHoldMaxDuration = GetValue("taskConfig.apmPreCalculate.storage.saveHoldMaxDuration", 500)
	StorageBloomFpRate = GetValue("taskConfig.apmPreCalculate.storage.bloom.fpRate", 0.01)
	StorageBloomAutoClean = GetValue("taskConfig.apmPreCalculate.storage.bloom.autoClean", 30)

	/*
	   Metric Config
	*/
	MetricEnabled = GetValue("taskConfig.apmPreCalculate.metrics.enabled", false)
	MetricReportInterval = GetValue("taskConfig.apmPreCalculate.metrics.reportInterval", 1000)
	ProfileEnabled = GetValue("taskConfig.apmPreCalculate.metrics.profile.enabled", false)
	ProfileHost = GetValue("taskConfig.apmPreCalculate.metrics.profile.host", "")
	MetricReportHost = GetValue("taskConfig.apmPreCalculate.metrics.reportHost", "")
	SaveRequestCountMetricDataId = GetValue("taskConfig.apmPreCalculate.metrics.saveRequestChanCount.dataId", 0)
	SaveRequestCountMetricAccessToken = GetValue("taskConfig.apmPreCalculate.metrics.saveRequestChanCount.accessToken", "")

	MessageCountMetricDataId = GetValue("taskConfig.apmPreCalculate.metrics.messageChanCount.dataId", 0)
	MessageCountMetricAccessToken = GetValue("taskConfig.apmPreCalculate.metrics.messageChanCount.accessToken", "")

	WindowTraceCountMetricDataId = GetValue("taskConfig.apmPreCalculate.metrics.windowTraceCount.dataId", 0)
	WindowTraceCountMetricAccessToken = GetValue("taskConfig.apmPreCalculate.metrics.windowTraceCount.accessToken", "")

	WindowSpanCountMetricDataId = GetValue("taskConfig.apmPreCalculate.metrics.windowSpanCount.dataId", 0)
	WindowSpanCountMetricAccessToken = GetValue("taskConfig.apmPreCalculate.metrics.windowSpanCount.accessToken", "")

	EsOriginTraceCountMetricDataId = GetValue("taskConfig.apmPreCalculate.metrics.esOriginTraceCount.dataId", 0)
	EsOriginTraceCountMetricAccessToken = GetValue("taskConfig.apmPreCalculate.metrics.esOriginTraceCount.accessToken", "")
	EsPreCalTraceCountMetricDataId = GetValue("taskConfig.apmPreCalculate.metrics.esPreCalTraceCount.dataId", 0)
	EsPreCalTraceCountMetricAccessToken = GetValue("taskConfig.apmPreCalculate.metrics.esPreCalTraceCount.accessToken", "")

}
