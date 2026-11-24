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
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/common/model"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	inner "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/utils"
)

// 服务侧初始化flux实例使用
type Service struct {
	ctx        context.Context
	cancelFunc context.CancelFunc
	wg         *sync.WaitGroup
	storageMu  sync.Mutex

	storageHash string

	// storageWatching and storageReadOK (the latest sync reached the backend) make this watcher's half of
	// unify_query_config_sync_up{source="storage"}.
	storageSyncMu   sync.Mutex
	storageWatching bool
	storageReadOK   bool
}

// setStorageSync records whether the storage watch is open and whether the latest sync reached the
// storage backend; nil leaves that half as it was. The watch retries inside the Consul and Redis
// clients without reporting a lost connection, so up also needs the latest read to have succeeded.
func (s *Service) setStorageSync(watching, readOK *bool) {
	s.storageSyncMu.Lock()
	defer s.storageSyncMu.Unlock()
	if watching != nil {
		s.storageWatching = *watching
	}
	if readOK != nil {
		s.storageReadOK = *readOK
	}
	metric.ConfigSyncSetUp(metric.ConfigSyncSourceStorage, metric.ConfigSyncWatcherInfluxDB, s.storageWatching && s.storageReadOK)
}

var (
	activeServiceMu sync.RWMutex
	activeService   *Service
)

// Type
func (s *Service) Type() string {
	return "influxdb"
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

	log.Debugf(ctx, "waiting for influxdb service close")
	// 等待上一次服务结束
	s.Wait()

	// 更新上下文控制方法
	s.ctx, s.cancelFunc = context.WithCancel(ctx)
	activeServiceMu.Lock()
	activeService = s
	activeServiceMu.Unlock()
	log.Debugf(ctx, "influxdb service context update success.")

	err = s.loopReloadStorage(s.ctx)
	if err != nil {
		log.Errorf(context.TODO(), "start loop reload es storage failed for->[%s]", err)
	}

	err = s.reloadInfluxDBRouter(s.ctx)
	if err != nil {
		log.Errorf(context.TODO(), "start loop reload influxdb router failed,err:%s", err)
	}

	err = s.reloadSpaceTsDbRouter(s.ctx)
	if err != nil {
		log.Errorf(context.TODO(), "start loop reload space tsDB router failed, err: %s", err)
	}

	log.Warnf(context.TODO(), "influxdb service reloaded or start success.")
}

// Wait
func (s *Service) Wait() {
	s.wg.Wait()
}

// Close
func (s *Service) Close() {
	if s.cancelFunc != nil {
		s.cancelFunc()
	}
	activeServiceMu.Lock()
	if activeService == s {
		activeService = nil
	}
	activeServiceMu.Unlock()
	log.Infof(context.TODO(), "influxdb service context cancel func called.")
}

// ReloadStorage refreshes the active InfluxDB storage map for diagnostic recovery.
func ReloadStorage(ctx context.Context) error {
	activeServiceMu.RLock()
	service := activeService
	activeServiceMu.RUnlock()
	if service == nil {
		return fmt.Errorf("influxdb service is not initialized")
	}
	return service.reloadStorageWithContext(ctx)
}

// reloadStorage
func (s *Service) reloadStorage() error {
	return s.reloadStorageWithContext(s.ctx)
}

func (s *Service) reloadStorageWithContext(ctx context.Context) error {
	s.storageMu.Lock()
	defer s.storageMu.Unlock()

	var (
		timeout time.Duration
		dTmp    model.Duration
		err     error
	)

	defer func() {
		if err == nil {
			metric.ConfigSyncSucceeded(metric.ConfigSyncSourceStorage)
		} else {
			metric.ConfigSyncFailed(ctx, metric.ConfigSyncSourceStorage, err)
		}
		answered := metric.ConfigSyncBackendAnswered(err)
		s.setStorageSync(nil, &answered)
	}()

	// 使用接口多态，根据配置自动选择 Consul 或 Redis
	provider := getStorageProvider()
	storageData, err := provider.GetInfluxdbStorageInfo(ctx)
	if err != nil {
		log.Errorf(context.TODO(), "get storage info failed,error:%s", err)
		return err
	}

	dTmp, parseErr := model.ParseDuration(Timeout)
	if parseErr != nil {
		timeout = 30 * time.Second
		log.Warnf(context.TODO(), "parse influxdb query timeout failed,use 30s as default")
	} else {
		timeout = time.Duration(dTmp)
	}

	option := &inner.Option{
		Timeout:              timeout,
		ContentType:          ContentType,
		PerQueryMaxGoroutine: PerQueryMaxGoroutine,
		ChunkSize:            ChunkSize,
		MaxLimit:             MaxLimit,
		MaxSLimit:            MaxSLimit,
		Tolerance:            Tolerance,
	}
	hash := utils.HashItDeterministic(struct {
		Storage map[string]any
		Option  *inner.Option
	}{
		Storage: storageData,
		Option:  option,
	})
	if hash == s.storageHash {
		log.Debugf(context.TODO(), "storage and query options hash not changed")
		return nil
	}

	hostList := make(map[string]*inner.Host, len(storageData))
	for key, value := range storageData {
		var address, username, password string
		switch s := value.(type) {
		case *consul.Storage:
			address, username, password = s.Address, s.Username, s.Password
		case *redis.Storage:
			address, username, password = s.Address, s.Username, s.Password
		default:
			log.Errorf(context.TODO(), "unsupported storage type: %T", value)
			continue
		}
		hostList[key] = &inner.Host{
			Address:  address,
			Username: username,
			Password: password,
		}
	}
	err = inner.ReloadStorage(ctx, hostList, option)
	if err != nil {
		log.Errorf(context.TODO(), "reload storage failed,error:%s", err)
		err = metric.ConfigSyncApplyError(err)
		return err
	}

	s.storageHash = hash
	return nil
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
		log.Errorf(context.TODO(), "initial watch storage failed, will retry,error:%s", err)
	}
	watching := err == nil
	s.setStorageSync(&watching, nil)
	if err := s.reloadStorage(); err != nil {
		log.Errorf(context.TODO(), "initial reload storage failed, will retry,error:%s", err)
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		reconcileTicker := time.NewTicker(time.Minute)
		defer reconcileTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Warnf(context.TODO(), "storage reload loop exit")
				return
			case _, ok := <-watchCh:
				if !ok {
					log.Warnf(context.TODO(), "storage watch channel closed, continue with periodic reconcile")
					watchCh = nil
					if ctx.Err() == nil {
						watching := false
						s.setStorageSync(&watching, nil)
					}
					continue
				}
				log.Debugf(context.TODO(), "get storage info changed notify")
				reloadErr := s.reloadStorage()
				if reloadErr != nil {
					log.Errorf(context.TODO(), "reload storage failed,error:%s", reloadErr)
				}
			case <-reconcileTicker.C:
				if watchCh == nil {
					newWatchCh, watchErr := provider.WatchStorageInfo(ctx)
					if watchErr != nil {
						log.Errorf(context.TODO(), "retry watch storage failed,error:%s", watchErr)
					} else {
						watchCh = newWatchCh
						watching := true
						s.setStorageSync(&watching, nil)
					}
				}
				if reloadErr := s.reloadStorage(); reloadErr != nil {
					log.Errorf(context.TODO(), "periodic storage reconcile failed,error:%s", reloadErr)
				}
			}
		}
	}()

	return nil
}

