// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"time"

	"github.com/spf13/viper"
)

var (
	NotifierChanBufferSize                int
	WindowMaxSize                         int
	WindowExpireInterval                  time.Duration
	WindowMaxDuration                     time.Duration
	WindowExpireIntervalIncrement         int
	WindowNoDataMaxDuration               time.Duration
	DistributiveWindowSubSize             int
	DistributiveWindowWatchExpireInterval time.Duration

	DistributiveWindowConcurrentCount             int
	DistributiveWindowConcurrentExpirationMaximum int
	EnabledTraceInfoCache                         int
	StorageSaveRequestBufferSize                  int
	StorageWorkerCount                            int
	StorageSaveHoldMaxCount                       int
	StorageSaveHoldMaxDuration                    time.Duration
	StorageBloomFpRate                            float64
	StorageBloomNormalAutoClean                   int
	StorageBloomNormalOverlapResetDuration        time.Duration
	StorageBloomLayersBloomLayers                 int
	StorageBloomDecreaseCap                       int
	StorageBloomDecreaseLayers                    int
	StorageBloomDecreaseDivisor                   int

	MetricEnabled           bool
	MetricReportInterval    time.Duration
	ProfileEnabled          bool
	ProfileHost             string
	ProfileAppIdx           string
	MetricReportHost        string
	MetricReportDataId      int
	MetricReportAccessToken string
)

func initApmVariables() {
	NotifierChanBufferSize = GetValue("taskConfig.apmPreCalculate.notifier.chanBufferSize", 100000)

	WindowMaxSize = GetValue("taskConfig.apmPreCalculate.window.maxSize", 100*100)
	WindowExpireInterval = GetValue("taskConfig.apmPreCalculate.window.expireInterval", time.Second, viper.GetDuration)
	WindowMaxDuration = GetValue("taskConfig.apmPreCalculate.window.maxDuration", 5*time.Minute, viper.GetDuration)
	WindowExpireIntervalIncrement = GetValue("taskConfig.apmPreCalculate.window.expireIntervalIncrement", 60)
	WindowNoDataMaxDuration = GetValue("taskConfig.apmPreCalculate.window.noDataMaxDuration", 2*time.Minute, viper.GetDuration)

	DistributiveWindowSubSize = GetValue("taskConfig.apmPreCalculate.window.distributive.subSize", 10)
	DistributiveWindowWatchExpireInterval = GetValue("taskConfig.apmPreCalculate.window.distributive.watchExpireInterval", 100*time.Millisecond, viper.GetDuration)
	DistributiveWindowConcurrentCount = GetValue("taskConfig.apmPreCalculate.window.distributive.concurrentCount", 1000)
	DistributiveWindowConcurrentExpirationMaximum = GetValue("taskConfig.apmPreCalculate.window.distributive.concurrentExpirationMaximum", 100000)

	EnabledTraceInfoCache = GetValue("taskConfig.apmPreCalculate.processor.enabledTraceInfoCache", 0)

	StorageSaveRequestBufferSize = GetValue("taskConfig.apmPreCalculate.storage.saveRequestBufferSize", 100000)
	StorageWorkerCount = GetValue("taskConfig.apmPreCalculate.storage.workerCount", 10)
	StorageSaveHoldMaxCount = GetValue("taskConfig.apmPreCalculate.storage.saveHoldMaxCount", 1000)
	StorageSaveHoldMaxDuration = GetValue("taskConfig.apmPreCalculate.storage.saveHoldMaxDuration", 500*time.Millisecond, viper.GetDuration)

	StorageBloomFpRate = GetValue("taskConfig.apmPreCalculate.storage.bloom.fpRate", 0.01)
	StorageBloomNormalAutoClean = GetValue("taskConfig.apmPreCalculate.storage.bloom.normal.autoClean", 24*60)
	StorageBloomNormalOverlapResetDuration = GetValue("taskConfig.apmPreCalculate.storage.bloom.normalOverlap.resetDuration", 2*time.Hour, viper.GetDuration)
	StorageBloomLayersBloomLayers = GetValue("taskConfig.apmPreCalculate.storage.bloom.layersBloom.layers", 5)
	StorageBloomDecreaseCap = GetValue("taskConfig.apmPreCalculate.storage.bloom.decreaseBloom.cap", 100000000)
	StorageBloomDecreaseLayers = GetValue("taskConfig.apmPreCalculate.storage.bloom.decreaseBloom.layers", 10)
	StorageBloomDecreaseDivisor = GetValue("taskConfig.apmPreCalculate.storage.bloom.decreaseBloom.divisor", 2)

	/*
	   Metric Config
	*/
	MetricEnabled = GetValue("taskConfig.apmPreCalculate.metrics.timeSeries.enabled", false)
	MetricReportHost = GetValue("taskConfig.apmPreCalculate.metrics.timeSeries.host", "")
	MetricReportInterval = GetValue("taskConfig.apmPreCalculate.metrics.timeSeries.interval", time.Minute, viper.GetDuration)
	MetricReportDataId = GetValue("taskConfig.apmPreCalculate.metrics.timeSeries.dataId", 0)
	MetricReportAccessToken = GetValue("taskConfig.apmPreCalculate.metrics.timeSeries.accessToken", "")
	ProfileEnabled = GetValue("taskConfig.apmPreCalculate.metrics.profile.enabled", false)
	ProfileHost = GetValue("taskConfig.apmPreCalculate.metrics.profile.host", "")
	ProfileAppIdx = GetValue("taskConfig.apmPreCalculate.metrics.profile.appIdx", "")
}
