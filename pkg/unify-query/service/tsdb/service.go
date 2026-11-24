// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tsdb

import (
	"context"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	inner "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

type Service struct {
	ctx        context.Context
	cancelFunc context.CancelFunc
	wg         *sync.WaitGroup
}

var storageReloadLock sync.Mutex

// storageWatching and storageReadOK (the latest sync reached the backend) make this watcher's half of
// unify_query_config_sync_up{source="storage"}.
var (
	storageSyncMu   sync.Mutex
	storageWatching bool
	storageReadOK   bool
)

// setStorageSync records whether the storage watch is open and whether the latest sync reached the
// storage backend; nil leaves that half as it was. The watch retries inside the Consul and Redis
// clients without reporting a lost connection, so up also needs the latest read to have succeeded.
func setStorageSync(watching, readOK *bool) {
	storageSyncMu.Lock()
	defer storageSyncMu.Unlock()
	if watching != nil {
		storageWatching = *watching
	}
	if readOK != nil {
		storageReadOK = *readOK
	}
	metric.ConfigSyncSetUp(metric.ConfigSyncSourceStorage, metric.ConfigSyncWatcherTsDBStorage, storageWatching && storageReadOK)
}

// Type
func (s *Service) Type() string {
	return "prometheus"
}

// Start
func (s *Service) Start(ctx context.Context) {
	s.Reload(ctx)
}

// Reload
func (s *Service) Reload(ctx context.Context) {
	var err error
	if s.wg == nil {
		s.wg = new(sync.WaitGroup)
	}
	// 关闭上一次的服务
	if s.cancelFunc != nil {
		s.cancelFunc()
	}

	log.Debugf(context.TODO(), "waiting for prometheus service close")
	// 等待上一次服务结束
	s.Wait()

	// 更新上下文控制方法
	s.ctx, s.cancelFunc = context.WithCancel(ctx)
	log.Debugf(context.TODO(), "prometheus service context update success.")

	err = s.loopReloadStorage(s.ctx)
	if err != nil {
		log.Errorf(context.TODO(), "start storage reload loop failed: %s", err)
		return
	}

	log.Infof(ctx, "prometheus service start success.")
}

// Wait
func (s *Service) Wait() {
	s.wg.Wait()
}

// Close
func (s *Service) Close() {
	s.cancelFunc()
	log.Infof(context.TODO(), "prometheus service close success")
}

// loopReloadStorage
func (s *Service) loopReloadStorage(ctx context.Context) error {
	// Consul Storage 没有可用地址时只在加载时具名报一次：地址不会在运行中出现，不起每分钟重试。
	if err := consulStorageAddressError(); err != nil {
		return err
	}
	metric.ConfigSyncEnable(metric.ConfigSyncSourceStorage, metric.ConfigSyncStorageWatchers...)
	// 先建立订阅再读取全量快照，避免首次读取和订阅确认之间遗漏 Redis 更新。
	provider := getStorageProvider()
	watchCh, err := provider.WatchStorageInfo(ctx)
	if err != nil {
		log.Errorf(context.TODO(), "initial watch storage failed, will retry: %v", err)
	}
	watching := err == nil
	setStorageSync(&watching, nil)
	if err := s.reloadStorage(); err != nil {
		log.Errorf(context.TODO(), "initial reload storage failed, will retry: %v", err)
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		reconcileTicker := time.NewTicker(time.Minute)
		defer reconcileTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Warnf(context.TODO(), "storage watch loop exit")
				return
			case _, ok := <-watchCh:
				if !ok {
					log.Warnf(context.TODO(), "storage watch channel closed, continue with periodic reconcile")
					watchCh = nil
					if ctx.Err() == nil {
						watching := false
						setStorageSync(&watching, nil)
					}
					continue
				}
				log.Debugf(context.TODO(), "get storage info changed notify")
				reloadErr := s.reloadStorage()
				if reloadErr != nil {
					log.Errorf(context.TODO(), "reload storage failed %v", reloadErr)
				}
			case <-reconcileTicker.C:
				if watchCh == nil {
					newWatchCh, watchErr := provider.WatchStorageInfo(ctx)
					if watchErr != nil {
						log.Errorf(context.TODO(), "retry watch storage failed %v", watchErr)
					} else {
						watchCh = newWatchCh
						watching := true
						setStorageSync(&watching, nil)
					}
				}
				if reloadErr := s.reloadStorage(); reloadErr != nil {
					log.Errorf(context.TODO(), "periodic storage reconcile failed %v", reloadErr)
				}
			}
		}
	}()

	return nil
}

// reloadStorage 加载 storage 实例
func (s *Service) reloadStorage() error {
	return ReloadStorage(s.ctx)
}

// ReloadStorage 串行完成配置读取与运行时替换，供 watcher、周期调和和诊断强刷共用。
func ReloadStorage(ctx context.Context) (err error) {
	storageReloadLock.Lock()
	defer storageReloadLock.Unlock()

	defer func() {
		if err == nil {
			metric.ConfigSyncSucceeded(metric.ConfigSyncSourceStorage)
		} else {
			metric.ConfigSyncFailed(ctx, metric.ConfigSyncSourceStorage, err)
		}
		answered := metric.ConfigSyncBackendAnswered(err)
		setStorageSync(nil, &answered)
	}()

	// 使用接口多态，根据配置自动选择 Consul 或 Redis
	provider := getStorageProvider()
	storageData, err := provider.GetTsDBStorageInfo(ctx)
	if err != nil {
		log.Errorf(context.TODO(), "get storage info failed %v", err)
		return err
	}

	options := &inner.Options{
		InfluxDB: &inner.InfluxDBOption{
			Timeout:        InfluxDBTimeout,
			ContentType:    InfluxDBContentType,
			ChunkSize:      InfluxDBChunkSize,
			RawUriPath:     InfluxDBQueryRawUriPath,
			Accept:         InfluxDBQueryRawAccept,
			AcceptEncoding: InfluxDBQueryRawAcceptEncoding,
			MaxLimit:       InfluxDBMaxLimit,
			MaxSLimit:      InfluxDBMaxSLimit,
			Tolerance:      InfluxDBTolerance,
			RouterPrefix:   InfluxDBRouterPrefix,
			ReadRateLimit:  InfluxDBQueryReadRateLimit,
		},
		Es: &inner.ESOption{
			Timeout:    EsTimeout,
			MaxRouting: EsMaxRouting,
			MaxSize:    EsMaxSize,
		},
	}
	hash := inner.StorageConfigHash(storageData, options)
	if hash == inner.StorageMapHash() {
		log.Debugf(context.TODO(), "storage and query options hash not changed")
		return nil
	}
	err = inner.ReloadTsDBStorage(ctx, storageData, options)
	if err != nil {
		log.Errorf(context.TODO(), "reload storage failed %v", err)
		return metric.ConfigSyncApplyError(err)
	}

	return nil
}
