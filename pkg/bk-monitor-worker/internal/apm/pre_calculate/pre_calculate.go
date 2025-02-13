// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pre_calculate

import (
	"context"

	"go.uber.org/zap"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/notifier"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/window"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/remote"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func Initial(parentCtx context.Context) (PreCalculateProcessor, error) {
	ctx, cancel := context.WithCancel(parentCtx)
	return NewPrecalculate().
		WithContext(ctx, cancel).
		WithNotifierConfig(
			notifier.Options{
				ChanBufferSize: config.NotifierChanBufferSize,
				Qps:            config.NotifierMessageQps,
			},
		).
		WithWindowRuntimeConfig(
			window.RuntimeConfig{
				MaxSize:                 config.WindowMaxSize,
				ExpireInterval:          config.WindowExpireInterval,
				MaxDuration:             config.WindowMaxDuration,
				ExpireIntervalIncrement: config.WindowExpireIntervalIncrement,
				NoDataMaxDuration:       config.WindowNoDataMaxDuration,
			},
		).
		WithDistributiveWindowConfig(
			window.DistributiveWindowOptions{
				SubSize:                     config.DistributiveWindowSubSize,
				WatchExpiredInterval:        config.DistributiveWindowWatchExpireInterval,
				ConcurrentHandleCount:       config.DistributiveWindowHandleEventConcurrentCount,
				ConcurrentExpirationMaximum: config.DistributiveWindowConcurrentExpirationMaximum,
				MappingMaxSpanCount:         config.DistributiveWindowSubWindowMappingMaxSpanCount,
			},
		).
		WithProcessorConfig(
			window.ProcessorOptions{
				EnabledTraceInfoCache:     config.EnabledTraceInfoCache != 0,
				TraceEsQueryRate:          config.TraceEsQueryRate,
				EnabledTraceMetricsReport: config.EnabledTraceMetricsReport,
				EnabledTraceInfoReport:    config.EnabledTraceInfoReport,
				EnabledLayer4MetricReport: config.MetricsProcessLayer4ExportEnabled,
			},
		).
		WithStorageConfig(
			storage.ProxyOptions{
				SaveRequestBufferSize: config.StorageSaveRequestBufferSize,
				WorkerCount:           config.StorageWorkerCount,
				SaveHoldMaxDuration:   config.StorageSaveHoldMaxDuration,
				SaveHoldMaxCount:      config.StorageSaveHoldMaxCount,
				CacheBackend:          storage.CacheTypeRedis,
				RedisCacheConfig: storage.RedisCacheOptions{
					Mode:             config.StorageRedisMode,
					Host:             config.StorageRedisStandaloneHost,
					Port:             config.StorageRedisStandalonePort,
					SentinelAddress:  config.StorageRedisSentinelAddress,
					MasterName:       config.StorageRedisSentinelMasterName,
					SentinelPassword: config.StorageRedisSentinelPassword,
					Password:         config.StorageRedisStandalonePassword,
					Db:               config.StorageRedisDatabase,
					DialTimeout:      config.StorageRedisDialTimeout,
					ReadTimeout:      config.StorageRedisReadTimeout,
				},
				BloomConfig: storage.BloomOptions{
					FpRate: config.StorageBloomFpRate,
					NormalMemoryBloomOptions: storage.MemoryBloomOptions{
						AutoClean: config.StorageBloomNormalAutoClean,
					},
					NormalOverlapBloomOptions: storage.OverlapBloomOptions{
						ResetDuration: config.StorageBloomNormalOverlapResetDuration,
					},
					LayersBloomOptions: storage.LayersBloomOptions{
						Layers: config.StorageBloomLayersBloomLayers,
					},
					LayersCapDecreaseBloomOptions: storage.LayersCapDecreaseBloomOptions{
						Cap:     config.StorageBloomDecreaseCap,
						Layers:  config.StorageBloomDecreaseLayers,
						Divisor: config.StorageBloomDecreaseDivisor,
					},
				},
				MetricsConfig: storage.MetricConfigOptions{
					RelationMetricMemDuration: config.RelationMetricsInMemDuration,
					FlowMetricMemDuration:     config.FlowMetricsInMemDuration,
					FlowMetricBuckets:         storage.ConvertMetricFlowBuckets(config.MetricsDurationBuckets),
				},
				PrometheusWriterConfig: remote.PrometheusWriterOptions{
					Url:     config.PromRemoteWriteUrl,
					Headers: config.PromRemoteWriteHeaders,
				},
			},
		).
		WithMetricReport(
			SidecarOptions{
				EnabledProfile:        config.ProfileEnabled,
				ProfileAddress:        config.ProfileHost,
				ProfileToken:          config.ProfileToken,
				ProfileAppIdx:         config.ProfileAppIdx,
				MetricsReportInterval: config.SemaphoreReportInterval,
			},
		).
		Build(), nil
}

var apmLogger = logger.With(zap.String("package", "apm_precalculate"))
