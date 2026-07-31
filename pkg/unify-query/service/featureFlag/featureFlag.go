// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package featureFlag

import (
	"context"
	"fmt"
	"sync"
	"time"

	ffclient "github.com/thomaspoignant/go-feature-flag"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/featureFlag"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	redisService "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/redis"
)

// Service
type Service struct {
	ctx        context.Context
	cancelFunc context.CancelFunc

	wg       *sync.WaitGroup
	provider FeatureFlagProvider

	refreshMu         sync.Mutex
	clientMu          sync.Mutex
	clientInitialized bool
}

var (
	activeServiceMu sync.RWMutex
	activeService   *Service
)

func (s *Service) registerAsActive() {
	activeServiceMu.Lock()
	activeService = s
	activeServiceMu.Unlock()
}

func (s *Service) unregisterAsActive() {
	activeServiceMu.Lock()
	if activeService == s {
		activeService = nil
	}
	activeServiceMu.Unlock()
}

// RefreshFeatureFlags 使用运行中的 Provider 强制读取并应用最新快照。
// 诊断接口只应经由此入口更新运行时，不能自行注入其他来源的数据。
func RefreshFeatureFlags(ctx context.Context) error {
	activeServiceMu.RLock()
	service := activeService
	activeServiceMu.RUnlock()
	if service == nil {
		return fmt.Errorf("feature flag service is not running")
	}
	// 不能用 HTTP 请求上下文重新初始化客户端：请求结束后 Context 会取消，
	// 会连带停止 go-feature-flag 的轮询。运行中的服务必须沿用自身生命周期上下文。
	if service.ctx != nil {
		ctx = service.ctx
	}
	return service.reconcileFeatureFlags(ctx)
}

// Type
func (s *Service) Type() string {
	return "feature flag"
}

// Start
func (s *Service) Start(ctx context.Context) {
	s.Reload(ctx)
}

// reloadFeatureFlags
func (s *Service) reloadFeatureFlags(ctx context.Context) (bool, error) {
	if s.provider == nil {
		return false, fmt.Errorf("feature flag provider is not initialized")
	}

	data, err := s.provider.GetFeatureFlags(ctx)
	if err != nil {
		log.Errorf(ctx, "get feature flags failed, error: %s", err)
		return false, err
	}

	return featureFlag.ReloadFeatureFlagsIfChanged(data)
}

func (s *Service) initFeatureFlagClientLocked(ctx context.Context) error {
	err := ffclient.Init(ffclient.Config{
		PollingInterval: 1 * time.Minute,
		Context:         ctx,
		Retriever:       &featureFlag.CustomRetriever{},
		FileFormat:      "json",
		DataExporter: ffclient.DataExporter{
			FlushInterval:    5 * time.Second,
			MaxEventInMemory: 100,
			Exporter:         &featureFlag.CustomExport{},
		},
	})
	if err != nil {
		ffclient.Close()
		return err
	}
	s.clientInitialized = true
	return nil
}

func (s *Service) ensureFeatureFlagClient(ctx context.Context) error {
	s.clientMu.Lock()
	defer s.clientMu.Unlock()
	return featureFlag.WithClientLock(func() error {
		if s.clientInitialized {
			return nil
		}
		return s.initFeatureFlagClientLocked(ctx)
	})
}

// refreshFeatureFlagClient 立即让 go-feature-flag 重新读取已更新的 Retriever 快照。
func (s *Service) refreshFeatureFlagClient(ctx context.Context) error {
	s.clientMu.Lock()
	defer s.clientMu.Unlock()
	return featureFlag.WithClientLock(func() error {
		if s.clientInitialized {
			ffclient.Close()
			s.clientInitialized = false
		}
		return s.initFeatureFlagClientLocked(ctx)
	})
}

