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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/errno"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	inner "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

type Service struct {
	ctx        context.Context
	cancelFunc context.CancelFunc
	wg         *sync.WaitGroup

	storageHash string
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
		codedErr := errno.ErrConfigReloadFailed().
			WithComponent("TSDB存储").
			WithOperation("启动重载循环").
			WithError(err).
			WithSolution("检查TSDB存储配置和连接")
		log.ErrorWithCodef(context.TODO(), codedErr)
		return
	}

	codedInfo := errno.ErrInfoServiceReady().
		WithComponent("Prometheus服务").
		WithOperation("服务重载或启动").
		WithContext("状态", "成功")
	log.InfoWithCodef(context.TODO(), codedInfo)
}

// Wait
func (s *Service) Wait() {
	s.wg.Wait()
}

// Close
func (s *Service) Close() {
	s.cancelFunc()
	codedInfo := errno.ErrInfoServiceShutdown().
		WithComponent("Prometheus服务").
		WithOperation("服务关闭").
		WithContext("状态", "上下文取消函数已调用")
	log.InfoWithCodef(context.TODO(), codedInfo)
}

// loopReloadStorage
func (s *Service) loopReloadStorage(ctx context.Context) error {
	err := s.reloadStorage()
	if err != nil {
		codedErr := errno.ErrConfigReloadFailed().
			WithComponent("TSDB存储").
			WithOperation("初始化重载").
			WithError(err).
			WithSolution("检查存储初始化配置")
		log.ErrorWithCodef(context.TODO(), codedErr)
		return err
	}
	ch, err := consul.WatchStorageInfo(ctx)
	if err != nil {
		return err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			select {
			case <-ctx.Done():
				codedWarn := errno.ErrWarningServiceLoop().
					WithComponent("TSDB存储").
					WithOperation("重载循环退出").
					WithSolution("检查上下文取消原因")
				log.WarnWithCodef(context.TODO(), codedWarn)
				return
			case <-ch:
				log.Debugf(context.TODO(), "get storage info changed notify")
				err = s.reloadStorage()
				if err != nil {
					codedErr := errno.ErrConfigReloadFailed().
						WithComponent("TSDB存储").
						WithOperation("动态重载").
						WithError(err).
						WithSolution("检查Consul通知和存储连接")
					log.ErrorWithCodef(context.TODO(), codedErr)
				}
			}
		}
	}()
	return nil
}

// reloadStorage 加载 storage 实例
func (s *Service) reloadStorage() error {
	consulData, err := consul.GetTsDBStorageInfo()
	if err != nil {
		codedErr := errno.ErrStorageConnFailed().
			WithComponent("Consul").
			WithOperation("获取存储信息").
			WithError(err).
			WithSolution("检查Consul存储信息配置")
		log.ErrorWithCodef(context.TODO(), codedErr)
		return err
	}
	hash := consul.HashIt(consulData)
	if hash == s.storageHash {
		log.Debugf(context.TODO(), "storage hash not changed")
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
	err = inner.ReloadTsDBStorage(s.ctx, consulData, options)
	if err != nil {
		codedErr := errno.ErrConfigReloadFailed().
			WithComponent("TSDB存储").
			WithOperation("重载存储实例").
			WithError(err).
			WithSolution("检查存储实例配置和连接")
		log.ErrorWithCodef(context.TODO(), codedErr)
		return err
	}

	return nil
}
