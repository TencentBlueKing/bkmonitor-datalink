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
	// NotifierChanBufferSize queue chan size
	NotifierChanBufferSize int
	// NotifierMessageQps Qps of queue
	NotifierMessageQps int
	// WindowMaxSize The maximum amount that a single trace can handle,
	// beyond which the window will be forced to expire.
	WindowMaxSize int
	// WindowExpireInterval The single expiration time of a single trace, which is increased with each reentry.
	WindowExpireInterval time.Duration
	// WindowMaxDuration The maximum time that a single trace can survive in a window,
	// beyond which the window will be forced to expire.
	WindowMaxDuration time.Duration
	// WindowExpireIntervalIncrement The increment of expiration time when span continues to add to the window.
	// When this increment is increased beyond the WindowMaxDuration,
	// the window expiration time will be changed to WindowMaxDuration.
	WindowExpireIntervalIncrement int
	// WindowNoDataMaxDuration The maximum duration without data received.
	// If the last update of trace exceeds this range, it will be forced to expire.
	// This field should be smaller than maxDuration
	WindowNoDataMaxDuration time.Duration
	// DistributiveWindowSubSize The number of sub-windows, each of which maintains its own data.
	DistributiveWindowSubSize int
	// DistributiveWindowWatchExpireInterval unit: ms. The duration of check expiration trace in window.
	// If value is too small, the concurrent performance may be affected
	DistributiveWindowWatchExpireInterval time.Duration
	// DistributiveWindowHandleEventConcurrentCount Set the number of goroutine that processes event concurrently
	// in each sub-window
	DistributiveWindowHandleEventConcurrentCount int
	// DistributiveWindowConcurrentExpirationMaximum Maximum number of concurrent expirations
	DistributiveWindowConcurrentExpirationMaximum int
	// DistributiveWindowSubWindowMappingMaxSpanCount Maximum number of span in mapping
	DistributiveWindowSubWindowMappingMaxSpanCount int

	// EnabledTraceInfoCache Whether to enable Storing the latest trace data into cache.
	// If this is enabled, the query frequency of elasticsearch is reduced.
	EnabledTraceInfoCache int
	// EnabledTraceMetricsReport enabled report metric
	EnabledTraceMetricsReport bool
	// EnabledTraceInfoReport enabled report info
	EnabledTraceInfoReport bool
	// TraceEsQueryRate To prevent too many es queries caused by bloom-filter,
	// each dataId needs to set a threshold for the maximum number of requests in a minute. default is 20
	TraceEsQueryRate int
	// StorageSaveRequestBufferSize Number of storage chan
	StorageSaveRequestBufferSize int
	// StorageWorkerCount The number of concurrent storage requests accepted simultaneously
	StorageWorkerCount int
	// StorageSaveHoldMaxCount Storage does not process the SaveRequest immediately upon receipt,
	// it waits for the conditions(SaveHoldDuration + SaveHoldMaxCount).
	// Condition 2: If the request count > SaveHoldMaxCount, it will be executed
	StorageSaveHoldMaxCount int
	// StorageSaveHoldMaxDuration
	// Storage does not process the SaveRequest immediately upon receipt,
	// it waits for the conditions(SaveHoldDuration + SaveHoldMaxCount).
	// Condition 1: If the wait time > SaveHoldDuration, it will be executed
	StorageSaveHoldMaxDuration time.Duration
	// StorageBloomFpRate fpRate of bloom-filter, this configuration is common to all types of bloom-filters
	StorageBloomFpRate float64
	// StorageBloomNormalAutoClean Automatic filter clearing time.
	// Data will be clear every time.Duration to avoid excessive memory usage.
	// Specific config of storage.MemoryBloom
	StorageBloomNormalAutoClean time.Duration
	// StorageBloomNormalOverlapResetDuration Configure the occurrence interval of overlapping filters.
	// For example, if set 2 * time.Hour,
	// a post-chain instance is created whenever 1 hour(2h / 2) is reached.
	// When 2h are up, the post-chain instance is moved forward to clear data.
	StorageBloomNormalOverlapResetDuration time.Duration
	// StorageBloomLayersBloomLayers is the number of layers of the multilayer filter.
	StorageBloomLayersBloomLayers int
	// StorageBloomDecreaseCap The initial capacity of the overlap-decrement filter,
	// and the capacity of each layer will decrease by StorageBloomDecreaseDivisor.
	// For example, StorageBloomDecreaseCap=100, StorageBloomDecreaseDivisor=2, StorageBloomDecreaseLayers=3,
	// then the first layer is 100 capacity, the second layer is 50, and the third layer is 25.
	StorageBloomDecreaseCap int
	// StorageBloomDecreaseLayers The number of layer of the overlap-decrement filter.
	StorageBloomDecreaseLayers int
	// StorageBloomDecreaseDivisor The divisor of the overlap-decrement filter.
	StorageBloomDecreaseDivisor int

	// ProfileEnabled Whether to enable indicator reporting.
	ProfileEnabled bool
	// ProfileHost profile report host
	ProfileHost string
	// ProfileToken profile report token
	ProfileToken string
	// ProfileAppIdx app name of profile
	ProfileAppIdx string
	// SemaphoreReportInterval time interval for reporting chan amount at the current time
	SemaphoreReportInterval time.Duration

	// PromRemoteWriteUrl remote write target url
	PromRemoteWriteUrl string
	// PromRemoteWriteHeaders remote write headers of http request
	PromRemoteWriteHeaders map[string]string
	// RelationMetricsInMemDuration duration of relation-metrics in memory
	RelationMetricsInMemDuration time.Duration
	// FlowMetricsInMemDuration duration of flow-metrics in memory
	FlowMetricsInMemDuration time.Duration
	// MetricsProcessLayer4ExportEnabled enabled layer-4 metrics indicators (include ip. )
	MetricsProcessLayer4ExportEnabled bool
	// MetricsDurationBuckets buckets of flow duration metric (unit: s)
	MetricsDurationBuckets []float64

	// HashSecret secret for hash
	HashSecret string
)

