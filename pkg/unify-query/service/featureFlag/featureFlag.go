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
	"sync"
	"time"

	ffclient "github.com/thomaspoignant/go-feature-flag"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/errno"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/featureFlag"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// Service
type Service struct {
	ctx        context.Context
	cancelFunc context.CancelFunc

	wg *sync.WaitGroup
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
func (s *Service) reloadFeatureFlags(ctx context.Context) error {
	data, err := consul.GetFeatureFlags()
	if err != nil {
		codedErr := errno.ErrConfigReloadFailed().
			WithComponent("功能开关").
			WithOperation("从Consul获取功能开关").
			WithError(err).
			WithSolution("检查Consul连接状态和功能开关配置文件路径")
		log.ErrorWithCodef(context.TODO(), codedErr)
		return codedErr
	}
	err = featureFlag.ReloadFeatureFlags(data)
	return err
}

// loopReloadFeatureFlags
func (s *Service) loopReloadFeatureFlags(ctx context.Context) error {
	err := s.reloadFeatureFlags(ctx)
	if err != nil {
		codedErr := errno.ErrConfigReloadFailed().
			WithComponent("功能开关").
			WithOperation("重载功能开关").
			WithError(err).
			WithSolution("检查功能开关重载逻辑和配置格式有效性")
		log.ErrorWithCodef(ctx, codedErr)
		return codedErr
	}
	ch, err := consul.WatchFeatureFlags(ctx)
	if err != nil {
		return err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			select {
			case <-ctx.Done():
				codedErr := errno.ErrWarningServiceDegraded().
					WithComponent("功能开关").
					WithOperation("重载循环退出").
					WithDetail("说明", "服务正在关闭，功能开关服务将停止")
				log.WarnWithCodef(context.TODO(), codedErr)
				return
			case <-ch:
				log.Debugf(context.TODO(), "get feature flags changed notify")
				err = s.reloadFeatureFlags(ctx)
				if err != nil {
					codedErr := errno.ErrConfigReloadFailed().
						WithComponent("功能开关").
						WithOperation("动态重载功能开关").
						WithError(err).
						WithSolution("检查Consul通知机制和功能开关数据格式")
					log.ErrorWithCodef(context.TODO(), codedErr)
				}
			}
		}
	}()
	return nil
}

// Reload
func (s *Service) Reload(ctx context.Context) {
	var err error
	if s.wg == nil {
		s.wg = new(sync.WaitGroup)
	}

	// 关闭上一次的操作
	s.Close()
	s.Wait()

	// 更新上下文控制方法
	s.ctx, s.cancelFunc = context.WithCancel(ctx)

	err = s.loopReloadFeatureFlags(s.ctx)
	if err != nil {
		codedErr := errno.ErrConfigReloadFailed().
			WithComponent("功能开关").
			WithOperation("启动功能开关循环").
			WithError(err).
			WithSolution("检查服务初始化状态和功能开关配置文件")
		log.ErrorWithCodef(s.ctx, codedErr)
		return
	}

	err = ffclient.Init(ffclient.Config{
		PollingInterval: 1 * time.Minute,
		Context:         s.ctx,
		Retriever:       &featureFlag.CustomRetriever{},
		FileFormat:      "json",
		DataExporter: ffclient.DataExporter{
			FlushInterval:    5 * time.Second,
			MaxEventInMemory: 100,
			Exporter:         &featureFlag.CustomExport{},
		},
	})
	if err != nil {
		codedErr := errno.ErrBusinessLogicError().
			WithComponent("功能开关").
			WithOperation("处理服务错误").
			WithError(err).
			WithSolution("检查服务整体状态")
		log.ErrorWithCodef(s.ctx, codedErr)
		return
	}

	log.Infof(s.ctx, "feature flag service reloaded or start success.")
}

// Wait
func (s *Service) Wait() {
}

// Close
func (s *Service) Close() {
	ffclient.Close()
	if s.cancelFunc != nil {
		s.cancelFunc()
	}
}
