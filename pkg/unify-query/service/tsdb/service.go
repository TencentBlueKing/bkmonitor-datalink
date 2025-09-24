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
		log.Errorf(context.TODO(), "start loop reload storage failed for->[%s]", err)
		return
	}

	log.Warnf(context.TODO(), "prometheus service reloaded or start success.")
}

// Wait
func (s *Service) Wait() {
	s.wg.Wait()
}

// Close
func (s *Service) Close() {
	s.cancelFunc()
	log.Infof(context.TODO(), "prometheus service context cancel func called.")
}

// loopReloadStorage
func (s *Service) loopReloadStorage(ctx context.Context) error {
	err := s.reloadStorage()
	if err != nil {
		log.Errorf(context.TODO(), "reload storage failed,error:%s", err)
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
				log.Warnf(context.TODO(), "storage reload loop exit")
				return
			case <-ch:
				log.Debugf(context.TODO(), "get storage info changed notify")
				err = s.reloadStorage()
				if err != nil {
					log.Errorf(context.TODO(), "reload storage failed,error:%s", err)
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
		log.Errorf(context.TODO(), "get storage info from consul failed,error:%s", err)
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
		log.Errorf(context.TODO(), "reload storage failed,error:%s", err)
		return err
	}

	return nil
}