// DurationBuckets unit: s (10 ms -> 5s)
// 未来需要由用户确定 目前暂时固定
var DurationBuckets = []float64{0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 1.25, 1.5, 1.75, 2, 2.5, 5}

func initApmVariables() {
	NotifierChanBufferSize = GetValue("taskConfig.apmPreCalculate.notifier.chanBufferSize", 1000)
	NotifierMessageQps = GetValue("taskConfig.apmPreCalculate.notifier.qps", 1000)

	WindowMaxSize = GetValue("taskConfig.apmPreCalculate.window.maxSize", 100*100)
	WindowExpireInterval = GetValue("taskConfig.apmPreCalculate.window.expireInterval", time.Minute, viper.GetDuration)
	WindowMaxDuration = GetValue("taskConfig.apmPreCalculate.window.maxDuration", 5*time.Minute, viper.GetDuration)
	WindowExpireIntervalIncrement = GetValue("taskConfig.apmPreCalculate.window.expireIntervalIncrement", 60)
	WindowNoDataMaxDuration = GetValue("taskConfig.apmPreCalculate.window.noDataMaxDuration", 2*time.Minute, viper.GetDuration)

	DistributiveWindowSubSize = GetValue("taskConfig.apmPreCalculate.window.distributive.subSize", 3)
	DistributiveWindowWatchExpireInterval = GetValue("taskConfig.apmPreCalculate.window.distributive.watchExpireInterval", 500*time.Millisecond, viper.GetDuration)
	DistributiveWindowHandleEventConcurrentCount = GetValue("taskConfig.apmPreCalculate.window.distributive.concurrentHandleCount", 50)
	DistributiveWindowConcurrentExpirationMaximum = GetValue("taskConfig.apmPreCalculate.window.distributive.concurrentExpirationMaximum", 1000)
	DistributiveWindowSubWindowMappingMaxSpanCount = GetValue("taskConfig.apmPreCalculate.window.distributive.mappingMaxSpanCount", 100000)

	EnabledTraceInfoCache = GetValue("taskConfig.apmPreCalculate.processor.enabledTraceInfoCache", 0)
	EnabledTraceMetricsReport = GetValue("taskConfig.apmPreCalculate.processor.enabledTraceMetricsReport", true)
	EnabledTraceInfoReport = GetValue("taskConfig.apmPreCalculate.processor.enabledTraceInfoReport", true)

	TraceEsQueryRate = GetValue("taskConfig.apmPreCalculate.processor.traceEsQueryRate", 20)
	StorageSaveRequestBufferSize = GetValue("taskConfig.apmPreCalculate.storage.saveRequestBufferSize", 1000)
	StorageWorkerCount = GetValue("taskConfig.apmPreCalculate.storage.workerCount", 10)
	StorageSaveHoldMaxCount = GetValue("taskConfig.apmPreCalculate.storage.saveHoldMaxCount", 30)
	StorageSaveHoldMaxDuration = GetValue("taskConfig.apmPreCalculate.storage.saveHoldMaxDuration", 1*time.Second, viper.GetDuration)

	StorageBloomFpRate = GetValue("taskConfig.apmPreCalculate.storage.bloom.fpRate", 0.01)
	StorageBloomNormalAutoClean = GetValue("taskConfig.apmPreCalculate.storage.bloom.normal.autoClean", 24*time.Hour, viper.GetDuration)
	StorageBloomNormalOverlapResetDuration = GetValue("taskConfig.apmPreCalculate.storage.bloom.normalOverlap.resetDuration", 2*time.Hour, viper.GetDuration)
	StorageBloomLayersBloomLayers = GetValue("taskConfig.apmPreCalculate.storage.bloom.layersBloom.layers", 5)
	StorageBloomDecreaseCap = GetValue("taskConfig.apmPreCalculate.storage.bloom.decreaseBloom.cap", 10000000)
	StorageBloomDecreaseLayers = GetValue("taskConfig.apmPreCalculate.storage.bloom.decreaseBloom.layers", 5)
	StorageBloomDecreaseDivisor = GetValue("taskConfig.apmPreCalculate.storage.bloom.decreaseBloom.divisor", 2)

	/*
	   Profile Config
	*/
	ProfileEnabled = GetValue("taskConfig.apmPreCalculate.metrics.profile.enabled", false)
	ProfileHost = GetValue("taskConfig.apmPreCalculate.metrics.profile.host", "")
	ProfileToken = GetValue("taskConfig.apmPreCalculate.metrics.profile.token", "")
	ProfileAppIdx = GetValue("taskConfig.apmPreCalculate.metrics.profile.appIdx", "")
	/*
	   Metric Config
	*/
	RelationMetricsInMemDuration = GetValue("taskConfig.apmPreCalculate.metrics.relationMetric.duration", 10*time.Minute, viper.GetDuration)
	FlowMetricsInMemDuration = GetValue("taskConfig.apmPreCalculate.metrics.flowMetric.duration", 1*time.Minute, viper.GetDuration)
	MetricsDurationBuckets = GetValue("taskConfig.apmPreCalculate.metrics.flowMetric.buckets", DurationBuckets, GetFloatSlice)
	MetricsProcessLayer4ExportEnabled = GetValue("taskConfig.apmPreCalculate.metrics.enabledLayer4", false)

	SemaphoreReportInterval = GetValue("taskConfig.apmPreCalculate.metrics.report.semaphoreReportInterval", 5*time.Second, viper.GetDuration)
	PromRemoteWriteUrl = GetValue("taskConfig.apmPreCalculate.metrics.report.prometheus.url", "")
	PromRemoteWriteHeaders = GetValue("taskConfig.apmPreCalculate.metrics.report.prometheus.headers", map[string]string{}, viper.GetStringMapString)

	HashSecret = GetValue("taskConfig.apmPreCalculate.hashSecret", "")
}