// reloadInfluxDBRouter 重新加载 InfluxDBRouter
func (s *Service) reloadInfluxDBRouter(ctx context.Context) error {
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(GrpcMaxCallRecvMsgSize),
			grpc.MaxCallSendMsgSize(GrpcMaxCallSendMsgSize),
		),
	}

	ir := inner.GetInfluxDBRouter()
	err := ir.ReloadRouter(ctx, RouterPrefix, dialOpts)
	if err != nil {
		return err
	}

	s.wg.Add(1)
	go func() {
		ticker := time.NewTicker(PingPeriod)
		defer ticker.Stop()
		defer s.wg.Done()
		for {
			select {
			case <-ctx.Done():
				log.Warnf(ctx, "maintain influxdb host status info loop exit")
				return
			case <-ticker.C:
				ir.Ping(ctx, PingTimeout, PingCount)
				log.Debugf(ctx, "finish to Ping goroutine.")
			}
		}
	}()

	ch := ir.RouterSubscribe(ctx)
	s.wg.Add(1)
	go func() {
		ticker := time.NewTicker(RouterInterval)
		defer ticker.Stop()
		defer s.wg.Done()
		for {
			select {
			case <-ctx.Done():
				log.Warnf(ctx, "space router loop exit")
				return
				// 订阅 redis
			case <-ticker.C:
				err = ir.ReloadAllKey(ctx)
				if err != nil {
					log.Errorf(ctx, "%s", err.Error())
				}
				log.Infof(ctx, "ir reload all key time ticker reload")
			case msg := <-ch:
				ir.ReloadByKey(ctx, msg.Payload)
				log.Debugf(ctx, "subscribe msg: %s, space: %s", msg.String(), msg.Payload)
			}
		}
	}()

	return nil
}

// reloadInfluxDBRouter 重新加载 SpaceTsDbRouter
func (s *Service) reloadSpaceTsDbRouter(ctx context.Context) error {
	ir, err := inner.SetSpaceTsDbRouter(ctx, SpaceRouterBboltPath, SpaceRouterBboltBucketName, SpaceRouterPrefix, SpaceRouterBboltWriteBatchSize, IsCache)
	if err != nil {
		return err
	}
	metric.ConfigSyncSetInterval(metric.ConfigSyncSourceSpaceRouterFull, RouterInterval)
	err = ir.ReloadAllKey(ctx, false)
	if err != nil {
		return err
	}

	ch := ir.RouterSubscribe(ctx)
	s.wg.Add(1)
	go func() {
		ticker := time.NewTicker(RouterInterval)
		defer ticker.Stop()
		defer s.wg.Done()
		for {
			select {
			case <-ctx.Done():
				log.Warnf(ctx, "[SpaceTSDB Router] Loop exit")
				return
				// 订阅 redis
			case <-ticker.C:
				err = ir.ReloadAllKey(ctx, true)
				if err != nil {
					log.Errorf(ctx, "[SpaceTSDB Router] TimeTicker reload with error, %v", err)
				}
			case msg, ok := <-ch:
				if !ok {
					// The subscription ends only with ctx.
					ch = nil
					continue
				}
				err = ir.ReloadByChannel(ctx, msg.Channel, msg.Payload)
				if err != nil {
					metric.ConfigSyncFailed(ctx, metric.ConfigSyncSourceSpaceRouterIncremental, err)
					log.Errorf(ctx, "[SpaceTSDB Router] Subscribe msg with error, %s, %v", msg.String(), err)
				} else {
					metric.ConfigSyncSucceeded(metric.ConfigSyncSourceSpaceRouterIncremental)
				}
			}
		}
	}()
	return nil
}
