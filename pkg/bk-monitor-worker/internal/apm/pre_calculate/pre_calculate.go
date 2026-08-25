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
	redisStore "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/remote"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

func Initial(parentCtx context.Context) (PreCalculateProcessor, error) {
	ctx, cancel := context.WithCancel(parentCtx)
	relationDefinitionProvider := newRelationDefinitionProvider(ctx)
	return NewPrecalculate().
		WithContext(ctx, cancel).
		WithNotifierConfig(
			notifier.BufferSize(config.NotifierChanBufferSize),
		).
		WithWindowRuntimeConfig(
			window.RuntimeConfigMaxSize(config.WindowMaxSize),
			window.RuntimeConfigExpireInterval(config.WindowExpireInterval),
			window.RuntimeConfigMaxDuration(config.WindowMaxDuration),
			window.ExpireIntervalIncrement(config.WindowExpireIntervalIncrement),
			window.NoDataMaxDuration(config.WindowNoDataMaxDuration),
		).
		WithDistributiveWindowConfig(
			window.DistributiveWindowSubSize(config.DistributiveWindowSubSize),
			window.DistributiveWindowWatchExpiredInterval(config.DistributiveWindowWatchExpireInterval),
			window.DistributiveWindowConcurrentProcessCount(config.DistributiveWindowHandleEventConcurrentCount),
			window.DistributiveWindowConcurrentExpirationMaximum(config.DistributiveWindowConcurrentExpirationMaximum),
			window.DistributiveWindowMappingMaxSpanCount(config.DistributiveWindowSubWindowMappingMaxSpanCount),
		).
		WithProcessorConfig(
			window.EnabledTraceInfoCache(config.EnabledTraceInfoCache != 0),
			window.TraceEsQueryRate(config.TraceEsQueryRate),
			window.TraceMetricsReportEnabled(config.EnabledTraceMetricsReport),
			window.TraceInfoReportEnabled(config.EnabledTraceInfoReport),
			window.TraceMetricsLayer4ReportEnabled(config.MetricsProcessLayer4ExportEnabled),
		).
		WithStorageConfig(
			storage.WorkerCount(config.StorageWorkerCount),
			storage.SaveHoldMaxCount(config.StorageSaveHoldMaxCount),
			storage.SaveHoldDuration(config.StorageSaveHoldMaxDuration),
			storage.CacheBackend(storage.CacheTypeRedis),
			storage.CacheRedisConfig(
				storage.RedisCacheMode(config.StorageRedisMode),
				storage.RedisCacheHost(config.StorageRedisStandaloneHost),
				storage.RedisCachePort(config.StorageRedisStandalonePort),
				storage.RedisCacheSentinelAddress(config.StorageRedisSentinelAddress...),
				storage.RedisCacheMasterName(config.StorageRedisSentinelMasterName),
				storage.RedisCacheSentinelPassword(config.StorageRedisSentinelPassword),
				storage.RedisCachePassword(config.StorageRedisStandalonePassword),
				storage.RedisCacheDb(config.StorageRedisDatabase),
				storage.RedisCacheDialTimeout(config.StorageRedisDialTimeout),
				storage.RedisCacheReadTimeout(config.StorageRedisReadTimeout),
			),
			storage.BloomConfig(
				storage.BloomFpRate(config.StorageBloomFpRate),
				storage.NormalMemoryBloomConfig(
					storage.MemoryBloomAutoClean(config.StorageBloomNormalAutoClean),
				),
				storage.NormalOverlapMemoryBloomConfig(
					storage.OverlapBloomResetDuration(config.StorageBloomNormalOverlapResetDuration),
				),
				storage.LayerBloomConfig(storage.Layers(config.StorageBloomLayersBloomLayers)),
				storage.LayerCapDecreaseBloomConfig(
					storage.CapDecreaseBloomCap(config.StorageBloomDecreaseCap),
					storage.CapDecreaseBloomLayers(config.StorageBloomDecreaseLayers),
					storage.CapDecreaseBloomDivisor(config.StorageBloomDecreaseDivisor),
				),
			),
			storage.SaveReqBufferSize(config.StorageSaveRequestBufferSize),
			storage.PrometheusWriterConfig(
				remote.PrometheusWriterUrl(config.PromRemoteWriteUrl),
				remote.PrometheusWriterHeaders(config.PromRemoteWriteHeaders),
			),
			storage.MetricsConfig(
				storage.MetricRelationMemDuration(config.RelationMetricsInMemDuration),
				storage.MetricFlowMemDuration(config.FlowMetricsInMemDuration),
				storage.MetricFlowBuckets(config.MetricsDurationBuckets),
				storage.MetricBuiltinRelationReport(
					config.BuiltinRelationReportEnabled,
					config.BuiltinRelationReportBizIDs,
					config.BuildInResultTableDetailKey,
				),
				storage.MetricRelationDefinitionProvider(relationDefinitionProvider),
			),
		).
		WithMetricReport(
			EnabledProfileReport(config.ProfileEnabled),
			ProfileAddress(config.ProfileHost),
			ProfileToken(config.ProfileToken),
			ProfileAppIdx(config.ProfileAppIdx),
			MetricReportInterval(config.SemaphoreReportInterval),
		).
		Build(), nil
}

func newRelationDefinitionProvider(ctx context.Context) relation.SchemaProvider {
	if !config.BuiltinRelationReportEnabled {
		return nil
	}
	redisInstance := redisStore.GetStorageRedisInstance()
	if redisInstance == nil || redisInstance.Client == nil {
		apmLogger.Errorf("create relation definition provider failed: storage redis is not initialized")
		return nil
	}
	provider, err := relation.NewRedisProvider(
		ctx,
		redisInstance.Client,
		relation.WithReloadOnStart(true),
	)
	if err != nil {
		apmLogger.Errorf("create relation definition provider failed: %s", err)
		return nil
	}
	go func() {
		<-ctx.Done()
		if closeErr := provider.Close(); closeErr != nil {
			apmLogger.Warnf("close relation definition provider failed: %s", closeErr)
		}
	}()
	return provider
}

var apmLogger = logger.With(zap.String("package", "apm_precalculate"))