func (s *Service) reconcileFeatureFlags(ctx context.Context) error {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	changed, err := s.reloadFeatureFlags(ctx)
	if err != nil {
		return err
	}
	if changed {
		return s.refreshFeatureFlagClient(ctx)
	}
	return s.ensureFeatureFlagClient(ctx)
}

// loopReloadFeatureFlags
func (s *Service) loopReloadFeatureFlags(ctx context.Context) error {
	if s.provider == nil {
		return fmt.Errorf("feature flag provider is not initialized")
	}

	// 先完成订阅，再读取一次全量配置，避免 GET 与订阅建立之间遗漏更新。
	ch, err := s.provider.WatchFeatureFlags(ctx)
	if err != nil {
		log.Errorf(ctx, "watch feature flags failed, will retry during periodic reconcile, error: %s", err)
	}
	if reconcileErr := s.reconcileFeatureFlags(ctx); reconcileErr != nil {
		// 后端不可用或配置非法时仍保留调和循环，修正配置后自动完成客户端初始化。
		log.Errorf(ctx, "initial reconcile feature flags failed, will retry, error: %s", reconcileErr)
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		reconcileTicker := time.NewTicker(time.Minute)
		defer reconcileTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Warnf(context.TODO(), "feature flags reload loop exit")
				return
			case _, ok := <-ch:
				if !ok {
					log.Warnf(context.TODO(), "feature flags watch channel closed, continue with periodic reconcile")
					ch = nil
					continue
				}
				log.Debugf(context.TODO(), "get feature flags changed notify")
				reloadErr := s.reconcileFeatureFlags(ctx)
				if reloadErr != nil {
					log.Errorf(context.TODO(), "reconcile feature flags failed, error: %s", reloadErr)
				}
			case <-reconcileTicker.C:
				if ch == nil {
					newCh, watchErr := s.provider.WatchFeatureFlags(ctx)
					if watchErr != nil {
						log.Errorf(context.TODO(), "retry watch feature flags failed, error: %s", watchErr)
					} else {
						ch = newCh
					}
				}
				if reloadErr := s.reconcileFeatureFlags(ctx); reloadErr != nil {
					log.Errorf(context.TODO(), "periodic feature flags reconcile failed, error: %s", reloadErr)
				}
			}
		}
	}()
	return nil
}

// Reload
func (s *Service) Reload(ctx context.Context) {
	if s.wg == nil {
		s.wg = new(sync.WaitGroup)
	}

	// 关闭上一次的操作
	s.Close()
	s.Wait()

	// 更新上下文控制方法
	s.ctx, s.cancelFunc = context.WithCancel(ctx)

	// 根据配置选择数据源，初始化 provider
	if DataSource == "redis" {
		redisClient := redis.Client()
		if redisClient == nil {
			log.Errorf(ctx, "redis client is not initialized")
			return
		}
		// 从配置获取 basePath，如果没有则使用默认值
		basePath := redisService.KVBasePath
		if basePath == "" {
			log.Errorf(ctx, "redis kv base path is not configured")
			return
		}
		s.provider = redis.NewFeatureFlagClient(redisClient, basePath)
	} else {
		// 默认使用 consul
		s.provider = consul.NewFeatureFlagProvider()
	}

	err := s.loopReloadFeatureFlags(s.ctx)
	if err != nil {
		log.Errorf(s.ctx, "start loop feature flags failed,error: %s", err)
		return
	}
	s.registerAsActive()

	log.Infof(s.ctx, "feature flag service reloaded or start success.")
}

// Wait
func (s *Service) Wait() {
	if s.wg != nil {
		s.wg.Wait()
	}
}

// Close
func (s *Service) Close() {
	s.unregisterAsActive()
	if s.cancelFunc != nil {
		s.cancelFunc()
	}
	s.clientMu.Lock()
	_ = featureFlag.WithClientLock(func() error {
		if s.clientInitialized {
			ffclient.Close()
			s.clientInitialized = false
		}
		return nil
	})
	s.clientMu.Unlock()
}
